package runtimeinstall

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"openclaw/internal/config"
)

// RuntimeConfig is the file written to the remote host for the runtime installer.
type RuntimeConfig struct {
	UseNemoClaw bool    `yaml:"use_nemoclaw"`
	NIMEndpoint string  `yaml:"nim_endpoint"`
	Model       string  `yaml:"model"`
	Port        int     `yaml:"port,omitempty"`
	Provider    string  `yaml:"provider,omitempty"`
	Sandbox     Sandbox `yaml:"sandbox"`
}

type Sandbox struct {
	Enabled         bool     `yaml:"enabled"`
	NetworkMode     string   `yaml:"network_mode"`
	FilesystemAllow []string `yaml:"filesystem_allow,omitempty"`
}

func RenderRuntimeConfig(cfg *config.Config, useNemoClaw *bool, port int) ([]byte, error) {
	if cfg == nil {
		return nil, fmt.Errorf("render runtime config: config is nil")
	}

	effectiveUseNemoClaw := cfg.Sandbox.UseNemoClaw
	if useNemoClaw != nil {
		effectiveUseNemoClaw = *useNemoClaw
	}

	effectivePort := cfg.Runtime.Port
	if port > 0 {
		effectivePort = port
	}

	rendered := RuntimeConfig{
		UseNemoClaw: effectiveUseNemoClaw,
		NIMEndpoint: strings.TrimSpace(cfg.Runtime.Endpoint),
		Model:       strings.TrimSpace(cfg.Runtime.Model),
		Port:        effectivePort,
		Provider:    strings.TrimSpace(cfg.Runtime.Provider),
		Sandbox: Sandbox{
			Enabled:         cfg.Sandbox.Enabled,
			NetworkMode:     strings.TrimSpace(cfg.Sandbox.NetworkMode),
			FilesystemAllow: append([]string(nil), cfg.Sandbox.FilesystemAllow...),
		},
	}

	data, err := yaml.Marshal(rendered)
	if err != nil {
		return nil, fmt.Errorf("render runtime config: marshal yaml: %w", err)
	}
	return data, nil
}
