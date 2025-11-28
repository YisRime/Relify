package internal

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// LoadConfig 从指定的文件路径读取并解析配置信息。
// 它首先读取文件内容，然后使用 YAML 解析器将其反序列化为 Config 结构体。
// 如果文件读取失败或 YAML 格式不正确，将返回错误。
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	config := DefaultConfig()
	if err := yaml.Unmarshal(data, config); err != nil {
		return nil, err
	}
	return config, nil
}

// Check 验证当前配置对象的完整性和业务逻辑正确性。
// 特别是在 Hub 模式下，它会检查指定的中心平台（Hub）是否存在且已启用。
func (c *Config) Check() error {
	if pc, ok := c.Platforms[c.Hub]; c.Mode == "hub" && (!ok || !pc.Enabled) {
		return fmt.Errorf("中心平台未配置: %s", c.Hub)
	}
	return nil
}

// SaveConfig 将配置结构体序列化为 YAML 格式并保存到指定路径。
// 保存的文件权限将被设置为 0644 (所有者可读写，组和其他人只读)。
func SaveConfig(path string, config *Config) error {
	data, err := yaml.Marshal(config)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// DefaultConfig 生成并返回应用程序的默认配置实例。
// 默认配置包含：
// - LogLevel: "info"
// - Mode: "hub" (中心化模式)
// - RetentDay: 7 (消息映射保留7天)
// - 预置了 "qq" 和 "matrix" 平台的示例配置。
func DefaultConfig() *Config {
	return &Config{
		LogLevel:  "info",
		Mode:      "hub",
		Hub:       "matrix",
		RetentDay: 7, // 默认保留7天数据
		Platforms: map[string]PlatformConfig{
			"qq": {
				Driver: "qq", Enabled: true,
				Config: Properties{
					"protocol": "ws",
					"url":      "ws://localhost:3001",
				},
			},
			"matrix": {
				Driver: "matrix", Enabled: true,
				Config: Properties{
					"server_url": "http://localhost:8448",
					"domain":     "localhost",
					"appservice": Properties{
						"id":        "relify",
						"token":     "relify",
						"namespace": "relify_",
						"listen":    "http://localhost:6168",
					},
				},
			},
		},
	}
}
