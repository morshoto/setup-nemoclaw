package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"

	"openclaw/internal/runtime"
)

func newRootCommand() *cobra.Command {
	opts := Options{}
	rootCmd := &cobra.Command{
		Use:   "openclaw",
		Short: "OpenClaw CLI",
	}

	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		ctx, _, err := applyRuntime(cmd.Context(), opts)
		if err != nil {
			return err
		}
		cmd.SetContext(ctx)
		rootCmd.SetContext(ctx)
		return nil
	}

	rootCmd.PersistentFlags().BoolVar(&opts.Verbose, "verbose", false, "enable informational logs")
	rootCmd.PersistentFlags().BoolVar(&opts.Debug, "debug", false, "enable debug logging")
	rootCmd.PersistentFlags().StringVar(&opts.ConfigPath, "config", "", "path to the configuration file")
	rootCmd.PersistentFlags().StringVar(&opts.Profile, "profile", "", "AWS profile to use")

	rootCmd.AddCommand(newVersionCommand())
	rootCmd.AddCommand(newDoctorCommand())

	return rootCmd
}

func newDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check the CLI runtime wiring",
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := loggerFromContext(cmd.Context())
			logger.Debug("starting doctor check")
			logger.Info("running doctor check")
			fmt.Fprintln(cmd.OutOrStdout(), "openclaw runtime is configured")
			return nil
		},
	}
}

func loggerFromContext(ctx context.Context) *slog.Logger {
	if logger := runtime.LoggerFromContext(ctx); logger != nil {
		return logger
	}
	return slog.Default()
}
