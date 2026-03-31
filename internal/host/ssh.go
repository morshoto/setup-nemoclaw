package host

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"
)

// SSHConfig describes how to reach a remote host over SSH.
type SSHConfig struct {
	Host           string
	Port           int
	User           string
	IdentityFile   string
	ConnectTimeout time.Duration
}

// SSHExecutor runs commands through the local ssh/scp clients.
type SSHExecutor struct {
	cfg SSHConfig
}

// NewSSHExecutor creates a host executor backed by ssh and scp.
func NewSSHExecutor(cfg SSHConfig) *SSHExecutor {
	if cfg.Port <= 0 {
		cfg.Port = 22
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 15 * time.Second
	}
	return &SSHExecutor{cfg: cfg}
}

func (e *SSHExecutor) Run(ctx context.Context, command string, args ...string) (CommandResult, error) {
	remoteCommand := buildRemoteCommand(command, args...)
	cmd := exec.CommandContext(ctx, "ssh", e.sshArgs(remoteCommand)...)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := CommandResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}
	if err == nil {
		return result, nil
	}

	result.ExitCode = exitCodeFromError(err)
	if result.ExitCode == 255 {
		return result, classifySSHError(stderr.String(), err)
	}
	return result, &RemoteCommandError{
		Command:  command,
		Args:     append([]string(nil), args...),
		ExitCode: result.ExitCode,
		Stderr:   stderr.String(),
		Err:      err,
	}
}

func (e *SSHExecutor) Upload(ctx context.Context, localPath, remotePath string) error {
	if strings.TrimSpace(localPath) == "" {
		return errors.New("local path is required")
	}
	if strings.TrimSpace(remotePath) == "" {
		return errors.New("remote path is required")
	}

	if dir := path.Dir(remotePath); dir != "." {
		if _, err := e.Run(ctx, "mkdir", "-p", dir); err != nil {
			return fmt.Errorf("prepare remote directory %q: %w", dir, err)
		}
	}

	args := []string{"-P", strconv.Itoa(e.cfg.Port), "-o", "BatchMode=yes", "-o", "ConnectTimeout=" + strconv.Itoa(int(e.cfg.ConnectTimeout.Seconds())), localPath, e.target() + ":" + remotePath}
	if strings.TrimSpace(e.cfg.IdentityFile) != "" {
		args = append([]string{"-i", e.cfg.IdentityFile}, args...)
	}
	cmd := exec.CommandContext(ctx, "scp", args...)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return nil
	}

	if exitCodeFromError(err) == 255 {
		return classifySSHError(stderr.String(), err)
	}
	return &RemoteCommandError{
		Command:  "scp",
		Args:     []string{localPath, remotePath},
		ExitCode: exitCodeFromError(err),
		Stderr:   stderr.String(),
		Err:      err,
	}
}

func (e *SSHExecutor) sshArgs(remoteCommand string) []string {
	args := []string{
		"-p", strconv.Itoa(e.cfg.Port),
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=" + strconv.Itoa(int(e.cfg.ConnectTimeout.Seconds())),
	}
	if strings.TrimSpace(e.cfg.IdentityFile) != "" {
		args = append(args, "-i", e.cfg.IdentityFile)
	}
	args = append(args, e.target(), remoteCommand)
	return args
}

func (e *SSHExecutor) target() string {
	if strings.TrimSpace(e.cfg.User) == "" {
		return strings.TrimSpace(e.cfg.Host)
	}
	return fmt.Sprintf("%s@%s", strings.TrimSpace(e.cfg.User), strings.TrimSpace(e.cfg.Host))
}

func buildRemoteCommand(command string, args ...string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, shellQuote(command))
	for _, arg := range args {
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if strings.IndexFunc(value, func(r rune) bool {
		return !(r == '-' || r == '_' || r == '.' || r == '/' || r == ':' || r == '@' || r == '=' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'))
	}) == -1 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func exitCodeFromError(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 0
}

// RemoteCommandError reports a remote command failure with the captured stderr.
type RemoteCommandError struct {
	Command  string
	Args     []string
	ExitCode int
	Stderr   string
	Err      error
}

func (e *RemoteCommandError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Stderr) != "" {
		return fmt.Sprintf("remote command %q failed with exit code %d: %s", e.Command, e.ExitCode, strings.TrimSpace(e.Stderr))
	}
	return fmt.Sprintf("remote command %q failed with exit code %d", e.Command, e.ExitCode)
}

func (e *RemoteCommandError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func classifySSHError(stderr string, err error) error {
	msg := strings.TrimSpace(stderr)
	lower := strings.ToLower(msg + " " + err.Error())
	switch {
	case strings.Contains(lower, "permission denied (publickey)") || strings.Contains(lower, "no supported methods remain"):
		return fmt.Errorf("ssh authentication failed: check the SSH user and private key: %w", err)
	case strings.Contains(lower, "host key verification failed"):
		return fmt.Errorf("ssh host key verification failed: remove the stale entry from known_hosts or accept the new host key: %w", err)
	case strings.Contains(lower, "connection refused"):
		return fmt.Errorf("ssh connection refused: ensure sshd is running and port 22 is reachable: %w", err)
	case strings.Contains(lower, "connection timed out") || strings.Contains(lower, "operation timed out"):
		return fmt.Errorf("ssh connection timed out: verify the host address, network path, and security groups: %w", err)
	case strings.Contains(lower, "could not resolve hostname") || strings.Contains(lower, "name or service not known"):
		return fmt.Errorf("ssh host could not be resolved: check the target host name or IP address: %w", err)
	default:
		if msg != "" {
			return fmt.Errorf("ssh connection failed: %s: %w", msg, err)
		}
		return fmt.Errorf("ssh connection failed: %w", err)
	}
}
