package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var detectSSHCIDR = defaultDetectSSHCIDR
var deriveSSHPublicKeyFunc = deriveSSHPublicKey
var ensureSSHPrivateKeyFunc = ensureSSHPrivateKeyExists

func defaultSSHPrivateKeyPath() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "~/.ssh/id_ed25519"
	}
	return filepath.Join(home, ".ssh", "id_ed25519")
}

func defaultSSHKeyName() string {
	return "openclaw"
}

func resolveSSHPrivateKeyPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("ssh private key path is required")
	}
	if strings.HasPrefix(value, "~") {
		home, err := os.UserHomeDir()
		if err == nil && strings.TrimSpace(home) != "" {
			value = filepath.Join(home, strings.TrimPrefix(value, "~/"))
		}
	}
	return value, nil
}

func deriveSSHPublicKey(ctx context.Context, privateKeyPath string) (string, error) {
	path, err := ensureSSHPrivateKeyFunc(ctx, privateKeyPath)
	if err != nil {
		return "", err
	}
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		return "", errors.New("ssh-keygen is required to derive the public key from the private key")
	}
	cmd := exec.CommandContext(ctx, "ssh-keygen", "-y", "-f", path)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("derive ssh public key from %q: %w", path, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func ensureSSHPrivateKeyExists(ctx context.Context, privateKeyPath string) (string, error) {
	path, err := resolveSSHPrivateKeyPath(privateKeyPath)
	if err != nil {
		return "", err
	}
	if _, statErr := os.Stat(path); statErr == nil {
		return path, nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return "", fmt.Errorf("read ssh private key %q: %w", path, statErr)
	}

	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		return "", errors.New("ssh-keygen is required to create the default SSH key pair")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create ssh key directory %q: %w", filepath.Dir(path), err)
	}
	cmd := exec.CommandContext(ctx, "ssh-keygen", "-t", "ed25519", "-N", "", "-f", path, "-C", "openclaw")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return "", fmt.Errorf("create ssh private key %q: %s: %w", path, msg, err)
		}
		return "", fmt.Errorf("create ssh private key %q: %w", path, err)
	}
	return path, nil
}

func resolveSSHCIDR(ctx context.Context, sshKeyName, sshCIDR string) (string, error) {
	sshKeyName = strings.TrimSpace(sshKeyName)
	sshCIDR = strings.TrimSpace(sshCIDR)
	if sshCIDR != "" {
		return normalizeSSHCIDR(sshCIDR)
	}
	if sshKeyName == "" {
		return "", nil
	}
	detected, err := detectSSHCIDR(ctx)
	if err != nil {
		return "", err
	}
	return normalizeSSHCIDR(detected)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func defaultDetectSSHCIDR(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.ipify.org", nil)
	if err != nil {
		return "", fmt.Errorf("detect public IP: %w", err)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("detect public IP: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("detect public IP: unexpected status %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("detect public IP: %w", err)
	}
	return strings.TrimSpace(string(body)), nil
}

func normalizeSSHCIDR(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("ssh cidr is required")
	}
	if strings.Contains(value, "/") {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return "", fmt.Errorf("invalid ssh cidr %q: %w", value, err)
		}
		return prefix.String(), nil
	}
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return "", fmt.Errorf("invalid ssh cidr %q: %w", value, err)
	}
	if addr.Is4() {
		return addr.String() + "/32", nil
	}
	return addr.String() + "/128", nil
}
