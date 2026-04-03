package runtimeinstall

import (
	"context"
	"errors"
	"strings"

	"openclaw/internal/config"
	"openclaw/internal/host"
)

// Check reports a single prerequisite result.
type Check struct {
	Name        string
	Passed      bool
	Skipped     bool
	Message     string
	Remediation string
}

// PrereqReport aggregates host suitability checks.
type PrereqReport struct {
	Checks []Check
}

func (r PrereqReport) Ready() bool {
	for _, check := range r.Checks {
		if !check.Passed && !check.Skipped {
			return false
		}
	}
	return true
}

type PrereqChecker struct {
	Host          host.Executor
	RequirePython bool
	ComputeClass  string
}

func (c PrereqChecker) Check(ctx context.Context) (PrereqReport, error) {
	if c.Host == nil {
		return PrereqReport{}, errors.New("prerequisite checker requires a host executor")
	}

	checks := []Check{
		c.runCheck(ctx, "docker", []string{"info"}, "Install Docker and ensure the daemon is running.", true),
	}
	if config.EffectiveComputeClass(c.ComputeClass) == config.ComputeClassGPU {
		checks = append(checks,
			c.runCheck(ctx, "nvidia-smi", []string{"-L"}, "Install NVIDIA drivers and verify `nvidia-smi` works.", true),
			c.runDockerGPUCheck(ctx),
		)
	}
	if c.RequirePython {
		checks = append(checks, c.runPythonCheck(ctx))
	}

	return PrereqReport{Checks: checks}, nil
}

func (c PrereqChecker) runCheck(ctx context.Context, name string, args []string, remediation string, required bool) Check {
	result, err := c.Host.Run(ctx, name, args...)
	if err != nil {
		if name == "docker" {
			sudoResult, sudoErr := c.Host.Run(ctx, "sudo", append([]string{name}, args...)...)
			if sudoErr == nil {
				msg := strings.TrimSpace(sudoResult.Stdout)
				if msg == "" {
					msg = "passed"
				}
				return Check{Name: name, Passed: true, Message: msg}
			}
		}
		msg := strings.TrimSpace(result.Stderr)
		if msg == "" {
			msg = err.Error()
		}
		if !required {
			return Check{Name: name, Skipped: true, Message: msg, Remediation: remediation}
		}
		return Check{Name: name, Passed: false, Message: msg, Remediation: remediation}
	}
	return Check{Name: name, Passed: true, Message: strings.TrimSpace(result.Stdout)}
}

func (c PrereqChecker) runDockerGPUCheck(ctx context.Context) Check {
	info, err := c.Host.Run(ctx, "docker", "info", "--format", "{{json .Runtimes}}")
	if err != nil {
		sudoInfo, sudoErr := c.Host.Run(ctx, "sudo", "docker", "info", "--format", "{{json .Runtimes}}")
		if sudoErr != nil {
			return Check{
				Name:        "docker-gpu",
				Skipped:     true,
				Message:     strings.TrimSpace(info.Stderr),
				Remediation: "Install NVIDIA Container Toolkit and enable the `nvidia` runtime in Docker.",
			}
		}
		info = sudoInfo
	}

	if !strings.Contains(strings.ToLower(info.Stdout), "nvidia") {
		return Check{
			Name:        "docker-gpu",
			Skipped:     true,
			Message:     "docker does not advertise an NVIDIA runtime",
			Remediation: "Install NVIDIA Container Toolkit and enable the `nvidia` runtime in Docker.",
		}
	}

	result, err := c.Host.Run(ctx, "docker", "run", "--rm", "--gpus", "all", "--pull=never", "nvidia/cuda:12.4.1-base-ubuntu22.04", "nvidia-smi")
	if err != nil {
		result, err = c.Host.Run(ctx, "sudo", "docker", "run", "--rm", "--gpus", "all", "--pull=never", "nvidia/cuda:12.4.1-base-ubuntu22.04", "nvidia-smi")
		if err == nil {
			return Check{Name: "docker-gpu", Passed: true, Message: strings.TrimSpace(result.Stdout)}
		}
		msg := strings.TrimSpace(result.Stderr)
		if msg == "" {
			msg = err.Error()
		}
		return Check{
			Name:        "docker-gpu",
			Passed:      false,
			Message:     msg,
			Remediation: "Verify the NVIDIA container runtime is installed and the CUDA base image is available on the host.",
		}
	}
	return Check{Name: "docker-gpu", Passed: true, Message: strings.TrimSpace(result.Stdout)}
}

func (c PrereqChecker) runPythonCheck(ctx context.Context) Check {
	result, err := c.Host.Run(ctx, "python3", "--version")
	if err == nil {
		return Check{Name: "python", Passed: true, Message: strings.TrimSpace(result.Stdout + " " + result.Stderr)}
	}
	result, err = c.Host.Run(ctx, "python", "--version")
	if err == nil {
		return Check{Name: "python", Passed: true, Message: strings.TrimSpace(result.Stdout + " " + result.Stderr)}
	}
	msg := strings.TrimSpace(result.Stderr)
	if msg == "" {
		msg = err.Error()
	}
	return Check{
		Name:        "python",
		Passed:      false,
		Message:     msg,
		Remediation: "Install Python 3 and make sure `python3` or `python` is available on the host.",
	}
}
