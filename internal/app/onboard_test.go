package app

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
)

func TestOnboardCommandRunsCodexOAuthLogin(t *testing.T) {
	called := false
	original := runCodexOAuthLogin
	runCodexOAuthLogin = func(ctx context.Context, out io.Writer) error {
		called = true
		_, _ = out.Write([]byte("mock codex login\n"))
		return nil
	}
	defer func() { runCodexOAuthLogin = original }()

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"openclaw", "onboard", "--auth-choice", "openai-codex"}

	app := New()
	cmd := newRootCommand(app)
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !called {
		t.Fatal("runCodexOAuthLogin was not called")
	}
	if got := stdout.String(); !strings.Contains(got, "Codex authentication configured") {
		t.Fatalf("stdout = %q, want success message", got)
	}
}

func TestOnboardCommandRejectsUnsupportedAuthChoice(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"openclaw", "onboard", "--auth-choice", "invalid"}

	app := New()
	cmd := newRootCommand(app)
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil")
	}
	if !strings.Contains(err.Error(), "unsupported auth choice") {
		t.Fatalf("error = %v, want unsupported auth choice", err)
	}
}
