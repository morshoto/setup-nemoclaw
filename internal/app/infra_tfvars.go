package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"openclaw/internal/config"
	"openclaw/internal/prompt"
)

var listAWSProfilesFunc = defaultListAWSProfiles

func newInfraTFVarsCommand(app *App) *cobra.Command {
	var outputPath string

	cmd := &cobra.Command{
		Use:   "tfvars",
		Short: "Generate deploy-ready terraform.tfvars from a configuration file",
		Long: strings.TrimSpace(`Generate a deploy-ready terraform.tfvars file from an OpenClaw config.

This command resolves the active AWS profile, derives the SSH public key from the configured private key path, and stages the current working tree as a bootstrap archive URL. It is intended for deploy-time use and can fail if the local git state, SSH key, or AWS environment is not ready.`),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(app.opts.ConfigPath) == "" {
				return errors.New("config file is required: pass --config <path>")
			}
			cfg, err := config.Load(app.opts.ConfigPath)
			if err != nil {
				return err
			}
			if err := validateInfraConfig(cfg); err != nil {
				return err
			}

			profile, err := selectAWSProfile(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), app.opts.Profile)
			if err != nil {
				return err
			}

			vars, err := buildTerraformVars(cmd.Context(), profile, cfg)
			if err != nil {
				return err
			}
			if err := writeTerraformVarsFile(outputPath, vars); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "terraform variables written to %s\n", outputPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&outputPath, "output", "terraform.tfvars", "path to write terraform variables")
	return cmd
}

func selectAWSProfile(ctx context.Context, in io.Reader, out io.Writer, existing string) (string, error) {
	profile := strings.TrimSpace(existing)
	if profile != "" {
		return profile, nil
	}

	defaultProfile := strings.TrimSpace(os.Getenv("AWS_PROFILE"))
	if defaultProfile == "" {
		defaultProfile = strings.TrimSpace(os.Getenv("AWS_DEFAULT_PROFILE"))
	}
	interactive := isInteractiveInput(in)
	profiles, err := listAWSProfilesFunc(ctx)
	if err != nil {
		if defaultProfile != "" {
			return defaultProfile, nil
		}
		if interactive {
			session := prompt.NewSession(in, out)
			value, promptErr := session.Text("AWS profile", "")
			if promptErr != nil {
				return "", promptErr
			}
			return strings.TrimSpace(value), nil
		}
		return "", err
	}

	if !interactive {
		switch {
		case defaultProfile != "":
			return defaultProfile, nil
		case len(profiles) == 1:
			return profiles[0], nil
		default:
			return "", errors.New("aws profile is required: pass --profile, set AWS_PROFILE, or run interactively")
		}
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

func buildTerraformVars(ctx context.Context, profile string, cfg *config.Config) (terraformVars, error) {
	inputs, err := buildTerraformInputs(ctx, profile, cfg, createOptions{})
	if err != nil {
		return terraformVars{}, err
	}

	return terraformVars{
		AWSProfile:      strings.TrimSpace(profile),
		Region:          cfg.Region.Name,
		ComputeClass:    config.EffectiveComputeClass(cfg.Compute.Class),
		InstanceType:    strings.TrimSpace(cfg.Instance.Type),
		DiskSizeGB:      cfg.Instance.DiskSizeGB,
		NetworkMode:     inputs.NetworkMode,
		ImageName:       strings.TrimSpace(cfg.Image.Name),
		ImageID:         strings.TrimSpace(cfg.Image.ID),
		RuntimePort:     inputs.RuntimePort,
		RuntimeCIDR:     inputs.RuntimeCIDR,
		RuntimeProvider: inputs.RuntimeProvider,
		SSHKeyName:      inputs.SSHKeyName,
		SSHPublicKey:    inputs.SSHPublicKey,
		SSHCIDR:         inputs.SSHCIDR,
		SSHUser:         inputs.SSHUser,
		NamePrefix:      "openclaw",
		UseNemoClaw:     cfg.Sandbox.UseNemoClaw,
		NIMEndpoint:     cfg.Runtime.Endpoint,
		Model:           cfg.Runtime.Model,
		SourceURL:       inputs.SourceURL,
	}, nil
}

func writeTerraformVarsFile(outputPath string, vars terraformVars) error {
	outputPath = strings.TrimSpace(outputPath)
	if outputPath == "" {
		return errors.New("output path is required")
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return fmt.Errorf("prepare output directory: %w", err)
	}

	data := []byte(renderTerraformVars(vars))
	if err := os.WriteFile(outputPath, data, 0o600); err != nil {
		return fmt.Errorf("write terraform vars %q: %w", outputPath, err)
	}
	return nil
}

func renderTerraformVars(vars terraformVars) string {
	keys := []string{
		"aws_profile",
		"region",
		"compute_class",
		"instance_type",
		"disk_size_gb",
		"network_mode",
		"image_name",
		"image_id",
		"runtime_port",
		"runtime_cidr",
		"runtime_provider",
		"ssh_key_name",
		"ssh_public_key",
		"ssh_cidr",
		"ssh_user",
		"name_prefix",
		"use_nemoclaw",
		"nim_endpoint",
		"model",
		"source_archive_url",
	}
	maxWidth := 0
	for _, key := range keys {
		if len(key) > maxWidth {
			maxWidth = len(key)
		}
	}
	lines := []string{
		fmt.Sprintf("%-*s = %s", maxWidth, "aws_profile", terraformQuoted(vars.AWSProfile)),
		fmt.Sprintf("%-*s = %s", maxWidth, "region", terraformQuoted(vars.Region)),
		fmt.Sprintf("%-*s = %s", maxWidth, "compute_class", terraformQuoted(vars.ComputeClass)),
		fmt.Sprintf("%-*s = %s", maxWidth, "instance_type", terraformQuoted(vars.InstanceType)),
		fmt.Sprintf("%-*s = %d", maxWidth, "disk_size_gb", vars.DiskSizeGB),
		fmt.Sprintf("%-*s = %s", maxWidth, "network_mode", terraformQuoted(vars.NetworkMode)),
		fmt.Sprintf("%-*s = %s", maxWidth, "image_name", terraformQuoted(vars.ImageName)),
		fmt.Sprintf("%-*s = %s", maxWidth, "image_id", terraformQuoted(vars.ImageID)),
		fmt.Sprintf("%-*s = %d", maxWidth, "runtime_port", vars.RuntimePort),
		fmt.Sprintf("%-*s = %s", maxWidth, "runtime_cidr", terraformQuoted(vars.RuntimeCIDR)),
		fmt.Sprintf("%-*s = %s", maxWidth, "runtime_provider", terraformQuoted(vars.RuntimeProvider)),
		fmt.Sprintf("%-*s = %s", maxWidth, "ssh_key_name", terraformQuoted(vars.SSHKeyName)),
		fmt.Sprintf("%-*s = %s", maxWidth, "ssh_public_key", terraformQuoted(vars.SSHPublicKey)),
		fmt.Sprintf("%-*s = %s", maxWidth, "ssh_cidr", terraformQuoted(vars.SSHCIDR)),
		fmt.Sprintf("%-*s = %s", maxWidth, "ssh_user", terraformQuoted(vars.SSHUser)),
		fmt.Sprintf("%-*s = %s", maxWidth, "name_prefix", terraformQuoted(vars.NamePrefix)),
		fmt.Sprintf("%-*s = %s", maxWidth, "use_nemoclaw", strconv.FormatBool(vars.UseNemoClaw)),
		fmt.Sprintf("%-*s = %s", maxWidth, "nim_endpoint", terraformQuoted(vars.NIMEndpoint)),
		fmt.Sprintf("%-*s = %s", maxWidth, "model", terraformQuoted(vars.Model)),
		fmt.Sprintf("%-*s = %s", maxWidth, "source_archive_url", terraformQuoted(vars.SourceURL)),
	}
	return strings.Join(lines, "\n") + "\n"
}

func terraformQuoted(value string) string {
	return strconv.Quote(strings.TrimSpace(value))
}

func isInteractiveInput(in io.Reader) bool {
	file, ok := in.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func defaultListAWSProfiles(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, "aws", "configure", "list-profiles")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list AWS profiles: %w", err)
	}
	lines := strings.Split(string(out), "\n")
	profiles := make([]string, 0, len(lines))
	for _, line := range lines {
		profile := strings.TrimSpace(line)
		if profile == "" {
			continue
		}
		profiles = append(profiles, profile)
	}
	return profiles, nil
}
