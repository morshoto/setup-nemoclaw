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

func TestInfraCreateCommandReportsCreatedInstance(t *testing.T) {
	original := newAWSProvider
	newAWSProvider = func(profile, computeClass string) provider.CloudProvider {
		return infraCreateStubCloudProvider{stubCloudProvider: stubCloudProvider{profile: profile}}
	}
	defer func() { newAWSProvider = original }()

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
				SecurityGroupRules: []string{"allow tcp/22 from 203.0.113.0/24"},
				Region:             cfg.Region.Name,
				NetworkMode:        "public",
			},
		}, nil
	}
	defer func() { newTerraformBackend = originalBackend }()
	defer func() { deriveSSHPublicKeyFunc = originalDeriveSSHPublicKey }()
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

func TestCreateCommandRunsEndToEndWorkflow(t *testing.T) {
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
				SecurityGroupRules: []string{"allow tcp/22 from 203.0.113.0/24"},
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
				case key == "nvidia-smi -L":
					return host.CommandResult{Stdout: "GPU 0: demo"}, nil
				case key == "docker info":
					return host.CommandResult{Stdout: "Docker Engine"}, nil
				case key == "docker info --format {{json .Runtimes}}":
					return host.CommandResult{Stdout: `{"nvidia":{}}`}, nil
				case key == "docker run --rm --gpus all --pull=never nvidia/cuda:12.4.1-base-ubuntu22.04 nvidia-smi":
					return host.CommandResult{Stdout: "NVIDIA-SMI"}, nil
				case key == "sudo mkdir -p /opt/openclaw":
					return host.CommandResult{}, nil
				case key == "chmod +x /opt/openclaw/install.sh":
					return host.CommandResult{}, nil
				case key == "sh /opt/openclaw/install.sh /opt/openclaw/runtime.yaml":
					return host.CommandResult{Stdout: "OpenClaw runtime installation complete"}, nil
				case key == "sudo mkdir -p /opt/openclaw/bin":
					return host.CommandResult{}, nil
				case key == "sudo chown -R ubuntu:ubuntu /opt/openclaw":
					return host.CommandResult{}, nil
				case key == "sudo mv /opt/openclaw/openclaw.upload /opt/openclaw/bin/openclaw":
					return host.CommandResult{}, nil
				case key == "chmod +x /opt/openclaw/bin/openclaw":
					return host.CommandResult{}, nil
				case key == "sudo mv /opt/openclaw/openclaw.service /etc/systemd/system/openclaw.service":
					return host.CommandResult{}, nil
				case key == "sudo systemctl daemon-reload":
					return host.CommandResult{}, nil
				case key == "sudo systemctl enable --now openclaw.service":
					return host.CommandResult{}, nil
				case command == "test" && strings.Join(args, " ") == "-s /opt/openclaw/runtime.yaml":
					return host.CommandResult{}, nil
				case command == "cat" && strings.Join(args, " ") == "/opt/openclaw/runtime.yaml":
					return host.CommandResult{Stdout: "use_nemoclaw: true\nnim_endpoint: http://localhost:11434\nmodel: llama3.2\n"}, nil
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
	for _, fragment := range []string{"instance id: i-0123456789abcdef0", "verification summary", "all required checks passed"} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("stdout = %q, want %q", got, fragment)
		}
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
		SecurityGroupRules: []string{"allow tcp/22 from 203.0.113.0/24"},
	}, nil
}

func (s stubCloudProvider) DeleteInstance(ctx context.Context, instanceID string) error { return nil }

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
		return host.CommandResult{}, errors.New("no run handler configured")
	}
	return f.run(command, args...)
}

func (f flexibleExecutor) Upload(ctx context.Context, localPath, remotePath string) error { return nil }

func writeConfig(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.TrimSpace(contents)+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
