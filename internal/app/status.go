package app

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"openclaw/internal/config"
)

type agentStatusReport struct {
	Root   string
	Agents []agentStatusEntry
}

type agentStatusEntry struct {
	Name   string
	Path   string
	Files  []string
	Config config.Config
	Err    error
}

func newStatusCommand(app *App) *cobra.Command {
	var agentsDir string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show formatted agent configuration status",
		RunE: func(cmd *cobra.Command, args []string) error {
			report, err := loadAgentStatusReport(agentsDir)
			printAgentStatusReport(cmd.OutOrStdout(), report)
			return err
		},
	}

	cmd.Flags().StringVar(&agentsDir, "agents-dir", "agents", "path to the agents directory")
	return cmd
}

func loadAgentStatusReport(root string) (agentStatusReport, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		root = "agents"
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return agentStatusReport{Root: root}, nil
		}
		return agentStatusReport{}, fmt.Errorf("read agents directory %q: %w", root, err)
	}

	var report agentStatusReport
	report.Root = root

	var errs []error
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		agent, agentErr := loadAgentStatusEntry(filepath.Join(root, entry.Name()))
		report.Agents = append(report.Agents, agent)
		if agentErr != nil {
			errs = append(errs, agentErr)
		}
	}

	sort.Slice(report.Agents, func(i, j int) bool {
		return report.Agents[i].Name < report.Agents[j].Name
	})

	return report, errors.Join(errs...)
}

func loadAgentStatusEntry(path string) (agentStatusEntry, error) {
	name := filepath.Base(path)
	entries, err := os.ReadDir(path)
	if err != nil {
		return agentStatusEntry{Name: name, Path: path, Err: fmt.Errorf("read agent directory %q: %w", path, err)}, fmt.Errorf("%s: %w", name, err)
	}

	files := make([]string, 0, len(entries))
	merged := map[string]any{}
	var errs []error

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !isYAMLConfigFile(entry.Name()) {
			continue
		}
		files = append(files, entry.Name())
		fragment, fragmentErr := loadYAMLDocument(filepath.Join(path, entry.Name()))
		if fragmentErr != nil {
			errs = append(errs, fmt.Errorf("%s/%s: %w", name, entry.Name(), fragmentErr))
			continue
		}
		mergeYAMLDocuments(merged, fragment)
	}

	if len(files) == 0 {
		err := fmt.Errorf("no YAML config files found in %q", path)
		return agentStatusEntry{Name: name, Path: path, Err: err}, err
	}
	if len(errs) > 0 {
		err := errors.Join(errs...)
		return agentStatusEntry{Name: name, Path: path, Files: append([]string(nil), files...), Err: err}, err
	}

	cfg, decodeErr := decodeMergedAgentConfig(merged)
	if decodeErr != nil {
		err := fmt.Errorf("decode merged config: %w", decodeErr)
		return agentStatusEntry{Name: name, Path: path, Files: append([]string(nil), files...), Err: err}, err
	}
	if validateErr := config.Validate(&cfg); validateErr != nil {
		err := fmt.Errorf("validate merged config: %w", validateErr)
		return agentStatusEntry{Name: name, Path: path, Files: append([]string(nil), files...), Config: cfg, Err: err}, err
	}

	return agentStatusEntry{Name: name, Path: path, Files: append([]string(nil), files...), Config: cfg}, nil
}

func loadYAMLDocument(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return map[string]any{}, nil
	}

	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}

	doc, ok := normalizeYAMLMap(raw).(map[string]any)
	if !ok {
		return nil, fmt.Errorf("config %q must contain a YAML mapping at the top level", path)
	}
	return doc, nil
}

func decodeMergedAgentConfig(doc map[string]any) (config.Config, error) {
	data, err := yaml.Marshal(doc)
	if err != nil {
		return config.Config{}, fmt.Errorf("marshal merged config: %w", err)
	}

	var cfg config.Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return config.Config{}, fmt.Errorf("decode merged config: %w", err)
	}
	return cfg, nil
}

func mergeYAMLDocuments(dst, src map[string]any) {
	for key, srcValue := range src {
		if dstValue, ok := dst[key]; ok {
			dstMap, dstOK := dstValue.(map[string]any)
			srcMap, srcOK := srcValue.(map[string]any)
			if dstOK && srcOK {
				mergeYAMLDocuments(dstMap, srcMap)
				continue
			}
		}
		dst[key] = srcValue
	}
}

func normalizeYAMLMap(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = normalizeYAMLMap(item)
		}
		return out
	case map[interface{}]interface{}:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[fmt.Sprint(key)] = normalizeYAMLMap(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = normalizeYAMLMap(item)
		}
		return out
	default:
		return value
	}
}

func isYAMLConfigFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".yaml", ".yml":
		return true
	default:
		return false
	}
}

func printAgentStatusReport(out io.Writer, report agentStatusReport) {
	fmt.Fprintln(out, "agent status")
	if len(report.Agents) == 0 {
		fmt.Fprintf(out, "no agents found under %s\n", report.Root)
		return
	}

	for _, agent := range report.Agents {
		fmt.Fprintf(out, "agent: %s\n", agent.Name)
		fmt.Fprintf(out, "  path: %s\n", agent.Path)
		if len(agent.Files) > 0 {
			fmt.Fprintf(out, "  files: %s\n", strings.Join(agent.Files, ", "))
		}
		if agent.Err != nil {
			fmt.Fprintln(out, "  status: invalid")
			fmt.Fprintf(out, "  error: %v\n", agent.Err)
			continue
		}
		fmt.Fprintln(out, "  status: valid")
		for _, line := range formatAgentConfigSummary(agent.Config) {
			fmt.Fprintf(out, "  %s\n", line)
		}
	}
}

func formatAgentConfigSummary(cfg config.Config) []string {
	lines := make([]string, 0, 8)

	if value := strings.TrimSpace(cfg.Platform.Name); value != "" {
		lines = append(lines, fmt.Sprintf("platform: %s", value))
	}
	if value := strings.TrimSpace(cfg.Compute.Class); value != "" {
		lines = append(lines, fmt.Sprintf("compute: %s", value))
	}
	if value := strings.TrimSpace(cfg.Region.Name); value != "" {
		lines = append(lines, fmt.Sprintf("region: %s", value))
	}
	if value := strings.TrimSpace(cfg.Instance.Type); value != "" {
		if cfg.Instance.DiskSizeGB > 0 {
			lines = append(lines, fmt.Sprintf("instance: %s (%d GB)", value, cfg.Instance.DiskSizeGB))
		} else {
			lines = append(lines, fmt.Sprintf("instance: %s", value))
		}
	}
	if value := strings.TrimSpace(cfg.Image.Name); value != "" {
		if imageID := strings.TrimSpace(cfg.Image.ID); imageID != "" {
			lines = append(lines, fmt.Sprintf("image: %s (%s)", value, imageID))
		} else {
			lines = append(lines, fmt.Sprintf("image: %s", value))
		}
	}
	if value := formatRuntimeSummary(cfg.Runtime); value != "" {
		lines = append(lines, "runtime: "+value)
	}
	if value := formatSandboxSummary(cfg.Sandbox); value != "" {
		lines = append(lines, "sandbox: "+value)
	}
	if value := formatSSHSummary(cfg.SSH); value != "" {
		lines = append(lines, "ssh: "+value)
	}
	if value := formatInfraSummary(cfg.Infra); value != "" {
		lines = append(lines, "infra: "+value)
	}

	return lines
}

func formatRuntimeSummary(cfg config.RuntimeConfig) string {
	parts := make([]string, 0, 5)
	if value := strings.TrimSpace(cfg.Provider); value != "" {
		parts = append(parts, "provider="+value)
	}
	if value := strings.TrimSpace(cfg.Endpoint); value != "" {
		parts = append(parts, "endpoint="+value)
	}
	if value := strings.TrimSpace(cfg.Model); value != "" {
		parts = append(parts, "model="+value)
	}
	if cfg.Port > 0 {
		parts = append(parts, fmt.Sprintf("port=%d", cfg.Port))
	}
	if value := strings.TrimSpace(cfg.PublicCIDR); value != "" {
		parts = append(parts, "public_cidr="+value)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

func formatSandboxSummary(cfg config.SandboxConfig) string {
	parts := make([]string, 0, 3)
	parts = append(parts, fmt.Sprintf("enabled=%t", cfg.Enabled))
	if value := strings.TrimSpace(cfg.NetworkMode); value != "" {
		parts = append(parts, "network="+value)
	}
	parts = append(parts, fmt.Sprintf("use_nemoclaw=%t", cfg.UseNemoClaw))
	if len(cfg.FilesystemAllow) > 0 {
		parts = append(parts, "filesystem_allow="+strings.Join(cfg.FilesystemAllow, ","))
	}
	return strings.Join(parts, " ")
}

func formatSSHSummary(cfg config.SSHConfig) string {
	parts := make([]string, 0, 4)
	if value := strings.TrimSpace(cfg.User); value != "" {
		parts = append(parts, "user="+value)
	}
	if value := strings.TrimSpace(cfg.KeyName); value != "" {
		parts = append(parts, "key_name="+value)
	}
	if value := strings.TrimSpace(cfg.CIDR); value != "" {
		parts = append(parts, "cidr="+value)
	}
	if value := strings.TrimSpace(cfg.PrivateKeyPath); value != "" {
		parts = append(parts, "private_key_path="+value)
	}
	if value := strings.TrimSpace(cfg.GitHubPrivateKeyPath); value != "" {
		parts = append(parts, "github_private_key_path="+value)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

func formatInfraSummary(cfg config.InfraConfig) string {
	parts := make([]string, 0, 2)
	if value := strings.TrimSpace(cfg.Backend); value != "" {
		parts = append(parts, "backend="+value)
	}
	if value := strings.TrimSpace(cfg.ModuleDir); value != "" {
		parts = append(parts, "module_dir="+value)
	}
	if value := strings.TrimSpace(cfg.AWSProfile); value != "" {
		parts = append(parts, "aws_profile="+value)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}
