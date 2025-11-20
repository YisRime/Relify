package internal

import (
	"context"
	"fmt"
)

// Core 核心层
type Core struct {
	router           *Router
	platformRegistry *PlatformRegistry
	store            *Store
	config           *Config
	logger           *Logger
}

// CoreConfig 核心层配置
type CoreConfig struct {
	DatabasePath string
	AppConfig    *Config
}

// NewCore 创建核心层实例
func NewCore(cfg *CoreConfig) (*Core, error) {
	log := GetGlobal()
	log.Info("core", "Initializing core layer")

	// 创建统一存储
	store, err := NewStore(cfg.DatabasePath, log)
	if err != nil {
		return nil, fmt.Errorf("initialize storage: %w", err)
	}

	platformRegistry := NewPlatformRegistry()

	// 创建路由引擎
	routerEngine := NewRouter(
		platformRegistry,
		store,
		cfg.AppConfig.Mode,
		cfg.AppConfig.HubPlatform,
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
func (c *Core) RegisterPlatform(p Platform) {
	name := p.Name()

	// 1. 检查配置是否存在且启用
	pCfg, exists := c.config.Platforms[name]
	if !exists {
		c.logger.Warn("core", "Platform registered but not found in config, skipping", map[string]interface{}{
			"platform": name,
		})
		return
	}

	if !pCfg.Enabled {
		c.logger.Info("core", "Platform disabled in config, skipping", map[string]interface{}{
			"platform": name,
		})
		return
	}

	// 2. 获取平台固有的路由类型
	routeType := p.GetRouteType()

	c.logger.Info("core", "Registering platform", map[string]interface{}{
		"platform":   name,
		"type":       pCfg.Type, // 配置文件中的 adapter type (e.g. "discord")
		"route_type": routeType, // 平台代码中定义的路由属性 (mirror/aggregate)
	})

	// 3. 注册到注册表
	c.platformRegistry.Register(p)

	// 4. 更新路由器的平台属性记录
	c.router.RegisterPlatformType(name, routeType)
}

// GetInboundHandler 获取入站处理器
func (c *Core) GetInboundHandler() InboundHandler {
	return c.router
}

// Start 启动核心层及所有平台
func (c *Core) Start(ctx context.Context) error {
	c.logger.Info("core", "Starting Relify")

	activeCount := 0
	for name, p := range c.platformRegistry.All() {
		c.logger.Info("core", "Starting platform", map[string]interface{}{"platform": name})

		if err := p.Start(ctx); err != nil {
			c.logger.Error("core", "Failed to start platform", map[string]interface{}{
				"platform": name,
				"error":    err.Error(),
			})

			// Hub 模式下，如果主 Hub 平台启动失败，则无法工作，属于致命错误
			if c.config.Mode == "hub" && name == c.config.HubPlatform {
				return fmt.Errorf("hub platform %s failed to start: %w", name, err)
			}
			// Peer 模式或其他平台失败，允许降级运行
			continue
		}
		activeCount++
	}

	if activeCount == 0 {
		c.logger.Warn("core", "No platforms started successfully")
	} else {
		c.logger.Info("core", "Relify started successfully", map[string]interface{}{
			"active_platforms": activeCount,
		})
	}

	return nil
}

// Stop 停止核心层及所有平台
func (c *Core) Stop(ctx context.Context) error {
	c.logger.Info("core", "Stopping Relify")

	for name, p := range c.platformRegistry.All() {
		c.logger.Info("core", "Stopping platform", map[string]interface{}{"platform": name})
		if err := p.Stop(ctx); err != nil {
			c.logger.Error("core", "Failed to stop platform", map[string]interface{}{
				"platform": name,
				"error":    err.Error(),
			})
		}
	}

	if err := c.store.Close(); err != nil {
		c.logger.Error("core", "Failed to close store", map[string]interface{}{"error": err.Error()})
		return err
	}

	c.logger.Info("core", "Relify stopped")
	return nil
}
