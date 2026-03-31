package app

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"openclaw/internal/config"
	"openclaw/internal/runtimeinstall"
)

func newInstallCommand(app *App) *cobra.Command {
	var target string
	var sshUser string
	var sshKey string
	var sshPort int
	var workingDir string
	var port int
	var useNemoClaw bool
	var disableNemoClaw bool

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install the OpenClaw runtime on a prepared host",
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
			if strings.TrimSpace(target) == "" {
				return errors.New("target is required: pass --target <instance-id-or-host>")
			}

			logger := loggerFromContext(cmd.Context())
			logger.Info("starting install workflow")
			fmt.Fprintln(cmd.OutOrStdout(), "running install workflow...")
			result, resolvedTarget, err := runInstallWorkflow(cmd.Context(), app.opts.Profile, cfg, installOptions{
				Target:          target,
				SSHUser:         sshUser,
				SSHKey:          sshKey,
				SSHPort:         sshPort,
				WorkingDir:      workingDir,
				Port:            port,
				UseNemoClaw:     useNemoClaw,
				DisableNemoClaw: disableNemoClaw,
			})
			printInstallResult(cmd.OutOrStdout(), result)
			printSuccessNextSteps(cmd.OutOrStdout(), app.opts.ConfigPath, resolvedTarget, false)
			if err != nil {
				return wrapUserFacingError(
					"install failed",
					err,
					"the SSH target is unreachable or the host prerequisites are missing",
					fmt.Sprintf("run `openclaw verify --config %s --target %s` after fixing the host", app.opts.ConfigPath, resolvedTarget),
					"check Docker, GPU drivers, and SSH access on the target host",
				)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&target, "target", "", "SSH target host or EC2 instance id")
	cmd.Flags().StringVar(&sshUser, "ssh-user", "", "SSH username for the target host")
	cmd.Flags().StringVar(&sshKey, "ssh-key", "", "path to the SSH private key")
	cmd.Flags().IntVar(&sshPort, "ssh-port", 22, "SSH port")
	cmd.Flags().StringVar(&workingDir, "working-dir", "/opt/openclaw", "remote working directory")
	cmd.Flags().IntVar(&port, "port", 0, "runtime port override")
	cmd.Flags().BoolVar(&useNemoClaw, "use-nemoclaw", false, "enable NemoClaw settings for the generated runtime config")
	cmd.Flags().BoolVar(&disableNemoClaw, "disable-nemoclaw", false, "disable NemoClaw settings for the generated runtime config")
	return cmd
}

func sshUsernameForImage(imageName, imageID string) string {
	lower := strings.ToLower(strings.TrimSpace(imageName) + " " + strings.TrimSpace(imageID))
	if strings.Contains(lower, "ubuntu") {
		return "ubuntu"
	}
	return "ec2-user"
}

func printInstallResult(out io.Writer, result runtimeinstall.Result) {
	fmt.Fprintln(out, "install workflow completed")
	if strings.TrimSpace(result.WorkingDir) != "" {
		fmt.Fprintf(out, "working directory: %s\n", result.WorkingDir)
	}
	if strings.TrimSpace(result.ConfigPath) != "" {
		fmt.Fprintf(out, "runtime config: %s\n", result.ConfigPath)
	}
	if strings.TrimSpace(result.ScriptPath) != "" {
		fmt.Fprintf(out, "install script: %s\n", result.ScriptPath)
	}
	if len(result.Prerequisites.Checks) > 0 {
		fmt.Fprintln(out, "prerequisites:")
		for _, check := range result.Prerequisites.Checks {
			status := "passed"
			if check.Skipped {
				status = "skipped"
			}
			if !check.Passed && !check.Skipped {
				status = "failed"
			}
			fmt.Fprintf(out, "- %s: %s\n", check.Name, status)
			if strings.TrimSpace(check.Message) != "" {
				fmt.Fprintf(out, "  %s\n", check.Message)
			}
			if strings.TrimSpace(check.Remediation) != "" && !check.Passed {
				fmt.Fprintf(out, "  remediation: %s\n", check.Remediation)
			}
		}
	}
	if len(result.CommandResults) > 0 {
		fmt.Fprintln(out, "backend output:")
		for _, r := range result.CommandResults {
			if strings.TrimSpace(r.Stdout) != "" {
				fmt.Fprint(out, r.Stdout)
				if !strings.HasSuffix(r.Stdout, "\n") {
					fmt.Fprintln(out)
				}
			}
			if strings.TrimSpace(r.Stderr) != "" {
				fmt.Fprint(out, r.Stderr)
				if !strings.HasSuffix(r.Stderr, "\n") {
					fmt.Fprintln(out)
				}
			}
		}
	}
}
