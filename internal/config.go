package internal

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := GenerateDefault()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	cfg.EnsureConfigsValid()
	return cfg, nil
}

func (c *Config) Validate() error {
	if c.Mode == "hub" && c.HubPlatform == "" {
		return fmt.Errorf("hub mode requires hub_platform")
	}
	return nil
}

func (c *Config) EnsureConfigsValid() {
	defaults := GenerateDefault()
	for name, platform := range c.Platforms {
		if platform.Config.Kind == 0 || platform.Config.Tag == "!!null" {
			if defaultPlatform, ok := defaults.Platforms[name]; ok {
				platform.Config = defaultPlatform.Config
				c.Platforms[name] = platform
				fmt.Printf("Warning: Platform %s config is null, using defaults\n", name)
			}
		}
	}
}

func (c *Config) GetDatabasePath() string { return c.DataDir + "/relify.db" }

func SaveConfig(path string, cfg *Config) error {
	cfg.EnsureConfigsValid()
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func GenerateDefault() *Config {
	var qqConfig, matrixConfig yaml.Node

	qqConfig.Encode(map[string]interface{}{
		"protocol": "ws",
		"url":      "ws://localhost:3001",
		"secret":   "",
		"group":    "",
	})

	matrixConfig.Encode(map[string]interface{}{
		"homeserver_url":   "http://localhost:8448",
		"domain":           "localhost",
		"as_token":         "as_token",
		"hs_token":         "hs_token",
		"bot_localpart":    "relify_bot",
		"listen":           "http://localhost:6168",
		"user_namespace":   "relify_",
		"auto_invite_user": "",
	})

	return &Config{
		DataDir:     "./data",
		LogLevel:    "info",
		Mode:        "hub",
		HubPlatform: "matrix",
		Platforms: map[string]PlatformConfig{
			"qq":     {Type: "qq", Enabled: true, Config: qqConfig},
			"matrix": {Type: "matrix", Enabled: true, Config: matrixConfig},
		},
	}
}
