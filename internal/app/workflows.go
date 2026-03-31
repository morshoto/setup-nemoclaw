package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"openclaw/internal/config"
	"openclaw/internal/host"
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

type verifyOptions struct {
	Target            string
	SSHUser           string
	SSHKey            string
	SSHPort           int
	RuntimeConfigPath string
}

func runInfraCreate(ctx context.Context, profile string, cfg *config.Config, sshKeyName, sshCIDR string) (*provider.Instance, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}

	prov := newAWSProvider(profile, cfg.Compute.Class)
	image, err := resolveInfraImage(ctx, prov, cfg)
	if err != nil {
		return nil, err
	}
	req := provider.CreateInstanceRequest{
		Region:           cfg.Region.Name,
		InstanceType:     cfg.Instance.Type,
		Image:            image.ID,
		ImageName:        image.Name,
		DiskSizeGB:       cfg.Instance.DiskSizeGB,
		NetworkMode:      cfg.Sandbox.NetworkMode,
		ConnectionMethod: connectionMethodFor(sshKeyName, cfg.Sandbox.NetworkMode),
		SSHKeyName:       sshKeyName,
		SSHCIDR:          sshCIDR,
	}
	return prov.CreateInstance(ctx, req)
}

func runInstallWorkflow(ctx context.Context, profile string, cfg *config.Config, opts installOptions) (runtimeinstall.Result, string, error) {
	if cfg == nil {
		return runtimeinstall.Result{}, "", errors.New("config is required")
	}
	if strings.TrimSpace(opts.Target) == "" {
		return runtimeinstall.Result{}, "", errors.New("target is required")
	}

	resolvedTarget, err := resolveHostTarget(ctx, profile, cfg, opts.Target)
	if err != nil {
		return runtimeinstall.Result{}, "", err
	}

	user := strings.TrimSpace(opts.SSHUser)
	if user == "" {
		user = sshUsernameForImage(cfg.Image.Name, cfg.Image.ID)
	}
	exec := newSSHExecutor(host.SSHConfig{
		Host:           resolvedTarget,
		Port:           opts.SSHPort,
		User:           user,
		IdentityFile:   strings.TrimSpace(opts.SSHKey),
		ConnectTimeout: 15 * time.Second,
	})

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

	user := strings.TrimSpace(opts.SSHUser)
	if user == "" && cfg != nil {
		user = sshUsernameForImage(cfg.Image.Name, cfg.Image.ID)
	}
	exec := newSSHExecutor(host.SSHConfig{
		Host:           resolvedTarget,
		Port:           opts.SSHPort,
		User:           user,
		IdentityFile:   strings.TrimSpace(opts.SSHKey),
		ConnectTimeout: 15 * time.Second,
	})

	report, err := verify.Verifier{Host: exec}.Verify(ctx, verify.Request{
		Config:            cfg,
		RuntimeConfigPath: runtimeConfigPath,
		TargetDescription: resolvedTarget,
	})
	return report, resolvedTarget, err
}

func runCreateWorkflow(ctx context.Context, profile string, cfg *config.Config, opts createOptions) (*provider.Instance, runtimeinstall.Result, verify.Report, error) {
	instance, err := runInfraCreate(ctx, profile, cfg, opts.SSHKeyName, opts.SSHCIDR)
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
