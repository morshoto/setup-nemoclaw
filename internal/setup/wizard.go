package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"openclaw/internal/config"
	"openclaw/internal/prompt"
	"openclaw/internal/provider"
	awsprovider "openclaw/internal/provider/aws"
)

type Wizard struct {
	Prompter        *prompt.Session
	Out             io.Writer
	ProviderFactory func(platform string) provider.CloudProvider
	Provider        provider.CloudProvider
	Existing        *config.Config
}

func NewWizard(prompter *prompt.Session, out io.Writer, factory func(platform string) provider.CloudProvider, existing *config.Config) *Wizard {
	return &Wizard{Prompter: prompter, Out: out, ProviderFactory: factory, Existing: existing}
}

func (w *Wizard) Run(ctx context.Context) (*config.Config, error) {
	platform, err := w.Prompter.Select("Select platform", []string{"aws", "gcp", "azure"}, config.PlatformAWS)
	if err != nil {
		return nil, err
	}
	if platform != config.PlatformAWS {
		return nil, fmt.Errorf("%s is not implemented yet", platform)
	}

	if w.Provider == nil && w.ProviderFactory != nil {
		w.Provider = w.ProviderFactory(platform)
	}
	if w.Provider != nil {
		if _, err := w.Provider.AuthCheck(ctx); err != nil {
			var authErr *awsprovider.AuthError
			if errors.As(err, &authErr) {
				fmt.Fprintln(w.Out, "Warning: AWS auth check unavailable; continuing.")
			} else {
				return nil, err
			}
		}
	}

	regions, err := w.listRegions(ctx)
	if err != nil {
		return nil, err
	}
	regionDefault := "us-east-1"
	if w.Existing != nil && strings.TrimSpace(w.Existing.Region.Name) != "" && slices.Contains(regions, w.Existing.Region.Name) {
		regionDefault = w.Existing.Region.Name
	}
	region, err := w.Prompter.Select("Select AWS region", regions, regionDefault)
	if err != nil {
		return nil, err
	}

	if err := w.warnOnQuota(ctx, region); err != nil {
		return nil, err
	}

	instanceTypes, err := w.listInstanceTypes(ctx, region)
	if err != nil {
		return nil, err
	}
	instanceType, err := w.Prompter.Select("Select instance type", instanceTypes, defaultOption(instanceTypes, "g5.xlarge"))
	if err != nil {
		return nil, err
	}

	images, err := w.listImages(ctx, region)
	if err != nil {
		var authErr *awsprovider.AuthError
		if errors.As(err, &authErr) && (authErr.Kind == "permission_denied" || authErr.Kind == "no_credentials") {
			fmt.Fprintln(w.Out, "Warning: AWS image lookup unavailable; using bundled fallback images.")
			images = fallbackAWSBaseImages(region)
		} else {
			return nil, err
		}
	}
	image, err := selectBaseImage(w.Prompter, images)
	if err != nil {
		return nil, err
	}

	diskSize, err := w.Prompter.Int("Enter disk size (GB)", 20)
	if err != nil {
		return nil, err
	}

	networkMode, err := w.Prompter.Select("Select network mode", []string{"private", "public"}, "private")
	if err != nil {
		return nil, err
	}

	useNemoClaw, err := w.Prompter.Confirm("Use NemoClaw", true)
	if err != nil {
		return nil, err
	}

	nimEndpoint, err := w.Prompter.Text("NIM endpoint", "http://localhost:11434")
	if err != nil {
		return nil, err
	}

	model, err := w.Prompter.Text("Model name", "llama3.2")
	if err != nil {
		return nil, err
	}

	cfg := &config.Config{
		Platform: config.PlatformConfig{Name: platform},
		Region:   config.RegionConfig{Name: region},
		Instance: config.InstanceConfig{Type: instanceType, DiskSizeGB: diskSize},
		Image:    config.ImageConfig{Name: image.Name, ID: image.ID},
		Runtime:  config.RuntimeConfig{Endpoint: nimEndpoint, Model: model},
		Sandbox: config.SandboxConfig{
			Enabled:     true,
			NetworkMode: networkMode,
			UseNemoClaw: useNemoClaw,
		},
	}

	fmt.Fprintln(w.Out, "")
	fmt.Fprintln(w.Out, "Summary")
	fmt.Fprintln(w.Out, "-------")
	fmt.Fprintf(w.Out, "platform: %s\n", cfg.Platform.Name)
	fmt.Fprintf(w.Out, "region: %s\n", cfg.Region.Name)
	fmt.Fprintf(w.Out, "instance: %s\n", cfg.Instance.Type)
	fmt.Fprintf(w.Out, "image: %s\n", cfg.Image.Name)
	if cfg.Image.ID != "" {
		fmt.Fprintf(w.Out, "image id: %s\n", cfg.Image.ID)
	}
	fmt.Fprintf(w.Out, "disk size: %d GB\n", cfg.Instance.DiskSizeGB)
	fmt.Fprintf(w.Out, "network mode: %s\n", cfg.Sandbox.NetworkMode)
	fmt.Fprintf(w.Out, "use NemoClaw: %t\n", cfg.Sandbox.UseNemoClaw)
	fmt.Fprintf(w.Out, "NIM endpoint: %s\n", cfg.Runtime.Endpoint)
	fmt.Fprintf(w.Out, "model: %s\n", cfg.Runtime.Model)

	confirm, err := w.Prompter.Confirm("Write this configuration", true)
	if err != nil {
		return nil, err
	}
	if !confirm {
		return nil, errors.New("setup cancelled")
	}

	return cfg, nil
}

func (w *Wizard) listRegions(ctx context.Context) ([]string, error) {
	if w.Provider == nil {
		return []string{"us-east-1", "us-west-2"}, nil
	}
	return w.Provider.ListRegions(ctx)
}

func (w *Wizard) listInstanceTypes(ctx context.Context, region string) ([]string, error) {
	if w.Provider == nil {
		return []string{"g5.xlarge", "g4dn.xlarge", "t3.medium"}, nil
	}
	items, err := w.Provider.ListInstanceTypes(ctx, region)
	if err != nil {
		return nil, err
	}
	options := make([]string, 0, len(items))
	for _, item := range items {
		options = append(options, item.Name)
	}
	if len(options) == 0 {
		return []string{"g5.xlarge"}, nil
	}
	return options, nil
}

func (w *Wizard) listImages(ctx context.Context, region string) ([]provider.BaseImage, error) {
	if w.Provider == nil {
		return fallbackAWSBaseImages(region), nil
	}
	items, err := w.Provider.ListBaseImages(ctx, region)
	if err != nil {
		var authErr *awsprovider.AuthError
		if errors.As(err, &authErr) {
			fmt.Fprintln(w.Out, "Warning: AWS image lookup unavailable; using bundled fallback images.")
			return fallbackAWSBaseImages(region), nil
		}
		return nil, err
	}
	if len(items) == 0 {
		return fallbackAWSBaseImages(region), nil
	}
	return items, nil
}

func (w *Wizard) warnOnQuota(ctx context.Context, region string) error {
	if w.Provider == nil {
		return nil
	}
	report, err := w.Provider.CheckGPUQuota(ctx, region, "g5")
	if err != nil {
		fmt.Fprintln(w.Out, "Warning: GPU quota check unavailable; continuing.")
		return nil
	}
	if report.Source == "mock" {
		fmt.Fprintln(w.Out, "Quota check is a mock report; live AWS Service Quotas access is not wired yet.")
		for _, note := range report.Notes {
			fmt.Fprintf(w.Out, "  - %s\n", note)
		}
		return nil
	}
	if report.LikelyCreatable {
		return nil
	}

	fmt.Fprintf(w.Out, "Warning: GPU quota for %s in %s looks insufficient.\n", report.InstanceFamily, report.Region)
	for _, check := range report.Checks {
		fmt.Fprintf(w.Out, "  %s: limit=%d usage=%s remaining=%d\n", check.QuotaName, check.CurrentLimit, formatUsage(check.CurrentUsage), check.EstimatedRemaining)
	}
	if len(report.Notes) > 0 {
		fmt.Fprintln(w.Out, "Notes:")
		for _, note := range report.Notes {
			fmt.Fprintf(w.Out, "  - %s\n", note)
		}
	}
	confirm, err := w.Prompter.Confirm("Quota looks insufficient. Continue anyway", false)
	if err != nil {
		return err
	}
	if !confirm {
		return errors.New("setup cancelled due to insufficient quota")
	}
	return nil
}

func defaultOption(options []string, fallback string) string {
	if len(options) == 0 {
		return fallback
	}
	for _, option := range options {
		if option == fallback {
			return fallback
		}
	}
	return options[0]
}

func selectBaseImage(prompter *prompt.Session, images []provider.BaseImage) (provider.BaseImage, error) {
	if len(images) == 0 {
		return provider.BaseImage{}, errors.New("no base images available")
	}

	options := make([]string, 0, len(images))
	for _, image := range images {
		options = append(options, image.Name)
	}
	defaultName := images[0].Name
	if preferred := findBaseImage(images, "AWS Deep Learning AMI GPU Ubuntu 22.04"); preferred.Name != "" {
		defaultName = preferred.Name
	}

	selected, err := prompter.Select("Select base image", options, defaultName)
	if err != nil {
		return provider.BaseImage{}, err
	}
	image := findBaseImage(images, selected)
	if image.Name == "" {
		return provider.BaseImage{}, fmt.Errorf("base image %q not found", selected)
	}
	return image, nil
}

func findBaseImage(images []provider.BaseImage, name string) provider.BaseImage {
	for _, image := range images {
		if image.Name == name {
			return image
		}
	}
	return provider.BaseImage{}
}

func formatUsage(value *int) string {
	if value == nil {
		return "n/a"
	}
	return fmt.Sprintf("%d", *value)
}

func fallbackAWSBaseImages(region string) []provider.BaseImage {
	return []provider.BaseImage{{
		Name:               "AWS Deep Learning AMI GPU Ubuntu 22.04",
		Architecture:       "x86_64",
		Owner:              "amazon",
		VirtualizationType: "hvm",
		RootDeviceType:     "ebs",
		Region:             region,
		Source:             "fallback",
		SSMParameter:       "/aws/service/deeplearning/ami/x86_64/base-oss-nvidia-driver-gpu-ubuntu-22.04/latest/ami-id",
	}}
}
