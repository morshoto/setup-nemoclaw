package host

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// LocalExecutor runs commands on the current machine.
type LocalExecutor struct{}

// NewLocalExecutor creates an executor that runs commands locally.
func NewLocalExecutor() *LocalExecutor {
	return &LocalExecutor{}
}

func (e *LocalExecutor) Run(ctx context.Context, command string, args ...string) (CommandResult, error) {
	cmd := exec.CommandContext(ctx, command, args...)

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

	exitCode := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exitCode = exitErr.ExitCode()
	}
	result.ExitCode = exitCode
	if result.Stderr != "" {
		return result, fmt.Errorf("local command %q failed with exit code %d: %s", command, exitCode, result.Stderr)
	}
	return result, fmt.Errorf("local command %q failed with exit code %d: %w", command, exitCode, err)
}

func (e *LocalExecutor) Upload(ctx context.Context, localPath, remotePath string) error {
	if localPath == "" {
		return errors.New("local path is required")
	}
	if remotePath == "" {
		return errors.New("remote path is required")
	}

	src, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer src.Close()

	if err := os.MkdirAll(filepath.Dir(remotePath), 0o755); err != nil {
		return err
	}

	dst, err := os.Create(remotePath)
	if err != nil {
		return err
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return err
	}
	return nil
}
