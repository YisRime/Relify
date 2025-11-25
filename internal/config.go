package internal

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Load 从指定路径加载 YAML 配置文件
// 参数:
//   - path: 配置文件的路径
//
// 返回:
//   - *Config: 解析后的配置对象
//   - error: 加载或解析过程中的错误
func Load(path string) (*Config, error) {
	// 读取文件内容
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// 使用默认配置作为基础
	cfg := Default()
	// 解析 YAML 数据到配置结构
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Check 验证配置的有效性
// 检查项包括:
//   - 中心模式是否指定了中心平台
//   - 中心平台是否已配置且已启用
//
// 返回:
//   - error: 配置错误信息，如果配置有效则返回 nil
func (c *Config) Check() error {
	// 中心模式必须指定中心平台
	if c.Mode == "hub" && c.Hub == "" {
		return fmt.Errorf("中心模式需要指定中心平台")
	}

	// 检查中心平台配置
	if c.Mode == "hub" {
		if pc, ok := c.Plats[c.Hub]; !ok {
			return fmt.Errorf("中心平台 %s 未配置", c.Hub)
		} else if !pc.Enabled {
			return fmt.Errorf("中心平台 %s 未启用", c.Hub)
		}
	}

	return nil
}

// Save 将配置保存到指定路径的 YAML 文件
// 参数:
//   - path: 目标文件路径
//   - cfg: 要保存的配置对象
//
// 返回:
//   - error: 保存过程中的错误
func Save(path string, cfg *Config) error {
	// 将配置序列化为 YAML 格式
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}

	// 写入文件，权限为 0644（所有者读写，组和其他用户只读）
	if err := os.WriteFile(path, data, 0644); err != nil {
		return err
	}

	return nil
}

// Default 返回默认配置
// 包括 QQ 和 Matrix 平台的默认参数
// 返回:
//   - *Config: 默认配置对象
func Default() *Config {
	// QQ 平台默认配置
	qq := Props{
		"protocol": "ws",                  // 协议类型: WebSocket
		"url":      "ws://localhost:3001", // OneBot 实现的 WebSocket 地址
		"secret":   "",                    // 鉴权密钥
		"group":    "",                    // 默认群组 ID
	}

	// Matrix 平台默认配置
	mtx := Props{
		"server_url":    "http://localhost:8448", // Matrix 服务器地址
		"domain":        "localhost",             // Matrix 域名
		"server_domain": "",                      // 服务器域名（用于媒体下载）
		"appservice": Props{ // AppService 配置
			"id":        "relify",                // AppService ID
			"token":     "relify",                // AppService 令牌
			"namespace": "relify_",               // 用户和房间的命名空间前缀
			"listen":    "http://localhost:6168", // AppService 监听地址
		},
		"auto_invite": "", // 自动邀请的用户 ID
	}

	// 返回默认配置，使用中心模式，Matrix 作为中心平台
	return &Config{
		Level: "info",   // 日志级别
		Mode:  "hub",    // 运行模式: hub（中心）或 peer（对等）
		Hub:   "matrix", // 中心平台名称
		Plats: map[string]PlatConf{
			"qq":     {Type: "qq", Enabled: true, Cfg: qq},      // QQ 平台配置
			"matrix": {Type: "matrix", Enabled: true, Cfg: mtx}, // Matrix 平台配置
		},
	}
}
