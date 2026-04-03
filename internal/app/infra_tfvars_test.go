package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInfraTFVarsCommandWritesTerraformVars(t *testing.T) {
	originalDeriveSSHPublicKey := deriveSSHPublicKeyFunc
	deriveSSHPublicKeyFunc = func(ctx context.Context, privateKeyPath string) (string, error) {
		return "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITfvarsTestKey openclaw", nil
	}
	defer func() { deriveSSHPublicKeyFunc = originalDeriveSSHPublicKey }()

	dir := t.TempDir()
	output := filepath.Join(dir, "terraform.tfvars")
	keyPath := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(keyPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("WriteFile(keyPath) error = %v", err)
	}
	configPath := filepath.Join(dir, "openclaw.yaml")
	writeConfig(t, configPath, `
platform:
  name: aws
compute:
  class: gpu
region:
  name: ap-northeast-1
instance:
  type: g5.xlarge
  disk_size_gb: 40
  network_mode: public
image:
  name: Ubuntu 22.04 LTS
runtime:
  endpoint: http://localhost:11434
  model: llama3.2
  public_cidr: 0.0.0.0/0
sandbox:
  enabled: true
  network_mode: public
  use_nemoclaw: true
ssh:
  key_name: demo-key
  private_key_path: `+keyPath+`
  cidr: 203.0.113.0/24
  user: ubuntu
`)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"openclaw", "--profile", "sso-dev", "--config", configPath, "infra", "tfvars", "--output", output}

	app := New()
	cmd := newRootCommand(app)
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	body := string(data)
	mustContainTerraformAssignment(t, body, "region", `"ap-northeast-1"`)
	mustContainTerraformAssignment(t, body, "compute_class", `"gpu"`)
	mustContainTerraformAssignment(t, body, "instance_type", `"g5.xlarge"`)
	mustContainTerraformAssignment(t, body, "disk_size_gb", `40`)
	mustContainTerraformAssignment(t, body, "network_mode", `"public"`)
	mustContainTerraformAssignment(t, body, "image_name", `"Ubuntu 22.04 LTS"`)
	mustContainTerraformAssignment(t, body, "image_id", `""`)
	mustContainTerraformAssignment(t, body, "runtime_port", `8080`)
	mustContainTerraformAssignment(t, body, "runtime_cidr", `"0.0.0.0/0"`)
	mustContainTerraformAssignment(t, body, "runtime_provider", `""`)
	mustContainTerraformAssignment(t, body, "ssh_key_name", `"demo-key"`)
	mustContainTerraformAssignment(t, body, "ssh_public_key", `"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITfvarsTestKey openclaw"`)
	mustContainTerraformAssignment(t, body, "ssh_cidr", `"203.0.113.0/24"`)
	mustContainTerraformAssignment(t, body, "ssh_user", `"ubuntu"`)
	mustContainTerraformAssignment(t, body, "name_prefix", `"openclaw"`)
	mustContainTerraformAssignment(t, body, "use_nemoclaw", `true`)
	mustContainTerraformAssignment(t, body, "nim_endpoint", `"http://localhost:11434"`)
	mustContainTerraformAssignment(t, body, "model", `"llama3.2"`)
	mustContainTerraformAssignment(t, body, "source_archive_url", `"https://example.com/openclaw-bootstrap.tar.gz"`)
	mustContainTerraformAssignment(t, body, "source_ref", `"`)
	mustContainTerraformAssignment(t, body, "aws_profile", `"sso-dev"`)
	if !strings.Contains(stdout.String(), "terraform variables written to") {
		t.Fatalf("stdout = %q, want success message", stdout.String())
	}
}

func TestInfraTFVarsCommandWritesAWSProfile(t *testing.T) {
	originalDeriveSSHPublicKey := deriveSSHPublicKeyFunc
	deriveSSHPublicKeyFunc = func(ctx context.Context, privateKeyPath string) (string, error) {
		return "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITfvarsTestKey openclaw", nil
	}
	defer func() { deriveSSHPublicKeyFunc = originalDeriveSSHPublicKey }()

	dir := t.TempDir()
	output := filepath.Join(dir, "terraform.tfvars")
	keyPath := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(keyPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("WriteFile(keyPath) error = %v", err)
	}
	configPath := filepath.Join(dir, "openclaw.yaml")
	writeConfig(t, configPath, `
platform:
  name: aws
region:
  name: ap-northeast-1
instance:
  type: g5.xlarge
  disk_size_gb: 40
  network_mode: public
image:
  name: Ubuntu 22.04 LTS
runtime:
  endpoint: http://localhost:11434
  model: llama3.2
sandbox:
  enabled: true
  network_mode: public
ssh:
  key_name: demo-key
  private_key_path: `+keyPath+`
  cidr: 203.0.113.0/24
  user: ubuntu
`)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"openclaw", "--profile", "sso-dev", "--config", configPath, "infra", "tfvars", "--output", output}

	app := New()
	cmd := newRootCommand(app)
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	body := string(data)
	mustContainTerraformAssignment(t, body, "aws_profile", `"sso-dev"`)
}

func mustContainTerraformAssignment(t *testing.T, body, key, value string) {
	t.Helper()
	if !strings.Contains(body, key) || !strings.Contains(body, value) {
		t.Fatalf("terraform vars file %q missing %s = %s", body, key, value)
	}
}
