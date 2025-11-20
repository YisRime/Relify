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
	store            *storage.Store
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

	// 创建统一存储
	store, err := storage.NewStore(cfg.DatabasePath, log)
	if err != nil {
		return nil, fmt.Errorf("initialize storage: %w", err)
	}

	platformRegistry := model.NewPlatformRegistry()
	platformConfigs := make(map[string]config.RouteType)

	// 创建路由引擎
	routerEngine := router.NewRouter(
		platformRegistry,
		store,
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
		store:            store,
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

	// 关闭统一存储
	if err := c.store.Close(); err != nil {
		c.logger.Error("core", "Failed to close store", map[string]interface{}{"error": err.Error()})
		return err
	}

	c.logger.Info("core", "Relify stopped")
	return nil
}
