package runtimeinstall

import (
	"context"
	"errors"
	"strings"
	"testing"

	"openclaw/internal/config"
	"openclaw/internal/host"
)

func TestRenderRuntimeConfigIncludesOptionalFields(t *testing.T) {
	cfg := &config.Config{
		Runtime: config.RuntimeConfig{
			Endpoint: "http://localhost:11434",
			Model:    "llama3.2",
			Port:     8080,
		},
		Sandbox: config.SandboxConfig{
			Enabled:         true,
			NetworkMode:     "private",
			UseNemoClaw:     true,
			FilesystemAllow: []string{"/tmp"},
		},
	}

	got, err := RenderRuntimeConfig(cfg, nil, 0)
	if err != nil {
		t.Fatalf("RenderRuntimeConfig() error = %v", err)
	}
	body := string(got)
	for _, fragment := range []string{
		"use_nemoclaw: true",
		"nim_endpoint: http://localhost:11434",
		"model: llama3.2",
		"port: 8080",
		"network_mode: private",
		"filesystem_allow:",
	} {
		if !strings.Contains(body, fragment) {
			t.Fatalf("rendered config %q missing %q", body, fragment)
		}
	}
}

func TestPrereqCheckerUsesHostExecutor(t *testing.T) {
	exec := &fakeExecutor{
		results: map[string]host.CommandResult{
			"nvidia-smi -L": {Stdout: "GPU 0: demo"},
			"docker info":   {Stdout: "Docker Engine"},
			"docker info --format {{json .Runtimes}}":                                                {Stdout: `{"nvidia":{}}`},
			"docker run --rm --gpus all --pull=never nvidia/cuda:12.4.1-base-ubuntu22.04 nvidia-smi": {Stdout: "NVIDIA-SMI"},
			"python3 --version": {Stdout: "Python 3.12.0"},
		},
	}

	report, err := PrereqChecker{Host: exec, RequirePython: true}.Check(context.Background())
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if !report.Ready() {
		t.Fatalf("report.Ready() = false, want true: %#v", report)
	}
}

func TestInstallerUploadsConfigAndRunsScript(t *testing.T) {
	exec := &fakeExecutor{
		results: map[string]host.CommandResult{
			"nvidia-smi -L": {Stdout: "GPU 0: demo"},
			"docker info":   {Stdout: "Docker Engine"},
			"docker info --format {{json .Runtimes}}":                                                {Stdout: `{"nvidia":{}}`},
			"docker run --rm --gpus all --pull=never nvidia/cuda:12.4.1-base-ubuntu22.04 nvidia-smi": {Stdout: "NVIDIA-SMI"},
			"mkdir -p /opt/openclaw":                                 {},
			"chmod +x /opt/openclaw/install.sh":                      {},
			"sh /opt/openclaw/install.sh /opt/openclaw/runtime.yaml": {Stdout: "OpenClaw runtime installation complete"},
		},
	}

	inst := Installer{Host: exec}
	_, err := inst.Install(context.Background(), Request{
		Config: &config.Config{
			Runtime: config.RuntimeConfig{Endpoint: "http://localhost:11434", Model: "llama3.2"},
			Sandbox: config.SandboxConfig{Enabled: true, NetworkMode: "private", UseNemoClaw: true},
		},
		WorkingDir: "/opt/openclaw",
	})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if len(exec.uploads) != 2 {
		t.Fatalf("uploads = %#v, want 2 uploads", exec.uploads)
	}
}

type fakeExecutor struct {
	results map[string]host.CommandResult
	uploads []uploadCall
}

type uploadCall struct {
	local  string
	remote string
}

func (f *fakeExecutor) Run(ctx context.Context, command string, args ...string) (host.CommandResult, error) {
	key := strings.TrimSpace(command + " " + strings.Join(args, " "))
	if result, ok := f.results[key]; ok {
		return result, nil
	}
	return host.CommandResult{}, errors.New("unexpected command: " + key)
}

func (f *fakeExecutor) Upload(ctx context.Context, localPath, remotePath string) error {
	f.uploads = append(f.uploads, uploadCall{local: localPath, remote: remotePath})
	return nil
}
