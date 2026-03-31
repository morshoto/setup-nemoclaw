package app

import (
	"context"
	"fmt"
	"log/slog"

	"openclaw/internal/config"
	"openclaw/internal/runtime"
)

// version information is injected at build time.
var (
	Version   = "dev"
	CommitSHA  = "none"
	BuildDate  = "unknown"
)

type Options struct {
	ConfigPath string
	Profile    string
	Verbose    bool
	Debug      bool
}

func Execute() error {
	return newRootCommand().Execute()
}

func applyRuntime(ctx context.Context, opts Options) (context.Context, *config.Config, error) {
	cfg, err := config.Load(opts.ConfigPath, opts.Profile)
	if err != nil {
		return ctx, nil, err
	}

	logger := runtime.NewLogger(opts.Verbose, opts.Debug)
	slog.SetDefault(logger)
	logger.Debug("runtime initialized")

	ctx = runtime.WithLogger(ctx, logger)
	ctx = runtime.WithConfig(ctx, cfg)

	return ctx, cfg, nil
}

func versionString() string {
	return fmt.Sprintf("openclaw %s\ncommit: %s\nbuild date: %s", Version, CommitSHA, BuildDate)
}
