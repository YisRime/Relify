package internal

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
)

// DriverFactory 定义了用于创建驱动程序实例的工厂函数签名。
// 它接收配置属性并返回初始化后的 Driver 接口或错误。
type DriverFactory func(Properties) (Driver, error)

var factories = make(map[string]DriverFactory)

// RegisterDriver 注册一个驱动程序工厂，使其可以通过配置文件按名称加载。
// 通常在驱动程序的 init 函数中调用。
func RegisterDriver(name string, f DriverFactory) { factories[name] = f }

// Registry 管理所有已加载的驱动程序实例及其对应的路由策略。
type Registry struct {
	drivers map[string]Driver
	routes  map[string]RoutePolicy
}

// NewRegistry 创建并初始化一个新的 Registry 实例。
func NewRegistry() *Registry {
	return &Registry{
		drivers: make(map[string]Driver),
		routes:  make(map[string]RoutePolicy),
	}
}

// Register 将一个已初始化的驱动实例及其名称添加到注册表中。
func (r *Registry) Register(name string, d Driver) { r.drivers[name] = d }

// GetDriver 根据名称获取已注册的驱动程序实例。
// 返回驱动实例和是否存在该驱动的布尔值。
func (r *Registry) GetDriver(name string) (Driver, bool) {
	d, ok := r.drivers[name]
	return d, ok
}

// GetRoutePolicy 获取指定驱动名称的路由策略。
func (r *Registry) GetRoutePolicy(name string) RoutePolicy { return r.routes[name] }

// GetAllDrivers 返回包含所有已注册驱动的映射表。
func (r *Registry) GetAllDrivers() map[string]Driver { return r.drivers }

// Core 是应用程序的核心结构体，负责集成配置、路由器、存储层和驱动管理。
// 它是整个应用生命周期的控制中心。
type Core struct {
	Config   *Config
	Router   *Router
	Registry *Registry
	Store    *Store
}

// NewCore 根据提供的配置初始化 Core 实例。
// 该过程包括：
// 1. 初始化 SQLite 存储层。
// 2. 创建驱动注册表。
// 3. 初始化消息路由器。
// 4. 根据配置实例化所有启用的驱动程序并注册。
func NewCore(config *Config) (*Core, error) {
	store, err := NewStore(filepath.Join("data", "relify.db"), config.RetentDay)
	if err != nil {
		return nil, err
	}
	registry := NewRegistry()
	router := NewRouter(config, registry, store)

	core := &Core{
		Config:   config,
		Router:   router,
		Registry: registry,
		Store:    store,
	}

	for name, platConf := range config.Platforms {
		if platConf.Enabled {
			if create, ok := factories[platConf.Driver]; ok {
				if driver, err := create(platConf.Config); err == nil {
					core.Registry.Register(name, driver)
				}
			}
		}
	}

	return core, nil
}

// Start 并发初始化并启动所有已注册的驱动程序。
// 它会等待所有驱动的 Init 方法执行完毕，聚合结果并输出日志。
// 如果有驱动初始化失败，将在日志中记录警告，但不会中断其他驱动的启动。
func (c *Core) Start(ctx context.Context) error {
	drivers := c.Registry.GetAllDrivers()
	count := len(drivers)

	type result struct {
		key    string
		name   string
		policy RoutePolicy
		err    error
	}
	resultChan := make(chan result, count)

	for key, drv := range drivers {
		go func(k string, d Driver) {
			name, policy, err := d.Init(ctx, c.Router)
			resultChan <- result{k, name, policy, err}
		}(key, drv)
	}

	var loaded []string
	var failed []string

	for i := 0; i < count; i++ {
		res := <-resultChan
		if res.err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", res.key, res.err))
			continue
		}
		c.Registry.routes[res.key] = res.policy
		loaded = append(loaded, fmt.Sprintf("%s(%s)", res.key, res.policy))
	}

	if len(failed) > 0 {
		slog.Warn("驱动加载出错", "loaded", loaded, "failed", failed)
	} else {
		slog.Info("驱动加载完成", "drivers", loaded)
	}

	return nil
}

// Stop 优雅地停止所有服务。
// 操作顺序：
// 1. 并发调用所有驱动的 Stop 方法。
// 2. 停止路由器的后台缓存清理任务。
// 3. 关闭存储层（保存数据、关闭 DB 连接）。
func (c *Core) Stop(ctx context.Context) error {
	var wg sync.WaitGroup
	for _, d := range c.Registry.GetAllDrivers() {
		wg.Add(1)
		go func(drv Driver) {
			defer wg.Done()
			drv.Stop(ctx)
		}(d)
	}
	wg.Wait()

	// 关闭路由器的缓存清理任务
	c.Router.Stop()

	return c.Store.Close()
}
