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

func NewCore(cfg *Config) (*Core, error) {
	log := GetGlobal()

	store, err := NewStore(cfg.GetDatabasePath(), log)
	if err != nil {
		return nil, fmt.Errorf("store init: %w", err)
	}

	registry := NewPlatformRegistry()
	router := NewRouter(cfg, registry, store, log)

	return &Core{
		Config:   cfg,
		Router:   router,
		Registry: registry,
		Store:    store,
		Logger:   log,
	}, nil
}

func (c *Core) RegisterPlatform(p Platform) {
	name := p.Name()
	pCfg, ok := c.Config.Platforms[name]

	if !ok || !pCfg.Enabled {
		return
	}

	c.Registry.Register(p)
	c.Logger.Log(InfoLevel, "core", "platform registered", map[string]interface{}{"plat": name})
}

func (c *Core) Start(ctx context.Context) error {
	active := 0
	for name, p := range c.Registry.All() {
		if err := p.Start(ctx); err != nil {
			c.Logger.Log(ErrorLevel, "core", "start failed", map[string]interface{}{"plat": name, "err": err.Error()})
			if c.Config.Mode == "hub" && c.Config.HubPlatform == name {
				return fmt.Errorf("hub platform died: %w", err)
			}
		} else {
			active++
		}
	}

	if active == 0 {
		return fmt.Errorf("no platforms active")
	}
	return nil
}

func (c *Core) Stop(ctx context.Context) error {
	for _, p := range c.Registry.All() {
		_ = p.Stop(ctx)
	}
	return c.Store.Close()
}
