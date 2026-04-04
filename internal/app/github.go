package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var (
	runGHAuthStatusFunc = runGHAuthStatus
	runGHAuthLoginFunc  = runGHAuthLogin
	listGHSSHKeysFunc   = listGHSSHKeys
	addGHSSHKeyFunc     = addGHSSHKey
)

func ensureGitHubSSHAccess(ctx context.Context, privateKeyPath string) error {
	privateKeyPath = strings.TrimSpace(privateKeyPath)
	if privateKeyPath == "" {
		return errors.New("github ssh private key path is required")
	}

	if _, err := exec.LookPath("gh"); err != nil {
		return errors.New("gh is required to authenticate with GitHub")
	}

	authenticated, err := runGHAuthStatusFunc(ctx)
	if err != nil {
		return err
	}
	if !authenticated {
		if err := runGHAuthLoginFunc(ctx); err != nil {
			return err
		}
	}

	publicKey, err := deriveSSHPublicKeyFunc(ctx, privateKeyPath)
	if err != nil {
		return err
	}

	exists, err := githubSSHKeyExists(ctx, publicKey)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	tmpDir, err := os.MkdirTemp("", "openclaw-github-key-*")
	if err != nil {
		return fmt.Errorf("create temporary github key workspace: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	publicKeyPath := filepath.Join(tmpDir, "id_ed25519.pub")
	if err := os.WriteFile(publicKeyPath, []byte(publicKey+"\n"), 0o600); err != nil {
		return fmt.Errorf("write github public key: %w", err)
	}
	if err := addGHSSHKeyFunc(ctx, publicKeyPath); err != nil {
		return err
	}
	return nil
}

func githubSSHKeyExists(ctx context.Context, publicKey string) (bool, error) {
	keys, err := listGHSSHKeysFunc(ctx)
	if err != nil {
		return false, err
	}
	needle := strings.TrimSpace(publicKey)
	for _, key := range keys {
		if strings.TrimSpace(key) == needle {
			return true, nil
		}
	}
	return false, nil
}

func runGHAuthStatus(ctx context.Context) (bool, error) {
	cmd := exec.CommandContext(ctx, "gh", "auth", "status", "--hostname", "github.com")
	if err := cmd.Run(); err != nil {
		return false, nil
	}
	return true, nil
}

func runGHAuthLogin(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "gh", "auth", "login", "--web", "--git-protocol", "ssh", "--skip-ssh-key", "--scopes", "admin:public_key")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run gh auth login: %w", err)
	}
	return nil
}

func listGHSSHKeys(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, "gh", "api", "user/keys", "--jq", ".[].key")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return nil, fmt.Errorf("list github ssh keys: %s: %w", msg, err)
		}
		return nil, fmt.Errorf("list github ssh keys: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	keys := make([]string, 0, len(lines))
	for _, line := range lines {
		if key := strings.TrimSpace(line); key != "" {
			keys = append(keys, key)
		}
	}
	return keys, nil
}

func addGHSSHKey(ctx context.Context, publicKeyPath string) error {
	title := "openclaw"
	if host, err := os.Hostname(); err == nil && strings.TrimSpace(host) != "" {
		title = fmt.Sprintf("openclaw-%s", host)
	}
	cmd := exec.CommandContext(ctx, "gh", "ssh-key", "add", publicKeyPath, "--title", title, "--type", "authentication")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("add github ssh key: %w", err)
	}
	return nil
}
