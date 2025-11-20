// Package core 提供核心业务逻辑层
// 六边形架构的应用核心，协调驱动、路由、存储等组件
package core

import (
	"context"
	"fmt"

	"Relify/internal/config"
	"Relify/internal/driver"
	"Relify/internal/logger"
	"Relify/internal/router"
	"Relify/internal/storage"
)

// Core 核心层（六边形架构的应用核心）
type Core struct {
	router          *router.Router
	driverRegistry  *driver.Registry
	routeStore      *storage.RouteStore
	messageMapStore *storage.MessageMapStore
	userMapStore    *storage.UserMapStore
	config          *config.Config
	logger          *logger.Logger
}

// Config 核心层配置
type Config struct {
	DatabasePath string         // SQLite 数据库路径
	AppConfig    *config.Config // 应用配置
}

// NewCore 创建核心层实例
// 参数：
//   - cfg: 核心层配置
//
// 返回：
//   - *Core: 核心层实例
//   - error: 错误信息
func NewCore(cfg *Config) (*Core, error) {
	// 初始化日志系统
	log, err := logger.NewFromConfig(
		cfg.AppConfig.Log.Level,
		cfg.AppConfig.Log.Format,
		cfg.AppConfig.Log.Output,
		cfg.AppConfig.Log.File,
	)
	if err != nil {
		return nil, fmt.Errorf("init logger: %w", err)
	}

	// 设置全局日志
	logger.SetGlobal(log)

	log.Info("core", "Initializing Relify core")

	// 初始化存储
	routeStore, err := storage.NewRouteStore(cfg.DatabasePath, log)
	if err != nil {
		return nil, fmt.Errorf("init route store: %w", err)
	}

	messageMapStore, err := storage.NewMessageMapStore(cfg.DatabasePath, log)
	if err != nil {
		routeStore.Close()
		return nil, fmt.Errorf("init message map store: %w", err)
	}

	userMapStore, err := storage.NewUserMapStore(cfg.DatabasePath, log)
	if err != nil {
		routeStore.Close()
		messageMapStore.Close()
		return nil, fmt.Errorf("init user map store: %w", err)
	}

	// 初始化驱动注册表
	driverRegistry := driver.NewRegistry()

	// 初始化路由引擎
	routerEngine := router.NewRouter(driverRegistry, routeStore, messageMapStore, userMapStore, log)

	log.Info("core", "Core initialized successfully")

	return &Core{
		router:          routerEngine,
		driverRegistry:  driverRegistry,
		routeStore:      routeStore,
		messageMapStore: messageMapStore,
		userMapStore:    userMapStore,
		config:          cfg.AppConfig,
		logger:          log,
	}, nil
}

// RegisterDriver 注册平台驱动
// 参数：
//   - drv: 驱动实例
func (c *Core) RegisterDriver(drv driver.Driver) {
	c.driverRegistry.Register(drv)
}

// GetInboundHandler 获取入站处理器（供驱动调用）
// 返回：
//   - driver.InboundHandler: 入站处理器
func (c *Core) GetInboundHandler() driver.InboundHandler {
	return c.router
}

// Start 启动核心层及所有驱动
// 参数：
//   - ctx: 上下文对象
//
// 返回：
//   - error: 启动错误
func (c *Core) Start(ctx context.Context) error {
	c.logger.Info("core", "Starting Relify")

	// 启动所有注册的驱动
	for name, drv := range c.driverRegistry.All() {
		c.logger.Info("core", "Starting driver", map[string]interface{}{
			"driver": name,
		})

		if err := drv.Start(ctx); err != nil {
			c.logger.Error("core", "Failed to start driver", map[string]interface{}{
				"driver": name,
				"error":  err.Error(),
			})
			return fmt.Errorf("start driver %s: %w", name, err)
		}

		c.logger.Info("core", "Driver started successfully", map[string]interface{}{
			"driver": name,
		})
	}

	c.logger.Info("core", "Relify started successfully")
	return nil
}

// Stop 停止核心层及所有驱动
// 参数：
//   - ctx: 上下文对象
//
// 返回：
//   - error: 停止错误
func (c *Core) Stop(ctx context.Context) error {
	c.logger.Info("core", "Stopping Relify")

	// 停止所有驱动
	for name, drv := range c.driverRegistry.All() {
		c.logger.Info("core", "Stopping driver", map[string]interface{}{
			"driver": name,
		})

		if err := drv.Stop(ctx); err != nil {
			c.logger.Error("core", "Failed to stop driver", map[string]interface{}{
				"driver": name,
				"error":  err.Error(),
			})
			// 记录错误但继续停止其他驱动
		}
	}

	// 关闭存储
	if err := c.routeStore.Close(); err != nil {
		c.logger.Error("core", "Failed to close route store", map[string]interface{}{
			"error": err.Error(),
		})
		return err
	}

	if err := c.messageMapStore.Close(); err != nil {
		c.logger.Error("core", "Failed to close message map store", map[string]interface{}{
			"error": err.Error(),
		})
		return err
	}

	if err := c.userMapStore.Close(); err != nil {
		c.logger.Error("core", "Failed to close user map store", map[string]interface{}{
			"error": err.Error(),
		})
		return err
	}

	c.logger.Info("core", "Relify stopped")
	return nil
}
