package app

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func agentEnvPathFromConfigPath(configPath string) string {
	configPath = strings.TrimSpace(configPath)
	if configPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(configPath), ".env")
}

func ensureAgentEnvTemplate(configPath string) (string, bool, error) {
	envPath := agentEnvPathFromConfigPath(configPath)
	if strings.TrimSpace(envPath) == "" {
		return "", false, nil
	}
	if _, err := os.Stat(envPath); err == nil {
		return envPath, false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, fmt.Errorf("stat agent env %q: %w", envPath, err)
	}

	content := strings.Join([]string{
		"# OpenClaw agent Slack environment",
		"# Fill these secrets before running `openclaw slack serve`.",
		"SLACK_BOT_TOKEN=",
		"SLACK_APP_TOKEN=",
		"",
	}, "\n")
	if err := os.WriteFile(envPath, []byte(content), 0o600); err != nil {
		return "", false, fmt.Errorf("write agent env %q: %w", envPath, err)
	}
	return envPath, true, nil
}

func loadAgentEnvFile(path string) (map[string]string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	data, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read agent env %q: %w", path, err)
	}
	defer data.Close()

	env := make(map[string]string)
	scanner := bufio.NewScanner(data)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(strings.ToLower(line), "export ") {
			line = strings.TrimSpace(line[len("export "):])
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("parse agent env %q: invalid assignment %q", path, line)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("parse agent env %q: missing key in %q", path, line)
		}
		value = strings.TrimSpace(value)
		if unquoted, err := strconv.Unquote(value); err == nil {
			value = unquoted
		} else {
			value = strings.Trim(value, `"'`)
		}
		env[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read agent env %q: %w", path, err)
	}
	return env, nil
}
