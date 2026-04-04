package runtimeinstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

func TestRenderSystemdUnitUsesRequestedPort(t *testing.T) {
	got := renderSystemdUnit("/opt/openclaw/bin/openclaw", "/opt/openclaw/runtime.yaml", 9090, 0, "", "")
	if !strings.Contains(got, "0.0.0.0:9090") {
		t.Fatalf("rendered unit %q does not use requested port", got)
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

func TestPrereqCheckerSkipsGPUChecksForCPUComputeClass(t *testing.T) {
	exec := &fakeExecutor{
		results: map[string]host.CommandResult{
			"docker info": {Stdout: "Docker Engine"},
		},
	}

	report, err := PrereqChecker{Host: exec, ComputeClass: config.ComputeClassCPU}.Check(context.Background())
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if !report.Ready() {
		t.Fatalf("report.Ready() = false, want true: %#v", report)
	}
	for _, check := range report.Checks {
		if check.Name == "nvidia-smi" || check.Name == "docker-gpu" {
			t.Fatalf("unexpected GPU check in CPU mode: %#v", report.Checks)
		}
	}
}

func TestInstallerUploadsConfigAndRunsScript(t *testing.T) {
	originalBuildRuntimeBinary := BuildRuntimeBinaryFunc
	BuildRuntimeBinaryFunc = func(ctx context.Context) (string, error) {
		dir := t.TempDir()
		path := filepath.Join(dir, "openclaw")
		if err := os.WriteFile(path, []byte("binary"), 0o700); err != nil {
			return "", err
		}
		return path, nil
	}
	defer func() { BuildRuntimeBinaryFunc = originalBuildRuntimeBinary }()

	exec := &fakeExecutor{
		results: map[string]host.CommandResult{
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
			"sudo mv /opt/openclaw/openclaw.env.upload /opt/openclaw/openclaw.env":                   {},
			"sudo chmod 600 /opt/openclaw/openclaw.env":                                              {},
			"sudo mv /opt/openclaw/openclaw.service /etc/systemd/system/openclaw.service":            {},
			"sudo systemctl daemon-reload":                                                           {},
			"sudo systemctl enable --now openclaw.service":                                           {},
		},
	}

	inst := Installer{Host: exec}
	_, err := inst.Install(context.Background(), Request{
		Config: &config.Config{
			Runtime: config.RuntimeConfig{
				Endpoint: "http://localhost:11434",
				Model:    "llama3.2",
				Provider: "codex",
				Codex:    config.CodexConfig{SecretID: "arn:aws:secretsmanager:ap-northeast-1:123456789012:secret:openclaw/codex-api-key"},
			},
			Sandbox: config.SandboxConfig{Enabled: true, NetworkMode: "private", UseNemoClaw: true},
		},
		WorkingDir:  "/opt/openclaw",
		CodexAPIKey: "sk-test",
	})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if len(exec.uploads) != 5 {
		t.Fatalf("uploads = %#v, want 5 uploads", exec.uploads)
	}
}

func TestInstallerSkipsCodexEnvWhenSecretIsUnavailable(t *testing.T) {
	originalBuildRuntimeBinary := BuildRuntimeBinaryFunc
	BuildRuntimeBinaryFunc = func(ctx context.Context) (string, error) {
		dir := t.TempDir()
		path := filepath.Join(dir, "openclaw")
		if err := os.WriteFile(path, []byte("binary"), 0o700); err != nil {
			return "", err
		}
		return path, nil
	}
	defer func() { BuildRuntimeBinaryFunc = originalBuildRuntimeBinary }()

	exec := &fakeExecutor{
		results: map[string]host.CommandResult{
			"docker info":   {Stdout: "Docker Engine"},
			"nvidia-smi -L": {Stdout: "GPU 0: demo"},
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

	inst := Installer{Host: exec}
	_, err := inst.Install(context.Background(), Request{
		Config: &config.Config{
			Runtime: config.RuntimeConfig{
				Endpoint: "http://localhost:11434",
				Model:    "llama3.2",
				Provider: "codex",
			},
			Sandbox: config.SandboxConfig{Enabled: true, NetworkMode: "private", UseNemoClaw: true},
		},
		WorkingDir: "/opt/openclaw",
	})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if len(exec.uploads) != 4 {
		t.Fatalf("uploads = %#v, want 4 uploads", exec.uploads)
	}
	for _, upload := range exec.uploads {
		if strings.Contains(upload.remote, "openclaw.env") {
			t.Fatalf("unexpected codex env upload: %#v", upload)
		}
	}
}

func TestInstallerSkipsGPUChecksForCPUComputeClass(t *testing.T) {
	originalBuildRuntimeBinary := BuildRuntimeBinaryFunc
	BuildRuntimeBinaryFunc = func(ctx context.Context) (string, error) {
		dir := t.TempDir()
		path := filepath.Join(dir, "openclaw")
		if err := os.WriteFile(path, []byte("binary"), 0o700); err != nil {
			return "", err
		}
		return path, nil
	}
	defer func() { BuildRuntimeBinaryFunc = originalBuildRuntimeBinary }()

	exec := &fakeExecutor{
		results: map[string]host.CommandResult{
			"docker info":                                                                 {Stdout: "Docker Engine"},
			"sudo mkdir -p /opt/openclaw":                                                 {},
			"chmod +x /opt/openclaw/install.sh":                                           {},
			"sh /opt/openclaw/install.sh /opt/openclaw/runtime.yaml":                      {Stdout: "OpenClaw runtime installation complete"},
			"sudo mkdir -p /opt/openclaw/bin":                                             {},
			"sudo chown -R ubuntu:ubuntu /opt/openclaw":                                   {},
			"sudo mv /opt/openclaw/openclaw.upload /opt/openclaw/bin/openclaw":            {},
			"chmod +x /opt/openclaw/bin/openclaw":                                         {},
			"sudo mv /opt/openclaw/openclaw.service /etc/systemd/system/openclaw.service": {},
			"sudo systemctl daemon-reload":                                                {},
			"sudo systemctl enable --now openclaw.service":                                {},
		},
	}

	inst := Installer{Host: exec}
	_, err := inst.Install(context.Background(), Request{
		Config: &config.Config{
			Compute: config.ComputeConfig{Class: config.ComputeClassCPU},
			Runtime: config.RuntimeConfig{Endpoint: "https://nim.example.com", Model: "llama3.2"},
			Sandbox: config.SandboxConfig{Enabled: true, NetworkMode: "public", UseNemoClaw: true},
		},
		WorkingDir:   "/opt/openclaw",
		ComputeClass: config.ComputeClassCPU,
	})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	for _, key := range []string{
		"nvidia-smi -L",
		"docker info --format {{json .Runtimes}}",
		"docker run --rm --gpus all --pull=never nvidia/cuda:12.4.1-base-ubuntu22.04 nvidia-smi",
	} {
		if _, ok := exec.results[key]; ok {
			t.Fatalf("unexpected GPU prereq command %q in CPU mode", key)
		}
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
