package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"openclaw/internal/config"
	"openclaw/internal/host"
	infratf "openclaw/internal/infra/terraform"
	"openclaw/internal/provider"
	"openclaw/internal/runtimeinstall"
	"openclaw/internal/verify"
)

var newLocalExecutor = func() host.Executor {
	return host.NewLocalExecutor()
}

var newSSHExecutor = func(cfg host.SSHConfig) host.Executor {
	return host.NewSSHExecutor(cfg)
}

var (
	defaultSSHReadyTimeout     = 5 * time.Minute
	defaultSSHReadyInitialWait = 2 * time.Second
	defaultSSHReadyMaxWait     = 10 * time.Second
)

type installOptions struct {
	Target            string
	SSHUser           string
	SSHKey            string
	SSHPort           int
	WorkingDir        string
	Port              int
	UseNemoClaw       bool
	DisableNemoClaw   bool
	RuntimeConfigPath string
}

type createOptions struct {
	SSHKeyName      string
	SSHCIDR         string
	SSHUser         string
	SSHKey          string
	SSHPort         int
	WorkingDir      string
	Port            int
	UseNemoClaw     bool
	DisableNemoClaw bool
}

type terraformVars struct {
	Region       string `json:"region"`
	ComputeClass string `json:"compute_class"`
	InstanceType string `json:"instance_type"`
	DiskSizeGB   int    `json:"disk_size_gb"`
	NetworkMode  string `json:"network_mode"`
	ImageID      string `json:"image_id"`
	SSHKeyName   string `json:"ssh_key_name"`
	SSHPublicKey string `json:"ssh_public_key"`
	SSHCIDR      string `json:"ssh_cidr"`
	SSHUser      string `json:"ssh_user"`
	NamePrefix   string `json:"name_prefix"`
	UseNemoClaw  bool   `json:"use_nemoclaw"`
	NIMEndpoint  string `json:"nim_endpoint"`
	Model        string `json:"model"`
}

type verifyOptions struct {
	Target            string
	SSHUser           string
	SSHKey            string
	SSHPort           int
	RuntimeConfigPath string
}

func runInfraCreate(ctx context.Context, profile string, cfg *config.Config, opts createOptions) (*provider.Instance, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}

	networkMode := effectiveNetworkMode(cfg)
	if networkMode == "" {
		networkMode = "public"
	}
	if networkMode == "private" {
		return nil, errors.New("private networking is not supported yet; use public networking or add an SSM/bastion executor")
	}
	if !config.IsValidNetworkMode(networkMode) {
		return nil, fmt.Errorf("unsupported network mode %q", networkMode)
	}

	sshKeyName, sshCIDR, sshUser, sshKeyPath, err := resolveProvisioningSSH(ctx, cfg, opts)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(sshKeyPath) == "" {
		return nil, errors.New("ssh private key path is required for public networking")
	}
	sshPublicKey, err := deriveSSHPublicKeyFunc(ctx, sshKeyPath)
	if err != nil {
		return nil, err
	}

	adviser := newAWSProvider(profile, cfg.Compute.Class)
	if _, err := adviser.CheckAuth(ctx); err != nil {
		return nil, fmt.Errorf("aws auth check failed: %w", err)
	}
	image, err := resolveInfraImage(ctx, adviser, cfg)
	if err != nil {
		return nil, err
	}
	instanceType := strings.TrimSpace(cfg.Instance.Type)
	if instanceType == "" {
		recs, recErr := adviser.RecommendInstanceTypes(ctx, cfg.Region.Name, cfg.Compute.Class)
		if recErr != nil {
			return nil, recErr
		}
		if len(recs) == 0 {
			return nil, errors.New("no recommended instance types available")
		}
		instanceType = recs[0].Name
	}

	workdir, err := prepareTerraformWorkdir()
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(workdir)

	varsPath, err := writeTerraformVars(workdir, terraformVars{
		Region:       cfg.Region.Name,
		ComputeClass: config.EffectiveComputeClass(cfg.Compute.Class),
		InstanceType: instanceType,
		DiskSizeGB:   cfg.Instance.DiskSizeGB,
		NetworkMode:  networkMode,
		ImageID:      image.ID,
		SSHKeyName:   sshKeyName,
		SSHPublicKey: sshPublicKey,
		SSHCIDR:      sshCIDR,
		SSHUser:      sshUser,
		NamePrefix:   "openclaw",
		UseNemoClaw:  cfg.Sandbox.UseNemoClaw,
		NIMEndpoint:  cfg.Runtime.Endpoint,
		Model:        cfg.Runtime.Model,
	})
	if err != nil {
		return nil, err
	}

	backend, err := newTerraformBackend(profile, cfg)
	if err != nil {
		return nil, err
	}
	if err := backend.Init(ctx, workdir); err != nil {
		return nil, err
	}
	if err := backend.Plan(ctx, workdir, varsPath); err != nil {
		return nil, err
	}
	if err := backend.Apply(ctx, workdir, varsPath); err != nil {
		return nil, err
	}
	output, err := backend.Output(ctx, workdir)
	if err != nil {
		return nil, err
	}
	return infraOutputToInstance(output, networkMode, sshUser, image), nil
}

func runInstallWorkflow(ctx context.Context, profile string, cfg *config.Config, opts installOptions) (runtimeinstall.Result, string, error) {
	if cfg == nil {
		return runtimeinstall.Result{}, "", errors.New("config is required")
	}
	if strings.TrimSpace(opts.Target) == "" {
		return runtimeinstall.Result{}, "", errors.New("target is required")
	}

	networkMode := effectiveNetworkMode(cfg)
	if networkMode == "private" {
		return runtimeinstall.Result{}, "", errors.New("private networking is not supported yet; install requires SSH access to the instance")
	}
	if !config.IsValidNetworkMode(networkMode) && networkMode != "" {
		return runtimeinstall.Result{}, "", fmt.Errorf("unsupported network mode %q", networkMode)
	}

	resolvedTarget, err := resolveHostTarget(ctx, profile, cfg, opts.Target)
	if err != nil {
		return runtimeinstall.Result{}, "", err
	}

	user, keyPath, err := resolveInstallSSH(cfg, opts.SSHUser, opts.SSHKey)
	if err != nil {
		return runtimeinstall.Result{}, "", err
	}
	exec := newSSHExecutor(host.SSHConfig{
		Host:           resolvedTarget,
		Port:           opts.SSHPort,
		User:           user,
		IdentityFile:   keyPath,
		ConnectTimeout: 15 * time.Second,
	})
	if err := waitForSSHReady(ctx, exec, resolvedTarget); err != nil {
		return runtimeinstall.Result{}, "", err
	}

	useNemo := cfg.Sandbox.UseNemoClaw
	if opts.UseNemoClaw {
		useNemo = true
	}
	if opts.DisableNemoClaw {
		useNemo = false
	}

	inst := runtimeinstall.Installer{Host: exec}
	result, err := inst.Install(ctx, runtimeinstall.Request{
		Config:       cfg,
		UseNemoClaw:  &useNemo,
		Port:         opts.Port,
		WorkingDir:   opts.WorkingDir,
		ComputeClass: cfg.Compute.Class,
	})
	return result, resolvedTarget, err
}

func runVerifyWorkflow(ctx context.Context, profile string, cfg *config.Config, opts verifyOptions) (verify.Report, string, error) {
	runtimeConfigPath := strings.TrimSpace(opts.RuntimeConfigPath)
	if runtimeConfigPath == "" {
		runtimeConfigPath = "/opt/openclaw/runtime.yaml"
	}

	if strings.TrimSpace(opts.Target) == "" {
		report, err := verify.Verifier{Host: newLocalExecutor()}.Verify(ctx, verify.Request{
			Config:            cfg,
			RuntimeConfigPath: runtimeConfigPath,
			TargetDescription: "local host",
		})
		return report, "local host", err
	}

	resolvedTarget, err := resolveVerifyTarget(ctx, profile, cfg, opts.Target)
	if err != nil {
		return verify.Report{}, "", err
	}

	user, keyPath, err := resolveInstallSSH(cfg, opts.SSHUser, opts.SSHKey)
	if err != nil {
		return verify.Report{}, "", err
	}
	exec := newSSHExecutor(host.SSHConfig{
		Host:           resolvedTarget,
		Port:           opts.SSHPort,
		User:           user,
		IdentityFile:   keyPath,
		ConnectTimeout: 15 * time.Second,
	})
	if err := waitForSSHReady(ctx, exec, resolvedTarget); err != nil {
		return verify.Report{}, "", err
	}

	report, err := verify.Verifier{Host: exec}.Verify(ctx, verify.Request{
		Config:            cfg,
		RuntimeConfigPath: runtimeConfigPath,
		TargetDescription: resolvedTarget,
	})
	return report, resolvedTarget, err
}

func runCreateWorkflow(ctx context.Context, profile string, cfg *config.Config, opts createOptions) (*provider.Instance, runtimeinstall.Result, verify.Report, error) {
	instance, err := runInfraCreate(ctx, profile, cfg, opts)
	if err != nil {
		return nil, runtimeinstall.Result{}, verify.Report{}, err
	}

	target := instanceTarget(instance)
	installResult, _, err := runInstallWorkflow(ctx, profile, cfg, installOptions{
		Target:          target,
		SSHUser:         opts.SSHUser,
		SSHKey:          opts.SSHKey,
		SSHPort:         opts.SSHPort,
		WorkingDir:      opts.WorkingDir,
		Port:            opts.Port,
		UseNemoClaw:     opts.UseNemoClaw,
		DisableNemoClaw: opts.DisableNemoClaw,
	})
	if err != nil {
		return instance, installResult, verify.Report{}, err
	}

	verifyReport, _, err := runVerifyWorkflow(ctx, profile, cfg, verifyOptions{
		Target:            target,
		SSHUser:           opts.SSHUser,
		SSHKey:            opts.SSHKey,
		SSHPort:           opts.SSHPort,
		RuntimeConfigPath: installResult.ConfigPath,
	})
	return instance, installResult, verifyReport, err
}

func resolveHostTarget(ctx context.Context, profile string, cfg *config.Config, target string) (string, error) {
	if strings.HasPrefix(strings.TrimSpace(target), "i-") {
		prov := newAWSProvider(profile, "")
		regions := []string{}
		if cfg != nil && strings.TrimSpace(cfg.Region.Name) != "" {
			regions = append(regions, strings.TrimSpace(cfg.Region.Name))
		} else {
			listedRegions, err := prov.ListRegions(ctx)
			if err != nil {
				return "", err
			}
			regions = append(regions, listedRegions...)
		}
		for _, region := range regions {
			instance, err := prov.GetInstance(ctx, region, target)
			if err != nil {
				continue
			}
			if strings.TrimSpace(instance.PublicIP) != "" {
				return instance.PublicIP, nil
			}
			if strings.TrimSpace(instance.PrivateIP) != "" {
				return instance.PrivateIP, nil
			}
		}
		return "", fmt.Errorf("instance %s does not expose an SSH-reachable address", target)
	}
	return target, nil
}

func resolveVerifyTarget(ctx context.Context, profile string, cfg *config.Config, target string) (string, error) {
	return resolveHostTarget(ctx, profile, cfg, target)
}

func effectiveNetworkMode(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	if mode := strings.TrimSpace(cfg.Instance.NetworkMode); mode != "" {
		return strings.ToLower(mode)
	}
	return strings.ToLower(strings.TrimSpace(cfg.Sandbox.NetworkMode))
}

func resolveProvisioningSSH(ctx context.Context, cfg *config.Config, opts createOptions) (string, string, string, string, error) {
	if cfg == nil {
		return "", "", "", "", errors.New("config is required")
	}

	sshKeyName := strings.TrimSpace(opts.SSHKeyName)
	if sshKeyName == "" {
		sshKeyName = strings.TrimSpace(cfg.SSH.KeyName)
	}
	if sshKeyName == "" {
		sshKeyName = defaultSSHKeyName()
	}
	sshCIDR := strings.TrimSpace(opts.SSHCIDR)
	if sshCIDR == "" {
		sshCIDR = strings.TrimSpace(cfg.SSH.CIDR)
	}
	if sshCIDR == "" {
		return "", "", "", "", errors.New("ssh cidr is required for public networking; run `openclaw init` or pass --ssh-cidr")
	}
	sshUser := strings.TrimSpace(opts.SSHUser)
	if sshUser == "" {
		sshUser = strings.TrimSpace(cfg.SSH.User)
	}
	if sshUser == "" {
		sshUser = sshUsernameForImage(cfg.Image.Name, cfg.Image.ID)
	}
	sshKeyPath := strings.TrimSpace(opts.SSHKey)
	if sshKeyPath == "" {
		sshKeyPath = strings.TrimSpace(cfg.SSH.PrivateKeyPath)
	}
	if sshKeyPath == "" {
		sshKeyPath = defaultSSHPrivateKeyPath()
	}

	return sshKeyName, sshCIDR, sshUser, sshKeyPath, nil
}

func resolveInstallSSH(cfg *config.Config, userFlag, keyFlag string) (string, string, error) {
	if cfg == nil {
		return "", "", errors.New("config is required")
	}
	user := strings.TrimSpace(userFlag)
	if user == "" {
		user = strings.TrimSpace(cfg.SSH.User)
	}
	if user == "" {
		user = sshUsernameForImage(cfg.Image.Name, cfg.Image.ID)
	}
	keyPath := strings.TrimSpace(keyFlag)
	if keyPath == "" {
		keyPath = strings.TrimSpace(cfg.SSH.PrivateKeyPath)
	}
	if keyPath == "" {
		keyPath = defaultSSHPrivateKeyPath()
	}
	resolved, err := resolveSSHPrivateKeyPath(keyPath)
	if err != nil {
		return "", "", err
	}
	if _, err := os.Stat(resolved); err != nil {
		return "", "", fmt.Errorf("ssh private key %q does not exist; pass --ssh-key or update ssh.private_key_path", resolved)
	}
	return user, resolved, nil
}

func waitForSSHReady(ctx context.Context, exec host.Executor, target string) error {
	waitCtx := ctx
	var cancel context.CancelFunc
	if _, ok := ctx.Deadline(); !ok {
		waitCtx, cancel = context.WithTimeout(ctx, defaultSSHReadyTimeout)
		defer cancel()
	}

	delay := defaultSSHReadyInitialWait
	for {
		_, err := exec.Run(waitCtx, "true")
		if err == nil {
			return nil
		}
		if !isTransientSSHError(err) {
			return fmt.Errorf("wait for ssh readiness on %s: %w", target, err)
		}
		if waitCtx.Err() != nil {
			return fmt.Errorf("wait for ssh readiness on %s: %w", target, err)
		}

		timer := time.NewTimer(delay)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			return fmt.Errorf("wait for ssh readiness on %s: %w", target, waitCtx.Err())
		case <-timer.C:
		}

		delay *= 2
		if delay > defaultSSHReadyMaxWait {
			delay = defaultSSHReadyMaxWait
		}
	}
}

func isTransientSSHError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, fragment := range []string{"connection refused", "connection timed out", "operation timed out"} {
		if strings.Contains(msg, fragment) {
			return true
		}
	}
	return false
}

func prepareTerraformWorkdir() (string, error) {
	workdir, err := os.MkdirTemp("", "openclaw-terraform-*")
	if err != nil {
		return "", fmt.Errorf("create terraform workspace: %w", err)
	}
	return workdir, nil
}

func resolveTerraformModuleDir(cfg *config.Config) (string, error) {
	if cfg == nil {
		return "", errors.New("config is required")
	}
	moduleDir := strings.TrimSpace(cfg.Infra.ModuleDir)
	if moduleDir == "" {
		moduleDir = filepath.Join("infra", "aws", "ec2")
	}
	if !filepath.IsAbs(moduleDir) {
		abs, err := filepath.Abs(moduleDir)
		if err != nil {
			return "", fmt.Errorf("resolve terraform module dir %q: %w", moduleDir, err)
		}
		moduleDir = abs
	}
	return moduleDir, nil
}

func writeTerraformVars(workdir string, vars terraformVars) (string, error) {
	data, err := json.MarshalIndent(vars, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal terraform vars: %w", err)
	}
	path := filepath.Join(workdir, "openclaw.auto.tfvars.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("write terraform vars: %w", err)
	}
	return path, nil
}

var newTerraformBackend = func(profile string, cfg *config.Config) (infratf.InfraBackend, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}
	moduleDir, err := resolveTerraformModuleDir(cfg)
	if err != nil {
		return nil, err
	}
	backend := infratf.New(moduleDir)
	backend.Profile = strings.TrimSpace(profile)
	backend.Region = strings.TrimSpace(cfg.Region.Name)
	return backend, nil
}

func infraOutputToInstance(output *infratf.InfraOutput, networkMode, sshUser string, image provider.BaseImage) *provider.Instance {
	if output == nil {
		return nil
	}
	instance := &provider.Instance{
		ID:                 strings.TrimSpace(output.InstanceID),
		Name:               strings.TrimSpace(output.InstanceID),
		Region:             strings.TrimSpace(output.Region),
		PublicIP:           strings.TrimSpace(output.PublicIP),
		PrivateIP:          strings.TrimSpace(output.PrivateIP),
		ConnectionInfo:     strings.TrimSpace(output.ConnectionInfo),
		SecurityGroupID:    strings.TrimSpace(output.SecurityGroupID),
		SecurityGroupRules: append([]string(nil), output.SecurityGroupRules...),
	}
	if instance.ConnectionInfo == "" {
		if instance.PublicIP != "" && strings.EqualFold(networkMode, "public") {
			instance.ConnectionInfo = fmt.Sprintf("ssh -i <your-key>.pem %s@%s", sshUser, instance.PublicIP)
		} else if instance.PrivateIP != "" {
			instance.ConnectionInfo = fmt.Sprintf("private IP access: %s", instance.PrivateIP)
		}
	}
	if instance.ConnectionInfo == "" && image.ID != "" {
		instance.ConnectionInfo = fmt.Sprintf("instance ready for %s", image.Name)
	}
	return instance
}

func instanceTarget(instance *provider.Instance) string {
	if instance == nil {
		return ""
	}
	if strings.TrimSpace(instance.PublicIP) != "" {
		return strings.TrimSpace(instance.PublicIP)
	}
	return strings.TrimSpace(instance.PrivateIP)
}

func printWorkflowSuccess(out io.Writer, instance *provider.Instance, installResult runtimeinstall.Result, verifyReport verify.Report, cfgPath string, cfg *config.Config, target string, createMode bool) {
	if instance != nil {
		printCreatedInstance(out, instance)
	}
	if strings.TrimSpace(target) != "" {
		fmt.Fprintf(out, "connection target: %s\n", target)
	}
	if strings.TrimSpace(installResult.WorkingDir) != "" {
		fmt.Fprintf(out, "working directory: %s\n", installResult.WorkingDir)
	}
	if strings.TrimSpace(installResult.ConfigPath) != "" {
		fmt.Fprintf(out, "runtime config: %s\n", installResult.ConfigPath)
	}
	if len(verifyReport.Checks) > 0 {
		printVerificationReport(out, verifyReport)
	}
	if strings.TrimSpace(cfgPath) != "" && strings.TrimSpace(target) != "" {
		fmt.Fprintf(out, "verify command example: openclaw verify --config %s --target %s\n", cfgPath, target)
	}
	if createMode && strings.TrimSpace(cfgPath) != "" && strings.TrimSpace(target) != "" {
		fmt.Fprintf(out, "install command example: openclaw install --config %s --target %s\n", cfgPath, target)
	}
	fmt.Fprintln(out, "next step: keep the runtime config and SSH target handy for future verify or install runs")
}

func printVerificationReport(out io.Writer, report verify.Report) {
	fmt.Fprintln(out, "verification summary")
	for _, check := range report.Checks {
		status := "PASS"
		switch {
		case check.Skipped:
			status = "SKIP"
		case !check.Passed:
			status = "FAIL"
		}
		fmt.Fprintf(out, "- %s: %s\n", check.Name, status)
		if strings.TrimSpace(check.Message) != "" {
			fmt.Fprintf(out, "  %s\n", check.Message)
		}
		if !check.Passed && strings.TrimSpace(check.Remediation) != "" {
			fmt.Fprintf(out, "  remediation: %s\n", check.Remediation)
		}
	}
	if report.Failed() {
		fmt.Fprintf(out, "required checks failed: %d\n", report.RequiredFailures())
	} else {
		fmt.Fprintln(out, "all required checks passed")
	}
}

func printSuccessNextSteps(out io.Writer, cfgPath, target string, includeInstall bool) {
	fmt.Fprintln(out, "next steps")
	if strings.TrimSpace(target) != "" && strings.TrimSpace(cfgPath) != "" {
		fmt.Fprintf(out, "- verify: openclaw verify --config %s --target %s\n", cfgPath, target)
	}
	if includeInstall && strings.TrimSpace(target) != "" && strings.TrimSpace(cfgPath) != "" {
		fmt.Fprintf(out, "- install: openclaw install --config %s --target %s\n", cfgPath, target)
	}
	fmt.Fprintln(out, "- destroy: not implemented yet")
}

func wrapUserFacingError(action string, err error, likelyCause string, nextSteps ...string) error {
	if err == nil {
		return nil
	}
	return &userFacingError{
		Action:      action,
		Cause:       err,
		LikelyCause: likelyCause,
		NextSteps:   append([]string(nil), nextSteps...),
	}
}

type userFacingError struct {
	Action      string
	Cause       error
	LikelyCause string
	NextSteps   []string
}

func (e *userFacingError) Error() string {
	if e == nil {
		return ""
	}
	var b strings.Builder
	if strings.TrimSpace(e.Action) != "" {
		b.WriteString(e.Action)
	}
	if e.Cause != nil {
		if b.Len() > 0 {
			b.WriteString(": ")
		}
		b.WriteString(e.Cause.Error())
	}
	if strings.TrimSpace(e.LikelyCause) != "" {
		b.WriteString("\nlikely cause: ")
		b.WriteString(strings.TrimSpace(e.LikelyCause))
	}
	if len(e.NextSteps) > 0 {
		b.WriteString("\nnext steps:")
		for _, step := range e.NextSteps {
			if strings.TrimSpace(step) == "" {
				continue
			}
			b.WriteString("\n- ")
			b.WriteString(strings.TrimSpace(step))
		}
	}
	return b.String()
}

func (e *userFacingError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
