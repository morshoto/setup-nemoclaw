package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openclaw/internal/config"
)

func TestInitWritesConfigFile(t *testing.T) {
	restore := stubAWSProviderFactory()
	defer restore()

	dir := t.TempDir()
	output := filepath.Join(dir, "openclaw.yaml")
	input := strings.Join([]string{
		"1",                      // platform aws
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
