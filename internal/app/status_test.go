package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"openclaw/internal/runtimeinstall"
)

func TestStatusCommandReportsIdleState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(runtimeStatusResponse{
			Status:      "ok",
			Active:      false,
			ActiveCount: 0,
		})
	}))
	t.Cleanup(server.Close)

	originalResolver := resolveRuntimeStatusURL
	resolveRuntimeStatusURL = func(cfg *runtimeinstall.RuntimeConfig) (string, error) {
		return server.URL, nil
	}
	t.Cleanup(func() { resolveRuntimeStatusURL = originalResolver })

	dir := t.TempDir()
	path := filepath.Join(dir, "runtime.yaml")
	writeConfig(t, path, `
provider: aws-bedrock
model: llama3.2
port: 8080
`)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"openclaw", "status", "--runtime-config", path}

	app := New()
	cmd := newRootCommand(app)
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	got := stdout.String()
	for _, fragment := range []string{"runtime status", "no active agents"} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("stdout = %q, want %q", got, fragment)
		}
	}
}

func TestStatusCommandReportsActiveAgents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(runtimeStatusResponse{
			Status:      "ok",
			Active:      true,
			ActiveCount: 2,
			ActiveAgents: []runtimeActiveAgent{
				{
					ID:                "agent-1",
					Task:              "generating response",
					Model:             "llama3.2",
					RunningForSeconds: 42,
				},
				{
					ID:                "agent-2",
					Task:              "waiting on tool call",
					RunningForSeconds: 63,
				},
			},
		})
	}))
	t.Cleanup(server.Close)

	originalResolver := resolveRuntimeStatusURL
	resolveRuntimeStatusURL = func(cfg *runtimeinstall.RuntimeConfig) (string, error) {
		return server.URL, nil
	}
	t.Cleanup(func() { resolveRuntimeStatusURL = originalResolver })

	dir := t.TempDir()
	path := filepath.Join(dir, "runtime.yaml")
	writeConfig(t, path, `
provider: aws-bedrock
model: llama3.2
port: 8080
`)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"openclaw", "status", "--runtime-config", path}

	app := New()
	cmd := newRootCommand(app)
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	got := stdout.String()
	for _, fragment := range []string{
		"runtime status",
		"active agents: 2",
		"- agent-1: generating response (model: llama3.2, running 42s)",
		"- agent-2: waiting on tool call (running 1m3s)",
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("stdout = %q, want %q", got, fragment)
		}
	}
}

func TestRuntimeStatusEndpointTracksGenerateLifecycle(t *testing.T) {
	generator := &blockingGenerator{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	state := newRuntimeServerState("/opt/openclaw/runtime.yaml", "127.0.0.1:8080", &runtimeinstall.RuntimeConfig{
		Provider: "aws-bedrock",
		Model:    "llama3.2",
	}, generator)
	server := httptest.NewServer(newRuntimeServerMux(state))
	t.Cleanup(server.Close)

	bootstrap := getRuntimeStatus(t, server.URL+"/status")
	if bootstrap.Active {
		t.Fatalf("initial status = %#v, want idle", bootstrap)
	}

	errCh := make(chan error, 1)
	go func() {
		req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/generate", strings.NewReader(`{"prompt":"hello"}`))
		if err != nil {
			errCh <- err
			return
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			errCh <- err
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			errCh <- fmt.Errorf("unexpected status %s: %s", resp.Status, strings.TrimSpace(string(body)))
			return
		}
		errCh <- nil
	}()

	select {
	case <-generator.started:
	case <-time.After(2 * time.Second):
		t.Fatal("generate request did not start")
	}

	active := getRuntimeStatus(t, server.URL+"/status")
	if !active.Active || active.ActiveCount != 1 {
		t.Fatalf("active status = %#v, want one active agent", active)
	}
	if len(active.ActiveAgents) != 1 || active.ActiveAgents[0].ID != "agent-1" {
		t.Fatalf("active agents = %#v, want agent-1", active.ActiveAgents)
	}
	if active.ActiveAgents[0].Model != "llama3.2" {
		t.Fatalf("active agents = %#v, want model to be tracked", active.ActiveAgents)
	}

	close(generator.release)
	if err := <-errCh; err != nil {
		t.Fatalf("generate request error = %v", err)
	}

	idle := getRuntimeStatus(t, server.URL+"/status")
	if idle.Active || idle.ActiveCount != 0 || len(idle.ActiveAgents) != 0 {
		t.Fatalf("final status = %#v, want idle", idle)
	}
}

type blockingGenerator struct {
	started chan struct{}
	release chan struct{}
}

func (g *blockingGenerator) Generate(ctx context.Context, prompt string) (string, error) {
	if g.started != nil {
		close(g.started)
		g.started = nil
	}
	select {
	case <-g.release:
		return "generated", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func getRuntimeStatus(t *testing.T, url string) runtimeStatusResponse {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s error = %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s status = %s body = %s", url, resp.Status, strings.TrimSpace(string(body)))
	}
	var status runtimeStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatalf("decode status response: %v", err)
	}
	return status
}
