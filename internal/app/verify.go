package app

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"openclaw/internal/config"
)

func newVerifyCommand(app *App) *cobra.Command {
	var target string
	var sshUser string
	var sshKey string
	var sshPort int
	var runtimeConfigPath string

	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Verify the runtime environment",
		RunE: func(cmd *cobra.Command, args []string) error {
			var cfg *config.Config
			var err error
			if strings.TrimSpace(app.opts.ConfigPath) != "" {
				cfg, err = config.Load(app.opts.ConfigPath)
				if err != nil {
					return err
				}
				if err := config.Validate(cfg); err != nil {
					return err
				}
			}

			logger := loggerFromContext(cmd.Context())
			logger.Info("starting verify workflow")
			fmt.Fprintln(cmd.OutOrStdout(), "running verification checks...")
			report, resolvedTarget, err := runVerifyWorkflow(cmd.Context(), app.opts.Profile, cfg, verifyOptions{
				Target:            target,
				SSHUser:           sshUser,
				SSHKey:            sshKey,
				SSHPort:           sshPort,
				RuntimeConfigPath: runtimeConfigPath,
			})
			printVerificationReport(cmd.OutOrStdout(), report)
			if strings.TrimSpace(resolvedTarget) != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "target: %s\n", resolvedTarget)
			}
			if err != nil {
				return wrapUserFacingError(
					"verification failed",
					err,
					"the target host is not reachable or the runtime config is missing",
					"re-run `openclaw install --target ...` to refresh the runtime",
					"check the host logs and network connectivity",
				)
			}
			if report.Failed() {
				return wrapUserFacingError(
					"verification failed",
					errors.New(fmt.Sprintf("%d required checks failed", report.RequiredFailures())),
					"one or more required readiness checks did not pass",
					"fix the failed checks and run `openclaw verify` again",
				)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&target, "target", "", "SSH target host or EC2 instance id")
	cmd.Flags().StringVar(&sshUser, "ssh-user", "", "SSH username for the target host")
	cmd.Flags().StringVar(&sshKey, "ssh-key", "", "path to the SSH private key")
	cmd.Flags().IntVar(&sshPort, "ssh-port", 22, "SSH port")
	cmd.Flags().StringVar(&runtimeConfigPath, "runtime-config", "/opt/openclaw/runtime.yaml", "path to the runtime config on the target host")
	return cmd
}
