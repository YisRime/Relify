package internal

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config 定义应用程序的配置结构
type Config struct {
	DataDir     string                    `yaml:"data_dir"`
	LogLevel    string                    `yaml:"log_level"`
	Mode        string                    `yaml:"mode"` // 运行模式: peer 或 hub
	HubPlatform string                    `yaml:"hub_platform"`
	Platforms   map[string]PlatformConfig `yaml:"platforms"`
}

// PlatformConfig 定义特定平台的配置
type PlatformConfig struct {
	Type    string                 `yaml:"type"`
	Enabled bool                   `yaml:"enabled"`
	Config  map[string]interface{} `yaml:"config"` // 灵活的键值对配置
}

// LoadConfig 从指定路径读取并解析 YAML 配置文件
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// 应用默认值
	setDefaults(&cfg)
	return &cfg, nil
}

// SaveConfig 将配置结构体序列化并写入文件
func SaveConfig(path string, cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("serialize config: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// GenerateDefault 生成一个包含示例数据的默认配置对象
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

// setDefaults 为缺失的字段填充默认值
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

// Validate 检查配置逻辑是否合法
func (c *Config) Validate() error {
	if c.Mode != "peer" && c.Mode != "hub" {
		return fmt.Errorf("invalid mode: %s", c.Mode)
	}
	// Hub 模式下必须指定主控平台
	if c.Mode == "hub" && c.HubPlatform == "" {
		return fmt.Errorf("hub_platform required in hub mode")
	}
	return nil
}

// GetDatabasePath 获取数据库文件的完整路径
func (c *Config) GetDatabasePath() string {
	return c.DataDir + "/relify.db"
}

// GetLogsDir 获取日志文件夹路径
func (c *Config) GetLogsDir() string {
	return c.DataDir + "/logs"
}
