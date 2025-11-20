package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 应用配置
type Config struct {
	// 数据库配置
	Database DatabaseConfig `yaml:"database"`

	// 驱动配置
	Drivers map[string]DriverConfig `yaml:"drivers"`

	// 日志配置
	Log LogConfig `yaml:"log"`

	// 服务器配置
	Server ServerConfig `yaml:"server"`
}

// DatabaseConfig 数据库配置
type DatabaseConfig struct {
	Path string `yaml:"path"` // 数据库文件路径，默认 "./relify.db"
}

// DriverConfig 驱动配置
type DriverConfig struct {
	Type    string                 `yaml:"type"`    // 驱动类型：telegram, discord, matrix
	Enabled bool                   `yaml:"enabled"` // 是否启用
	Config  map[string]interface{} `yaml:"config"`  // 驱动特定配置
}

// LogConfig 日志配置
type LogConfig struct {
	Level  string `yaml:"level"`  // 日志级别：debug, info, warn, error
	Format string `yaml:"format"` // 日志格式：json, text
	Output string `yaml:"output"` // 输出目标：stdout, stderr, file
	File   string `yaml:"file"`   // 日志文件路径（当 output=file 时）
}

// ServerConfig HTTP 服务器配置
type ServerConfig struct {
	Host            string        `yaml:"host"`             // HTTP 服务器地址
	Port            int           `yaml:"port"`             // HTTP 服务器端口
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"` // 优雅关闭超时时间
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

	// 设置默认值
	setDefaults(&cfg)

	return &cfg, nil
}

// SaveConfig 保存配置到文件
func SaveConfig(path string, cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}

	return nil
}

// setDefaults 设置默认配置值
func setDefaults(cfg *Config) {
	// 数据库默认值
	if cfg.Database.Path == "" {
		cfg.Database.Path = "./relify.db"
	}

	// 日志默认值
	if cfg.Log.Level == "" {
		cfg.Log.Level = "info"
	}
	if cfg.Log.Format == "" {
		cfg.Log.Format = "json"
	}
	if cfg.Log.Output == "" {
		cfg.Log.Output = "stdout"
	}

	// 服务器默认值
	if cfg.Server.Host == "" {
		cfg.Server.Host = "127.0.0.1"
	}
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}
	if cfg.Server.ShutdownTimeout == 0 {
		cfg.Server.ShutdownTimeout = 30 * time.Second
	}
}

// Validate 验证配置
func (c *Config) Validate() error {
	// 验证日志级别
	validLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
	}
	if !validLevels[c.Log.Level] {
		return fmt.Errorf("invalid log level: %s", c.Log.Level)
	}

	// 验证日志格式
	validFormats := map[string]bool{
		"json": true,
		"text": true,
	}
	if !validFormats[c.Log.Format] {
		return fmt.Errorf("invalid log format: %s", c.Log.Format)
	}

	// 验证日志输出
	validOutputs := map[string]bool{
		"stdout": true,
		"stderr": true,
		"file":   true,
	}
	if !validOutputs[c.Log.Output] {
		return fmt.Errorf("invalid log output: %s", c.Log.Output)
	}

	return nil
}
