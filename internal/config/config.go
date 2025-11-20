// Package config 提供应用配置管理
// 支持从 YAML 文件加载配置，包含数据库、驱动、日志等配置项
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config 应用配置
type Config struct {
	// 数据目录配置
	DataDir string `yaml:"data_dir"`

	// 日志级别配置
	LogLevel string `yaml:"log_level"`

	// 驱动配置
	Drivers map[string]DriverConfig `yaml:"drivers"`
}

// DatabaseConfig 数据库配置（已废弃，数据库路径现在自动设置为 data_dir/relify.db）
type DatabaseConfig struct {
	Path string `yaml:"path"` // 数据库文件路径
}

// GetDatabasePath 获取数据库文件路径
func (c *Config) GetDatabasePath() string {
	return c.DataDir + "/relify.db"
}

// GetLogsDir 获取日志目录路径
func (c *Config) GetLogsDir() string {
	return c.DataDir + "/logs"
}

// DriverConfig 驱动配置
type DriverConfig struct {
	Type    string                 `yaml:"type"`    // 驱动类型：telegram, discord, matrix
	Enabled bool                   `yaml:"enabled"` // 是否启用
	Config  map[string]interface{} `yaml:"config"`  // 驱动特定配置
}

// LoadConfig 从文件加载配置
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

// SaveConfig 保存配置到文件
func SaveConfig(path string, cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// setDefaults 设置默认配置值
func setDefaults(cfg *Config) {
	// 数据目录默认值
	if cfg.DataDir == "" {
		cfg.DataDir = "./data"
	}

	// 日志默认值
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}
}

// Validate 验证配置的有效性
func (c *Config) Validate() error {
	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[c.LogLevel] {
		return fmt.Errorf("invalid log level: %s", c.LogLevel)
	}

	return nil
}
