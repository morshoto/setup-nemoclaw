package slackbot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

const (
	defaultRuntimeURL      = "http://127.0.0.1:8080"
	defaultRequestTimeout  = 30 * time.Second
	defaultConversationLen = 12
	defaultHeartbeatPeriod = 15 * time.Second
)

// Config controls the Slack adapter service.
type Config struct {
	BotToken        string
	AppToken        string
	RuntimeURL      string
	BotUserID       string
	AllowedChannels []string
	RequestTimeout  time.Duration
	Debug           bool
}

// RuntimeClient sends prompts to an OpenClaw runtime server.
type RuntimeClient struct {
	baseURL *url.URL
	client  *http.Client
}

type runtimeGenerator interface {
	Generate(ctx context.Context, prompt string) (string, error)
}

// NewRuntimeClient creates a runtime client for the OpenClaw HTTP API.
func NewRuntimeClient(runtimeURL string, timeout time.Duration) (*RuntimeClient, error) {
	parsed, err := url.Parse(strings.TrimSpace(runtimeURL))
	if err != nil {
		return nil, fmt.Errorf("parse runtime url %q: %w", runtimeURL, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("runtime url %q must include scheme and host", runtimeURL)
	}
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}
	return &RuntimeClient{
		baseURL: parsed,
		client: &http.Client{
			Timeout: timeout,
		},
	}, nil
}

// Generate submits a prompt to the runtime and returns the model output.
func (c *RuntimeClient) Generate(ctx context.Context, prompt string) (string, error) {
	if c == nil {
		return "", errors.New("runtime client is required")
	}
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/v1/generate"

	body, err := json.Marshal(map[string]string{
		"prompt": prompt,
	})
	if err != nil {
		return "", fmt.Errorf("marshal generate request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create generate request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("call runtime generate endpoint: %w", err)
	}
	defer resp.Body.Close()

	payload, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		return "", fmt.Errorf("read generate response: %w", readErr)
	}
	if resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(payload))
		if msg == "" {
			msg = resp.Status
		}
		return "", fmt.Errorf("runtime generate failed: %s", msg)
	}

	var response struct {
		Status string `json:"status"`
		Output string `json:"output"`
	}
	if err := json.Unmarshal(payload, &response); err != nil {
		return "", fmt.Errorf("decode generate response: %w", err)
	}
	if strings.TrimSpace(response.Output) == "" {
		return "", errors.New("runtime returned an empty response")
	}
	return strings.TrimSpace(response.Output), nil
}

type messagePoster interface {
	PostMessage(ctx context.Context, channelID, text, threadTS string) error
}

type slackPoster struct {
	client *slack.Client
}

func (p slackPoster) PostMessage(ctx context.Context, channelID, text, threadTS string) error {
	if p.client == nil {
		return errors.New("slack client is required")
	}
	opts := []slack.MsgOption{slack.MsgOptionText(strings.TrimSpace(text), false)}
	if strings.TrimSpace(threadTS) != "" {
		opts = append(opts, slack.MsgOptionTS(strings.TrimSpace(threadTS)))
	}
	_, _, err := p.client.PostMessageContext(ctx, strings.TrimSpace(channelID), opts...)
	return err
}

type conversationStore struct {
	mu      sync.Mutex
	turns   map[string][]conversationTurn
	maxTurn int
}

type conversationTurn struct {
	Role string
	Text string
}

func newConversationStore(maxTurn int) *conversationStore {
	if maxTurn <= 0 {
		maxTurn = defaultConversationLen
	}
	return &conversationStore{
		turns:   make(map[string][]conversationTurn),
		maxTurn: maxTurn,
	}
}

func (s *conversationStore) append(key, role, text string) {
	key = strings.TrimSpace(key)
	role = strings.TrimSpace(role)
	text = strings.TrimSpace(text)
	if key == "" || role == "" || text == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	turns := append(append([]conversationTurn(nil), s.turns[key]...), conversationTurn{Role: role, Text: text})
	if len(turns) > s.maxTurn {
		turns = append([]conversationTurn(nil), turns[len(turns)-s.maxTurn:]...)
	}
	s.turns[key] = turns
}

func (s *conversationStore) prompt(key, latest string) string {
	key = strings.TrimSpace(key)
	latest = strings.TrimSpace(latest)

	s.mu.Lock()
	turns := append([]conversationTurn(nil), s.turns[key]...)
	s.mu.Unlock()

	var b strings.Builder
	b.WriteString("You are OpenClaw, a concise Slack assistant.\n")
	b.WriteString("Answer the latest user message directly. Keep it short unless details are requested.\n")
	if len(turns) > 0 {
		b.WriteString("\nConversation so far:\n")
		for _, turn := range turns {
			if strings.TrimSpace(turn.Text) == "" {
				continue
			}
			b.WriteString(strings.Title(strings.ToLower(strings.TrimSpace(turn.Role))))
			b.WriteString(": ")
			b.WriteString(strings.TrimSpace(turn.Text))
			b.WriteString("\n")
		}
	}
	b.WriteString("User: ")
	b.WriteString(latest)
	b.WriteString("\nAssistant:")
	return b.String()
}

type Service struct {
	runtime        runtimeGenerator
	poster         messagePoster
	botUserID      string
	allowed        map[string]struct{}
	conversations  *conversationStore
	requestTimeout time.Duration
	out            io.Writer
}

// NewService creates a Slack adapter service.
func NewService(runtime runtimeGenerator, poster messagePoster, botUserID string, allowedChannels []string, requestTimeout time.Duration, out ...io.Writer) *Service {
	allowed := make(map[string]struct{}, len(allowedChannels))
	for _, channel := range allowedChannels {
		channel = strings.TrimSpace(channel)
		if channel == "" {
			continue
		}
		allowed[channel] = struct{}{}
	}
	if requestTimeout <= 0 {
		requestTimeout = defaultRequestTimeout
	}
	return &Service{
		runtime:        runtime,
		poster:         poster,
		botUserID:      strings.TrimSpace(botUserID),
		allowed:        allowed,
		conversations:  newConversationStore(defaultConversationLen),
		requestTimeout: requestTimeout,
		out:            firstWriter(out...),
	}
}

// Run starts a Slack Socket Mode adapter.
func Run(ctx context.Context, cfg Config, out io.Writer) error {
	cfg = normalizeConfig(cfg)
	if err := validateConfig(cfg); err != nil {
		return err
	}
	if out == nil {
		out = io.Discard
	}

	fmt.Fprintln(out, "ok starting Slack adapter")

	api := slack.New(
		cfg.BotToken,
		slack.OptionAppLevelToken(cfg.AppToken),
	)
	if cfg.Debug {
		api = slack.New(
			cfg.BotToken,
			slack.OptionDebug(true),
			slack.OptionAppLevelToken(cfg.AppToken),
		)
	}

	botUserID := strings.TrimSpace(cfg.BotUserID)
	if botUserID == "" {
		fmt.Fprintln(out, "ok resolving Slack bot user id")
		auth, err := api.AuthTestContext(ctx)
		if err != nil {
			return fmt.Errorf("resolve slack bot user id: %w", err)
		}
		botUserID = strings.TrimSpace(auth.UserID)
	}

	runtimeClient, err := NewRuntimeClient(cfg.RuntimeURL, cfg.RequestTimeout)
	if err != nil {
		return err
	}

	service := NewService(runtimeClient, slackPoster{client: api}, botUserID, cfg.AllowedChannels, cfg.RequestTimeout, out)
	client := socketmode.New(api, socketmode.OptionDebug(cfg.Debug))

	fmt.Fprintf(out, "ok connecting to Slack Socket Mode\n")
	fmt.Fprintf(out, "listening for app mentions, DMs, and slash commands\n")
	fmt.Fprintf(out, "runtime url: %s\n", cfg.RuntimeURL)
	if len(cfg.AllowedChannels) > 0 {
		fmt.Fprintf(out, "allowed channels: %s\n", strings.Join(cfg.AllowedChannels, ", "))
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- client.RunContext(ctx)
	}()

	lastSignal := time.Now()
	heartbeatTicker := time.NewTicker(defaultHeartbeatPeriod)
	defer heartbeatTicker.Stop()
	fmt.Fprintln(out, "waiting for Slack events")

	for {
		select {
		case <-heartbeatTicker.C:
			if time.Since(lastSignal) >= defaultHeartbeatPeriod {
				fmt.Fprintln(out, "waiting for Slack events")
			}
		case evt, ok := <-client.Events:
			if !ok {
				select {
				case err := <-errCh:
					if err != nil {
						return err
					}
				default:
				}
				return errors.New("slack socket mode event stream closed unexpectedly")
			}
			switch evt.Type {
			case socketmode.EventTypeConnecting:
				fmt.Fprintln(out, "waiting for Slack Socket Mode connection")
			case socketmode.EventTypeConnected:
				fmt.Fprintln(out, "ok Slack Socket Mode connected")
			case socketmode.EventTypeConnectionError:
				fmt.Fprintln(out, "warn Slack Socket Mode connection error; waiting to reconnect")
			case socketmode.EventTypeHello:
				fmt.Fprintln(out, "ok Slack Socket Mode hello")
				fmt.Fprintln(out, "ok Slack Socket Mode ready; waiting for Slack events")
			}
			lastSignal = time.Now()
			if err := service.handleEvent(ctx, client, evt); err != nil && cfg.Debug {
				fmt.Fprintf(out, "slack adapter: %v\n", err)
			}
		case err := <-errCh:
			if err != nil {
				return err
			}
			return errors.New("slack socket mode client stopped unexpectedly")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func normalizeConfig(cfg Config) Config {
	if strings.TrimSpace(cfg.RuntimeURL) == "" {
		cfg.RuntimeURL = os.Getenv("OPENCLAW_RUNTIME_URL")
	}
	if strings.TrimSpace(cfg.RuntimeURL) == "" {
		cfg.RuntimeURL = defaultRuntimeURL
	}
	if strings.TrimSpace(cfg.BotToken) == "" {
		cfg.BotToken = os.Getenv("SLACK_BOT_TOKEN")
	}
	if strings.TrimSpace(cfg.AppToken) == "" {
		cfg.AppToken = os.Getenv("SLACK_APP_TOKEN")
	}
	if strings.TrimSpace(cfg.BotUserID) == "" {
		cfg.BotUserID = os.Getenv("SLACK_BOT_USER_ID")
	}
	if len(cfg.AllowedChannels) == 0 {
		if raw := strings.TrimSpace(os.Getenv("SLACK_ALLOWED_CHANNELS")); raw != "" {
			cfg.AllowedChannels = splitAndTrim(raw, ",")
		}
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = defaultRequestTimeout
	}
	return cfg
}

func validateConfig(cfg Config) error {
	var missing []string
	if strings.TrimSpace(cfg.BotToken) == "" {
		missing = append(missing, "slack bot token")
	}
	if strings.TrimSpace(cfg.AppToken) == "" {
		missing = append(missing, "slack app token")
	}
	if strings.TrimSpace(cfg.RuntimeURL) == "" {
		missing = append(missing, "runtime url")
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("missing slack adapter configuration: %s", strings.Join(missing, ", "))
	}
	if !strings.HasPrefix(strings.TrimSpace(cfg.BotToken), "xoxb-") {
		return errors.New("slack bot token must start with xoxb-")
	}
	if !strings.HasPrefix(strings.TrimSpace(cfg.AppToken), "xapp-") {
		return errors.New("slack app token must start with xapp-")
	}
	parsed, err := url.Parse(strings.TrimSpace(cfg.RuntimeURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("runtime url %q must include a scheme and host", cfg.RuntimeURL)
	}
	return nil
}

func (s *Service) handleEvent(ctx context.Context, client *socketmode.Client, evt socketmode.Event) error {
	switch evt.Type {
	case socketmode.EventTypeConnecting, socketmode.EventTypeConnected, socketmode.EventTypeConnectionError, socketmode.EventTypeHello:
		return nil
	case socketmode.EventTypeEventsAPI:
		eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
		if !ok {
			return nil
		}
		if evt.Request != nil {
			client.Ack(*evt.Request)
		}
		go s.handleEventsAPI(ctx, eventsAPIEvent)
		return nil
	case socketmode.EventTypeSlashCommand:
		cmd, ok := evt.Data.(slack.SlashCommand)
		if !ok {
			return nil
		}
		if evt.Request != nil {
			client.Ack(*evt.Request)
		}
		go s.handleSlashCommand(ctx, cmd)
		return nil
	default:
		return nil
	}
}

func (s *Service) handleEventsAPI(ctx context.Context, event slackevents.EventsAPIEvent) {
	switch inner := event.InnerEvent.Data.(type) {
	case *slackevents.AppMentionEvent:
		if s.shouldIgnoreMention(inner) {
			return
		}
		s.logf("received app mention in %s", strings.TrimSpace(inner.Channel))
		text := stripLeadingMention(inner.Text, s.botUserID)
		s.replyToMessage(ctx, inner.Channel, threadTimestamp(inner.ThreadTimeStamp, inner.TimeStamp), inner.User, text)
	case *slackevents.MessageEvent:
		if s.shouldIgnoreMessage(inner) {
			return
		}
		if !isDirectMessage(inner.ChannelType) {
			if !s.channelAllowed(inner.Channel) {
				return
			}
			s.logf("received channel message in %s", strings.TrimSpace(inner.Channel))
			s.replyToMessage(ctx, inner.Channel, threadTimestamp(inner.ThreadTimeStamp, inner.TimeStamp), inner.User, inner.Text)
			return
		}
		s.logf("received direct message in %s", strings.TrimSpace(inner.Channel))
		s.replyToMessage(ctx, inner.Channel, threadTimestamp(inner.ThreadTimeStamp, inner.TimeStamp), inner.User, inner.Text)
	}
}

func (s *Service) handleSlashCommand(ctx context.Context, cmd slack.SlashCommand) {
	if !s.channelAllowed(cmd.ChannelID) {
		return
	}
	s.logf("received slash command in %s", strings.TrimSpace(cmd.ChannelID))
	s.replyToMessage(ctx, cmd.ChannelID, "", cmd.UserID, cmd.Text)
}

func (s *Service) replyToMessage(ctx context.Context, channel, threadTS, userID, text string) {
	channel = strings.TrimSpace(channel)
	text = strings.TrimSpace(text)
	if channel == "" || text == "" {
		return
	}
	if !s.channelAllowed(channel) {
		return
	}

	key := conversationKey(channel, threadTS)
	prompt := s.conversations.prompt(key, text)
	runCtx, cancel := context.WithTimeout(ctx, s.requestTimeout)
	defer cancel()
	reply, err := s.runtime.Generate(runCtx, prompt)
	if err != nil {
		s.logf("runtime request failed for %s: %v", channel, err)
		_ = s.poster.PostMessage(runCtx, channel, fmt.Sprintf("OpenClaw error: %v", err), threadTS)
		return
	}

	reply = strings.TrimSpace(reply)
	if reply == "" {
		reply = "No response generated."
	}
	s.conversations.append(key, "user", text)
	s.conversations.append(key, "assistant", reply)

	if err := s.poster.PostMessage(runCtx, channel, reply, threadTS); err != nil {
		s.logf("posting reply failed for %s: %v", channel, err)
		return
	}
	s.logf("posted reply to %s", channel)
}

func (s *Service) shouldIgnoreMention(ev *slackevents.AppMentionEvent) bool {
	if ev == nil {
		return true
	}
	if strings.TrimSpace(ev.User) != "" && strings.TrimSpace(ev.User) == s.botUserID {
		return true
	}
	if strings.TrimSpace(ev.BotID) != "" {
		return true
	}
	return false
}

func (s *Service) shouldIgnoreMessage(ev *slackevents.MessageEvent) bool {
	if ev == nil {
		return true
	}
	if strings.TrimSpace(ev.User) != "" && strings.TrimSpace(ev.User) == s.botUserID {
		return true
	}
	if strings.TrimSpace(ev.BotID) != "" || strings.TrimSpace(ev.SubType) != "" {
		return true
	}
	return false
}

func (s *Service) channelAllowed(channel string) bool {
	channel = strings.TrimSpace(channel)
	if channel == "" {
		return false
	}
	if len(s.allowed) == 0 {
		return true
	}
	_, ok := s.allowed[channel]
	return ok
}

func isDirectMessage(channelType string) bool {
	switch strings.ToLower(strings.TrimSpace(channelType)) {
	case "im", "mpim", "mim":
		return true
	default:
		return false
	}
}

func threadTimestamp(threadTS, fallback string) string {
	if strings.TrimSpace(threadTS) != "" {
		return strings.TrimSpace(threadTS)
	}
	return strings.TrimSpace(fallback)
}

func conversationKey(channel, threadTS string) string {
	if strings.TrimSpace(threadTS) != "" {
		return strings.TrimSpace(channel) + ":" + strings.TrimSpace(threadTS)
	}
	return strings.TrimSpace(channel)
}

func stripLeadingMention(text, botUserID string) string {
	text = strings.TrimSpace(text)
	_ = botUserID
	if text == "" {
		return text
	}
	for strings.HasPrefix(text, "<@") {
		end := strings.Index(text, ">")
		if end < 0 {
			break
		}
		text = strings.TrimSpace(text[end+1:])
	}
	return text
}

func splitAndTrim(value, sep string) []string {
	parts := strings.Split(value, sep)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func firstWriter(writers ...io.Writer) io.Writer {
	for _, w := range writers {
		if w != nil {
			return w
		}
	}
	return io.Discard
}

func (s *Service) logf(format string, args ...any) {
	if s == nil || s.out == nil {
		return
	}
	fmt.Fprintf(s.out, "%s\n", fmt.Sprintf(format, args...))
}
