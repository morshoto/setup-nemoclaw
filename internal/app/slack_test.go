package app

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openclaw/internal/slackbot"
)

func TestSlackServeLoadsAgentEnvFile(t *testing.T) {
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, "agents")
	configPath := filepath.Join(agentsDir, "alpha", "config.yaml")
	envPath := filepath.Join(agentsDir, "alpha", ".env")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(configPath, []byte(strings.Join([]string{
		"runtime:",
		"  endpoint: http://localhost:11434",
		"slack:",
		"  runtime_url: http://agent-runtime.example.com",
		"  bot_user_id: UAGENT",
		"  allowed_channels:",
		"    - C123",
		"    - C456",
		"",
	}, "\n")), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	if err := os.WriteFile(envPath, []byte(strings.Join([]string{
		"SLACK_BOT_TOKEN=xoxb-agent-token",
		"SLACK_APP_TOKEN=xapp-agent-token",
		"",
	}, "\n")), 0o600); err != nil {
		t.Fatalf("WriteFile(env) error = %v", err)
	}

	t.Setenv("SLACK_BOT_TOKEN", "xoxb-global-token")
	t.Setenv("SLACK_APP_TOKEN", "xapp-global-token")
	t.Setenv("OPENCLAW_RUNTIME_URL", "http://global-runtime.example.com")
	t.Setenv("SLACK_BOT_USER_ID", "UGLOBAL")
	t.Setenv("SLACK_ALLOWED_CHANNELS", "C999")

	originalRunSlackAdapter := runSlackAdapter
	defer func() { runSlackAdapter = originalRunSlackAdapter }()

	var got slackbot.Config
	runSlackAdapter = func(ctx context.Context, cfg slackbot.Config, out io.Writer) error {
		got = cfg
		return nil
	}

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"openclaw", "--config", configPath, "slack", "serve"}

	app := New()
	cmd := newRootCommand(app)
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got.BotToken != "xoxb-agent-token" || got.AppToken != "xapp-agent-token" {
		t.Fatalf("slack config = %#v, want agent env values", got)
	}
	if got.RuntimeURL != "http://agent-runtime.example.com" {
		t.Fatalf("runtime url = %q, want agent runtime url", got.RuntimeURL)
	}
	if got.BotUserID != "UAGENT" {
		t.Fatalf("bot user id = %q, want UAGENT", got.BotUserID)
	}
	if strings.Join(got.AllowedChannels, ",") != "C123,C456" {
		t.Fatalf("allowed channels = %#v, want agent env values", got.AllowedChannels)
	}
}
