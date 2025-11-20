package internal

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	DataDir     string                    `yaml:"data_dir"`
	LogLevel    string                    `yaml:"log_level"`
	Mode        string                    `yaml:"mode"`
	HubPlatform string                    `yaml:"hub_platform"`
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
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.DataDir == "" {
		cfg.DataDir = "./data"
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}
	return &cfg, nil
}

func (c *Config) Validate() error {
	if c.Mode == "hub" && c.HubPlatform == "" {
		return fmt.Errorf("hub mode requires hub_platform")
	}
	return nil
}

func (c *Config) GetDatabasePath() string { return c.DataDir + "/relify.db" }
func (c *Config) GetLogsDir() string      { return c.DataDir + "/logs" }

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
			"example": {Type: "discord", Enabled: false},
		},
	}
}
