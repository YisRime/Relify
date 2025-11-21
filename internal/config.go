package internal

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	DataDir     string                    `yaml:"data_dir"`
	LogLevel    string                    `yaml:"log_level"`
	Mode        string                    `yaml:"mode"`         // "hub" or "peer"
	HubPlatform string                    `yaml:"hub_platform"` // Only used if Mode == "hub"
	Platforms   map[string]PlatformConfig `yaml:"platforms"`
}

type PlatformConfig struct {
	Type    string                 `yaml:"type"`
	Enabled bool                   `yaml:"enabled"`
	Config  map[string]interface{} `yaml:"config"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := GenerateDefault()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) Validate() error {
	if c.Mode == "hub" {
		if c.HubPlatform == "" {
			return fmt.Errorf("hub mode requires hub_platform to be set")
		}
	}
	return nil
}

func (c *Config) GetDatabasePath() string { return c.DataDir + "/relify.db" }

func SaveConfig(path string, cfg *Config) error {
	data, _ := yaml.Marshal(cfg)
	return os.WriteFile(path, data, 0644)
}

func GenerateDefault() *Config {
	return &Config{
		DataDir:  "./data",
		LogLevel: "info",
		Mode:     "peer",
		Platforms: map[string]PlatformConfig{
			"discord_example": {Type: "discord", Enabled: false},
		},
	}
}
