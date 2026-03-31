package terraform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type InfraBackend interface {
	Init(ctx context.Context, workdir string) error
	Plan(ctx context.Context, workdir string, varsFile string) error
	Apply(ctx context.Context, workdir string, varsFile string) error
	Destroy(ctx context.Context, workdir string, varsFile string) error
	Output(ctx context.Context, workdir string) (*InfraOutput, error)
}

type InfraOutput struct {
	InstanceID         string   `json:"instance_id"`
	PublicIP           string   `json:"public_ip"`
	PrivateIP          string   `json:"private_ip"`
	ConnectionInfo     string   `json:"connection_info"`
	SecurityGroupID    string   `json:"security_group_id"`
	SecurityGroupRules []string `json:"security_group_rules"`
	Region             string   `json:"region"`
	NetworkMode        string   `json:"network_mode"`
}

type TerraformBackend struct {
	Binary    string
	ModuleDir string
	Profile   string
	Region    string
}

func New(moduleDir string) *TerraformBackend {
	return &TerraformBackend{Binary: "terraform", ModuleDir: moduleDir}
}

func (t *TerraformBackend) Init(ctx context.Context, workdir string) error {
	if err := t.ensureBinary(); err != nil {
		return err
	}
	if err := t.prepareWorkspace(workdir); err != nil {
		return err
	}
	return t.run(ctx, workdir, "init", "-input=false", "-no-color")
}

func (t *TerraformBackend) Plan(ctx context.Context, workdir string, varsFile string) error {
	if err := t.ensureBinary(); err != nil {
		return err
	}
	args := []string{"plan", "-input=false", "-no-color"}
	if strings.TrimSpace(varsFile) != "" {
		args = append(args, "-var-file="+varsFile)
	}
	return t.run(ctx, workdir, args...)
}

func (t *TerraformBackend) Apply(ctx context.Context, workdir string, varsFile string) error {
	if err := t.ensureBinary(); err != nil {
		return err
	}
	args := []string{"apply", "-input=false", "-auto-approve", "-no-color"}
	if strings.TrimSpace(varsFile) != "" {
		args = append(args, "-var-file="+varsFile)
	}
	return t.run(ctx, workdir, args...)
}

func (t *TerraformBackend) Destroy(ctx context.Context, workdir string, varsFile string) error {
	if err := t.ensureBinary(); err != nil {
		return err
	}
	args := []string{"destroy", "-input=false", "-auto-approve", "-no-color"}
	if strings.TrimSpace(varsFile) != "" {
		args = append(args, "-var-file="+varsFile)
	}
	return t.run(ctx, workdir, args...)
}

func (t *TerraformBackend) Output(ctx context.Context, workdir string) (*InfraOutput, error) {
	if err := t.ensureBinary(); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, t.binary(), "output", "-json")
	cmd.Dir = workdir
	cmd.Env = t.commandEnv()
	data, err := cmd.Output()
	if err != nil {
		return nil, t.wrapCommandError(err, "output")
	}
	if len(data) == 0 {
		return nil, errors.New("terraform output returned no data")
	}
	return parseInfraOutput(data)
}

func (t *TerraformBackend) ensureBinary() error {
	if _, err := exec.LookPath(t.binary()); err != nil {
		return fmt.Errorf("terraform binary is required: install Terraform and ensure %q is on PATH", t.binary())
	}
	return nil
}

func (t *TerraformBackend) binary() string {
	if strings.TrimSpace(t.Binary) == "" {
		return "terraform"
	}
	return strings.TrimSpace(t.Binary)
}

func (t *TerraformBackend) run(ctx context.Context, workdir string, args ...string) error {
	cmd := exec.CommandContext(ctx, t.binary(), args...)
	cmd.Dir = workdir
	cmd.Env = t.commandEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return t.wrapCommandErrorWithOutput(err, strings.Join(args, " "), out)
	}
	return nil
}

func (t *TerraformBackend) commandEnv() []string {
	env := filterEnv(os.Environ(), "AWS_PROFILE", "AWS_SDK_LOAD_CONFIG", "AWS_REGION", "AWS_DEFAULT_REGION")
	if profile := strings.TrimSpace(t.Profile); profile != "" {
		env = append(env, "AWS_PROFILE="+profile)
		env = append(env, "AWS_SDK_LOAD_CONFIG=1")
	}
	if region := strings.TrimSpace(t.Region); region != "" {
		env = append(env, "AWS_REGION="+region)
		env = append(env, "AWS_DEFAULT_REGION="+region)
	}
	return env
}

func filterEnv(env []string, keys ...string) []string {
	if len(env) == 0 || len(keys) == 0 {
		return append([]string(nil), env...)
	}
	blocked := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		blocked[key+"="] = struct{}{}
	}
	out := make([]string, 0, len(env))
	for _, entry := range env {
		skip := false
		for prefix := range blocked {
			if strings.HasPrefix(entry, prefix) {
				skip = true
				break
			}
		}
		if !skip {
			out = append(out, entry)
		}
	}
	return out
}

func (t *TerraformBackend) wrapCommandError(err error, command string) error {
	return fmt.Errorf("terraform %s failed: %w", command, err)
}

func (t *TerraformBackend) wrapCommandErrorWithOutput(err error, command string, out []byte) error {
	msg := strings.TrimSpace(string(out))
	if msg == "" {
		return t.wrapCommandError(err, command)
	}
	return fmt.Errorf("terraform %s failed: %s: %w", command, msg, err)
}

func (t *TerraformBackend) prepareWorkspace(workdir string) error {
	if strings.TrimSpace(t.ModuleDir) == "" {
		return nil
	}
	src := strings.TrimSpace(t.ModuleDir)
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("terraform module dir %q: %w", src, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("terraform module dir %q is not a directory", src)
	}

	return filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if strings.HasPrefix(rel, ".terraform") || rel == "terraform.tfstate" || strings.HasPrefix(rel, "terraform.tfstate.") {
			return nil
		}
		dst := filepath.Join(workdir, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if _, err := os.Stat(dst); err == nil {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o600)
	})
}

func parseInfraOutput(data []byte) (*InfraOutput, error) {
	var raw map[string]struct {
		Value any `json:"value"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse terraform output: %w", err)
	}

	out := &InfraOutput{}
	out.InstanceID = asString(raw["instance_id"].Value)
	out.PublicIP = asString(raw["public_ip"].Value)
	out.PrivateIP = asString(raw["private_ip"].Value)
	out.ConnectionInfo = asString(raw["connection_info"].Value)
	out.SecurityGroupID = asString(raw["security_group_id"].Value)
	out.Region = asString(raw["region"].Value)
	out.NetworkMode = asString(raw["network_mode"].Value)
	out.SecurityGroupRules = asStringSlice(raw["security_group_rules"].Value)
	return out, nil
}

func asString(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	default:
		return ""
	}
}

func asStringSlice(value any) []string {
	switch v := value.(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s := asString(item); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s := strings.TrimSpace(item); s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
