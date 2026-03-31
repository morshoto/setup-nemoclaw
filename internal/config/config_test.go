package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAndValidateValidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "openclaw.yaml")
	writeFile(t, path, `
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
  enabled: true
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateReportsReadableErrors(t *testing.T) {
	err := Validate(&Config{})
	if err == nil {
		t.Fatal("Validate() error = nil")
	}

	msg := err.Error()
	for _, field := range []string{
		"platform.name",
		"region.name",
		"instance.type",
		"instance.disk_size_gb",
		"image.name",
		"runtime.endpoint",
		"runtime.model",
	} {
		if !strings.Contains(msg, field) {
			t.Fatalf("error %q does not mention %q", msg, field)
		}
	}
}

func TestLoadRejectsInvalidYaml(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "openclaw.yaml")
	writeFile(t, path, `
platform:
  name: aws
unknown:
  name: nope
`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil")
	}
	if got := err.Error(); !strings.Contains(got, "unknown section") {
		t.Fatalf("Load() error = %q, want unknown section", got)
	}
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.TrimSpace(contents)+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
