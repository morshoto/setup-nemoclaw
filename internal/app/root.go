package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"

	"openclaw/internal/config"
	"openclaw/internal/prompt"
	"openclaw/internal/runtime"
	"openclaw/internal/setup"
)

func newRootCommand(app *App) *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "openclaw",
		Short: "OpenClaw CLI",
	}

	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		ctx := app.applyRuntime(cmd.Context())
		cmd.SetContext(ctx)
		rootCmd.SetContext(ctx)
		return nil
	}

	rootCmd.PersistentFlags().BoolVar(&app.opts.Verbose, "verbose", false, "enable informational logs")
	rootCmd.PersistentFlags().BoolVar(&app.opts.Debug, "debug", false, "enable debug logging")
	rootCmd.PersistentFlags().StringVar(&app.opts.ConfigPath, "config", "", "path to the configuration file")
	rootCmd.PersistentFlags().StringVar(&app.opts.Profile, "profile", "", "AWS profile to use")

	rootCmd.AddCommand(newVersionCommand(app))
	rootCmd.AddCommand(newDoctorCommand())
	rootCmd.AddCommand(newConfigCommand(app))
	rootCmd.AddCommand(newInitCommand(app))

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

func newConfigCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage configuration files",
	}
	cmd.AddCommand(newConfigValidateCommand(app))
	return cmd
}

func newConfigValidateCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate a configuration file",
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(app.opts.ConfigPath) == "" {
				return errors.New("config file is required: pass --config <path>")
			}
			cfg, err := config.Load(app.opts.ConfigPath)
			if err != nil {
				return err
			}
			if err := config.Validate(cfg); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "configuration is valid")
			return nil
		},
	}
}

func newInitCommand(app *App) *cobra.Command {
	var outputPath string

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Run the interactive setup flow",
		RunE: func(cmd *cobra.Command, args []string) error {
			session := prompt.NewSession(cmd.InOrStdin(), cmd.OutOrStdout())
			wizard := setup.NewWizard(session, cmd.OutOrStdout())
			cfg, err := wizard.Run(cmd.Context())
			if err != nil {
				return err
			}

			if strings.TrimSpace(outputPath) == "" {
				return errors.New("output path is required")
			}
			if err := config.Save(outputPath, cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "configuration written to %s\n", outputPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&outputPath, "output", "openclaw.yaml", "path to write the generated configuration")
	return cmd
}
