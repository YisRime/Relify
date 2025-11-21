package internal

import (
	"context"
	"fmt"
	"log/slog"
	"os"
)

type Core struct {
	Config   *Config
	Router   *Router
	Registry *PlatformRegistry
	Store    *Store
}

func NewCore(cfg *Config) (*Core, error) {
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		return nil, err
	}

	logLevel := slog.LevelInfo
	if cfg.LogLevel == "debug" {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

	store, err := NewStore(cfg.GetDatabasePath())
	if err != nil {
		return nil, fmt.Errorf("store init: %w", err)
	}

	registry := NewPlatformRegistry()
	router := NewRouter(cfg, registry, store)

	return &Core{
		Config:   cfg,
		Router:   router,
		Registry: registry,
		Store:    store,
	}, nil
}

func (c *Core) RegisterPlatform(p Platform) {
	name := p.Name()
	pCfg, ok := c.Config.Platforms[name]
	if !ok || !pCfg.Enabled {
		return
	}
	c.Registry.Register(p)
	slog.Info("platform registered", "name", name)
}

func (c *Core) Start(ctx context.Context) error {
	active := 0
	for name, p := range c.Registry.All() {
		if err := p.Start(ctx); err != nil {
			slog.Error("platform start failed", "name", name, "err", err)
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
	slog.Info("core running", "mode", c.Config.Mode)
	return nil
}

func (c *Core) Stop(ctx context.Context) error {
	for _, p := range c.Registry.All() {
		p.Stop(ctx)
	}
	return c.Store.Close()
}
