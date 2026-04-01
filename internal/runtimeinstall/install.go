package runtimeinstall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"openclaw/internal/config"
	"openclaw/internal/host"
)

var BuildRuntimeBinaryFunc = buildRuntimeBinary

const (
	defaultRuntimeIdleTimeout         = 24 * time.Hour
	defaultRuntimeIdleShutdownCommand = "shutdown -h now"
)

// Request describes a runtime installation job.
type Request struct {
	Config        *config.Config
	UseNemoClaw   *bool
	Port          int
	WorkingDir    string
	RequirePython bool
	ComputeClass  string
}

// Result reports the outcome of an installation workflow.
type Result struct {
	WorkingDir     string
	ConfigPath     string
	ScriptPath     string
	BinaryPath     string
	ServicePath    string
	Prerequisites  PrereqReport
	CommandResults []host.CommandResult
}

// Backend defines the installer command strategy.
type Backend interface {
	Name() string
	ScriptContents(renderedConfigPath string) []byte
	EntryCommand(remoteWorkDir, configPath, scriptPath string) (string, []string)
	RequirePython() bool
}

// ShellBackend is a minimal shell-based installer backend.
type ShellBackend struct{}

func (ShellBackend) Name() string { return "shell" }

func (ShellBackend) RequirePython() bool { return false }

func (ShellBackend) ScriptContents(renderedConfigPath string) []byte {
	var buf bytes.Buffer
	buf.WriteString("#!/bin/sh\n")
	buf.WriteString("set -eu\n")
	buf.WriteString("CONFIG_PATH=\"$1\"\n")
	buf.WriteString("echo \"Installing OpenClaw runtime\"\n")
	buf.WriteString("echo \"config: $CONFIG_PATH\"\n")
	buf.WriteString("if [ ! -f \"$CONFIG_PATH\" ]; then\n")
	buf.WriteString("  echo \"missing runtime config\" >&2\n")
	buf.WriteString("  exit 1\n")
	buf.WriteString("fi\n")
	buf.WriteString("if command -v docker >/dev/null 2>&1; then\n")
	buf.WriteString("  echo \"docker: available\"\n")
	buf.WriteString("fi\n")
	buf.WriteString("echo \"OpenClaw runtime installation complete\"\n")
	return buf.Bytes()
}

func (ShellBackend) EntryCommand(remoteWorkDir, configPath, scriptPath string) (string, []string) {
	return "sh", []string{scriptPath, configPath}
}

// Installer coordinates host checks, uploads, and backend execution.
type Installer struct {
	Host    host.Executor
	Backend Backend
}

func (i Installer) Install(ctx context.Context, req Request) (Result, error) {
	if i.Host == nil {
		return Result{}, errors.New("install requires a host executor")
	}
	if req.Config == nil {
		return Result{}, errors.New("install requires a config")
	}

	backend := i.Backend
	if backend == nil {
		backend = ShellBackend{}
	}
	workingDir := strings.TrimSpace(req.WorkingDir)
	if workingDir == "" {
		workingDir = "/opt/openclaw"
	}

	report, err := PrereqChecker{
		Host:          i.Host,
		RequirePython: req.RequirePython || backend.RequirePython(),
		ComputeClass:  req.ComputeClass,
	}.Check(ctx)
	if err != nil {
		return Result{}, err
	}
	for _, check := range report.Checks {
		if !check.Passed && !check.Skipped {
			return Result{Prerequisites: report}, fmt.Errorf("%s prerequisite failed: %s", check.Name, check.Message)
		}
	}

	rendered, err := RenderRuntimeConfig(req.Config, req.UseNemoClaw, req.Port)
	if err != nil {
		return Result{}, err
	}

	tmpDir, err := os.MkdirTemp("", "openclaw-install-*")
	if err != nil {
		return Result{}, fmt.Errorf("create temporary install workspace: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	localConfigPath := filepath.Join(tmpDir, "runtime.yaml")
	if err := os.WriteFile(localConfigPath, rendered, 0o600); err != nil {
		return Result{}, fmt.Errorf("write rendered config: %w", err)
	}

	localScriptPath := filepath.Join(tmpDir, "install.sh")
	script := backend.ScriptContents(localConfigPath)
	if err := os.WriteFile(localScriptPath, script, 0o700); err != nil {
		return Result{}, fmt.Errorf("write install script: %w", err)
	}

	if _, err := i.Host.Run(ctx, "mkdir", "-p", workingDir); err != nil {
		return Result{}, fmt.Errorf("prepare working directory %q: %w", workingDir, err)
	}

	remoteConfigPath := pathJoin(workingDir, "runtime.yaml")
	remoteScriptPath := pathJoin(workingDir, "install.sh")
	if err := i.Host.Upload(ctx, localConfigPath, remoteConfigPath); err != nil {
		return Result{}, fmt.Errorf("upload runtime config: %w", err)
	}
	if err := i.Host.Upload(ctx, localScriptPath, remoteScriptPath); err != nil {
		return Result{}, fmt.Errorf("upload install script: %w", err)
	}

	if _, err := i.Host.Run(ctx, "chmod", "+x", remoteScriptPath); err != nil {
		return Result{}, fmt.Errorf("prepare install script: %w", err)
	}

	command, args := backend.EntryCommand(workingDir, remoteConfigPath, remoteScriptPath)
	cmdResult, err := i.Host.Run(ctx, command, args...)
	if err != nil {
		return Result{
			WorkingDir:     workingDir,
			ConfigPath:     remoteConfigPath,
			ScriptPath:     remoteScriptPath,
			Prerequisites:  report,
			CommandResults: []host.CommandResult{cmdResult},
		}, fmt.Errorf("run install backend %q: %w", backend.Name(), err)
	}

	serviceResult, err := i.installService(ctx, req, workingDir)
	if err != nil {
		return Result{
			WorkingDir:     workingDir,
			ConfigPath:     remoteConfigPath,
			ScriptPath:     remoteScriptPath,
			Prerequisites:  report,
			CommandResults: []host.CommandResult{cmdResult},
		}, err
	}

	return Result{
		WorkingDir:     workingDir,
		ConfigPath:     remoteConfigPath,
		ScriptPath:     remoteScriptPath,
		BinaryPath:     serviceResult.BinaryPath,
		ServicePath:    serviceResult.ServicePath,
		Prerequisites:  report,
		CommandResults: []host.CommandResult{cmdResult},
	}, nil
}

type serviceInstallResult struct {
	BinaryPath  string
	ServicePath string
}

func (i Installer) installService(ctx context.Context, req Request, workingDir string) (serviceInstallResult, error) {
	localBinaryPath, err := BuildRuntimeBinaryFunc(ctx)
	if err != nil {
		return serviceInstallResult{}, err
	}

	remoteBinaryPath := pathJoin(workingDir, "bin", "openclaw")
	remoteServicePath := "/etc/systemd/system/openclaw.service"

	if err := i.Host.Upload(ctx, localBinaryPath, remoteBinaryPath); err != nil {
		return serviceInstallResult{}, fmt.Errorf("upload openclaw runtime binary: %w", err)
	}

	listenPort := req.Config.Runtime.Port
	if listenPort <= 0 {
		listenPort = 8080
	}
	unitContents := renderSystemdUnit(
		remoteBinaryPath,
		pathJoin(workingDir, "runtime.yaml"),
		listenPort,
		defaultRuntimeIdleTimeout,
		defaultRuntimeIdleShutdownCommand,
	)

	tmpDir, err := os.MkdirTemp("", "openclaw-systemd-*")
	if err != nil {
		return serviceInstallResult{}, fmt.Errorf("create temporary service workspace: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	localUnitPath := filepath.Join(tmpDir, "openclaw.service")
	if err := os.WriteFile(localUnitPath, []byte(unitContents), 0o600); err != nil {
		return serviceInstallResult{}, fmt.Errorf("write systemd unit: %w", err)
	}
	if err := i.Host.Upload(ctx, localUnitPath, remoteServicePath); err != nil {
		return serviceInstallResult{}, fmt.Errorf("upload systemd unit: %w", err)
	}

	if _, err := i.Host.Run(ctx, "systemctl", "daemon-reload"); err != nil {
		return serviceInstallResult{}, fmt.Errorf("reload systemd: %w", err)
	}
	if _, err := i.Host.Run(ctx, "systemctl", "enable", "--now", "openclaw.service"); err != nil {
		return serviceInstallResult{}, fmt.Errorf("enable openclaw service: %w", err)
	}

	return serviceInstallResult{
		BinaryPath:  remoteBinaryPath,
		ServicePath: remoteServicePath,
	}, nil
}

func buildRuntimeBinary(ctx context.Context) (string, error) {
	tmpDir, err := os.MkdirTemp("", "openclaw-runtime-bin-*")
	if err != nil {
		return "", fmt.Errorf("create temporary binary workspace: %w", err)
	}

	outputPath := filepath.Join(tmpDir, "openclaw")
	cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", outputPath, "./cmd/openclaw")
	cmd.Env = append(os.Environ(),
		"GOOS=linux",
		"GOARCH=amd64",
		"CGO_ENABLED=0",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return "", fmt.Errorf("build linux runtime binary: %s: %w", msg, err)
		}
		return "", fmt.Errorf("build linux runtime binary: %w", err)
	}
	return outputPath, nil
}

func renderSystemdUnit(binaryPath, runtimeConfigPath string, listenPort int, idleTimeout time.Duration, idleShutdownCommand string) string {
	if listenPort <= 0 {
		listenPort = 8080
	}
	idleArgs := ""
	if idleTimeout > 0 {
		idleArgs = fmt.Sprintf(" --idle-timeout %s", idleTimeout)
	}
	if strings.TrimSpace(idleShutdownCommand) != "" {
		idleArgs += fmt.Sprintf(" --idle-shutdown-command %q", strings.TrimSpace(idleShutdownCommand))
	}
	return fmt.Sprintf(`[Unit]
Description=OpenClaw runtime
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=/opt/openclaw
ExecStart=%s serve --runtime-config %s --listen 0.0.0.0:%d%s
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`, binaryPath, runtimeConfigPath, listenPort, idleArgs)
}

func pathJoin(elem ...string) string {
	parts := make([]string, 0, len(elem))
	for _, part := range elem {
		parts = append(parts, strings.TrimRight(strings.TrimSpace(part), "/"))
	}
	return strings.Join(parts, "/")
}
