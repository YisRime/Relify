// Package core 提供核心业务逻辑层
package core

import (
	"context"
	"fmt"

	"Relify/internal/config"
	"Relify/internal/logger"
	"Relify/internal/model"
	"Relify/internal/router"
	"Relify/internal/storage"
)

// Core 核心层
type Core struct {
	router           *router.Router
	platformRegistry *model.PlatformRegistry
	routeStore       *storage.RouteStore
	messageMapStore  *storage.MessageMapStore
	userMapStore     *storage.UserMapStore
	config           *config.Config
	logger           *logger.Logger
}

// Config 核心层配置
type Config struct {
	DatabasePath string
	AppConfig    *config.Config
}

// NewCore 创建核心层实例
func NewCore(cfg *Config) (*Core, error) {
	log := logger.GetGlobal()
	log.Info("core", "Initializing core layer")

	routeStore, err := storage.NewRouteStore(cfg.DatabasePath, log)
	if err != nil {
		return nil, fmt.Errorf("initialize route store: %w", err)
	}

	messageMapStore, err := storage.NewMessageMapStore(cfg.DatabasePath, log)
	if err != nil {
		routeStore.Close()
		return nil, fmt.Errorf("initialize message map store: %w", err)
	}

	userMapStore, err := storage.NewUserMapStore(cfg.DatabasePath, log)
	if err != nil {
		routeStore.Close()
		messageMapStore.Close()
		return nil, fmt.Errorf("initialize user map store: %w", err)
	}

	platformRegistry := model.NewPlatformRegistry()
	platformConfigs := make(map[string]config.RouteType)

	routerEngine := router.NewRouter(
		platformRegistry,
		routeStore,
		messageMapStore,
		userMapStore,
		cfg.AppConfig.Mode,
		cfg.AppConfig.HubPlatform,
		platformConfigs,
		log,
	)

	log.Info("core", "Core layer initialized", map[string]interface{}{
		"mode":         cfg.AppConfig.Mode,
		"hub_platform": cfg.AppConfig.HubPlatform,
	})

	return &Core{
		router:           routerEngine,
		platformRegistry: platformRegistry,
		routeStore:       routeStore,
		messageMapStore:  messageMapStore,
		userMapStore:     userMapStore,
		config:           cfg.AppConfig,
		logger:           log,
	}, nil
}

// RegisterPlatform 注册平台适配器
func (c *Core) RegisterPlatform(p model.Platform) {
	c.platformRegistry.Register(p)
	c.router.UpdatePlatformRouteType(p.Name(), p.GetRouteType())
}

// GetInboundHandler 获取入站处理器
func (c *Core) GetInboundHandler() model.InboundHandler {
	return c.router
}

// Start 启动核心层及所有平台
func (c *Core) Start(ctx context.Context) error {
	c.logger.Info("core", "Starting Relify")

	for name, p := range c.platformRegistry.All() {
		c.logger.Info("core", "Starting platform", map[string]interface{}{"platform": name})
		if err := p.Start(ctx); err != nil {
			c.logger.Error("core", "Failed to start platform", map[string]interface{}{
				"platform": name,
				"error":    err.Error(),
			})
			return fmt.Errorf("start platform %s: %w", name, err)
		}
	}

	c.logger.Info("core", "Relify started successfully")
	return nil
}

// Stop 停止核心层及所有平台
func (c *Core) Stop(ctx context.Context) error {
	c.logger.Info("core", "Stopping Relify")

	for name, p := range c.platformRegistry.All() {
		if err := p.Stop(ctx); err != nil {
			c.logger.Error("core", "Failed to stop platform", map[string]interface{}{
				"platform": name,
				"error":    err.Error(),
			})
		}
	}

	if err := c.routeStore.Close(); err != nil {
		return fmt.Errorf("close route store: %w", err)
	}
	if err := c.messageMapStore.Close(); err != nil {
		return fmt.Errorf("close message map store: %w", err)
	}
	if err := c.userMapStore.Close(); err != nil {
		return fmt.Errorf("close user map store: %w", err)
	}

	c.logger.Info("core", "Relify stopped")
	return nil
}
