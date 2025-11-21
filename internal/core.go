package internal

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
)

type PlatformRegistry struct {
	platforms map[string]Platform
}

func NewPlatformRegistry() *PlatformRegistry {
	return &PlatformRegistry{platforms: make(map[string]Platform)}
}

func (r *PlatformRegistry) Register(p Platform) { r.platforms[p.Name()] = p }

func (r *PlatformRegistry) Get(name string) (Platform, bool) {
	p, ok := r.platforms[name]
	return p, ok
}

func (r *PlatformRegistry) All() map[string]Platform { return r.platforms }

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

	storeInstance, err := NewStore(cfg.GetDatabasePath())
	if err != nil {
		return nil, fmt.Errorf("store init: %w", err)
	}

	reg := NewPlatformRegistry()
	router := NewRouter(cfg, reg, storeInstance)

	return &Core{
		Config:   cfg,
		Router:   router,
		Registry: reg,
		Store:    storeInstance,
	}, nil
}

func (c *Core) RegisterPlatform(p Platform) {
	name := p.Name()
	pCfg, ok := c.Config.Platforms[name]
	if !ok || !pCfg.Enabled {
		return
	}
	c.Registry.Register(p)
	slog.Info("platform registered", "name", name, "type", p.GetRouteType())
}

func (c *Core) Start(ctx context.Context) error {
	var active int
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
	var wg sync.WaitGroup
	for _, p := range c.Registry.All() {
		wg.Add(1)
		go func(p Platform) {
			defer wg.Done()
			_ = p.Stop(ctx)
		}(p)
	}
	wg.Wait()
	return c.Store.Close()
}
