package runtime

import (
	"context"
	"log/slog"
	"os"
)

type contextKey string

const (
	loggerKey contextKey = "openclaw/logger"
	configKey contextKey = "openclaw/config"
)

func NewLogger(verbose, debug bool) *slog.Logger {
	level := slog.LevelWarn
	if verbose {
		level = slog.LevelInfo
	}
	if debug {
		level = slog.LevelDebug
	}

	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
	})
	return slog.New(handler)
}

func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, logger)
}

func WithConfig(ctx context.Context, cfg any) context.Context {
	return context.WithValue(ctx, configKey, cfg)
}

func LoggerFromContext(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(loggerKey).(*slog.Logger); ok && logger != nil {
		return logger
	}
	return nil
}
