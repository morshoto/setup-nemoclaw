package verify

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"openclaw/internal/config"
	"openclaw/internal/host"
	"openclaw/internal/runtimeinstall"
)

const defaultRuntimeConfigPath = "/opt/openclaw/runtime.yaml"

// Check reports a single verification result.
type Check struct {
	Name        string
	Passed      bool
	Skipped     bool
	Message     string
	Remediation string
}

// Report aggregates environment verification checks.
type Report struct {
	RuntimeConfigPath string
	Checks            []Check
}

func (r Report) Failed() bool {
	for _, check := range r.Checks {
		if !check.Passed && !check.Skipped {
			return true
		}
	}
	return false
}

func (r Report) RequiredFailures() int {
	failures := 0
	for _, check := range r.Checks {
		if !check.Passed && !check.Skipped {
			failures++
		}
	}
	return failures
}

// Verifier runs host and runtime readiness checks.
type Verifier struct {
	Host host.Executor
}

// Request describes a verification run.
type Request struct {
	Config            *config.Config
	RuntimeConfigPath string
	TargetDescription string
}

func (v Verifier) Verify(ctx context.Context, req Request) (Report, error) {
	if v.Host == nil {
		return Report{}, errors.New("verification requires a host executor")
	}

	runtimeConfigPath := strings.TrimSpace(req.RuntimeConfigPath)
	if runtimeConfigPath == "" {
		runtimeConfigPath = defaultRuntimeConfigPath
	}

	report := Report{RuntimeConfigPath: runtimeConfigPath}
	report.Checks = append(report.Checks,
		runOpenClawProcessCheck(ctx, v.Host),
		runRuntimeConfigCheck(ctx, v.Host, runtimeConfigPath, req.Config),
	)
	if req.Config == nil || config.EffectiveComputeClass(req.Config.Compute.Class) == config.ComputeClassGPU {
		report.Checks = append(report.Checks, runCommandCheck(ctx, v.Host, "gpu visibility", "nvidia-smi", []string{"-L"}, "Install NVIDIA drivers and verify `nvidia-smi` works."))
	}

	if !isBedrockProvider(req.Config) {
		endpoint, err := resolveEndpoint(ctx, v.Host, runtimeConfigPath, req.Config)
		if err != nil {
			report.Checks = append(report.Checks, Check{
				Name:        "nim-endpoint",
				Passed:      false,
				Message:     err.Error(),
				Remediation: "Ensure the runtime config is present and points at a reachable NIM endpoint.",
			})
		} else {
			report.Checks = append(report.Checks, runEndpointCheck(ctx, v.Host, endpoint))
		}
	}

	port, err := resolveRuntimePort(ctx, v.Host, runtimeConfigPath, req.Config)
	if err != nil {
		report.Checks = append(report.Checks, Check{
			Name:        "openclaw health",
			Passed:      false,
			Message:     err.Error(),
			Remediation: "Ensure the runtime config defines a valid port and the OpenClaw service is listening on that port.",
		})
	} else {
		report.Checks = append(report.Checks, runOpenClawHealthCheck(ctx, v.Host, port))
	}
	if isBedrockProvider(req.Config) {
		report.Checks = append(report.Checks, runBedrockGenerateCheck(ctx, v.Host, port))
	}
	return report, nil
}

func isBedrockProvider(cfg *config.Config) bool {
	return cfg != nil && strings.EqualFold(strings.TrimSpace(cfg.Runtime.Provider), "aws-bedrock")
}

func runCommandCheck(ctx context.Context, exec host.Executor, name, command string, args []string, remediation string) Check {
	result, err := exec.Run(ctx, command, args...)
	if err != nil {
		if command == "docker" {
			sudoResult, sudoErr := exec.Run(ctx, "sudo", append([]string{command}, args...)...)
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
		return Check{Name: name, Passed: false, Message: msg, Remediation: remediation}
	}
	msg := strings.TrimSpace(result.Stdout)
	if msg == "" {
		msg = "passed"
	}
	return Check{Name: name, Passed: true, Message: msg}
}

func runRuntimeConfigCheck(ctx context.Context, exec host.Executor, runtimeConfigPath string, cfg *config.Config) Check {
	result, err := exec.Run(ctx, "test", "-s", runtimeConfigPath)
	if err != nil {
		msg := strings.TrimSpace(result.Stderr)
		if msg == "" {
			msg = "runtime config file is missing or empty"
		}
		return Check{
			Name:        "runtime config presence",
			Passed:      false,
			Message:     msg,
			Remediation: "Run `openclaw install` to generate the runtime config on the target host.",
		}
	}

	configResult, err := exec.Run(ctx, "cat", runtimeConfigPath)
	if err != nil {
		msg := strings.TrimSpace(configResult.Stderr)
		if msg == "" {
			msg = err.Error()
		}
		return Check{
			Name:        "runtime config presence",
			Passed:      false,
			Message:     msg,
			Remediation: "Run `openclaw install` to generate the runtime config on the target host.",
		}
	}

	var runtimeCfg runtimeinstall.RuntimeConfig
	if err := yaml.Unmarshal([]byte(configResult.Stdout), &runtimeCfg); err != nil {
		return Check{
			Name:        "runtime config presence",
			Passed:      false,
			Message:     fmt.Sprintf("unable to parse runtime config: %v", err),
			Remediation: "Reinstall the runtime so the generated runtime config can be validated.",
		}
	}

	msg := "runtime config is present"
	if cfg != nil {
		if strings.TrimSpace(cfg.Runtime.Endpoint) != "" && strings.TrimSpace(runtimeCfg.NIMEndpoint) != "" && strings.TrimSpace(cfg.Runtime.Endpoint) != strings.TrimSpace(runtimeCfg.NIMEndpoint) {
			return Check{
				Name:        "runtime config presence",
				Passed:      false,
				Message:     fmt.Sprintf("runtime config endpoint %q does not match config endpoint %q", runtimeCfg.NIMEndpoint, cfg.Runtime.Endpoint),
				Remediation: "Re-run `openclaw install` with the desired configuration.",
			}
		}
		if strings.TrimSpace(cfg.Runtime.Model) != "" && strings.TrimSpace(runtimeCfg.Model) != "" && strings.TrimSpace(cfg.Runtime.Model) != strings.TrimSpace(runtimeCfg.Model) {
			return Check{
				Name:        "runtime config presence",
				Passed:      false,
				Message:     fmt.Sprintf("runtime config model %q does not match config model %q", runtimeCfg.Model, cfg.Runtime.Model),
				Remediation: "Re-run `openclaw install` with the desired configuration.",
			}
		}
		msg = fmt.Sprintf("runtime config matches %s", runtimeConfigPath)
	}

	return Check{Name: "runtime config presence", Passed: true, Message: msg}
}

func resolveEndpoint(ctx context.Context, exec host.Executor, runtimeConfigPath string, cfg *config.Config) (string, error) {
	if cfg != nil && strings.TrimSpace(cfg.Runtime.Endpoint) != "" {
		return strings.TrimSpace(cfg.Runtime.Endpoint), nil
	}

	result, err := exec.Run(ctx, "cat", runtimeConfigPath)
	if err != nil {
		msg := strings.TrimSpace(result.Stderr)
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("read runtime config: %s", msg)
	}

	var runtimeCfg runtimeinstall.RuntimeConfig
	if err := yaml.Unmarshal([]byte(result.Stdout), &runtimeCfg); err != nil {
		return "", fmt.Errorf("parse runtime config: %w", err)
	}
	if strings.TrimSpace(runtimeCfg.NIMEndpoint) == "" {
		return "", errors.New("runtime config does not define a nim_endpoint")
	}
	return strings.TrimSpace(runtimeCfg.NIMEndpoint), nil
}

func runEndpointCheck(ctx context.Context, exec host.Executor, endpoint string) Check {
	script := fmt.Sprintf(`
endpoint=%s
if command -v curl >/dev/null 2>&1; then
  curl --max-time 5 -fsS "$endpoint" >/dev/null
  exit $?
fi
if command -v python3 >/dev/null 2>&1; then
  ENDPOINT="$endpoint" python3 - <<'PY'
import urllib.request
import os
urllib.request.urlopen(os.environ["ENDPOINT"], timeout=5).read()
PY
  exit $?
fi
if command -v python >/dev/null 2>&1; then
  ENDPOINT="$endpoint" python - <<'PY'
import urllib.request
import os
urllib.request.urlopen(os.environ["ENDPOINT"], timeout=5).read()
PY
  exit $?
fi
exit 127
`, strconv.Quote(strings.TrimSpace(endpoint)))

	result, err := exec.Run(ctx, "sh", "-lc", script)
	if err != nil {
		msg := strings.TrimSpace(result.Stderr)
		if msg == "" {
			msg = err.Error()
		}
		return Check{
			Name:        "nim endpoint connectivity",
			Passed:      false,
			Message:     msg,
			Remediation: "Verify the NIM endpoint is running and reachable from the target host.",
		}
	}
	msg := strings.TrimSpace(result.Stdout)
	if msg == "" {
		msg = fmt.Sprintf("reachable: %s", endpoint)
	}
	return Check{Name: "nim endpoint connectivity", Passed: true, Message: msg}
}

func runOpenClawProcessCheck(ctx context.Context, exec host.Executor) Check {
	script := `
if command -v docker >/dev/null 2>&1; then
  if docker ps --filter name='^/openclaw$' --format '{{.Names}} {{.Status}}' | grep -q '^openclaw '; then
    docker ps --filter name='^/openclaw$' --format '{{.Names}} {{.Status}}'
    exit 0
  fi
fi
if command -v systemctl >/dev/null 2>&1; then
  if systemctl is-active --quiet openclaw; then
    echo "openclaw systemd service is active"
    exit 0
  fi
fi
if command -v pgrep >/dev/null 2>&1 && pgrep -af openclaw >/dev/null 2>&1; then
  echo "openclaw process is running"
  exit 0
fi
exit 1
`
	result, err := exec.Run(ctx, "sh", "-lc", script)
	if err != nil {
		msg := strings.TrimSpace(result.Stderr)
		if msg == "" {
			msg = "openclaw process or service was not found"
		}
		return Check{
			Name:        "openclaw service/process",
			Passed:      false,
			Message:     msg,
			Remediation: "Start the OpenClaw service or process on the target host before verifying.",
		}
	}
	msg := strings.TrimSpace(result.Stdout)
	if msg == "" {
		msg = "openclaw service or process is running"
	}
	return Check{Name: "openclaw service/process", Passed: true, Message: msg}
}

func resolveRuntimePort(ctx context.Context, exec host.Executor, runtimeConfigPath string, cfg *config.Config) (int, error) {
	if cfg != nil && cfg.Runtime.Port > 0 {
		return cfg.Runtime.Port, nil
	}

	result, err := exec.Run(ctx, "cat", runtimeConfigPath)
	if err != nil {
		msg := strings.TrimSpace(result.Stderr)
		if msg == "" {
			msg = err.Error()
		}
		return 0, fmt.Errorf("read runtime config: %s", msg)
	}

	var runtimeCfg runtimeinstall.RuntimeConfig
	if err := yaml.Unmarshal([]byte(result.Stdout), &runtimeCfg); err != nil {
		return 0, fmt.Errorf("parse runtime config: %w", err)
	}
	if runtimeCfg.Port <= 0 {
		return 8080, nil
	}
	return runtimeCfg.Port, nil
}

func runOpenClawHealthCheck(ctx context.Context, exec host.Executor, port int) Check {
	if port <= 0 {
		port = 8080
	}
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/healthz", port)
	script := fmt.Sprintf(`
endpoint=%s
if command -v curl >/dev/null 2>&1; then
  curl --max-time 5 -fsS "$endpoint" >/dev/null
  exit $?
fi
if command -v python3 >/dev/null 2>&1; then
  ENDPOINT="$endpoint" python3 - <<'PY'
import urllib.request
import os
urllib.request.urlopen(os.environ["ENDPOINT"], timeout=5).read()
PY
  exit $?
fi
if command -v python >/dev/null 2>&1; then
  ENDPOINT="$endpoint" python - <<'PY'
import urllib.request
import os
urllib.request.urlopen(os.environ["ENDPOINT"], timeout=5).read()
PY
  exit $?
fi
exit 127
`, strconv.Quote(endpoint))
	result, err := exec.Run(ctx, "sh", "-lc", script)
	if err != nil {
		msg := strings.TrimSpace(result.Stderr)
		if msg == "" {
			msg = err.Error()
		}
		return Check{
			Name:        "openclaw health",
			Passed:      false,
			Message:     msg,
			Remediation: "Ensure the OpenClaw service is listening on localhost and responding to /healthz.",
		}
	}
	msg := strings.TrimSpace(result.Stdout)
	if msg == "" {
		msg = fmt.Sprintf("reachable: %s", endpoint)
	}
	return Check{Name: "openclaw health", Passed: true, Message: msg}
}

func runBedrockGenerateCheck(ctx context.Context, exec host.Executor, port int) Check {
	if port <= 0 {
		port = 8080
	}
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/v1/generate", port)
	script := fmt.Sprintf(`
endpoint=%s
payload='{"prompt":"say hello in one short sentence"}'
if command -v curl >/dev/null 2>&1; then
  curl --max-time 20 -fsS -X POST -H 'Content-Type: application/json' -d "$payload" "$endpoint" >/dev/null
  exit $?
fi
exit 127
`, strconv.Quote(endpoint))
	result, err := exec.Run(ctx, "sh", "-lc", script)
	if err != nil {
		msg := strings.TrimSpace(result.Stderr)
		if msg == "" {
			msg = err.Error()
		}
		return Check{
			Name:        "bedrock generation",
			Passed:      false,
			Message:     msg,
			Remediation: "Ensure the runtime has a Bedrock instance role and can invoke the selected model.",
		}
	}
	msg := strings.TrimSpace(result.Stdout)
	if msg == "" {
		msg = fmt.Sprintf("reachable: %s", endpoint)
	}
	return Check{Name: "bedrock generation", Passed: true, Message: msg}
}
