package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"openclaw/internal/config"
	"openclaw/internal/host"
	infratf "openclaw/internal/infra/terraform"
	"openclaw/internal/provider"
	awsprovider "openclaw/internal/provider/aws"
	"openclaw/internal/runtimeinstall"
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

func TestQuotaCheckCommandReportsLiveQuotaStatus(t *testing.T) {
	restore := stubAWSProviderFactory()
	defer restore()

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
		"Quota report for g5 in ap-northeast-1",
		"Data source: aws-service-quotas",
		"Likely creatable: true",
		"Notes:",
		"quota check complete",
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("stdout = %q, want %q", got, fragment)
		}
	}
}

func TestAuthCheckCommandReportsSuccess(t *testing.T) {
	restore := stubAWSProviderFactory()
	defer restore()

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"openclaw", "--profile", "test-profile", "auth", "check"}

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
		"AWS auth check passed",
		"profile: test-profile",
		"caller identity: arn:aws:sts::123456789012:assumed-role/test-role/test-session",
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("stdout = %q, want %q", got, fragment)
		}
	}
}

func TestRootHelpGroupsCommands(t *testing.T) {
	app := New()
	cmd := newRootCommand(app)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"openclaw", "--help"}

	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	got := stdout.String()
	for _, fragment := range []string{
		"Setup",
		"Provision",
		"Runtime",
		"Integrations",
		"Inspect",
		"Support",
		"init",
		"create",
		"slack",
		"status",
		"verify",
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("help output %q missing %q", got, fragment)
		}
	}
	if strings.Contains(got, "\x1b[34m") {
		t.Fatalf("help output should be plain text when not writing to a terminal: %q", got)
	}
}

func TestInfraCreateCommandReportsCreatedInstance(t *testing.T) {
	original := newAWSProvider
	newAWSProvider = func(profile, computeClass string) provider.CloudProvider {
		return infraCreateStubCloudProvider{stubCloudProvider: stubCloudProvider{profile: profile}}
	}
	defer func() { newAWSProvider = original }()
	restoreSourceArchive := stubSourceArchiveURL(t)
	defer restoreSourceArchive()

	originalBackend := newTerraformBackend
	originalDeriveSSHPublicKey := deriveSSHPublicKeyFunc
	originalReadGitHubPrivateKey := readGitHubPrivateKeyFunc
	originalEnsureSSHPrivateKey := ensureSSHPrivateKeyFunc
	ensureSSHPrivateKeyFunc = func(ctx context.Context, privateKeyPath string) (string, error) {
		return privateKeyPath, nil
	}
	deriveSSHPublicKeyFunc = func(ctx context.Context, privateKeyPath string) (string, error) {
		return "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestPublicKey openclaw", nil
	}
	readGitHubPrivateKeyFunc = func(ctx context.Context, privateKeyPath string) (string, error) {
		return "-----BEGIN OPENSSH PRIVATE KEY-----\nTEST-GITHUB-KEY\n-----END OPENSSH PRIVATE KEY-----", nil
	}
	newTerraformBackend = func(profile string, cfg *config.Config) (infratf.InfraBackend, error) {
		return fakeTerraformBackend{
			output: &infratf.InfraOutput{
				InstanceID:         "i-0123456789abcdef0",
				PublicIP:           "203.0.113.10",
				PrivateIP:          "10.0.0.10",
				ConnectionInfo:     "ssh -i <your-key>.pem ubuntu@203.0.113.10",
				SecurityGroupID:    "sg-0123456789abcdef0",
				SecurityGroupRules: []string{"allow tcp/22 from 203.0.113.0/24", "allow tcp/8080 from 0.0.0.0/0"},
				Region:             cfg.Region.Name,
				NetworkMode:        "public",
			},
		}, nil
	}
	defer func() { newTerraformBackend = originalBackend }()
	defer func() { deriveSSHPublicKeyFunc = originalDeriveSSHPublicKey }()
	defer func() { readGitHubPrivateKeyFunc = originalReadGitHubPrivateKey }()
	defer func() { ensureSSHPrivateKeyFunc = originalEnsureSSHPrivateKey }()

	dir := t.TempDir()
	path := filepath.Join(dir, "openclaw.yaml")
	writeConfig(t, path, `
platform:
  name: aws
region:
  name: us-east-1
ssh:
  key_name: demo-key
  private_key_path: /tmp/demo.pem
  cidr: 203.0.113.0/24
  user: ubuntu
instance:
  type: g5.xlarge
  disk_size_gb: 40
  network_mode: public
image:
  id: ami-0123456789abcdef0
sandbox:
  enabled: true
  network_mode: public
`)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"openclaw", "--config", path, "infra", "create", "--ssh-key-name", "demo-key", "--ssh-cidr", "203.0.113.0/24"}

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
		"instance id: i-0123456789abcdef0",
		"region: us-east-1",
		"public ip: 203.0.113.10",
		"connection: ssh -i <your-key>.pem ubuntu@203.0.113.10",
		"security group rules:",
		"allow tcp/22 from 203.0.113.0/24",
		"allow tcp/8080 from 0.0.0.0/0",
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("stdout = %q, want %q", got, fragment)
		}
	}
}

func TestCreateCommandRequiresSSHAccessConfiguration(t *testing.T) {
	restore := stubAWSProviderFactory()
	defer restore()

	dir := t.TempDir()
	path := filepath.Join(dir, "openclaw.yaml")
	writeConfig(t, path, `
platform:
  name: aws
compute:
  class: cpu
region:
  name: us-east-1
instance:
  type: t3.xlarge
  disk_size_gb: 20
image:
  name: Ubuntu 22.04 LTS
runtime:
  endpoint: https://nim.example.com
  model: llama3.2
sandbox:
  network_mode: public
`)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"openclaw", "--config", path, "create"}

	app := New()
	cmd := newRootCommand(app)
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil")
	}
	if !strings.Contains(err.Error(), "ssh cidr is required for public networking") {
		t.Fatalf("error = %v, want SSH config validation", err)
	}
}

func TestResolveSSHCIDRAutoDetectsPublicIP(t *testing.T) {
	original := detectSSHCIDR
	detectSSHCIDR = func(ctx context.Context) (string, error) {
		return "198.51.100.23", nil
	}
	defer func() { detectSSHCIDR = original }()

	cidr, err := resolveSSHCIDR(context.Background(), "demo-key", "")
	if err != nil {
		t.Fatalf("resolveSSHCIDR() error = %v", err)
	}
	if cidr != "198.51.100.23/32" {
		t.Fatalf("cidr = %q, want 198.51.100.23/32", cidr)
	}
}

func TestInstallCommandRunsWorkflowAgainstResolvedInstance(t *testing.T) {
	restore := stubAWSProviderFactory()
	defer restore()

	originalBuildRuntimeBinary := runtimeinstall.BuildRuntimeBinaryFunc
	runtimeinstall.BuildRuntimeBinaryFunc = func(ctx context.Context) (string, error) {
		dir := t.TempDir()
		path := filepath.Join(dir, "openclaw")
		if err := os.WriteFile(path, []byte("binary"), 0o700); err != nil {
			return "", err
		}
		return path, nil
	}
	defer func() { runtimeinstall.BuildRuntimeBinaryFunc = originalBuildRuntimeBinary }()

	originalExecutor := newSSHExecutor
	newSSHExecutor = func(cfg host.SSHConfig) host.Executor {
		return fakeSSHExecutor{
			results: map[string]host.CommandResult{
				"true":          {},
				"nvidia-smi -L": {Stdout: "GPU 0: demo"},
				"docker info":   {Stdout: "Docker Engine"},
				"docker info --format {{json .Runtimes}}":                                                {Stdout: `{"nvidia":{}}`},
				"docker run --rm --gpus all --pull=never nvidia/cuda:12.4.1-base-ubuntu22.04 nvidia-smi": {Stdout: "NVIDIA-SMI"},
				"sudo mkdir -p /opt/openclaw":                                                            {},
				"chmod +x /opt/openclaw/install.sh":                                                      {},
				"sh /opt/openclaw/install.sh /opt/openclaw/runtime.yaml":                                 {Stdout: "OpenClaw runtime installation complete"},
				"sudo mkdir -p /opt/openclaw/bin":                                                        {},
				"sudo chown -R ubuntu:ubuntu /opt/openclaw":                                              {},
				"sudo mv /opt/openclaw/openclaw.upload /opt/openclaw/bin/openclaw":                       {},
				"chmod +x /opt/openclaw/bin/openclaw":                                                    {},
				"sudo mv /opt/openclaw/openclaw.service /etc/systemd/system/openclaw.service":            {},
				"sudo systemctl daemon-reload":                                                           {},
				"sudo systemctl enable --now openclaw.service":                                           {},
			},
		}
	}
	defer func() { newSSHExecutor = originalExecutor }()

	dir := t.TempDir()
	keyPath := filepath.Join(dir, "demo.pem")
	if err := os.WriteFile(keyPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("WriteFile(key) error = %v", err)
	}
	path := filepath.Join(dir, "openclaw.yaml")
	writeConfig(t, path, `
platform:
  name: aws
region:
  name: us-east-1
ssh:
  key_name: demo-key
  private_key_path: `+keyPath+`
  cidr: 203.0.113.0/24
  user: ubuntu
instance:
  type: g5.xlarge
  disk_size_gb: 40
  network_mode: public
image:
  name: ubuntu-24.04
runtime:
  endpoint: http://localhost:11434
  model: llama3.2
sandbox:
  enabled: true
  network_mode: public
  use_nemoclaw: false
`)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"openclaw", "--config", path, "install", "--target", "i-0123456789abcdef0", "--working-dir", "/opt/openclaw", "--ssh-user", "ubuntu", "--ssh-key", keyPath}

	app := New()
	cmd := newRootCommand(app)
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := stdout.String(); !strings.Contains(got, "install workflow completed") {
		t.Fatalf("stdout = %q, want install summary", got)
	}
}

func TestVerifyCommandReportsSuccess(t *testing.T) {
	restore := stubAWSProviderFactory()
	defer restore()

	originalLocalExecutor := newLocalExecutor
	newLocalExecutor = func() host.Executor {
		return flexibleExecutor{
			run: func(command string, args ...string) (host.CommandResult, error) {
				switch {
				case command == "true" && len(args) == 0:
					return host.CommandResult{}, nil
				case command == "nvidia-smi" && strings.Join(args, " ") == "-L":
					return host.CommandResult{Stdout: "GPU 0: demo"}, nil
				case command == "docker" && strings.Join(args, " ") == "info":
					return host.CommandResult{Stdout: "Docker Engine"}, nil
				case command == "test" && strings.Join(args, " ") == "-s /opt/openclaw/runtime.yaml":
					return host.CommandResult{}, nil
				case command == "cat" && strings.Join(args, " ") == "/opt/openclaw/runtime.yaml":
					return host.CommandResult{Stdout: "use_nemoclaw: true\nnim_endpoint: http://localhost:11434\nmodel: llama3.2\n"}, nil
				case command == "sh" && len(args) >= 2 && args[0] == "-lc":
					return host.CommandResult{Stdout: "ok"}, nil
				default:
					return host.CommandResult{}, errors.New("unexpected command: " + command + " " + strings.Join(args, " "))
				}
			},
		}
	}
	defer func() { newLocalExecutor = originalLocalExecutor }()

	dir := t.TempDir()
	path := filepath.Join(dir, "openclaw.yaml")
	writeConfig(t, path, `
platform:
  name: aws
region:
  name: us-east-1
instance:
  type: g5.xlarge
  disk_size_gb: 40
image:
  name: ubuntu-24.04
runtime:
  endpoint: http://localhost:11434
  model: llama3.2
`)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"openclaw", "--config", path, "verify"}

	app := New()
	cmd := newRootCommand(app)
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	got := stdout.String()
	for _, fragment := range []string{"verification summary", "gpu visibility: PASS", "all required checks passed"} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("stdout = %q, want %q", got, fragment)
		}
	}
}

func TestWaitForSSHReadyRetriesTransientErrors(t *testing.T) {
	originalTimeout := defaultSSHReadyTimeout
	originalInitialWait := defaultSSHReadyInitialWait
	originalMaxWait := defaultSSHReadyMaxWait
	defaultSSHReadyTimeout = 500 * time.Millisecond
	defaultSSHReadyInitialWait = 10 * time.Millisecond
	defaultSSHReadyMaxWait = 10 * time.Millisecond
	defer func() {
		defaultSSHReadyTimeout = originalTimeout
		defaultSSHReadyInitialWait = originalInitialWait
		defaultSSHReadyMaxWait = originalMaxWait
	}()

	attempts := 0
	exec := flexibleExecutor{
		run: func(command string, args ...string) (host.CommandResult, error) {
			if command != "true" || len(args) != 0 {
				return host.CommandResult{}, errors.New("unexpected command: " + command + " " + strings.Join(args, " "))
			}
			attempts++
			if attempts < 2 {
				return host.CommandResult{}, errors.New("ssh connection refused")
			}
			return host.CommandResult{}, nil
		},
	}

	if err := waitForSSHReady(context.Background(), exec, "203.0.113.10"); err != nil {
		t.Fatalf("waitForSSHReady() error = %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestWaitForBootstrapReadyRetriesTransientSSHErrors(t *testing.T) {
	restore := stubAWSProviderFactory()
	defer restore()

	original := newSSHExecutor
	attempts := 0
	newSSHExecutor = func(cfg host.SSHConfig) host.Executor {
		return flexibleExecutor{
			run: func(command string, args ...string) (host.CommandResult, error) {
				key := command + " " + strings.Join(args, " ")
				switch {
				case strings.TrimSpace(key) == "true":
					return host.CommandResult{}, nil
				case key == "test -f /opt/openclaw/bootstrap.done":
					attempts++
					if attempts == 1 {
						return host.CommandResult{}, errors.New("ssh connection timed out: verify the host address, network path, and security groups: exit status 255")
					}
					return host.CommandResult{}, nil
				case command == "sh" && len(args) >= 2 && args[0] == "-lc":
					return host.CommandResult{Stdout: "status: running"}, nil
				default:
					return host.CommandResult{}, errors.New("unexpected command: " + key)
				}
			},
		}
	}
	defer func() { newSSHExecutor = original }()

	dir := t.TempDir()
	keyPath := filepath.Join(dir, "demo.pem")
	if err := os.WriteFile(keyPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("WriteFile(key) error = %v", err)
	}
	cfg := &config.Config{
		Platform: config.PlatformConfig{Name: config.PlatformAWS},
		Region:   config.RegionConfig{Name: "us-east-1"},
		Instance: config.InstanceConfig{NetworkMode: "public"},
		Image:    config.ImageConfig{Name: "ubuntu-24.04"},
		SSH: config.SSHConfig{
			KeyName:        "demo-key",
			PrivateKeyPath: keyPath,
			CIDR:           "203.0.113.0/24",
			User:           "ubuntu",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var stdout bytes.Buffer
	if err := waitForBootstrapReady(ctx, cfg, "203.0.113.10", "", "", 22, &stdout); err != nil {
		t.Fatalf("waitForBootstrapReady() error = %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestCreateCommandRunsEndToEndWorkflow(t *testing.T) {
	restore := stubAWSProviderFactory()
	defer restore()
	restoreSourceArchive := stubSourceArchiveURL(t)
	defer restoreSourceArchive()

	originalBuildRuntimeBinary := runtimeinstall.BuildRuntimeBinaryFunc
	runtimeinstall.BuildRuntimeBinaryFunc = func(ctx context.Context) (string, error) {
		dir := t.TempDir()
		path := filepath.Join(dir, "openclaw")
		if err := os.WriteFile(path, []byte("binary"), 0o700); err != nil {
			return "", err
		}
		return path, nil
	}
	defer func() { runtimeinstall.BuildRuntimeBinaryFunc = originalBuildRuntimeBinary }()

	originalBackend := newTerraformBackend
	originalDeriveSSHPublicKey := deriveSSHPublicKeyFunc
	originalEnsureSSHPrivateKey := ensureSSHPrivateKeyFunc
	ensureSSHPrivateKeyFunc = func(ctx context.Context, privateKeyPath string) (string, error) {
		return privateKeyPath, nil
	}
	deriveSSHPublicKeyFunc = func(ctx context.Context, privateKeyPath string) (string, error) {
		return "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestPublicKey openclaw", nil
	}
	newTerraformBackend = func(profile string, cfg *config.Config) (infratf.InfraBackend, error) {
		return fakeTerraformBackend{
			output: &infratf.InfraOutput{
				InstanceID:         "i-0123456789abcdef0",
				PublicIP:           "203.0.113.10",
				PrivateIP:          "10.0.0.10",
				ConnectionInfo:     "ssh -i <your-key>.pem ubuntu@203.0.113.10",
				SecurityGroupID:    "sg-0123456789abcdef0",
				SecurityGroupRules: []string{"allow tcp/22 from 203.0.113.0/24", "allow tcp/8080 from 0.0.0.0/0"},
				Region:             cfg.Region.Name,
				NetworkMode:        "public",
			},
		}, nil
	}
	defer func() { newTerraformBackend = originalBackend }()
	defer func() { deriveSSHPublicKeyFunc = originalDeriveSSHPublicKey }()
	defer func() { ensureSSHPrivateKeyFunc = originalEnsureSSHPrivateKey }()

	original := newSSHExecutor
	newSSHExecutor = func(cfg host.SSHConfig) host.Executor {
		return flexibleExecutor{
			run: func(command string, args ...string) (host.CommandResult, error) {
				key := command + " " + strings.Join(args, " ")
				switch {
				case strings.TrimSpace(key) == "true":
					return host.CommandResult{}, nil
				case command == "test" && strings.Join(args, " ") == "-f /opt/openclaw/bootstrap.done":
					return host.CommandResult{}, nil
				case key == "nvidia-smi -L":
					return host.CommandResult{Stdout: "GPU 0: demo"}, nil
				case key == "docker info":
					return host.CommandResult{Stdout: "Docker Engine"}, nil
				case key == "docker info --format {{json .Runtimes}}":
					return host.CommandResult{Stdout: `{"nvidia":{}}`}, nil
				case key == "docker run --rm --gpus all --pull=never nvidia/cuda:12.4.1-base-ubuntu22.04 nvidia-smi":
					return host.CommandResult{Stdout: "NVIDIA-SMI"}, nil
				case command == "test" && strings.Join(args, " ") == "-s /opt/openclaw/runtime.yaml":
					return host.CommandResult{}, nil
				case command == "cat" && strings.Join(args, " ") == "/opt/openclaw/runtime.yaml":
					return host.CommandResult{Stdout: "use_nemoclaw: true\nnim_endpoint: http://localhost:11434\nmodel: llama3.2\nport: 8080\n"}, nil
				case command == "sh" && len(args) >= 2 && args[0] == "-lc" && strings.Contains(args[1], "curl --max-time 5 -fsS"):
					return host.CommandResult{Stdout: "ok"}, nil
				case command == "sh" && len(args) >= 2 && args[0] == "-lc" && strings.Contains(args[1], "docker ps --filter name='^/openclaw$'"):
					return host.CommandResult{Stdout: "openclaw Up 10 seconds"}, nil
				case command == "sh" && len(args) >= 2 && args[0] == "-lc":
					return host.CommandResult{Stdout: "ok"}, nil
				default:
					return host.CommandResult{}, errors.New("unexpected command: " + key)
				}
			},
		}
	}
	defer func() { newSSHExecutor = original }()

	dir := t.TempDir()
	keyPath := filepath.Join(dir, "demo.pem")
	if err := os.WriteFile(keyPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("WriteFile(key) error = %v", err)
	}
	path := filepath.Join(dir, "openclaw.yaml")
	writeConfig(t, path, `
platform:
  name: aws
region:
  name: us-east-1
ssh:
  key_name: demo-key
  private_key_path: `+keyPath+`
  cidr: 203.0.113.0/24
  user: ubuntu
instance:
  type: g5.xlarge
  disk_size_gb: 40
  network_mode: public
image:
  name: ubuntu-24.04
runtime:
  endpoint: http://localhost:11434
  model: llama3.2
sandbox:
  enabled: true
  network_mode: public
  use_nemoclaw: true
`)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"openclaw", "--config", path, "create"}

	app := New()
	cmd := newRootCommand(app)
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	got := stdout.String()
	for _, fragment := range []string{"instance id: i-0123456789abcdef0", "verification summary", "all required checks passed", "health url: http://203.0.113.10:8080/healthz"} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("stdout = %q, want %q", got, fragment)
		}
	}
}

func TestCreateCommandPromptsForConfigFileWhenConfigPathMissing(t *testing.T) {
	restore := stubAWSProviderFactory()
	defer restore()
	restoreSourceArchive := stubSourceArchiveURL(t)
	defer restoreSourceArchive()
	originalListAWSProfiles := listAWSProfilesFunc
	listAWSProfilesFunc = func(ctx context.Context) ([]string, error) {
		t.Fatal("unexpected AWS profile listing when config already stores a profile")
		return nil, nil
	}
	defer func() { listAWSProfilesFunc = originalListAWSProfiles }()

	originalBuildRuntimeBinary := runtimeinstall.BuildRuntimeBinaryFunc
	runtimeinstall.BuildRuntimeBinaryFunc = func(ctx context.Context) (string, error) {
		dir := t.TempDir()
		path := filepath.Join(dir, "openclaw")
		if err := os.WriteFile(path, []byte("binary"), 0o700); err != nil {
			return "", err
		}
		return path, nil
	}
	defer func() { runtimeinstall.BuildRuntimeBinaryFunc = originalBuildRuntimeBinary }()

	originalBackend := newTerraformBackend
	originalDeriveSSHPublicKey := deriveSSHPublicKeyFunc
	originalEnsureSSHPrivateKey := ensureSSHPrivateKeyFunc
	generatedProfile := ""
	ensureSSHPrivateKeyFunc = func(ctx context.Context, privateKeyPath string) (string, error) {
		return privateKeyPath, nil
	}
	deriveSSHPublicKeyFunc = func(ctx context.Context, privateKeyPath string) (string, error) {
		return "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestPublicKey openclaw", nil
	}
	newTerraformBackend = func(profile string, cfg *config.Config) (infratf.InfraBackend, error) {
		generatedProfile = profile
		return fakeTerraformBackend{
			output: &infratf.InfraOutput{
				InstanceID:         "i-0123456789abcdef0",
				PublicIP:           "203.0.113.10",
				PrivateIP:          "10.0.0.10",
				ConnectionInfo:     "ssh -i <your-key>.pem ubuntu@203.0.113.10",
				SecurityGroupID:    "sg-0123456789abcdef0",
				SecurityGroupRules: []string{"allow tcp/22 from 203.0.113.0/24", "allow tcp/8080 from 0.0.0.0/0"},
				Region:             cfg.Region.Name,
				NetworkMode:        "public",
			},
		}, nil
	}
	defer func() { newTerraformBackend = originalBackend }()
	defer func() { deriveSSHPublicKeyFunc = originalDeriveSSHPublicKey }()
	defer func() { ensureSSHPrivateKeyFunc = originalEnsureSSHPrivateKey }()

	original := newSSHExecutor
	newSSHExecutor = func(cfg host.SSHConfig) host.Executor {
		return flexibleExecutor{
			run: func(command string, args ...string) (host.CommandResult, error) {
				key := command + " " + strings.Join(args, " ")
				switch {
				case strings.TrimSpace(key) == "true":
					return host.CommandResult{}, nil
				case command == "test" && strings.Join(args, " ") == "-f /opt/openclaw/bootstrap.done":
					return host.CommandResult{}, nil
				case key == "nvidia-smi -L":
					return host.CommandResult{Stdout: "GPU 0: demo"}, nil
				case key == "docker info":
					return host.CommandResult{Stdout: "Docker Engine"}, nil
				case key == "docker info --format {{json .Runtimes}}":
					return host.CommandResult{Stdout: `{"nvidia":{}}`}, nil
				case key == "docker run --rm --gpus all --pull=never nvidia/cuda:12.4.1-base-ubuntu22.04 nvidia-smi":
					return host.CommandResult{Stdout: "NVIDIA-SMI"}, nil
				case command == "test" && strings.Join(args, " ") == "-s /opt/openclaw/runtime.yaml":
					return host.CommandResult{}, nil
				case command == "cat" && strings.Join(args, " ") == "/opt/openclaw/runtime.yaml":
					return host.CommandResult{Stdout: "use_nemoclaw: true\nnim_endpoint: http://localhost:11434\nmodel: llama3.2\nport: 9090\n"}, nil
				case command == "sh" && len(args) >= 2 && args[0] == "-lc" && strings.Contains(args[1], "curl --max-time 5 -fsS"):
					return host.CommandResult{Stdout: "ok"}, nil
				case command == "sh" && len(args) >= 2 && args[0] == "-lc" && strings.Contains(args[1], "docker ps --filter name='^/openclaw$'"):
					return host.CommandResult{Stdout: "openclaw Up 10 seconds"}, nil
				case command == "sh" && len(args) >= 2 && args[0] == "-lc":
					return host.CommandResult{Stdout: "ok"}, nil
				default:
					return host.CommandResult{}, errors.New("unexpected command: " + key)
				}
			},
		}
	}
	defer func() { newSSHExecutor = original }()

	dir := t.TempDir()
	keyPath := filepath.Join(dir, "demo.pem")
	if err := os.WriteFile(keyPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("WriteFile(key) error = %v", err)
	}
	agentsDir := filepath.Join(dir, "agents")
	if err := os.MkdirAll(filepath.Join(agentsDir, "alpha"), 0o755); err != nil {
		t.Fatalf("MkdirAll(alpha) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(agentsDir, "beta"), 0o755); err != nil {
		t.Fatalf("MkdirAll(beta) error = %v", err)
	}
	writeConfig(t, filepath.Join(agentsDir, "alpha", "config.yaml"), `
platform:
  name: aws
region:
  name: us-east-1
infra:
  aws_profile: sso-dev
ssh:
  key_name: demo-key
  private_key_path: `+keyPath+`
  cidr: 203.0.113.0/24
  user: ubuntu
instance:
  type: g5.xlarge
  disk_size_gb: 40
  network_mode: public
image:
  name: ubuntu-24.04
runtime:
  endpoint: http://localhost:11434
  model: llama3.2
  port: 8080
sandbox:
  enabled: true
  network_mode: public
  use_nemoclaw: true
`)
	writeConfig(t, filepath.Join(agentsDir, "beta", "config.yaml"), `
platform:
  name: aws
region:
  name: us-east-1
infra:
  aws_profile: sso-dev
ssh:
  key_name: demo-key
  private_key_path: `+keyPath+`
  cidr: 203.0.113.0/24
  user: ubuntu
instance:
  type: g5.xlarge
  disk_size_gb: 40
  network_mode: public
image:
  name: ubuntu-24.04
runtime:
  endpoint: http://localhost:11434
  model: llama3.2
  port: 9090
sandbox:
  enabled: true
  network_mode: public
  use_nemoclaw: true
`)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"openclaw", "--profile", "sso-dev", "create", "--agents-dir", agentsDir}

	app := New()
	cmd := newRootCommand(app)
	cmd.SetIn(strings.NewReader("2\n"))
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	got := stdout.String()
	for _, fragment := range []string{"instance id: i-0123456789abcdef0", "health url: http://203.0.113.10:9090/healthz", "verification summary"} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("stdout = %q, want %q", got, fragment)
		}
	}
	if generatedProfile != "sso-dev" {
		t.Fatalf("generated profile = %q, want sso-dev", generatedProfile)
	}
	loadedCfg, err := config.Load(filepath.Join(agentsDir, "beta", "config.yaml"))
	if err != nil {
		t.Fatalf("Load(config) error = %v", err)
	}
	if loadedCfg.Slack.RuntimeURL != "http://203.0.113.10:9090" {
		t.Fatalf("slack runtime url = %q, want http://203.0.113.10:9090", loadedCfg.Slack.RuntimeURL)
	}
}

func TestCreateCommandRefreshesSSHCIDRBeforeProvisioning(t *testing.T) {
	restore := stubAWSProviderFactory()
	defer restore()
	restoreSourceArchive := stubSourceArchiveURL(t)
	defer restoreSourceArchive()

	originalDetectSSHCIDR := detectSSHCIDR
	detectSSHCIDR = func(ctx context.Context) (string, error) {
		return "198.51.100.23", nil
	}
	defer func() { detectSSHCIDR = originalDetectSSHCIDR }()

	originalBuildRuntimeBinary := runtimeinstall.BuildRuntimeBinaryFunc
	runtimeinstall.BuildRuntimeBinaryFunc = func(ctx context.Context) (string, error) {
		dir := t.TempDir()
		path := filepath.Join(dir, "openclaw")
		if err := os.WriteFile(path, []byte("binary"), 0o700); err != nil {
			return "", err
		}
		return path, nil
	}
	defer func() { runtimeinstall.BuildRuntimeBinaryFunc = originalBuildRuntimeBinary }()

	originalBackend := newTerraformBackend
	originalDeriveSSHPublicKey := deriveSSHPublicKeyFunc
	originalEnsureSSHPrivateKey := ensureSSHPrivateKeyFunc
	capturedCIDR := ""
	ensureSSHPrivateKeyFunc = func(ctx context.Context, privateKeyPath string) (string, error) {
		return privateKeyPath, nil
	}
	deriveSSHPublicKeyFunc = func(ctx context.Context, privateKeyPath string) (string, error) {
		return "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestPublicKey openclaw", nil
	}
	newTerraformBackend = func(profile string, cfg *config.Config) (infratf.InfraBackend, error) {
		capturedCIDR = cfg.SSH.CIDR
		return fakeTerraformBackend{
			output: &infratf.InfraOutput{
				InstanceID:         "i-0123456789abcdef0",
				PublicIP:           "203.0.113.10",
				PrivateIP:          "10.0.0.10",
				ConnectionInfo:     "ssh -i <your-key>.pem ubuntu@203.0.113.10",
				SecurityGroupID:    "sg-0123456789abcdef0",
				SecurityGroupRules: []string{"allow tcp/22 from 198.51.100.23/32", "allow tcp/8080 from 0.0.0.0/0"},
				Region:             cfg.Region.Name,
				NetworkMode:        "public",
			},
		}, nil
	}
	defer func() { newTerraformBackend = originalBackend }()
	defer func() { deriveSSHPublicKeyFunc = originalDeriveSSHPublicKey }()
	defer func() { ensureSSHPrivateKeyFunc = originalEnsureSSHPrivateKey }()

	original := newSSHExecutor
	newSSHExecutor = func(cfg host.SSHConfig) host.Executor {
		return flexibleExecutor{
			run: func(command string, args ...string) (host.CommandResult, error) {
				key := command + " " + strings.Join(args, " ")
				switch {
				case strings.TrimSpace(key) == "true":
					return host.CommandResult{}, nil
				case command == "test" && strings.Join(args, " ") == "-f /opt/openclaw/bootstrap.done":
					return host.CommandResult{}, nil
				case key == "nvidia-smi -L":
					return host.CommandResult{Stdout: "GPU 0: demo"}, nil
				case key == "docker info":
					return host.CommandResult{Stdout: "Docker Engine"}, nil
				case key == "docker info --format {{json .Runtimes}}":
					return host.CommandResult{Stdout: `{"nvidia":{}}`}, nil
				case key == "docker run --rm --gpus all --pull=never nvidia/cuda:12.4.1-base-ubuntu22.04 nvidia-smi":
					return host.CommandResult{Stdout: "NVIDIA-SMI"}, nil
				case command == "test" && strings.Join(args, " ") == "-s /opt/openclaw/runtime.yaml":
					return host.CommandResult{}, nil
				case command == "cat" && strings.Join(args, " ") == "/opt/openclaw/runtime.yaml":
					return host.CommandResult{Stdout: "use_nemoclaw: true\nnim_endpoint: http://localhost:11434\nmodel: llama3.2\nport: 8080\n"}, nil
				case command == "sh" && len(args) >= 2 && args[0] == "-lc" && strings.Contains(args[1], "curl --max-time 5 -fsS"):
					return host.CommandResult{Stdout: "ok"}, nil
				case command == "sh" && len(args) >= 2 && args[0] == "-lc" && strings.Contains(args[1], "docker ps --filter name='^/openclaw$'"):
					return host.CommandResult{Stdout: "openclaw Up 10 seconds"}, nil
				case command == "sh" && len(args) >= 2 && args[0] == "-lc":
					return host.CommandResult{Stdout: "ok"}, nil
				default:
					return host.CommandResult{}, errors.New("unexpected command: " + key)
				}
			},
		}
	}
	defer func() { newSSHExecutor = original }()

	dir := t.TempDir()
	keyPath := filepath.Join(dir, "demo.pem")
	if err := os.WriteFile(keyPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("WriteFile(key) error = %v", err)
	}
	agentsDir := filepath.Join(dir, "agents")
	if err := os.MkdirAll(filepath.Join(agentsDir, "alpha"), 0o755); err != nil {
		t.Fatalf("MkdirAll(alpha) error = %v", err)
	}
	writeConfig(t, filepath.Join(agentsDir, "alpha", "config.yaml"), `
platform:
  name: aws
region:
  name: us-east-1
infra:
  aws_profile: sso-dev
ssh:
  key_name: demo-key
  private_key_path: `+keyPath+`
  cidr: 27.253.251.152/32
  user: ubuntu
instance:
  type: g5.xlarge
  disk_size_gb: 40
  network_mode: public
image:
  name: ubuntu-24.04
runtime:
  endpoint: http://localhost:11434
  model: llama3.2
  port: 8080
sandbox:
  enabled: true
  network_mode: public
  use_nemoclaw: true
`)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"openclaw", "create", "--agents-dir", agentsDir}

	app := New()
	cmd := newRootCommand(app)
	cmd.SetIn(strings.NewReader("\n"))
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if capturedCIDR != "198.51.100.23/32" {
		t.Fatalf("captured CIDR = %q, want 198.51.100.23/32", capturedCIDR)
	}
}

func TestCreateCommandCleansUpInstanceOnInstallFailure(t *testing.T) {
	restoreSourceArchive := stubSourceArchiveURL(t)
	defer restoreSourceArchive()

	originalTimeout := defaultSSHReadyTimeout
	originalInitialWait := defaultSSHReadyInitialWait
	originalMaxWait := defaultSSHReadyMaxWait
	defaultSSHReadyTimeout = 150 * time.Millisecond
	defaultSSHReadyInitialWait = 10 * time.Millisecond
	defaultSSHReadyMaxWait = 10 * time.Millisecond
	defer func() {
		defaultSSHReadyTimeout = originalTimeout
		defaultSSHReadyInitialWait = originalInitialWait
		defaultSSHReadyMaxWait = originalMaxWait
	}()

	var deletedRegion string
	var deletedInstanceID string
	originalAWSProvider := newAWSProvider
	newAWSProvider = func(profile, computeClass string) provider.CloudProvider {
		return cleanupTrackingCloudProvider{
			stubCloudProvider: stubCloudProvider{profile: profile},
			onDelete: func(region, instanceID string) {
				deletedRegion = region
				deletedInstanceID = instanceID
			},
		}
	}
	defer func() { newAWSProvider = originalAWSProvider }()

	originalBackend := newTerraformBackend
	newTerraformBackend = func(profile string, cfg *config.Config) (infratf.InfraBackend, error) {
		return fakeTerraformBackend{
			output: &infratf.InfraOutput{
				InstanceID:      "i-0123456789abcdef0",
				PublicIP:        "203.0.113.10",
				PrivateIP:       "10.0.0.10",
				ConnectionInfo:  "ssh -i <your-key>.pem ubuntu@203.0.113.10",
				SecurityGroupID: "sg-0123456789abcdef0",
				Region:          cfg.Region.Name,
				NetworkMode:     "public",
			},
		}, nil
	}
	defer func() { newTerraformBackend = originalBackend }()

	originalBuildRuntimeBinary := runtimeinstall.BuildRuntimeBinaryFunc
	runtimeinstall.BuildRuntimeBinaryFunc = func(ctx context.Context) (string, error) {
		dir := t.TempDir()
		path := filepath.Join(dir, "openclaw")
		if err := os.WriteFile(path, []byte("binary"), 0o700); err != nil {
			return "", err
		}
		return path, nil
	}
	defer func() { runtimeinstall.BuildRuntimeBinaryFunc = originalBuildRuntimeBinary }()

	originalDeriveSSHPublicKey := deriveSSHPublicKeyFunc
	originalEnsureSSHPrivateKey := ensureSSHPrivateKeyFunc
	deriveSSHPublicKeyFunc = func(ctx context.Context, privateKeyPath string) (string, error) {
		return "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestPublicKey openclaw", nil
	}
	ensureSSHPrivateKeyFunc = func(ctx context.Context, privateKeyPath string) (string, error) {
		return privateKeyPath, nil
	}
	defer func() { deriveSSHPublicKeyFunc = originalDeriveSSHPublicKey }()
	defer func() { ensureSSHPrivateKeyFunc = originalEnsureSSHPrivateKey }()

	originalSSHExecutor := newSSHExecutor
	newSSHExecutor = func(cfg host.SSHConfig) host.Executor {
		return flexibleExecutor{
			run: func(command string, args ...string) (host.CommandResult, error) {
				key := command + " " + strings.Join(args, " ")
				switch {
				case strings.TrimSpace(key) == "true":
					return host.CommandResult{}, errors.New("ssh connection timed out: verify the host address, network path, and security groups: exit status 255")
				default:
					return host.CommandResult{}, errors.New("unexpected command: " + key)
				}
			},
		}
	}
	defer func() { newSSHExecutor = originalSSHExecutor }()

	dir := t.TempDir()
	keyPath := filepath.Join(dir, "demo.pem")
	if err := os.WriteFile(keyPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("WriteFile(key) error = %v", err)
	}
	agentsDir := filepath.Join(dir, "agents")
	if err := os.MkdirAll(filepath.Join(agentsDir, "alpha"), 0o755); err != nil {
		t.Fatalf("MkdirAll(alpha) error = %v", err)
	}
	writeConfig(t, filepath.Join(agentsDir, "alpha", "config.yaml"), `
platform:
  name: aws
region:
  name: us-east-1
infra:
  aws_profile: sso-dev
ssh:
  key_name: demo-key
  private_key_path: `+keyPath+`
  cidr: 203.0.113.0/24
  user: ubuntu
instance:
  type: g5.xlarge
  disk_size_gb: 40
  network_mode: public
image:
  name: ubuntu-24.04
runtime:
  endpoint: http://localhost:11434
  model: llama3.2
sandbox:
  enabled: true
  network_mode: public
  use_nemoclaw: true
`)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"openclaw", "create", "--agents-dir", agentsDir}

	app := New()
	cmd := newRootCommand(app)
	cmd.SetIn(strings.NewReader("\n"))
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil")
	}
	if !strings.Contains(err.Error(), "create workflow failed") {
		t.Fatalf("error = %v, want create workflow failure", err)
	}
	if deletedRegion != "us-east-1" || deletedInstanceID != "i-0123456789abcdef0" {
		t.Fatalf("deleted instance = %s/%s, want us-east-1/i-0123456789abcdef0", deletedRegion, deletedInstanceID)
	}
}

func TestCreateCommandPromptsForAWSProfileWhenNotStored(t *testing.T) {
	restore := stubAWSProviderFactory()
	defer restore()
	restoreSourceArchive := stubSourceArchiveURL(t)
	defer restoreSourceArchive()
	originalListAWSProfiles := listAWSProfilesFunc
	listAWSProfilesFunc = func(ctx context.Context) ([]string, error) {
		return []string{"sso-dev", "dev"}, nil
	}
	defer func() { listAWSProfilesFunc = originalListAWSProfiles }()

	originalBuildRuntimeBinary := runtimeinstall.BuildRuntimeBinaryFunc
	runtimeinstall.BuildRuntimeBinaryFunc = func(ctx context.Context) (string, error) {
		dir := t.TempDir()
		path := filepath.Join(dir, "openclaw")
		if err := os.WriteFile(path, []byte("binary"), 0o700); err != nil {
			return "", err
		}
		return path, nil
	}
	defer func() { runtimeinstall.BuildRuntimeBinaryFunc = originalBuildRuntimeBinary }()

	originalBackend := newTerraformBackend
	originalDeriveSSHPublicKey := deriveSSHPublicKeyFunc
	originalEnsureSSHPrivateKey := ensureSSHPrivateKeyFunc
	generatedProfile := ""
	ensureSSHPrivateKeyFunc = func(ctx context.Context, privateKeyPath string) (string, error) {
		return privateKeyPath, nil
	}
	deriveSSHPublicKeyFunc = func(ctx context.Context, privateKeyPath string) (string, error) {
		return "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestPublicKey openclaw", nil
	}
	newTerraformBackend = func(profile string, cfg *config.Config) (infratf.InfraBackend, error) {
		generatedProfile = profile
		return fakeTerraformBackend{
			output: &infratf.InfraOutput{
				InstanceID:         "i-0123456789abcdef0",
				PublicIP:           "203.0.113.10",
				PrivateIP:          "10.0.0.10",
				ConnectionInfo:     "ssh -i <your-key>.pem ubuntu@203.0.113.10",
				SecurityGroupID:    "sg-0123456789abcdef0",
				SecurityGroupRules: []string{"allow tcp/22 from 203.0.113.0/24", "allow tcp/8080 from 0.0.0.0/0"},
				Region:             cfg.Region.Name,
				NetworkMode:        "public",
			},
		}, nil
	}
	defer func() { newTerraformBackend = originalBackend }()
	defer func() { deriveSSHPublicKeyFunc = originalDeriveSSHPublicKey }()
	defer func() { ensureSSHPrivateKeyFunc = originalEnsureSSHPrivateKey }()

	original := newSSHExecutor
	newSSHExecutor = func(cfg host.SSHConfig) host.Executor {
		return flexibleExecutor{
			run: func(command string, args ...string) (host.CommandResult, error) {
				key := command + " " + strings.Join(args, " ")
				switch {
				case strings.TrimSpace(key) == "true":
					return host.CommandResult{}, nil
				case command == "test" && strings.Join(args, " ") == "-f /opt/openclaw/bootstrap.done":
					return host.CommandResult{}, nil
				case key == "nvidia-smi -L":
					return host.CommandResult{Stdout: "GPU 0: demo"}, nil
				case key == "docker info":
					return host.CommandResult{Stdout: "Docker Engine"}, nil
				case key == "docker info --format {{json .Runtimes}}":
					return host.CommandResult{Stdout: `{"nvidia":{}}`}, nil
				case key == "docker run --rm --gpus all --pull=never nvidia/cuda:12.4.1-base-ubuntu22.04 nvidia-smi":
					return host.CommandResult{Stdout: "NVIDIA-SMI"}, nil
				case command == "test" && strings.Join(args, " ") == "-s /opt/openclaw/runtime.yaml":
					return host.CommandResult{}, nil
				case command == "cat" && strings.Join(args, " ") == "/opt/openclaw/runtime.yaml":
					return host.CommandResult{Stdout: "use_nemoclaw: true\nnim_endpoint: http://localhost:11434\nmodel: llama3.2\nport: 9090\n"}, nil
				case command == "sh" && len(args) >= 2 && args[0] == "-lc" && strings.Contains(args[1], "curl --max-time 5 -fsS"):
					return host.CommandResult{Stdout: "ok"}, nil
				case command == "sh" && len(args) >= 2 && args[0] == "-lc" && strings.Contains(args[1], "docker ps --filter name='^/openclaw$'"):
					return host.CommandResult{Stdout: "openclaw Up 10 seconds"}, nil
				case command == "sh" && len(args) >= 2 && args[0] == "-lc":
					return host.CommandResult{Stdout: "ok"}, nil
				default:
					return host.CommandResult{}, errors.New("unexpected command: " + key)
				}
			},
		}
	}
	defer func() { newSSHExecutor = original }()

	dir := t.TempDir()
	keyPath := filepath.Join(dir, "demo.pem")
	if err := os.WriteFile(keyPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("WriteFile(key) error = %v", err)
	}
	agentsDir := filepath.Join(dir, "agents")
	if err := os.MkdirAll(filepath.Join(agentsDir, "alpha"), 0o755); err != nil {
		t.Fatalf("MkdirAll(alpha) error = %v", err)
	}
	writeConfig(t, filepath.Join(agentsDir, "alpha", "config.yaml"), `
platform:
  name: aws
region:
  name: us-east-1
ssh:
  key_name: demo-key
  private_key_path: `+keyPath+`
  cidr: 203.0.113.0/24
  user: ubuntu
instance:
  type: g5.xlarge
  disk_size_gb: 40
  network_mode: public
image:
  name: ubuntu-24.04
runtime:
  endpoint: http://localhost:11434
  model: llama3.2
  port: 9090
sandbox:
  enabled: true
  network_mode: public
  use_nemoclaw: true
`)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"openclaw", "create", "--agents-dir", agentsDir}

	app := New()
	cmd := newRootCommand(app)
	cmd.SetIn(strings.NewReader("\n\n"))
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if generatedProfile != "sso-dev" {
		t.Fatalf("generated profile = %q, want sso-dev", generatedProfile)
	}
}

func stubAWSProviderFactory() func() {
	original := newAWSProvider
	newAWSProvider = func(profile, computeClass string) provider.CloudProvider {
		return stubCloudProvider{profile: profile}
	}
	return func() {
		newAWSProvider = original
	}
}

func stubSourceArchiveURL(t *testing.T) func() {
	t.Helper()
	original := resolveSourceArchiveURLFunc
	resolveSourceArchiveURLFunc = func(ctx context.Context, profile, region string) (string, string, error) {
		return "https://example.com/openclaw-bootstrap.tar.gz", "test-sha", nil
	}
	return func() {
		resolveSourceArchiveURLFunc = original
	}
}

type stubCloudProvider struct {
	profile string
}

type authFailingCloudProvider struct {
	stubCloudProvider
	authErr error
}

func (s authFailingCloudProvider) AuthCheck(ctx context.Context) (provider.AuthStatus, error) {
	return provider.AuthStatus{}, s.authErr
}
func (s authFailingCloudProvider) CheckAuth(ctx context.Context) (provider.AuthStatus, error) {
	return provider.AuthStatus{}, s.authErr
}

type baseImageFailingCloudProvider struct {
	stubCloudProvider
	baseImageErr error
}

func (s baseImageFailingCloudProvider) ListBaseImages(ctx context.Context, region string) ([]provider.BaseImage, error) {
	return nil, s.baseImageErr
}
func (s baseImageFailingCloudProvider) RecommendBaseImages(ctx context.Context, region, computeClass string) ([]provider.BaseImage, error) {
	return nil, s.baseImageErr
}

func (s stubCloudProvider) AuthCheck(ctx context.Context) (provider.AuthStatus, error) {
	return provider.AuthStatus{
		Profile: s.profile,
		Account: "123456789012",
		Arn:     "arn:aws:sts::123456789012:assumed-role/test-role/test-session",
		UserID:  "test-session",
	}, nil
}

func (s stubCloudProvider) CheckAuth(ctx context.Context) (provider.AuthStatus, error) {
	return s.AuthCheck(ctx)
}

func (s stubCloudProvider) ListRegions(ctx context.Context) ([]string, error) {
	return []string{"ap-northeast-1", "us-east-1", "us-west-2"}, nil
}

func (s stubCloudProvider) CheckGPUQuota(ctx context.Context, region, instanceFamily string) (provider.GPUQuotaReport, error) {
	quotaUsage := 1
	return provider.GPUQuotaReport{
		Source:         awsprovider.QuotaSourceServiceQuotas,
		Region:         region,
		InstanceFamily: instanceFamily,
		Checks: []provider.GPUQuotaCheck{{
			QuotaName:          "Running On-Demand G and VT instances",
			CurrentLimit:       2,
			CurrentUsage:       &quotaUsage,
			EstimatedRemaining: 1,
			UsageIsEstimated:   false,
		}},
		LikelyCreatable: true,
		Notes:           []string{"quota check complete"},
	}, nil
}

func (s stubCloudProvider) ListInstanceTypes(ctx context.Context, region string) ([]provider.InstanceType, error) {
	return []provider.InstanceType{{Name: "g5.xlarge"}}, nil
}

func (s stubCloudProvider) RecommendInstanceTypes(ctx context.Context, region, computeClass string) ([]provider.InstanceType, error) {
	return s.ListInstanceTypes(ctx, region)
}

func (s stubCloudProvider) ListBaseImages(ctx context.Context, region string) ([]provider.BaseImage, error) {
	return []provider.BaseImage{{
		Name:               "AWS Deep Learning AMI GPU Ubuntu 22.04",
		ID:                 "ami-0123456789abcdef0",
		Description:        "Deep Learning Base OSS Nvidia Driver GPU AMI (Ubuntu 22.04)",
		Architecture:       "x86_64",
		Owner:              "amazon",
		VirtualizationType: "hvm",
		RootDeviceType:     "ebs",
		Region:             region,
		Source:             "mock",
		SSMParameter:       "/aws/service/deeplearning/ami/x86_64/base-oss-nvidia-driver-gpu-ubuntu-22.04/latest/ami-id",
	}}, nil
}

func (s stubCloudProvider) RecommendBaseImages(ctx context.Context, region, computeClass string) ([]provider.BaseImage, error) {
	return s.ListBaseImages(ctx, region)
}

func (s stubCloudProvider) CreateInstance(ctx context.Context, req provider.CreateInstanceRequest) (*provider.Instance, error) {
	return &provider.Instance{
		ID:                 "i-0123456789abcdef0",
		Name:               "i-0123456789abcdef0",
		Region:             req.Region,
		PublicIP:           "203.0.113.10",
		PrivateIP:          "10.0.0.10",
		ConnectionInfo:     "ssh -i <your-key>.pem ubuntu@203.0.113.10",
		SecurityGroupID:    "sg-0123456789abcdef0",
		SecurityGroupRules: []string{"allow tcp/22 from 203.0.113.0/24", "allow tcp/8080 from 0.0.0.0/0"},
	}, nil
}

func (s stubCloudProvider) DeleteInstance(ctx context.Context, region, instanceID string) error {
	return nil
}

func (s stubCloudProvider) GetInstance(ctx context.Context, region, instanceID string) (*provider.Instance, error) {
	return &provider.Instance{
		ID:             instanceID,
		Name:           instanceID,
		Region:         region,
		PublicIP:       "203.0.113.10",
		PrivateIP:      "10.0.0.10",
		ConnectionInfo: "public IP: 203.0.113.10",
	}, nil
}

type infraCreateStubCloudProvider struct {
	stubCloudProvider
}

type cleanupTrackingCloudProvider struct {
	stubCloudProvider
	onDelete func(region, instanceID string)
}

func (c cleanupTrackingCloudProvider) DeleteInstance(ctx context.Context, region, instanceID string) error {
	if c.onDelete != nil {
		c.onDelete(region, instanceID)
	}
	return nil
}

type fakeTerraformBackend struct {
	output *infratf.InfraOutput
}

func (f fakeTerraformBackend) Init(ctx context.Context, workdir string) error { return nil }
func (f fakeTerraformBackend) Plan(ctx context.Context, workdir string, varsFile string) error {
	return nil
}
func (f fakeTerraformBackend) Apply(ctx context.Context, workdir string, varsFile string) error {
	return nil
}
func (f fakeTerraformBackend) Destroy(ctx context.Context, workdir string, varsFile string) error {
	return nil
}
func (f fakeTerraformBackend) Output(ctx context.Context, workdir string) (*infratf.InfraOutput, error) {
	if f.output == nil {
		return &infratf.InfraOutput{}, nil
	}
	return f.output, nil
}

type fakeSSHExecutor struct {
	results map[string]host.CommandResult
}

func (f fakeSSHExecutor) Run(ctx context.Context, command string, args ...string) (host.CommandResult, error) {
	key := strings.TrimSpace(command + " " + strings.Join(args, " "))
	if result, ok := f.results[key]; ok {
		return result, nil
	}
	return host.CommandResult{}, errors.New("unexpected command: " + key)
}

func (f fakeSSHExecutor) Upload(ctx context.Context, localPath, remotePath string) error { return nil }

type flexibleExecutor struct {
	run func(command string, args ...string) (host.CommandResult, error)
}

func (f flexibleExecutor) Run(ctx context.Context, command string, args ...string) (host.CommandResult, error) {
	if f.run == nil {
		if result, ok := defaultFlexibleCommand(command, args...); ok {
			return result, nil
		}
		return host.CommandResult{}, errors.New("no run handler configured")
	}
	result, err := f.run(command, args...)
	if err == nil {
		return result, nil
	}
	if !strings.HasPrefix(err.Error(), "unexpected command:") && !strings.Contains(err.Error(), "no run handler configured") {
		return result, err
	}
	if result, ok := defaultFlexibleCommand(command, args...); ok {
		return result, nil
	}
	return result, err
}

func (f flexibleExecutor) Upload(ctx context.Context, localPath, remotePath string) error { return nil }

func defaultFlexibleCommand(command string, args ...string) (host.CommandResult, bool) {
	key := strings.TrimSpace(command + " " + strings.Join(args, " "))
	switch {
	case key == "true":
		return host.CommandResult{}, true
	case key == "docker info":
		return host.CommandResult{Stdout: "Docker Engine"}, true
	case key == "sudo docker info":
		return host.CommandResult{Stdout: "Docker Engine"}, true
	case key == "docker info --format {{json .Runtimes}}":
		return host.CommandResult{Stdout: `{"nvidia":{}}`}, true
	case key == "docker run --rm --gpus all --pull=never nvidia/cuda:12.4.1-base-ubuntu22.04 nvidia-smi":
		return host.CommandResult{Stdout: "NVIDIA-SMI"}, true
	case key == "nvidia-smi -L":
		return host.CommandResult{Stdout: "GPU 0: demo"}, true
	case command == "sudo" && len(args) >= 2 && args[0] == "mkdir" && args[1] == "-p":
		return host.CommandResult{}, true
	case command == "sudo" && len(args) >= 2 && args[0] == "chown" && args[1] == "-R":
		return host.CommandResult{}, true
	case command == "sudo" && len(args) >= 2 && args[0] == "mv":
		return host.CommandResult{}, true
	case command == "sudo" && len(args) >= 3 && args[0] == "systemctl" && args[1] == "enable" && args[2] == "--now":
		return host.CommandResult{}, true
	case command == "sudo" && len(args) >= 1 && args[0] == "systemctl" && len(args) >= 2 && args[1] == "daemon-reload":
		return host.CommandResult{}, true
	case command == "chmod" && len(args) >= 2 && args[0] == "+x":
		return host.CommandResult{}, true
	case command == "chmod" && len(args) >= 2 && args[0] == "600":
		return host.CommandResult{}, true
	case command == "sh" && len(args) >= 2 && args[0] == "-lc":
		script := args[1]
		switch {
		case strings.Contains(script, "curl --max-time 5 -fsS"):
			return host.CommandResult{Stdout: "ok"}, true
		case strings.Contains(script, "systemctl is-active --quiet openclaw"):
			return host.CommandResult{Stdout: "openclaw systemd service is active"}, true
		case strings.Contains(script, "docker ps --filter name='^/openclaw$'"):
			return host.CommandResult{Stdout: "openclaw Up 10 seconds"}, true
		case strings.Contains(script, "apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y docker.io"):
			return host.CommandResult{Stdout: "docker installed"}, true
		}
	case command == "sh" && len(args) >= 2 && strings.Contains(strings.Join(args, " "), "/opt/openclaw/install.sh /opt/openclaw/runtime.yaml"):
		return host.CommandResult{Stdout: "OpenClaw runtime installation complete"}, true
	}
	return host.CommandResult{}, false
}

func writeConfig(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.TrimSpace(contents)+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
