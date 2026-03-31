package host

import "context"

// CommandResult is the output of a remote command execution.
type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Executor runs commands and uploads files on a remote host.
type Executor interface {
	Run(ctx context.Context, command string, args ...string) (CommandResult, error)
	Upload(ctx context.Context, localPath, remotePath string) error
}
