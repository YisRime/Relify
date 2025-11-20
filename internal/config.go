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

// LoadConfig 加载配置
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	setDefaults(&cfg)
	return &cfg, nil
}

// SaveConfig 保存配置
func SaveConfig(path string, cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("serialize config: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// GenerateDefault 生成默认配置结构体
func GenerateDefault() *Config {
	return &Config{
		DataDir:     "./data",
		LogLevel:    "info",
		Mode:        "peer",
		HubPlatform: "",
		Platforms: map[string]PlatformConfig{
			"discord_example": {
				Type:    "discord",
				Enabled: false,
				Config: map[string]interface{}{
					"token":      "YOUR_TOKEN_HERE",
					"status_msg": "Relify Bot",
				},
			},
			"telegram_example": {
				Type:    "telegram",
				Enabled: false,
				Config: map[string]interface{}{
					"token": "YOUR_TOKEN_HERE",
					"debug": false,
				},
			},
		},
	}
}

func setDefaults(cfg *Config) {
	if cfg.DataDir == "" {
		cfg.DataDir = "./data"
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}
	if cfg.Mode == "" {
		cfg.Mode = "peer"
	}
}

func (c *Config) Validate() error {
	if c.Mode != "peer" && c.Mode != "hub" {
		return fmt.Errorf("invalid mode: %s", c.Mode)
	}
	if c.Mode == "hub" && c.HubPlatform == "" {
		return fmt.Errorf("hub_platform required in hub mode")
	}
	return nil
}

func (c *Config) GetDatabasePath() string {
	return c.DataDir + "/relify.db"
}

func (c *Config) GetLogsDir() string {
	return c.DataDir + "/logs"
}
