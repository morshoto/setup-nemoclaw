package app

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openclaw/internal/config"
	"openclaw/internal/provider"
	awsprovider "openclaw/internal/provider/aws"
)

func TestInitWritesConfigFile(t *testing.T) {
	restore := stubAWSProviderFactory()
	defer restore()

	dir := t.TempDir()
	output := filepath.Join(dir, "openclaw.yaml")
	input := strings.Join([]string{
		"1",                      // platform aws
		"",                       // accept default GPU compute mode
		"2",                      // region us-east-1
		"",                       // accept default instance g5.xlarge
		"1",                      // image ubuntu-24.04
		"20",                     // disk size
		"1",                      // network private
		"y",                      // use NemoClaw
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
		"network_mode: private",
		"use_nemoclaw: true",
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
		"1",
		"y",
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

	dir := t.TempDir()
	output := filepath.Join(dir, "openclaw.yaml")
	input := strings.Join([]string{
		"1",                      // platform aws
		"",                       // accept default GPU compute mode
		"2",                      // region us-east-1
		"",                       // accept default instance g5.xlarge
		"1",                      // image ubuntu-24.04
		"20",                     // disk size
		"1",                      // network private
		"y",                      // use NemoClaw
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

	dir := t.TempDir()
	output := filepath.Join(dir, "openclaw.yaml")
	input := strings.Join([]string{
		"1",                      // platform aws
		"",                       // accept default GPU compute mode
		"2",                      // region us-east-1
		"",                       // accept default instance g5.xlarge
		"1",                      // image ubuntu-24.04
		"20",                     // disk size
		"1",                      // network private
		"y",                      // use NemoClaw
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

	dir := t.TempDir()
	output := filepath.Join(dir, "openclaw.yaml")
	input := strings.Join([]string{
		"1",                      // platform aws
		"",                       // accept default GPU compute mode
		"2",                      // region us-east-1
		"",                       // accept default instance g5.xlarge
		"1",                      // image fallback selection
		"20",                     // disk size
		"1",                      // network private
		"y",                      // use NemoClaw
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
