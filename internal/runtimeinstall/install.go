package runtimeinstall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"openclaw/internal/config"
	"openclaw/internal/host"
)

// Request describes a runtime installation job.
type Request struct {
	Config        *config.Config
	UseNemoClaw   *bool
	Port          int
	WorkingDir    string
	RequirePython bool
}

// Result reports the outcome of an installation workflow.
type Result struct {
	WorkingDir     string
	ConfigPath     string
	ScriptPath     string
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

	return Result{
		WorkingDir:     workingDir,
		ConfigPath:     remoteConfigPath,
		ScriptPath:     remoteScriptPath,
		Prerequisites:  report,
		CommandResults: []host.CommandResult{cmdResult},
	}, nil
}

func pathJoin(elem ...string) string {
	parts := make([]string, 0, len(elem))
	for _, part := range elem {
		parts = append(parts, strings.TrimRight(strings.TrimSpace(part), "/"))
	}
	return strings.Join(parts, "/")
}
