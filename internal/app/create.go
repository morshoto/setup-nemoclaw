package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"openclaw/internal/config"
	"openclaw/internal/prompt"
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
	var agentsDir string

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create and verify a new environment",
		RunE: func(cmd *cobra.Command, args []string) error {
			configPath := strings.TrimSpace(app.opts.ConfigPath)
			if configPath == "" {
				session := prompt.NewSession(cmd.InOrStdin(), cmd.OutOrStdout())
				selectedConfigPath, err := selectAgentConfigPath(session, agentsDir)
				if err != nil {
					return err
				}
				configPath = selectedConfigPath
				app.opts.ConfigPath = configPath
			}
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			if err := config.Validate(cfg); err != nil {
				return err
			}
			profile, err := selectCreateAWSProfile(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), firstNonEmpty(app.opts.Profile, cfg.Infra.AWSProfile))
			if err != nil {
				return err
			}
			app.opts.Profile = profile
			effectiveSSHKeyName := firstNonEmpty(sshKeyName, cfg.SSH.KeyName)
			effectiveSSHCIDR := firstNonEmpty(sshCIDR, cfg.SSH.CIDR)
			if err := validateCreateWorkflowSSHFlags(cfg, effectiveSSHKeyName, effectiveSSHCIDR); err != nil {
				return err
			}
			if err := validateInfraCreateFlags(cfg, effectiveSSHKeyName, effectiveSSHCIDR); err != nil {
				return err
			}

			logger := loggerFromContext(cmd.Context())
			logger.Info("starting create workflow")
			progress := newProgressRenderer(cmd.OutOrStdout())
			instance, installResult, verifyReport, err := runCreateWorkflow(cmd.Context(), profile, cfg, createOptions{
				SSHKeyName:      sshKeyName,
				SSHCIDR:         sshCIDR,
				SSHUser:         sshUser,
				SSHKey:          sshKey,
				SSHPort:         sshPort,
				WorkingDir:      workingDir,
				Port:            port,
				UseNemoClaw:     useNemoClaw,
				DisableNemoClaw: disableNemoClaw,
			}, progress)
			if err != nil {
				return wrapUserFacingError(
					"create workflow failed",
					err,
					"one of the infra, install, or verify stages failed",
					"inspect the summary above",
					"re-run the failed stage directly once the host is ready",
				)
			}
			printWorkflowSuccess(cmd.OutOrStdout(), instance, installResult, verifyReport, configPath, cfg, instanceTarget(instance), true)
			return nil
		},
	}

	cmd.Flags().StringVar(&sshKeyName, "ssh-key-name", "", "SSH key pair name to attach to the instance")
	cmd.Flags().StringVar(&sshCIDR, "ssh-cidr", "", "CIDR allowed to reach port 22; auto-detected from your public IP when omitted")
	cmd.Flags().StringVar(&sshUser, "ssh-user", "", "SSH username for the target host")
	cmd.Flags().StringVar(&sshKey, "ssh-key", "", "path to the SSH private key")
	cmd.Flags().IntVar(&sshPort, "ssh-port", 22, "SSH port")
	cmd.Flags().StringVar(&workingDir, "working-dir", "/opt/openclaw", "remote working directory")
	cmd.Flags().IntVar(&port, "port", 0, "runtime port override")
	cmd.Flags().BoolVar(&useNemoClaw, "use-nemoclaw", false, "enable NemoClaw settings for the generated runtime config")
	cmd.Flags().BoolVar(&disableNemoClaw, "disable-nemoclaw", false, "disable NemoClaw settings for the generated runtime config")
	cmd.Flags().StringVar(&agentsDir, "agents-dir", "agents", "path to the agents directory")
	return cmd
}

func selectAgentConfigPath(session *prompt.Session, agentsDir string) (string, error) {
	if session == nil || !session.Interactive {
		return "", errors.New("config file is required: pass --config <path> or run interactively")
	}
	files, err := listAgentConfigFiles(agentsDir)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		root := strings.TrimSpace(agentsDir)
		if root == "" {
			root = "agents"
		}
		return "", fmt.Errorf("no agent config files found under %q; run openclaw init first", root)
	}

	options := make([]string, len(files))
	defaultValue := files[0].Label
	for i, file := range files {
		options[i] = file.Label
	}
	selected, err := session.SelectSearch("Select configuration file", options, defaultValue)
	if err != nil {
		return "", err
	}
	for _, file := range files {
		if file.Label == selected {
			return file.Path, nil
		}
	}
	return "", fmt.Errorf("selected configuration file %q not found", selected)
}

func selectCreateAWSProfile(ctx context.Context, in io.Reader, out io.Writer, existing string) (string, error) {
	profile := strings.TrimSpace(existing)
	if profile != "" {
		return profile, nil
	}

	defaultProfile := strings.TrimSpace(os.Getenv("AWS_PROFILE"))
	if defaultProfile == "" {
		defaultProfile = strings.TrimSpace(os.Getenv("AWS_DEFAULT_PROFILE"))
	}
	profiles, err := listAWSProfilesFunc(ctx)
	if err != nil {
		if defaultProfile != "" {
			return defaultProfile, nil
		}
		session := prompt.NewSession(in, out)
		value, promptErr := session.Text("AWS profile", "")
		if promptErr != nil {
			return "", promptErr
		}
		return strings.TrimSpace(value), nil
	}

	session := prompt.NewSession(in, out)
	if len(profiles) == 0 {
		value, err := session.Text("AWS profile", defaultProfile)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(value), nil
	}
	if defaultProfile == "" && len(profiles) > 0 {
		defaultProfile = profiles[0]
	}
	value, err := session.Select("Select AWS profile", profiles, defaultProfile)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

func validateCreateWorkflowSSHFlags(cfg *config.Config, sshKeyName, sshCIDR string) error {
	sshKeyName = strings.TrimSpace(sshKeyName)
	sshCIDR = strings.TrimSpace(sshCIDR)
	networkMode := config.EffectiveNetworkMode(cfg)
	switch {
	case networkMode == "private":
		return errors.New("private networking is not supported yet; use public networking or add an SSM/bastion executor")
	default:
		return nil
	}
}
