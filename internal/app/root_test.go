package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigValidateCommandAcceptsConfigFlagAfterSubcommand(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "openclaw.yaml")
	writeConfig(t, path, `
platform:
  name: aws
region:
  name: us-east-1
instance:
  type: t3.medium
  disk_size_gb: 20
image:
  name: ubuntu-24.04
runtime:
  endpoint: http://localhost:11434
  model: llama3.2
sandbox:
  enabled: false
`)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"openclaw", "config", "validate", "--config", path}

	app := New()
	cmd := newRootCommand(app)
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := stdout.String(); !strings.Contains(got, "configuration is valid") {
		t.Fatalf("stdout = %q, want validation success message", got)
	}
}

func TestConfigValidateCommandReturnsErrorOnInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "openclaw.yaml")
	writeConfig(t, path, `
platform:
  name: gcp
region:
  name: us-east-1
instance:
  type: t3.medium
  disk_size_gb: 0
image:
  name: ubuntu-24.04
runtime:
  endpoint: not-a-url
  model: ""
`)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"openclaw", "config", "validate", "--config", path}

	app := New()
	cmd := newRootCommand(app)
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil")
	}
	msg := err.Error()
	for _, field := range []string{"platform.name", "instance.disk_size_gb", "runtime.endpoint", "runtime.model"} {
		if !strings.Contains(msg, field) {
			t.Fatalf("error %q does not mention %q", msg, field)
		}
	}
}

func TestQuotaCheckCommandReportsInsufficientCapacity(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"openclaw", "quota", "check", "--platform", "aws", "--region", "ap-northeast-1", "--instance-family", "g5"}

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
		"Quota status for g5 in ap-northeast-1",
		"Likely creatable: false",
		"estimated remaining capacity: 0",
		"Suggested actions:",
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("stdout = %q, want %q", got, fragment)
		}
	}
}

func writeConfig(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.TrimSpace(contents)+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
