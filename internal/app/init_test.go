package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitWritesConfigFile(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "openclaw.yaml")
	input := strings.Join([]string{
		"1",                      // platform aws
		"1",                      // region us-east-1
		"1",                      // instance t3.medium
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
