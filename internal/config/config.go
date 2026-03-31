package config

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const PlatformAWS = "aws"

type Config struct {
	Platform PlatformConfig `yaml:"platform"`
	Region   RegionConfig   `yaml:"region"`
	Instance InstanceConfig `yaml:"instance"`
	Image    ImageConfig    `yaml:"image"`
	Runtime  RuntimeConfig  `yaml:"runtime"`
	Sandbox  SandboxConfig  `yaml:"sandbox"`
}

type PlatformConfig struct {
	Name string `yaml:"name"`
}

type RegionConfig struct {
	Name string `yaml:"name"`
}

type InstanceConfig struct {
	Type       string `yaml:"type"`
	DiskSizeGB int    `yaml:"disk_size_gb"`
}

type ImageConfig struct {
	Name string `yaml:"name"`
	ID   string `yaml:"id,omitempty"`
}

type RuntimeConfig struct {
	Endpoint string `yaml:"endpoint"`
	Model    string `yaml:"model"`
}

type SandboxConfig struct {
	Enabled         bool     `yaml:"enabled"`
	NetworkMode     string   `yaml:"network_mode"`
	UseNemoClaw     bool     `yaml:"use_nemoclaw"`
	FilesystemAllow []string `yaml:"filesystem_allow,omitempty"`
}

type ValidationError struct {
	Fields []FieldError
}

type FieldError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	if e == nil || len(e.Fields) == 0 {
		return ""
	}
	var parts []string
	for _, field := range e.Fields {
		parts = append(parts, fmt.Sprintf("%s: %s", field.Field, field.Message))
	}
	sort.Strings(parts)
	return "config validation failed: " + strings.Join(parts, "; ")
}

func (e *ValidationError) Add(field, message string) {
	e.Fields = append(e.Fields, FieldError{Field: field, Message: message})
}

func (e *ValidationError) OrNil() error {
	if e == nil || len(e.Fields) == 0 {
		return nil
	}
	return e
}

func Load(path string) (*Config, error) {
	if strings.TrimSpace(path) == "" {
		return &Config{}, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return &Config{}, nil
	}

	var cfg Config
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	return &cfg, nil
}

func Validate(cfg *Config) error {
	if cfg == nil {
		return errors.New("config validation failed: config is nil")
	}

	var v ValidationError

	if cfg.Platform.Name == "" {
		v.Add("platform.name", "is required")
	} else if cfg.Platform.Name != PlatformAWS {
		v.Add("platform.name", fmt.Sprintf("unsupported platform %q", cfg.Platform.Name))
	}

	if cfg.Region.Name == "" {
		v.Add("region.name", "is required")
	}

	if cfg.Instance.Type == "" {
		v.Add("instance.type", "is required")
	}
	if cfg.Instance.DiskSizeGB <= 0 {
		v.Add("instance.disk_size_gb", "must be greater than 0")
	}

	if cfg.Image.Name == "" {
		v.Add("image.name", "is required")
	}

	if cfg.Runtime.Endpoint == "" {
		v.Add("runtime.endpoint", "is required")
	} else if parsed, err := url.Parse(cfg.Runtime.Endpoint); err != nil || parsed.Scheme == "" || parsed.Host == "" {
		v.Add("runtime.endpoint", "must be a valid URL with scheme and host")
	}
	if cfg.Runtime.Model == "" {
		v.Add("runtime.model", "is required")
	}

	if cfg.Sandbox.NetworkMode != "" && cfg.Sandbox.NetworkMode != "public" && cfg.Sandbox.NetworkMode != "private" {
		v.Add("sandbox.network_mode", "must be public or private")
	}

	return v.OrNil()
}

func LoadAndValidate(path string) (*Config, error) {
	cfg, err := Load(path)
	if err != nil {
		return nil, err
	}
	if err := Validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func Save(path string, cfg *Config) error {
	if cfg == nil {
		return errors.New("config save failed: config is nil")
	}
	if strings.TrimSpace(path) == "" {
		return errors.New("config save failed: output path is required")
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(path, data, 0o600)
}
