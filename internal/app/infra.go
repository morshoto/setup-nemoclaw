package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"openclaw/internal/config"
	"openclaw/internal/provider"
)

func newInfraCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "infra",
		Short: "Provision infrastructure",
	}
	cmd.AddCommand(newInfraCreateCommand(app))
	return cmd
}

func newInfraCreateCommand(app *App) *cobra.Command {
	var sshKeyName string
	var sshCIDR string

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an EC2 instance from the current configuration",
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
			if err := validateInfraCreateFlags(sshKeyName, sshCIDR); err != nil {
				return err
			}

			logger := loggerFromContext(cmd.Context())
			logger.Info("starting infra create")
			fmt.Fprintln(cmd.OutOrStdout(), "creating infrastructure...")
			instance, err := runInfraCreate(cmd.Context(), app.opts.Profile, cfg, sshKeyName, sshCIDR)
			printCreatedInstance(cmd.OutOrStdout(), instance)
			if instance != nil {
				printSuccessNextSteps(cmd.OutOrStdout(), app.opts.ConfigPath, instanceTarget(instance), true)
			}
			if err != nil {
				return wrapUserFacingError(
					"infra create failed",
					err,
					"the AWS provider rejected the request or the selected region lacks capacity",
					"check the AWS error above",
					fmt.Sprintf("run `openclaw quota check --platform aws --region %s --instance-family %s` before retrying", cfg.Region.Name, cfg.Instance.Type),
				)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&sshKeyName, "ssh-key-name", "", "SSH key pair name to attach to the instance")
	cmd.Flags().StringVar(&sshCIDR, "ssh-cidr", "", "CIDR allowed to reach port 22 when SSH access is configured")
	return cmd
}

func validateInfraConfig(cfg *config.Config) error {
	if cfg == nil {
		return errors.New("config validation failed: config is nil")
	}

	var v config.ValidationError
	if cfg.Platform.Name != config.PlatformAWS {
		if cfg.Platform.Name == "" {
			v.Add("platform.name", "is required")
		} else {
			v.Add("platform.name", fmt.Sprintf("unsupported platform %q", cfg.Platform.Name))
		}
	}
	if strings.TrimSpace(cfg.Region.Name) == "" {
		v.Add("region.name", "is required")
	}
	if strings.TrimSpace(cfg.Instance.Type) == "" {
		v.Add("instance.type", "is required")
	}
	if cfg.Instance.DiskSizeGB <= 0 {
		v.Add("instance.disk_size_gb", "must be greater than 0")
	}
	if strings.TrimSpace(cfg.Image.ID) == "" && strings.TrimSpace(cfg.Image.Name) == "" {
		v.Add("image.name", "or image.id is required")
	}
	if mode := strings.TrimSpace(cfg.Sandbox.NetworkMode); mode != "" && mode != "public" && mode != "private" {
		v.Add("sandbox.network_mode", "must be public or private")
	}
	return v.OrNil()
}

func validateInfraCreateFlags(sshKeyName, sshCIDR string) error {
	sshKeyName = strings.TrimSpace(sshKeyName)
	sshCIDR = strings.TrimSpace(sshCIDR)
	switch {
	case sshKeyName != "" && sshCIDR == "":
		return errors.New("ssh-cidr is required when ssh-key-name is set")
	case sshKeyName == "" && sshCIDR != "":
		return errors.New("ssh-key-name is required when ssh-cidr is set")
	default:
		return nil
	}
}

func resolveInfraImage(ctx context.Context, prov provider.CloudProvider, cfg *config.Config) (provider.BaseImage, error) {
	if cfg == nil {
		return provider.BaseImage{}, errors.New("config is nil")
	}
	if imageID := strings.TrimSpace(cfg.Image.ID); imageID != "" {
		return provider.BaseImage{
			ID:   imageID,
			Name: cfg.Image.Name,
		}, nil
	}

	imageName := strings.TrimSpace(cfg.Image.Name)
	if imageName == "" {
		return provider.BaseImage{}, errors.New("image name or image id is required")
	}
	if prov == nil {
		return provider.BaseImage{}, fmt.Errorf("resolve image %q: provider is unavailable", imageName)
	}

	images, err := prov.ListBaseImages(ctx, cfg.Region.Name)
	if err != nil {
		return provider.BaseImage{}, fmt.Errorf("resolve image %q: %w", imageName, err)
	}
	if len(images) == 0 {
		return provider.BaseImage{}, fmt.Errorf("resolve image %q: no base images available", imageName)
	}
	if len(images) == 1 {
		return images[0], nil
	}

	for _, image := range images {
		if strings.EqualFold(strings.TrimSpace(image.Name), imageName) || strings.EqualFold(strings.TrimSpace(image.ID), imageName) {
			return image, nil
		}
	}
	return provider.BaseImage{}, fmt.Errorf("resolve image %q: no matching base image found", imageName)
}

func connectionMethodFor(sshKeyName, networkMode string) string {
	if strings.TrimSpace(sshKeyName) != "" {
		return "ssh"
	}
	if strings.TrimSpace(networkMode) == "private" {
		return "private-ip"
	}
	return "public-ip"
}

func printCreatedInstance(out io.Writer, instance *provider.Instance) {
	if instance == nil {
		fmt.Fprintln(out, "instance created")
		return
	}
	fmt.Fprintf(out, "instance id: %s\n", instance.ID)
	if strings.TrimSpace(instance.Region) != "" {
		fmt.Fprintf(out, "region: %s\n", instance.Region)
	}
	if strings.TrimSpace(instance.PublicIP) != "" {
		fmt.Fprintf(out, "public ip: %s\n", instance.PublicIP)
	}
	if strings.TrimSpace(instance.PrivateIP) != "" {
		fmt.Fprintf(out, "private ip: %s\n", instance.PrivateIP)
	}
	if strings.TrimSpace(instance.ConnectionInfo) != "" {
		fmt.Fprintf(out, "connection: %s\n", instance.ConnectionInfo)
	}
	if strings.TrimSpace(instance.SecurityGroupID) != "" {
		fmt.Fprintf(out, "security group: %s\n", instance.SecurityGroupID)
	}
	if len(instance.SecurityGroupRules) > 0 {
		fmt.Fprintln(out, "security group rules:")
		for _, rule := range instance.SecurityGroupRules {
			fmt.Fprintf(out, "  - %s\n", rule)
		}
	}
}
