package slackbot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/slack-go/slack/slackevents"
)

type fakeRuntime struct {
	prompts []string
	reply   string
	err     error
}

func (f *fakeRuntime) Generate(ctx context.Context, prompt string) (string, error) {
	f.prompts = append(f.prompts, prompt)
	if f.err != nil {
		return "", f.err
	}
	if f.reply == "" {
		return "ok", nil
	}
	return f.reply, nil
}

type fakePoster struct {
	posts []postedMessage
}

type postedMessage struct {
	Channel  string
	Text     string
	ThreadTS string
}

func (f *fakePoster) PostMessage(ctx context.Context, channelID, text, threadTS string) error {
	f.posts = append(f.posts, postedMessage{
		Channel:  channelID,
		Text:     text,
		ThreadTS: threadTS,
	})
	return nil
}

func TestRuntimeClientGenerate(t *testing.T) {
	var gotPrompt string
	failCh := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			failCh <- "method"
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != "/v1/generate" {
			failCh <- "path"
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var req struct {
			Prompt string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			failCh <- err.Error()
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		gotPrompt = req.Prompt
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
			"output": "hello from runtime",
		})
	}))
	defer server.Close()

	client, err := NewRuntimeClient(server.URL, time.Second)
	if err != nil {
		t.Fatalf("NewRuntimeClient() error = %v", err)
	}
	out, err := client.Generate(context.Background(), "say hello")
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if gotPrompt != "say hello" {
		t.Fatalf("prompt = %q, want %q", gotPrompt, "say hello")
	}
	if out != "hello from runtime" {
		t.Fatalf("output = %q, want %q", out, "hello from runtime")
	}
	select {
	case reason := <-failCh:
		t.Fatalf("handler assertion failed: %s", reason)
	default:
	}
}

func TestServiceRepliesToAppMentionAndTracksConversation(t *testing.T) {
	runtime := &fakeRuntime{reply: "hello"}
	poster := &fakePoster{}
	service := NewService(runtime, poster, "U123", nil, time.Second)

	service.handleEventsAPI(context.Background(), slackevents.EventsAPIEvent{
		InnerEvent: slackevents.EventsAPIInnerEvent{
			Data: &slackevents.AppMentionEvent{
				User:           "U456",
				Text:           "<@U123> say hello",
				Channel:        "C123",
				TimeStamp:      "111.222",
				EventTimeStamp: "111.222",
			},
		},
	})

	if len(runtime.prompts) != 1 {
		t.Fatalf("runtime prompts = %d, want 1", len(runtime.prompts))
	}
	if !strings.Contains(runtime.prompts[0], "User: say hello") {
		t.Fatalf("prompt %q missing user text", runtime.prompts[0])
	}
	if len(poster.posts) != 1 {
		t.Fatalf("posts = %d, want 1", len(poster.posts))
	}
	if poster.posts[0].Channel != "C123" || poster.posts[0].ThreadTS != "111.222" || poster.posts[0].Text != "hello" {
		t.Fatalf("post = %#v, want channel C123 thread 111.222 text hello", poster.posts[0])
	}

	runtime.reply = "second"
	service.handleEventsAPI(context.Background(), slackevents.EventsAPIEvent{
		InnerEvent: slackevents.EventsAPIInnerEvent{
			Data: &slackevents.AppMentionEvent{
				User:            "U456",
				Text:            "<@U123> follow up",
				Channel:         "C123",
				ThreadTimeStamp: "111.222",
				TimeStamp:       "222.333",
				EventTimeStamp:  "222.333",
			},
		},
	})

	if len(runtime.prompts) != 2 {
		t.Fatalf("runtime prompts = %d, want 2", len(runtime.prompts))
	}
	if !strings.Contains(runtime.prompts[1], "User: say hello") || !strings.Contains(runtime.prompts[1], "Assistant: hello") || !strings.Contains(runtime.prompts[1], "User: follow up") {
		t.Fatalf("prompt %q missing conversation history", runtime.prompts[1])
	}
}

func TestServiceRepliesToDirectMessage(t *testing.T) {
	runtime := &fakeRuntime{reply: "hello dm"}
	poster := &fakePoster{}
	service := NewService(runtime, poster, "U123", nil, time.Second)

	service.handleEventsAPI(context.Background(), slackevents.EventsAPIEvent{
		InnerEvent: slackevents.EventsAPIInnerEvent{
			Data: &slackevents.MessageEvent{
				User:           "U456",
				Text:           "how are you?",
				Channel:        "D123",
				ChannelType:    "im",
				TimeStamp:      "333.444",
				EventTimeStamp: "333.444",
			},
		},
	})

	if len(runtime.prompts) != 1 {
		t.Fatalf("runtime prompts = %d, want 1", len(runtime.prompts))
	}
	if len(poster.posts) != 1 {
		t.Fatalf("posts = %d, want 1", len(poster.posts))
	}
	if poster.posts[0].ThreadTS != "333.444" {
		t.Fatalf("thread ts = %q, want 333.444", poster.posts[0].ThreadTS)
	}
}

func TestServiceIgnoresDisallowedChannels(t *testing.T) {
	runtime := &fakeRuntime{reply: "ignored"}
	poster := &fakePoster{}
	service := NewService(runtime, poster, "U123", []string{"C123"}, time.Second)

	service.handleEventsAPI(context.Background(), slackevents.EventsAPIEvent{
		InnerEvent: slackevents.EventsAPIInnerEvent{
			Data: &slackevents.AppMentionEvent{
				User:           "U456",
				Text:           "<@U123> hello",
				Channel:        "C999",
				TimeStamp:      "111.222",
				EventTimeStamp: "111.222",
			},
		},
	})

	if len(runtime.prompts) != 0 {
		t.Fatalf("runtime prompts = %d, want 0", len(runtime.prompts))
	}
	if len(poster.posts) != 0 {
		t.Fatalf("posts = %d, want 0", len(poster.posts))
	}
}

func TestServiceRepliesToAllowedChannelMessages(t *testing.T) {
	runtime := &fakeRuntime{reply: "hello channel"}
	poster := &fakePoster{}
	service := NewService(runtime, poster, "U123", []string{"C123"}, time.Second)

	service.handleEventsAPI(context.Background(), slackevents.EventsAPIEvent{
		InnerEvent: slackevents.EventsAPIInnerEvent{
			Data: &slackevents.MessageEvent{
				User:           "U456",
				Text:           "hello team",
				Channel:        "C123",
				ChannelType:    "channel",
				TimeStamp:      "444.555",
				EventTimeStamp: "444.555",
			},
		},
	})

	if len(runtime.prompts) != 1 {
		t.Fatalf("runtime prompts = %d, want 1", len(runtime.prompts))
	}
	if len(poster.posts) != 1 {
		t.Fatalf("posts = %d, want 1", len(poster.posts))
	}
	if poster.posts[0].Text != "hello channel" {
		t.Fatalf("post text = %q, want hello channel", poster.posts[0].Text)
	}
}
