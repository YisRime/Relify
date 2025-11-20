package internal

import (
	"context"
	"fmt"
)

// Core 是应用程序的核心容器，持有所有主要组件
type Core struct {
	Config   *Config
	Router   *Router
	Registry *PlatformRegistry
	Store    *Store
	Logger   *Logger
}

type CoreConfig struct {
	AppConfig *Config
}

// NewCore 初始化核心组件
func NewCore(cfg *CoreConfig) (*Core, error) {
	log := GetGlobal()

	// 初始化存储层
	store, err := NewStore(cfg.AppConfig.GetDatabasePath(), log)
	if err != nil {
		return nil, fmt.Errorf("store init: %w", err)
	}

	// 初始化注册表和路由
	registry := NewPlatformRegistry()
	router := NewRouter(registry, store, log)

	return &Core{
		Config:   cfg.AppConfig,
		Router:   router,
		Registry: registry,
		Store:    store,
		Logger:   log,
	}, nil
}

// RegisterPlatform 注册一个具体的平台实现到系统
func (c *Core) RegisterPlatform(p Platform) {
	name := p.Name()

	// 检查配置中是否启用了该平台
	pCfg, ok := c.Config.Platforms[name]
	if !ok || !pCfg.Enabled {
		c.Logger.Log(InfoLevel, "core", "platform disabled or missing config", map[string]interface{}{"plat": name})
		return
	}

	c.Logger.Log(InfoLevel, "core", "platform registered", map[string]interface{}{"plat": name})
	c.Registry.Register(p)
}

// GetInboundHandler 返回处理入站消息的句柄
func (c *Core) GetInboundHandler() InboundHandler {
	return c.Router
}

// Start 启动所有已注册的平台
func (c *Core) Start(ctx context.Context) error {
	c.Logger.Log(InfoLevel, "core", "starting...", nil)

	started := 0
	for name, p := range c.Registry.All() {
		// 尝试启动每个平台
		if err := p.Start(ctx); err != nil {
			c.Logger.Log(ErrorLevel, "core", "platform start failed", map[string]interface{}{
				"plat": name, "err": err.Error(),
			})
			// 如果是 Hub 模式且核心平台启动失败，则中断启动
			if c.Config.Mode == "hub" && c.Config.HubPlatform == name {
				return fmt.Errorf("hub platform failed: %w", err)
			}
			continue
		}
		started++
	}

	if started == 0 {
		return fmt.Errorf("no platforms started")
	}
	return nil
}

// Stop 停止所有平台并关闭资源
func (c *Core) Stop(ctx context.Context) error {
	c.Logger.Log(InfoLevel, "core", "stopping...", nil)

	for _, p := range c.Registry.All() {
		p.Stop(ctx)
	}
	return c.Store.Close()
}
