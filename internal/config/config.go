package config

import (
	"bufio"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
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
}

type RuntimeConfig struct {
	Endpoint string `yaml:"endpoint"`
	Model    string `yaml:"model"`
}

type SandboxConfig struct {
	Enabled bool `yaml:"enabled"`
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

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config %q: %w", path, err)
	}
	defer file.Close()

	parsed, err := parseYAML(file)
	if err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}

	return bind(parsed)
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

func parseYAML(file *os.File) (map[string]map[string]string, error) {
	return parseYAMLReader(bufio.NewReader(file))
}

func parseYAMLReader(r *bufio.Reader) (map[string]map[string]string, error) {
	result := map[string]map[string]string{}
	var currentSection string
	scanner := bufio.NewScanner(r)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if idx := strings.Index(line, "#"); idx >= 0 {
			line = line[:idx]
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := countIndent(line)
		trimmed := strings.TrimSpace(line)
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			return nil, fmt.Errorf("line %d: expected key: value", lineNum)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		switch indent {
		case 0:
			if value != "" {
				return nil, fmt.Errorf("line %d: top-level key %q must start a section", lineNum, key)
			}
			currentSection = key
			if _, exists := result[currentSection]; exists {
				return nil, fmt.Errorf("line %d: duplicate section %q", lineNum, key)
			}
			if !isKnownSection(key) {
				return nil, fmt.Errorf("line %d: unknown section %q", lineNum, key)
			}
			result[currentSection] = map[string]string{}
		case 2:
			if currentSection == "" {
				return nil, fmt.Errorf("line %d: field %q appears before any section", lineNum, key)
			}
			if !isKnownField(currentSection, key) {
				return nil, fmt.Errorf("line %d: unknown field %q in section %q", lineNum, key, currentSection)
			}
			result[currentSection][key] = trimQuotes(value)
		default:
			return nil, fmt.Errorf("line %d: unsupported indentation level", lineNum)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func bind(raw map[string]map[string]string) (*Config, error) {
	cfg := &Config{}

	cfg.Platform.Name = rawValue(raw, "platform", "name")
	cfg.Region.Name = rawValue(raw, "region", "name")
	cfg.Instance.Type = rawValue(raw, "instance", "type")
	if disk := rawValue(raw, "instance", "disk_size_gb"); disk != "" {
		value, err := strconv.Atoi(disk)
		if err != nil {
			return nil, fmt.Errorf("instance.disk_size_gb: must be an integer")
		}
		cfg.Instance.DiskSizeGB = value
	}
	cfg.Image.Name = rawValue(raw, "image", "name")
	cfg.Runtime.Endpoint = rawValue(raw, "runtime", "endpoint")
	cfg.Runtime.Model = rawValue(raw, "runtime", "model")
	if enabled := rawValue(raw, "sandbox", "enabled"); enabled != "" {
		value, err := strconv.ParseBool(enabled)
		if err != nil {
			return nil, fmt.Errorf("sandbox.enabled: must be true or false")
		}
		cfg.Sandbox.Enabled = value
	}

	return cfg, nil
}

func rawValue(raw map[string]map[string]string, section, field string) string {
	sectionValues, ok := raw[section]
	if !ok {
		return ""
	}
	return sectionValues[field]
}

func countIndent(line string) int {
	for i, r := range line {
		if r != ' ' {
			return i
		}
	}
	return len(line)
}

func trimQuotes(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		if (strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"")) || (strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'")) {
			return value[1 : len(value)-1]
		}
	}
	return value
}

func isKnownSection(name string) bool {
	switch name {
	case "platform", "region", "instance", "image", "runtime", "sandbox":
		return true
	default:
		return false
	}
}

func isKnownField(section, field string) bool {
	switch section {
	case "platform":
		return field == "name"
	case "region":
		return field == "name"
	case "instance":
		return field == "type" || field == "disk_size_gb"
	case "image":
		return field == "name"
	case "runtime":
		return field == "endpoint" || field == "model"
	case "sandbox":
		return field == "enabled"
	default:
		return false
	}
}
