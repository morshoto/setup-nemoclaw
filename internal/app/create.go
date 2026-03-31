package app

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"openclaw/internal/config"
)

func newCreateCommand(app *App) *cobra.Command {
	var sshKeyName string
	var sshCIDR string
	var sshUser string
	var sshKey string
	var sshPort int
	var workingDir string
	var port int
	var useNemoClaw bool
	var disableNemoClaw bool

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create, install, and verify a new environment",
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
			if err := validateInfraCreateFlags(sshKeyName, sshCIDR); err != nil {
				return err
			}

			logger := loggerFromContext(cmd.Context())
			logger.Info("starting create workflow")
			fmt.Fprintln(cmd.OutOrStdout(), "running create workflow...")
			instance, installResult, verifyReport, err := runCreateWorkflow(cmd.Context(), app.opts.Profile, cfg, createOptions{
				SSHKeyName:      sshKeyName,
				SSHCIDR:         sshCIDR,
				SSHUser:         sshUser,
				SSHKey:          sshKey,
				SSHPort:         sshPort,
				WorkingDir:      workingDir,
				Port:            port,
				UseNemoClaw:     useNemoClaw,
				DisableNemoClaw: disableNemoClaw,
			})
			printWorkflowSuccess(cmd.OutOrStdout(), instance, installResult, verifyReport, app.opts.ConfigPath, cfg, instanceTarget(instance), true)
			if err != nil {
				return wrapUserFacingError(
					"create workflow failed",
					err,
					"one of the infra, install, or verify stages failed",
					"inspect the summary above",
					"re-run the failed stage directly once the host is ready",
				)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&sshKeyName, "ssh-key-name", "", "SSH key pair name to attach to the instance")
	cmd.Flags().StringVar(&sshCIDR, "ssh-cidr", "", "CIDR allowed to reach port 22 when SSH access is configured")
	cmd.Flags().StringVar(&sshUser, "ssh-user", "", "SSH username for the target host")
	cmd.Flags().StringVar(&sshKey, "ssh-key", "", "path to the SSH private key")
	cmd.Flags().IntVar(&sshPort, "ssh-port", 22, "SSH port")
	cmd.Flags().StringVar(&workingDir, "working-dir", "/opt/openclaw", "remote working directory")
	cmd.Flags().IntVar(&port, "port", 0, "runtime port override")
	cmd.Flags().BoolVar(&useNemoClaw, "use-nemoclaw", false, "enable NemoClaw settings for the generated runtime config")
	cmd.Flags().BoolVar(&disableNemoClaw, "disable-nemoclaw", false, "disable NemoClaw settings for the generated runtime config")
	return cmd
}
