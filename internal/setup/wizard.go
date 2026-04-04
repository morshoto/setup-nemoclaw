package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"openclaw/internal/config"
	"openclaw/internal/prompt"
	"openclaw/internal/provider"
	awsprovider "openclaw/internal/provider/aws"
)

type Wizard struct {
	Prompter        *prompt.Session
	Out             io.Writer
	ProviderFactory func(platform, computeClass string) provider.CloudProvider
	Provider        provider.CloudProvider
	Existing        *config.Config
	AWSProfile      string
	GitHubSetup     func(context.Context, string) error
}

const initAWSLookupTimeout = 5 * time.Second

var detectInitSSHCIDR = defaultDetectInitSSHCIDR

func NewWizard(prompter *prompt.Session, out io.Writer, factory func(platform, computeClass string) provider.CloudProvider, existing *config.Config) *Wizard {
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

	computeClass := defaultComputeClass(w.Existing)
	computeClass, err = w.Prompter.Select("Select compute mode", []string{config.ComputeClassCPU, config.ComputeClassGPU}, computeClass)
	if err != nil {
		return nil, err
	}

	profile, prompted, err := w.selectAWSProfile()
	if err != nil {
		return nil, err
	}
	w.AWSProfile = profile
	if profile == "" {
		return nil, errors.New("AWS profile is required")
	}
	fmt.Fprintf(w.Out, "Using AWS profile: %s\n", profile)
	if prompted {
		fmt.Fprintf(w.Out, "If this profile uses AWS SSO, run `aws sso login --profile %s` now.\n", profile)
		ready, err := w.Prompter.Confirm("Continue after AWS SSO login", true)
		if err != nil {
			return nil, err
		}
		if !ready {
			return nil, errors.New("setup cancelled")
		}
	}

	if w.Provider == nil && w.ProviderFactory != nil {
		w.Provider = w.ProviderFactory(platform, computeClass)
	}
	if w.Provider != nil {
		authCtx, cancel := bestEffortAWSContext(ctx)
		if _, err := w.Provider.CheckAuth(authCtx); err != nil {
			cancel()
			var authErr *awsprovider.AuthError
			if errors.As(err, &authErr) {
				fmt.Fprintln(w.Out, "Warning: AWS auth check unavailable; continuing.")
			} else {
				return nil, err
			}
		}
		cancel()
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

	instanceTypes, err := w.listInstanceTypes(ctx, region, computeClass)
	if err != nil {
		return nil, err
	}
	instanceType, err := w.Prompter.SelectSearch("Select instance type", instanceTypes, defaultInstanceType(computeClass))
	if err != nil {
		return nil, err
	}

	images, err := w.listImages(ctx, region, computeClass)
	if err != nil {
		fmt.Fprintln(w.Out, "Warning: AWS image lookup unavailable; using bundled fallback images.")
		images = fallbackAWSBaseImages(region, computeClass)
	}
	image, err := selectBaseImage(w.Prompter, images)
	if err != nil {
		return nil, err
	}

	diskSize, err := w.Prompter.Int("Enter disk size (GB)", 20)
	if err != nil {
		return nil, err
	}

	networkMode, err := w.Prompter.Select("Select network mode", []string{"private", "public"}, defaultNetworkMode(computeClass))
	if err != nil {
		return nil, err
	}

	sshKeyName := ""
	sshPrivateKeyPath := defaultSSHPrivateKeyPath()
	sshCIDR := ""
	sshUser := ""
	if w.Existing != nil {
		sshKeyName = strings.TrimSpace(w.Existing.SSH.KeyName)
		if existingPath := strings.TrimSpace(w.Existing.SSH.PrivateKeyPath); existingPath != "" {
			sshPrivateKeyPath = existingPath
		}
		sshCIDR = strings.TrimSpace(w.Existing.SSH.CIDR)
		sshUser = strings.TrimSpace(w.Existing.SSH.User)
	}
	if sshKeyName == "" {
		sshKeyName = defaultSSHKeyName()
	}
	if networkMode == "public" {
		sshKeyName, err = w.Prompter.Text("SSH key pair name", sshKeyName)
		if err != nil {
			return nil, err
		}
		if sshCIDR == "" {
			if detected, detectErr := detectInitSSHCIDR(ctx); detectErr == nil {
				sshCIDR = detected
			}
		}
		sshPrivateKeyPath, err = w.Prompter.Text("SSH private key path", sshPrivateKeyPath)
		if err != nil {
			return nil, err
		}
		sshCIDR, err = w.Prompter.Text("SSH CIDR", sshCIDR)
		if err != nil {
			return nil, err
		}
		sshUserDefault := sshUser
		if sshUserDefault == "" {
			sshUserDefault = sshUsernameForImage(image.Name, image.ID)
		}
		sshUser, err = w.Prompter.Text("SSH user", sshUserDefault)
		if err != nil {
			return nil, err
		}
	}

	agentName, err := w.Prompter.Text("Agent name", defaultAgentName(w.Existing))
	if err != nil {
		return nil, err
	}
	agentName = sanitizeAgentName(agentName)

	if w.GitHubSetup != nil {
		connectGitHub, err := w.Prompter.Confirm("Authenticate Git with your GitHub credentials?", true)
		if err != nil {
			return nil, err
		}
		if connectGitHub {
			if err := w.GitHubSetup(ctx, sshPrivateKeyPath); err != nil {
				return nil, err
			}
		}
	}

	useNemoClaw, err := w.Prompter.Confirm("Use NemoClaw", true)
	if err != nil {
		return nil, err
	}

	runtimeProvider, err := w.Prompter.Select("Select model provider", runtimeProviderOptions(), defaultRuntimeProvider(w.Existing))
	if err != nil {
		return nil, err
	}

	if runtimeProvider == "codex" {
		fmt.Fprintln(w.Out, "Codex auth uses the local browser login flow or existing signed-in state.")
		fmt.Fprintln(w.Out, "If you are not already authenticated, run `openclaw onboard --auth-choice openai-codex` before provisioning.")
	}

	runtimePublicCIDR := "0.0.0.0/0"
	if w.Existing != nil {
		if existingCIDR := strings.TrimSpace(w.Existing.Runtime.PublicCIDR); existingCIDR != "" {
			runtimePublicCIDR = existingCIDR
		}
	}
	if networkMode != "public" {
		runtimePublicCIDR = ""
	}

	nimEndpoint := ""
	if runtimeProvider != "aws-bedrock" {
		nimEndpoint, err = w.Prompter.Text("NIM endpoint", defaultEndpoint(computeClass))
		if err != nil {
			return nil, err
		}
	}

	model := ""
	if runtimeProvider != "codex" {
		model, err = w.Prompter.Text("Model name", defaultRuntimeModel(runtimeProvider))
		if err != nil {
			return nil, err
		}
	}

	cfg := &config.Config{
		Platform: config.PlatformConfig{Name: platform},
		Compute:  config.ComputeConfig{Class: computeClass},
		Region:   config.RegionConfig{Name: region},
		Instance: config.InstanceConfig{Type: instanceType, DiskSizeGB: diskSize, NetworkMode: networkMode},
		Image:    config.ImageConfig{Name: image.Name, ID: image.ID},
		SSH: config.SSHConfig{
			KeyName:        sshKeyName,
			PrivateKeyPath: sshPrivateKeyPath,
			CIDR:           sshCIDR,
			User:           sshUser,
		},
		Infra: config.InfraConfig{
			Backend:   "terraform",
			ModuleDir: filepath.Join("infra", "aws", "ec2"),
		},
		Sandbox: config.SandboxConfig{
			Enabled:     true,
			NetworkMode: networkMode,
			UseNemoClaw: useNemoClaw,
		},
		Runtime: config.RuntimeConfig{
			Endpoint:   nimEndpoint,
			Model:      model,
			Provider:   runtimeProvider,
			PublicCIDR: runtimePublicCIDR,
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
	if strings.TrimSpace(cfg.SSH.KeyName) != "" {
		fmt.Fprintf(w.Out, "ssh key pair: %s\n", cfg.SSH.KeyName)
	}
	if strings.TrimSpace(cfg.SSH.PrivateKeyPath) != "" {
		fmt.Fprintf(w.Out, "ssh private key: %s\n", cfg.SSH.PrivateKeyPath)
	}
	if strings.TrimSpace(cfg.SSH.CIDR) != "" {
		fmt.Fprintf(w.Out, "ssh cidr: %s\n", cfg.SSH.CIDR)
	}
	if strings.TrimSpace(cfg.Runtime.PublicCIDR) != "" {
		fmt.Fprintf(w.Out, "runtime cidr: %s\n", cfg.Runtime.PublicCIDR)
	}
	if strings.TrimSpace(cfg.SSH.User) != "" {
		fmt.Fprintf(w.Out, "ssh user: %s\n", cfg.SSH.User)
	}
	fmt.Fprintf(w.Out, "infra backend: %s\n", cfg.Infra.Backend)
	fmt.Fprintf(w.Out, "terraform module: %s\n", cfg.Infra.ModuleDir)
	fmt.Fprintf(w.Out, "agent name: %s\n", agentName)
	fmt.Fprintf(w.Out, "use NemoClaw: %t\n", cfg.Sandbox.UseNemoClaw)
	fmt.Fprintf(w.Out, "runtime provider: %s\n", cfg.Runtime.Provider)
	if cfg.Runtime.Provider == "codex" {
		fmt.Fprintf(w.Out, "codex auth: browser login or existing local auth\n")
	}
	if cfg.Runtime.Provider == "aws-bedrock" {
		fmt.Fprintf(w.Out, "bedrock auth: uses instance role\n")
	}
	if strings.TrimSpace(cfg.Runtime.Endpoint) != "" {
		fmt.Fprintf(w.Out, "NIM endpoint: %s\n", cfg.Runtime.Endpoint)
	}
	if strings.TrimSpace(cfg.Runtime.Model) != "" {
		fmt.Fprintf(w.Out, "model: %s\n", cfg.Runtime.Model)
	}

	confirm, err := w.Prompter.Confirm("Write this configuration", true)
	if err != nil {
		return nil, err
	}
	if !confirm {
		return nil, errors.New("setup cancelled")
	}

	return cfg, nil
}

func runtimeProviderOptions() []string {
	return []string{"codex", "aws-bedrock", "gemini", "claude-code"}
}

func defaultRuntimeProvider(existing *config.Config) string {
	if existing == nil {
		return "codex"
	}
	provider := strings.ToLower(strings.TrimSpace(existing.Runtime.Provider))
	if provider == "" {
		return "codex"
	}
	return provider
}

func defaultAgentName(existing *config.Config) string {
	if existing == nil {
		return "default"
	}
	return sanitizeAgentName(strings.TrimSpace(existing.Infra.ModuleDir))
}

func sanitizeAgentName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "default"
	}
	return name
}

func defaultRuntimeModel(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "aws-bedrock":
		return "anthropic.claude-3-haiku-20240307-v1:0"
	case "codex":
		return ""
	default:
		return "llama3.2"
	}
}

func (w *Wizard) selectAWSProfile() (string, bool, error) {
	profile := strings.TrimSpace(w.AWSProfile)
	if profile == "" {
		profile = strings.TrimSpace(os.Getenv("AWS_PROFILE"))
	}
	if profile == "" {
		profile = strings.TrimSpace(os.Getenv("AWS_DEFAULT_PROFILE"))
	}
	if profile != "" {
		return profile, false, nil
	}
	if !w.Prompter.Interactive {
		return "", false, errors.New("AWS profile is required: pass --profile, set AWS_PROFILE, or run interactively")
	}
	value, err := w.Prompter.Text("AWS profile", "")
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(value), true, nil
}

func (w *Wizard) listRegions(ctx context.Context) ([]string, error) {
	if w.Provider == nil {
		return fallbackAWSRegions(), nil
	}
	regions, err := w.Provider.ListRegions(ctx)
	if err != nil {
		fmt.Fprintln(w.Out, "Warning: AWS region lookup unavailable; using bundled fallback regions.")
		return fallbackAWSRegions(), nil
	}
	if len(regions) == 0 {
		fmt.Fprintln(w.Out, "Warning: AWS region lookup returned no regions; using bundled fallback regions.")
		return fallbackAWSRegions(), nil
	}
	return regions, nil
}

func (w *Wizard) listInstanceTypes(ctx context.Context, region, computeClass string) ([]string, error) {
	if w.Provider == nil {
		return fallbackAWSInstanceTypes(computeClass), nil
	}
	items, err := w.Provider.RecommendInstanceTypes(ctx, region, computeClass)
	if err != nil {
		fmt.Fprintln(w.Out, "Warning: AWS instance type lookup unavailable; using bundled fallback instance types.")
		return fallbackAWSInstanceTypes(computeClass), nil
	}
	options := make([]string, 0, len(items))
	for _, item := range items {
		options = append(options, item.Name)
	}
	if len(options) == 0 {
		fmt.Fprintln(w.Out, "Warning: AWS instance type lookup returned no options; using bundled fallback instance types.")
		return fallbackAWSInstanceTypes(computeClass), nil
	}
	return options, nil
}

func (w *Wizard) listImages(ctx context.Context, region, computeClass string) ([]provider.BaseImage, error) {
	if w.Provider == nil {
		return fallbackAWSBaseImages(region, computeClass), nil
	}
	imageCtx, cancel := bestEffortAWSContext(ctx)
	defer cancel()
	items, err := w.Provider.RecommendBaseImages(imageCtx, region, computeClass)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return fallbackAWSBaseImages(region, computeClass), nil
	}
	return items, nil
}

func (w *Wizard) warnOnQuota(ctx context.Context, region string) error {
	if w.Provider == nil {
		return nil
	}
	quotaCtx, cancel := bestEffortAWSContext(ctx)
	defer cancel()
	report, err := w.Provider.CheckGPUQuota(quotaCtx, region, "g5")
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

func defaultComputeClass(existing *config.Config) string {
	if existing == nil {
		return config.ComputeClassGPU
	}
	if class := config.EffectiveComputeClass(existing.Compute.Class); class != "" {
		return class
	}
	return config.ComputeClassGPU
}

func defaultInstanceType(computeClass string) string {
	if config.EffectiveComputeClass(computeClass) == config.ComputeClassCPU {
		return "t3.xlarge"
	}
	return "g5.xlarge"
}

func defaultNetworkMode(computeClass string) string {
	return "public"
}

func defaultEndpoint(computeClass string) string {
	if config.EffectiveComputeClass(computeClass) == config.ComputeClassCPU {
		return "https://nim.example.com"
	}
	return "http://localhost:11434"
}

func defaultSSHPrivateKeyPath() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "~/.ssh/id_ed25519"
	}
	return filepath.Join(home, ".ssh", "id_ed25519")
}

func defaultSSHKeyName() string {
	return "openclaw"
}

func defaultDetectInitSSHCIDR(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.ipify.org", nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("unexpected status %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(body))
	if value == "" {
		return "", errors.New("empty public IP")
	}
	if strings.Contains(value, "/") {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return "", err
		}
		return prefix.String(), nil
	}
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return "", err
	}
	if addr.Is4() {
		return addr.String() + "/32", nil
	}
	return addr.String() + "/128", nil
}

func sshUsernameForImage(imageName, imageID string) string {
	lower := strings.ToLower(strings.TrimSpace(imageName) + " " + strings.TrimSpace(imageID))
	if strings.Contains(lower, "ubuntu") {
		return "ubuntu"
	}
	return "ec2-user"
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
	if preferred := findBaseImage(images, preferredBaseImageName(images)); preferred.Name != "" {
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

func preferredBaseImageName(images []provider.BaseImage) string {
	for _, image := range images {
		lower := strings.ToLower(strings.TrimSpace(image.Name))
		if strings.Contains(lower, "ubuntu 22.04") && !strings.Contains(lower, "gpu") {
			return image.Name
		}
	}
	for _, image := range images {
		lower := strings.ToLower(strings.TrimSpace(image.Name))
		if strings.Contains(lower, "deep learning ami gpu ubuntu 22.04") {
			return image.Name
		}
	}
	if len(images) > 0 {
		return images[0].Name
	}
	return ""
}

func formatUsage(value *int) string {
	if value == nil {
		return "n/a"
	}
	return fmt.Sprintf("%d", *value)
}

func fallbackAWSBaseImages(region, computeClass string) []provider.BaseImage {
	if config.EffectiveComputeClass(computeClass) == config.ComputeClassCPU {
		return []provider.BaseImage{{
			Name:               "Ubuntu 22.04 LTS",
			Architecture:       "x86_64",
			Owner:              "canonical",
			VirtualizationType: "hvm",
			RootDeviceType:     "ebs",
			Region:             region,
			Source:             "fallback",
			SSMParameter:       "/aws/service/canonical/ubuntu/server/22.04/stable/current/amd64/hvm/ebs-gp2/ami-id",
		}}
	}
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

func fallbackAWSRegions() []string {
	return []string{"us-east-1", "us-west-2"}
}

func fallbackAWSInstanceTypes(computeClass string) []string {
	if config.EffectiveComputeClass(computeClass) == config.ComputeClassCPU {
		return []string{"t3.xlarge", "t3.2xlarge", "t3.medium"}
	}
	return []string{"g5.xlarge", "g4dn.xlarge", "g6.xlarge"}
}

func bestEffortAWSContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, initAWSLookupTimeout)
}
