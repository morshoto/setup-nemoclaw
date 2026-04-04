package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"openclaw/internal/config"
	"openclaw/internal/prompt"
	"openclaw/internal/provider"
	awsprovider "openclaw/internal/provider/aws"
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

	configureRootCommandDisplay(rootCmd)
	rootCmd.AddCommand(newVersionCommand(app))
	rootCmd.AddCommand(newDoctorCommand())
	rootCmd.AddCommand(newAuthCommand(app))
	rootCmd.AddCommand(newOnboardCommand(app))
	rootCmd.AddCommand(newConfigCommand(app))
	rootCmd.AddCommand(newStatusCommand(app))
	rootCmd.AddCommand(newQuotaCommand(app))
	rootCmd.AddCommand(newSlackCommand(app))
	rootCmd.AddCommand(newInitCommand(app))
	rootCmd.AddCommand(newCreateCommand(app))
	rootCmd.AddCommand(newServeCommand(app))
	rootCmd.AddCommand(newInfraCommand(app))
	rootCmd.AddCommand(newInstallCommand(app))
	rootCmd.AddCommand(newVerifyCommand(app))

	return rootCmd
}

func newDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "doctor",
		Short:   "Check the CLI runtime wiring",
		GroupID: "inspect",
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := loggerFromContext(cmd.Context())
			logger.Debug("starting doctor check")
			logger.Info("running doctor check")
			fmt.Fprintln(cmd.OutOrStdout(), "openclaw runtime is configured")
			return nil
		},
	}
}

func newAuthCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "auth",
		Short:   "Check AWS authentication",
		GroupID: "inspect",
	}
	cmd.AddCommand(newAuthCheckCommand(app))
	return cmd
}

func newAuthCheckCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Verify AWS credentials and API access",
		RunE: func(cmd *cobra.Command, args []string) error {
			status, err := newAWSProvider(app.opts.Profile, "").CheckAuth(cmd.Context())
			if err != nil {
				return err
			}

			fmt.Fprintln(cmd.OutOrStdout(), "AWS auth check passed")
			if status.Profile != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "profile: %s\n", status.Profile)
			}
			if status.Arn != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "caller identity: %s\n", status.Arn)
			}
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
		Use:     "config",
		Short:   "Manage configuration files",
		GroupID: "setup",
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
	var agentsDir string
	var provisionNow bool

	cmd := &cobra.Command{
		Use:     "init",
		Short:   "Run the interactive setup flow",
		GroupID: "setup",
		RunE: func(cmd *cobra.Command, args []string) error {
			existing, err := existingConfig(app.opts.ConfigPath)
			if err != nil {
				return err
			}
			session := prompt.NewSession(cmd.InOrStdin(), cmd.OutOrStdout())
			wizard := setup.NewWizard(session, cmd.OutOrStdout(), nil, existing)
			wizard.ProviderFactory = func(platform, computeClass string) provider.CloudProvider {
				if platform != config.PlatformAWS {
					return nil
				}
				return newAWSProvider(wizard.AWSProfile, computeClass)
			}
			wizard.AWSProfile = app.opts.Profile
			wizard.GitHubSetup = ensureGitHubSSHAccess
			cfg, err := wizard.Run(cmd.Context())
			if err != nil {
				return err
			}
			if err := config.Validate(cfg); err != nil {
				return err
			}
			cfg.Infra.AWSProfile = strings.TrimSpace(wizard.AWSProfile)

			agentName := strings.TrimSpace(wizard.AgentName)
			if agentName == "" {
				agentName = "default"
			}
			configPath := filepath.Join(agentsDir, agentName, "config.yaml")
			if err := config.Save(configPath, cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "configuration written to %s\n", configPath)
			if envPath, created, err := ensureAgentEnvTemplate(configPath); err != nil {
				return err
			} else if created {
				fmt.Fprintf(cmd.OutOrStdout(), "environment template written to %s\n", envPath)
			}
			if !provisionNow {
				fmt.Fprintln(cmd.OutOrStdout(), "provisioning skipped")
				fmt.Fprintf(cmd.OutOrStdout(), "next step: run %s when you are ready\n", commandRef(cmd.OutOrStdout(), "openclaw", "create", "--config", configPath))
				return nil
			}

			logger := loggerFromContext(cmd.Context())
			logger.Info("starting init provision flow")
			progress := newProgressRenderer(cmd.OutOrStdout())
			instance, installResult, verifyReport, err := runCreateWorkflow(cmd.Context(), app.opts.Profile, cfg, createOptions{}, progress)
			if err != nil {
				return wrapUserFacingError(
					"init provisioning failed",
					err,
					"the create workflow failed after the configuration was written",
					"inspect the summary above",
					"run "+commandRef(cmd.OutOrStdout(), "openclaw", "create", "--config", configPath)+" once the host is ready",
				)
			}
			cfg.Slack.RuntimeURL = runtimeBaseURL(instance, cfg)
			if err := config.Save(configPath, cfg); err != nil {
				return err
			}
			printWorkflowSuccess(cmd.OutOrStdout(), instance, installResult, verifyReport, configPath, cfg, instanceTarget(instance), true)
			return nil
		},
	}

	cmd.Flags().StringVar(&agentsDir, "agents-dir", "agents", "path to the agents directory")
	cmd.Flags().BoolVar(&provisionNow, "provision", false, "provision infrastructure after writing the configuration")
	return cmd
}

func newQuotaCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "quota",
		Short:   "Inspect cloud quotas",
		GroupID: "inspect",
	}
	cmd.AddCommand(newQuotaCheckCommand(app))
	return cmd
}

func newSlackCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "slack",
		Short:   "Run Slack integration commands",
		GroupID: "integrations",
	}
	cmd.AddCommand(newSlackServeCommand(app))
	return cmd
}

func newQuotaCheckCommand(app *App) *cobra.Command {
	var platform string
	var region string
	var instanceFamily string

	cmd := &cobra.Command{
		Use:   "check",
		Short: "Check quota readiness for a GPU instance family",
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(platform) == "" {
				platform = config.PlatformAWS
			}
			if platform != config.PlatformAWS {
				return fmt.Errorf("%s quota checks are not implemented yet", platform)
			}
			if strings.TrimSpace(region) == "" {
				return errors.New("region is required")
			}
			if strings.TrimSpace(instanceFamily) == "" {
				instanceFamily = "g5"
			}

			provider := newAWSProvider(app.opts.Profile, "")
			if _, err := provider.CheckAuth(cmd.Context()); err != nil {
				return err
			}
			report, err := provider.CheckGPUQuota(cmd.Context(), region, instanceFamily)
			if err != nil {
				return err
			}

			printQuotaReport(cmd.OutOrStdout(), report)
			return nil
		},
	}

	cmd.Flags().StringVar(&platform, "platform", config.PlatformAWS, "cloud platform to inspect")
	cmd.Flags().StringVar(&region, "region", "", "region to inspect")
	cmd.Flags().StringVar(&instanceFamily, "instance-family", "g5", "GPU instance family to inspect")
	return cmd
}

var newAWSProvider = func(profile, computeClass string) provider.CloudProvider {
	return awsprovider.New(awsprovider.Config{Profile: profile, ComputeClass: computeClass})
}

func existingConfig(path string) (*config.Config, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	return config.Load(path)
}

func printQuotaReport(out io.Writer, report provider.GPUQuotaReport) {
	fmt.Fprintf(out, "Quota report for %s in %s\n", report.InstanceFamily, report.Region)
	switch report.Source {
	case awsprovider.QuotaSourceMock:
		fmt.Fprintln(out, "Data source: mock")
		fmt.Fprintln(out, "Live AWS Service Quotas integration is not wired yet.")
		fmt.Fprintln(out, "Creatability assessment: unavailable")
	default:
		if strings.TrimSpace(report.Source) != "" {
			fmt.Fprintf(out, "Data source: %s\n", report.Source)
		}
		fmt.Fprintf(out, "Likely creatable: %t\n", report.LikelyCreatable)
	}
	for _, check := range report.Checks {
		fmt.Fprintf(out, "- %s\n", check.QuotaName)
		fmt.Fprintf(out, "  current limit: %d\n", check.CurrentLimit)
		if check.CurrentUsage == nil {
			fmt.Fprintln(out, "  current usage: n/a")
		} else {
			fmt.Fprintf(out, "  current usage: %d\n", *check.CurrentUsage)
		}
		fmt.Fprintf(out, "  estimated remaining capacity: %d\n", check.EstimatedRemaining)
		if check.UsageIsEstimated {
			fmt.Fprintln(out, "  usage source: estimate")
		} else {
			fmt.Fprintln(out, "  usage source: actual")
		}
	}
	if len(report.Notes) > 0 {
		fmt.Fprintln(out, "Notes:")
		for _, note := range report.Notes {
			fmt.Fprintf(out, "- %s\n", note)
		}
	}
	if report.Source != awsprovider.QuotaSourceMock && !report.LikelyCreatable {
		fmt.Fprintln(out, "Suggested actions:")
		fmt.Fprintln(out, "- Try another region.")
		fmt.Fprintln(out, "- Request a Service Quotas increase for the relevant G-family quota.")
	}
}
