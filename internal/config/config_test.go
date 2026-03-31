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
sandbox:
  enabled: true
  unknown_field: nope
`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil")
	}
	if got := err.Error(); !strings.Contains(got, "field unknown_field") {
		t.Fatalf("Load() error = %q, want unknown field", got)
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "openclaw.yaml")
	cfg := &Config{
		Platform: PlatformConfig{Name: PlatformAWS},
		Region:   RegionConfig{Name: "us-east-1"},
		Instance: InstanceConfig{Type: "t3.medium", DiskSizeGB: 20},
		Image:    ImageConfig{Name: "AWS Deep Learning AMI GPU Ubuntu 22.04", ID: "ami-0123456789abcdef0"},
		Runtime:  RuntimeConfig{Endpoint: "http://localhost:11434", Model: "llama3.2"},
		Sandbox:  SandboxConfig{Enabled: true, NetworkMode: "private", UseNemoClaw: true, FilesystemAllow: []string{"/tmp", "/var/tmp"}},
	}

	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.Platform.Name != cfg.Platform.Name || loaded.Image.ID != cfg.Image.ID || loaded.Sandbox.NetworkMode != "private" || !loaded.Sandbox.UseNemoClaw {
		t.Fatalf("round trip mismatch: %#v", loaded)
	}
	if len(loaded.Sandbox.FilesystemAllow) != 2 || loaded.Sandbox.FilesystemAllow[0] != "/tmp" {
		t.Fatalf("round trip list mismatch: %#v", loaded.Sandbox.FilesystemAllow)
	}
}

func TestLoadSupportsListsAndNestedYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "openclaw.yaml")
	writeFile(t, path, `
platform:
  name: aws
region:
  name: ap-northeast-1
instance:
  type: g5.xlarge
  disk_size_gb: 40
image:
  name: ubuntu-24.04
runtime:
  endpoint: http://localhost:11434
  model: llama3.2
sandbox:
  enabled: true
  network_mode: private
  use_nemoclaw: false
  filesystem_allow:
    - /tmp
    - /var/tmp
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := len(cfg.Sandbox.FilesystemAllow); got != 2 {
		t.Fatalf("filesystem_allow length = %d, want 2", got)
	}
	if cfg.Sandbox.FilesystemAllow[1] != "/var/tmp" {
		t.Fatalf("filesystem_allow = %#v", cfg.Sandbox.FilesystemAllow)
	}
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.TrimSpace(contents)+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
