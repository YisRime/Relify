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
	HubRoomID   string                    `yaml:"hub_room_id"`
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
		if c.HubPlatform == "" || c.HubRoomID == "" {
			return fmt.Errorf("hub mode requires hub_platform and hub_room_id")
		}
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
			"discord_example": {Type: "discord", Enabled: false},
		},
	}
}
