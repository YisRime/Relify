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
	os.MkdirAll(cfg.DataDir, 0755)

	lvl := slog.LevelInfo
	if cfg.LogLevel == "debug" {
		lvl = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})))

	store, err := NewStore(cfg.GetDatabasePath())
	if err != nil {
		return nil, fmt.Errorf("store init: %w", err)
	}

	reg := NewPlatformRegistry()
	return &Core{
		Config:   cfg,
		Router:   NewRouter(cfg, reg, store),
		Registry: reg,
		Store:    store,
	}, nil
}

func (c *Core) RegisterPlatform(p Platform) {
	if pCfg, ok := c.Config.Platforms[p.Name()]; ok && pCfg.Enabled {
		c.Registry.Register(p)
		slog.Info("platform loaded", "name", p.Name())
	}
}

func (c *Core) Start(ctx context.Context) error {
	active := 0
	for name, p := range c.Registry.All() {
		if err := p.Start(ctx); err != nil {
			slog.Error("platform failed", "name", name, "err", err)
			if c.Config.Mode == "hub" && c.Config.HubPlatform == name {
				return fmt.Errorf("hub platform failed: %w", err)
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
	var wg sync.WaitGroup
	for _, p := range c.Registry.All() {
		wg.Add(1)
		go func(p Platform) {
			defer wg.Done()
			p.Stop(ctx)
		}(p)
	}
	wg.Wait()
	return c.Store.Close()
}
