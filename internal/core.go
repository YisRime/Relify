package internal

import (
	"context"
	"fmt"
)

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

func NewCore(cfg *CoreConfig) (*Core, error) {
	log := GetGlobal()

	store, err := NewStore(cfg.AppConfig.GetDatabasePath(), log)
	if err != nil {
		return nil, fmt.Errorf("store init: %w", err)
	}

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

func (c *Core) RegisterPlatform(p Platform) {
	name := p.Name()

	// 检查配置
	pCfg, ok := c.Config.Platforms[name]
	if !ok || !pCfg.Enabled {
		c.Logger.Log(InfoLevel, "core", "platform disabled or missing config", map[string]interface{}{"plat": name})
		return
	}

	c.Logger.Log(InfoLevel, "core", "platform registered", map[string]interface{}{"plat": name})
	c.Registry.Register(p)
}

func (c *Core) GetInboundHandler() InboundHandler {
	return c.Router
}

func (c *Core) Start(ctx context.Context) error {
	c.Logger.Log(InfoLevel, "core", "starting...", nil)

	started := 0
	for name, p := range c.Registry.All() {
		if err := p.Start(ctx); err != nil {
			c.Logger.Log(ErrorLevel, "core", "platform start failed", map[string]interface{}{
				"plat": name, "err": err.Error(),
			})
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

func (c *Core) Stop(ctx context.Context) error {
	c.Logger.Log(InfoLevel, "core", "stopping...", nil)

	for _, p := range c.Registry.All() {
		p.Stop(ctx)
	}
	return c.Store.Close()
}
