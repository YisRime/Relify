// Package config 提供应用配置管理
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config 应用配置
type Config struct {
	DataDir     string
	LogLevel    string
	Mode        string
	HubPlatform string
	Platforms   map[string]PlatformConfig
}

// GetDatabasePath 获取数据库路径
func (c *Config) GetDatabasePath() string {
	return c.DataDir + "/relify.db"
}

// GetLogsDir 获取日志目录
func (c *Config) GetLogsDir() string {
	return c.DataDir + "/logs"
}

// PlatformConfig 平台配置
type PlatformConfig struct {
	Type    string
	Enabled bool
	Config  map[string]interface{}
}

// RouteType 路由类型
type RouteType string

const (
	RouteTypeMirror    RouteType = "mirror"
	RouteTypeAggregate RouteType = "aggregate"
)

// LoadConfig 加载配置
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
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

// setDefaults 设置默认值
func setDefaults(cfg *Config) {
	if cfg.DataDir == "" {
		cfg.DataDir = "./data"
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}
}

// Validate 验证配置
func (c *Config) Validate() error {
	validLevels := map[string]struct{}{"debug": {}, "info": {}, "warn": {}, "error": {}}
	if _, ok := validLevels[c.LogLevel]; !ok {
		return fmt.Errorf("invalid log level: %s", c.LogLevel)
	}

	if c.Mode == "" {
		return fmt.Errorf("mode must be specified: 'peer' or 'hub'")
	}

	validModes := map[string]struct{}{"peer": {}, "hub": {}}
	if _, ok := validModes[c.Mode]; !ok {
		return fmt.Errorf("invalid mode: %s (must be 'peer' or 'hub')", c.Mode)
	}

	if c.Mode == "hub" {
		if c.HubPlatform == "" {
			return fmt.Errorf("hub_platform must be specified in hub mode")
		}
		if _, exists := c.Platforms[c.HubPlatform]; !exists {
			return fmt.Errorf("hub_platform '%s' not found in platform configurations", c.HubPlatform)
		}
	}

	return nil
}
