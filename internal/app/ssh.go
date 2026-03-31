package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

var detectSSHCIDR = defaultDetectSSHCIDR

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
