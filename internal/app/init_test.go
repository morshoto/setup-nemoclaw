package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openclaw/internal/codexauth"
	"openclaw/internal/config"
	"openclaw/internal/provider"
	awsprovider "openclaw/internal/provider/aws"
)

func stubCodexSecretStore(t *testing.T) {
	t.Helper()
	original := codexauth.StoreAPIKeyFunc
	codexauth.StoreAPIKeyFunc = func(ctx context.Context, profile, region, secretName, apiKey string) (string, error) {
		return "arn:aws:secretsmanager:ap-northeast-1:123456789012:secret:openclaw/codex-api-key", nil
	}
	t.Cleanup(func() { codexauth.StoreAPIKeyFunc = original })
}

func TestInitWritesConfigFile(t *testing.T) {
	restore := stubAWSProviderFactory()
	defer restore()
	stubCodexSecretStore(t)

	dir := t.TempDir()
	output := filepath.Join(dir, "openclaw.yaml")
	input := strings.Join([]string{
		"1",                      // platform aws
		"",                       // accept default GPU compute mode
		"2",                      // region us-east-1
		"",                       // accept default instance g5.xlarge
		"1",                      // image ubuntu-24.04
		"20",                     // disk size
		"",                       // accept default public network mode
		"demo-key",               // ssh key pair name
		"/tmp/demo.pem",          // ssh private key
		"203.0.113.0/24",         // ssh cidr
		"ubuntu",                 // ssh user
		"",                       // github ssh private key path
		"y",                      // use NemoClaw
		"1",                      // provider codex
		"sk-test",                // OpenAI API key
		"http://localhost:11434", // endpoint
		"llama3.2",               // model
		"y",                      // confirm summary
	}, "\n") + "\n"

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"openclaw", "init", "--output", output}

	app := New()
	cmd := newRootCommand(app)
	cmd.SetIn(strings.NewReader(input))
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
	for _, fragment := range []string{
		"platform:",
		"name: aws",
		"region:",
		"disk_size_gb: 20",
		"image:",
		"id: ami-0123456789abcdef0",
		"network_mode: public",
		"key_name: demo-key",
		"private_key_path: /tmp/demo.pem",
		"github_private_key_path: /tmp/demo.pem",
		"cidr: 203.0.113.0/24",
		"user: ubuntu",
		"backend: terraform",
		"module_dir: infra/aws/ec2",
		"use_nemoclaw: true",
		"provider: codex",
		"secret_id: arn:aws:secretsmanager:ap-northeast-1:123456789012:secret:openclaw/codex-api-key",
		"endpoint: http://localhost:11434",
		"model: llama3.2",
	} {
		if !strings.Contains(body, fragment) {
			t.Fatalf("config file %q missing %q", body, fragment)
		}
	}
	if !strings.Contains(stdout.String(), "Summary") {
		t.Fatalf("stdout = %q, want summary", stdout.String())
	}
}

func TestInitSupportsCPUComputeMode(t *testing.T) {
	original := newAWSProvider
	newAWSProvider = func(profile, computeClass string) provider.CloudProvider {
		if computeClass == config.ComputeClassCPU {
			return cpuInitCloudProvider{stubCloudProvider: stubCloudProvider{profile: profile}}
		}
		return stubCloudProvider{profile: profile}
	}
	defer func() { newAWSProvider = original }()
	stubCodexSecretStore(t)

	dir := t.TempDir()
	output := filepath.Join(dir, "openclaw.yaml")
	input := strings.Join([]string{
		"1", // platform aws
		"1", // cpu compute mode
		"2", // region us-east-1
		"",  // accept default instance t3.xlarge
		"1", // Ubuntu 22.04 LTS
		"20",
		"", // accept default public network mode
		"demo-key",
		"/tmp/demo.pem",
		"203.0.113.0/24",
		"ubuntu",
		"",
		"y",
		"1",
		"sk-test",
		"", // accept placeholder external endpoint
		"", // accept default model
		"y",
	}, "\n") + "\n"

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"openclaw", "init", "--output", output}

	app := New()
	cmd := newRootCommand(app)
	cmd.SetIn(strings.NewReader(input))
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	loaded, err := config.Load(output)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.Compute.Class != config.ComputeClassCPU {
		t.Fatalf("loaded compute class = %q, want cpu", loaded.Compute.Class)
	}
	if loaded.Instance.Type != "t3.xlarge" {
		t.Fatalf("loaded instance type = %q, want t3.xlarge", loaded.Instance.Type)
	}
	if loaded.Image.Name != "Ubuntu 22.04 LTS" {
		t.Fatalf("loaded image = %q, want Ubuntu 22.04 LTS", loaded.Image.Name)
	}
	if loaded.Sandbox.NetworkMode != "public" {
		t.Fatalf("loaded network mode = %q, want public", loaded.Sandbox.NetworkMode)
	}
	if loaded.SSH.KeyName != "demo-key" || loaded.SSH.PrivateKeyPath != "/tmp/demo.pem" {
		t.Fatalf("loaded ssh config = %#v", loaded.SSH)
	}
}

func TestInitRejectsNonAWSPlatform(t *testing.T) {
	restore := stubAWSProviderFactory()
	defer restore()

	input := strings.Join([]string{
		"2", // gcp
	}, "\n") + "\n"

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"openclaw", "init", "--output", filepath.Join(t.TempDir(), "openclaw.yaml")}

	app := New()
	cmd := newRootCommand(app)
	cmd.SetIn(strings.NewReader(input))
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil")
	}
	if !strings.Contains(err.Error(), "not implemented yet") {
		t.Fatalf("error = %v, want not implemented", err)
	}
}

func TestInitDoesNotCreateAWSProviderBeforePlatformSelection(t *testing.T) {
	called := false
	original := newAWSProvider
	newAWSProvider = func(profile, computeClass string) provider.CloudProvider {
		called = true
		return stubCloudProvider{profile: profile}
	}
	defer func() { newAWSProvider = original }()

	input := strings.Join([]string{
		"2", // gcp
	}, "\n") + "\n"

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"openclaw", "init", "--output", filepath.Join(t.TempDir(), "openclaw.yaml")}

	app := New()
	cmd := newRootCommand(app)
	cmd.SetIn(strings.NewReader(input))
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil")
	}
	if !strings.Contains(err.Error(), "not implemented yet") {
		t.Fatalf("error = %v, want not implemented", err)
	}
	if called {
		t.Fatal("AWS provider should not be created before platform selection")
	}
}

func TestInitPreselectsRegionFromExistingConfig(t *testing.T) {
	restore := stubAWSProviderFactory()
	defer restore()
	stubCodexSecretStore(t)

	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.yaml")
	writeConfig(t, existing, `
platform:
  name: aws
region:
  name: us-west-2
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
  network_mode: private
  use_nemoclaw: false
`)
	output := filepath.Join(dir, "output.yaml")
	input := strings.Join([]string{
		"1", // platform aws
		"",  // accept default GPU compute mode
		"",  // accept preselected region from existing config
		"",  // accept default instance g5.xlarge
		"1", // image
		"20",
		"",
		"demo-key",
		"/tmp/demo.pem",
		"203.0.113.0/24",
		"ubuntu",
		"",
		"y",
		"1",
		"sk-test",
		"http://localhost:11434",
		"llama3.2",
		"y",
	}, "\n") + "\n"

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"openclaw", "--config", existing, "init", "--output", output}

	app := New()
	cmd := newRootCommand(app)
	cmd.SetIn(strings.NewReader(input))
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	loaded, err := config.Load(output)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.Region.Name != "us-west-2" {
		t.Fatalf("loaded region = %q, want us-west-2", loaded.Region.Name)
	}
}

func TestInitContinuesWhenAWSAuthCheckIsPermissionDenied(t *testing.T) {
	original := newAWSProvider
	newAWSProvider = func(profile, computeClass string) provider.CloudProvider {
		return authFailingCloudProvider{
			stubCloudProvider: stubCloudProvider{profile: profile},
			authErr: &awsprovider.AuthError{
				Kind:    "permission_denied",
				Profile: profile,
				Stage:   "api",
				Cause:   errors.New("AccessDenied: denied"),
			},
		}
	}
	defer func() { newAWSProvider = original }()
	stubCodexSecretStore(t)

	dir := t.TempDir()
	output := filepath.Join(dir, "openclaw.yaml")
	input := strings.Join([]string{
		"1",                      // platform aws
		"",                       // accept default GPU compute mode
		"2",                      // region us-east-1
		"",                       // accept default instance g5.xlarge
		"1",                      // image ubuntu-24.04
		"20",                     // disk size
		"",                       // accept default public network mode
		"demo-key",               // ssh key pair name
		"/tmp/demo.pem",          // ssh private key
		"203.0.113.0/24",         // ssh cidr
		"ubuntu",                 // ssh user
		"",                       // github ssh private key path
		"y",                      // use NemoClaw
		"1",                      // provider codex
		"sk-test",                // OpenAI API key
		"http://localhost:11434", // endpoint
		"llama3.2",               // model
		"y",                      // confirm summary
	}, "\n") + "\n"

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"openclaw", "init", "--output", output}

	app := New()
	cmd := newRootCommand(app)
	cmd.SetIn(strings.NewReader(input))
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := stdout.String(); !strings.Contains(got, "Warning: AWS auth check unavailable; continuing.") {
		t.Fatalf("stdout = %q, want permission-denied warning", got)
	}
}

func TestInitContinuesWhenAWSAuthCheckFailsAtSTS(t *testing.T) {
	original := newAWSProvider
	newAWSProvider = func(profile, computeClass string) provider.CloudProvider {
		return authFailingCloudProvider{
			stubCloudProvider: stubCloudProvider{profile: profile},
			authErr: &awsprovider.AuthError{
				Kind:    "api_call_failed",
				Profile: profile,
				Stage:   "api",
				Cause:   errors.New("AWS auth check failed while calling sts:GetCallerIdentity"),
			},
		}
	}
	defer func() { newAWSProvider = original }()
	stubCodexSecretStore(t)

	dir := t.TempDir()
	output := filepath.Join(dir, "openclaw.yaml")
	input := strings.Join([]string{
		"1",                      // platform aws
		"",                       // accept default GPU compute mode
		"2",                      // region us-east-1
		"",                       // accept default instance g5.xlarge
		"1",                      // image ubuntu-24.04
		"20",                     // disk size
		"",                       // accept default public network mode
		"demo-key",               // ssh key pair name
		"/tmp/demo.pem",          // ssh private key
		"203.0.113.0/24",         // ssh cidr
		"ubuntu",                 // ssh user
		"",                       // github ssh private key path
		"y",                      // use NemoClaw
		"1",                      // provider codex
		"sk-test",                // OpenAI API key
		"http://localhost:11434", // endpoint
		"llama3.2",               // model
		"y",                      // confirm summary
	}, "\n") + "\n"

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"openclaw", "init", "--output", output}

	app := New()
	cmd := newRootCommand(app)
	cmd.SetIn(strings.NewReader(input))
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := stdout.String(); !strings.Contains(got, "Warning: AWS auth check unavailable; continuing.") {
		t.Fatalf("stdout = %q, want STS warning", got)
	}
}

func TestInitFallsBackWhenAWSImageLookupIsPermissionDenied(t *testing.T) {
	original := newAWSProvider
	newAWSProvider = func(profile, computeClass string) provider.CloudProvider {
		return baseImageFailingCloudProvider{
			stubCloudProvider: stubCloudProvider{profile: profile},
			baseImageErr: &awsprovider.AuthError{
				Kind:    "permission_denied",
				Profile: profile,
				Stage:   "api",
				Cause:   errors.New("UnrecognizedClientException: The security token included in the request is invalid"),
			},
		}
	}
	defer func() { newAWSProvider = original }()
	stubCodexSecretStore(t)

	dir := t.TempDir()
	output := filepath.Join(dir, "openclaw.yaml")
	input := strings.Join([]string{
		"1",                      // platform aws
		"",                       // accept default GPU compute mode
		"2",                      // region us-east-1
		"",                       // accept default instance g5.xlarge
		"1",                      // image fallback selection
		"20",                     // disk size
		"",                       // accept default public network mode
		"demo-key",               // ssh key pair name
		"/tmp/demo.pem",          // ssh private key
		"203.0.113.0/24",         // ssh cidr
		"ubuntu",                 // ssh user
		"",                       // github ssh private key path
		"y",                      // use NemoClaw
		"1",                      // provider codex
		"sk-test",                // OpenAI API key
		"http://localhost:11434", // endpoint
		"llama3.2",               // model
		"y",                      // confirm summary
	}, "\n") + "\n"

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"openclaw", "init", "--output", output}

	app := New()
	cmd := newRootCommand(app)
	cmd.SetIn(strings.NewReader(input))
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	got := stdout.String()
	for _, fragment := range []string{
		"Warning: AWS image lookup unavailable; using bundled fallback images.",
		"Summary",
		"image: AWS Deep Learning AMI GPU Ubuntu 22.04",
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("stdout = %q, want %q", got, fragment)
		}
	}
}

type cpuInitCloudProvider struct {
	stubCloudProvider
}

func (cpuInitCloudProvider) ListBaseImages(ctx context.Context, region string) ([]provider.BaseImage, error) {
	return []provider.BaseImage{{
		Name:               "Ubuntu 22.04 LTS",
		ID:                 "ami-0ubuntu1234567890",
		Architecture:       "x86_64",
		Owner:              "canonical",
		VirtualizationType: "hvm",
		RootDeviceType:     "ebs",
		Region:             region,
		Source:             "mock",
		SSMParameter:       "/aws/service/canonical/ubuntu/server/22.04/stable/current/amd64/hvm/ebs-gp2/ami-id",
	}}, nil
}

func (cpuInitCloudProvider) RecommendBaseImages(ctx context.Context, region, computeClass string) ([]provider.BaseImage, error) {
	return []provider.BaseImage{{
		Name:               "Ubuntu 22.04 LTS",
		ID:                 "ami-0ubuntu1234567890",
		Architecture:       "x86_64",
		Owner:              "canonical",
		VirtualizationType: "hvm",
		RootDeviceType:     "ebs",
		Region:             region,
		Source:             "mock",
		SSMParameter:       "/aws/service/canonical/ubuntu/server/22.04/stable/current/amd64/hvm/ebs-gp2/ami-id",
	}}, nil
}

func (cpuInitCloudProvider) ListInstanceTypes(ctx context.Context, region string) ([]provider.InstanceType, error) {
	return []provider.InstanceType{
		{Name: "t3.xlarge", MemoryGB: 16},
		{Name: "t3.2xlarge", MemoryGB: 32},
	}, nil
}

func (cpuInitCloudProvider) RecommendInstanceTypes(ctx context.Context, region, computeClass string) ([]provider.InstanceType, error) {
	return []provider.InstanceType{
		{Name: "t3.xlarge", MemoryGB: 16},
		{Name: "t3.2xlarge", MemoryGB: 32},
	}, nil
}
