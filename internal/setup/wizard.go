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
)

type Wizard struct {
	Prompter *prompt.Session
	Out      io.Writer
	Provider provider.CloudProvider
	Existing *config.Config
}

func NewWizard(prompter *prompt.Session, out io.Writer, p provider.CloudProvider, existing *config.Config) *Wizard {
	return &Wizard{Prompter: prompter, Out: out, Provider: p, Existing: existing}
}

func (w *Wizard) Run(ctx context.Context) (*config.Config, error) {
	_ = ctx
	platform, err := w.Prompter.Select("Select platform", []string{"aws", "gcp", "azure"}, config.PlatformAWS)
	if err != nil {
		return nil, err
	}
	if platform != config.PlatformAWS {
		return nil, fmt.Errorf("%s is not implemented yet", platform)
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
	instanceType, err := w.Prompter.Select("Select instance type", instanceTypes, defaultOption(instanceTypes, "t3.medium"))
	if err != nil {
		return nil, err
	}

	images, err := w.listImages(ctx, region)
	if err != nil {
		return nil, err
	}
	image, err := w.Prompter.Select("Select base image", images, defaultOption(images, "ubuntu-24.04"))
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
		Image:    config.ImageConfig{Name: image},
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
		return []string{"t3.medium", "g4dn.xlarge", "g5.xlarge"}, nil
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
		return []string{"t3.medium"}, nil
	}
	return options, nil
}

func (w *Wizard) listImages(ctx context.Context, region string) ([]string, error) {
	if w.Provider == nil {
		return []string{"ubuntu-24.04", "amazon-linux-2023"}, nil
	}
	items, err := w.Provider.ListBaseImages(ctx, region)
	if err != nil {
		return nil, err
	}
	options := make([]string, 0, len(items))
	for _, item := range items {
		options = append(options, item.Name)
	}
	if len(options) == 0 {
		return []string{"ubuntu-24.04"}, nil
	}
	return options, nil
}

func (w *Wizard) warnOnQuota(ctx context.Context, region string) error {
	if w.Provider == nil {
		return nil
	}
	report, err := w.Provider.CheckGPUQuota(ctx, region, "g5")
	if err != nil {
		return err
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

func formatUsage(value *int) string {
	if value == nil {
		return "n/a"
	}
	return fmt.Sprintf("%d", *value)
}
