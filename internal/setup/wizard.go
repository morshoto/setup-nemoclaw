package setup

import (
	"context"
	"errors"
	"fmt"
	"io"

	"openclaw/internal/config"
	"openclaw/internal/prompt"
)

type Wizard struct {
	Prompter *prompt.Session
	Out      io.Writer
}

func NewWizard(prompter *prompt.Session, out io.Writer) *Wizard {
	return &Wizard{Prompter: prompter, Out: out}
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

	regions := []string{"us-east-1", "us-west-2"}
	region, err := w.Prompter.Select("Select AWS region", regions, "us-east-1")
	if err != nil {
		return nil, err
	}

	instanceTypes := []string{"t3.medium", "g4dn.xlarge"}
	instanceType, err := w.Prompter.Select("Select instance type", instanceTypes, "t3.medium")
	if err != nil {
		return nil, err
	}

	images := []string{"ubuntu-24.04", "amazon-linux-2023"}
	image, err := w.Prompter.Select("Select base image", images, "ubuntu-24.04")
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
