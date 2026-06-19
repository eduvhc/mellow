package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Providers map[string]ProviderConfig `yaml:"providers"`
	Navidrome NavidromeConfig `yaml:"navidrome"`
}

type ServerConfig struct {
	Port int    `yaml:"port"`
	Host string `yaml:"host"`
}

type DatabaseConfig struct {
	Path string `yaml:"path"`
}

type ProviderConfig struct {
	Enabled bool              `yaml:"enabled"`
	Options map[string]string `yaml:"options"`
}

type NavidromeConfig struct {
	BaseURL   string `yaml:"base_url"`
	Username  string `yaml:"username"`
	Password  string `yaml:"password"`
	MusicPath string `yaml:"music_path"`
}

func Load(path string) (*Config, error) {
	cfg := &Config{
		Server: ServerConfig{
			Port: 8080,
			Host: "0.0.0.0",
		},
		Database: DatabaseConfig{
			Path: "mellow.db",
		},
		Providers: make(map[string]ProviderConfig),
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}
