package config

type Config struct {
	Path    string
	Profile string
}

func Load(path, profile string) (*Config, error) {
	return &Config{
		Path:    path,
		Profile: profile,
	}, nil
}

