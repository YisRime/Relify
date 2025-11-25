package internal

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// Registry 维护所有已注册的平台驱动
type Registry struct {
	m map[string]Driver // 驱动名称到驱动实例的映射
}

// NewReg 创建新的驱动注册表
// 返回:
//   - *Registry: 新的注册表实例
func NewReg() *Registry {
	return &Registry{m: make(map[string]Driver)}
}

// Add 向注册表添加一个驱动
// 参数:
//   - d: 要添加的驱动
func (r *Registry) Add(d Driver) {
	r.m[d.Name()] = d
}

// Get 从注册表获取指定名称的驱动
// 参数:
//   - n: 驱动名称
//
// 返回:
//   - Driver: 驱动实例
//   - bool: 是否找到该驱动
func (r *Registry) Get(n string) (Driver, bool) {
	d, ok := r.m[n]
	return d, ok
}

// All 返回所有已注册的驱动
// 返回:
//   - map[string]Driver: 驱动名称到驱动实例的映射
func (r *Registry) All() map[string]Driver { return r.m }

// Core 是应用程序的核心结构
// 管理配置、路由、驱动注册和数据存储
type Core struct {
	Cfg    *Config   // 应用配置
	Router *Router   // 消息路由器
	Reg    *Registry // 驱动注册表
	Store  *Store    // 数据存储
}

// NewCore 创建新的核心实例
// 参数:
//   - cfg: 应用配置
//
// 返回:
//   - *Core: 新的核心实例
//   - error: 初始化过程中的错误
func NewCore(cfg *Config) (*Core, error) {
	// 确保数据目录存在
	os.MkdirAll("data", 0755)

	// 初始化 SQLite 数据库
	dbPath := filepath.Join("data", "relify.db")
	slog.Info("初始化数据库", "path", dbPath)
	s, err := NewStore(dbPath)
	if err != nil {
		return nil, fmt.Errorf("初始化存储: %w", err)
	}
	slog.Info("数据库初始化完成")

	// 创建驱动注册表和路由器
	reg := NewReg()
	router := NewRouter(cfg, reg, s)

	return &Core{
		Cfg:    cfg,
		Router: router,
		Reg:    reg,
		Store:  s,
	}, nil
}

// Add 向核心添加一个平台驱动
// 只有在配置中启用的平台才会被添加
// 参数:
//   - d: 要添加的驱动
func (c *Core) Add(d Driver) {
	if pc, ok := c.Cfg.Plats[d.Name()]; ok && pc.Enabled {
		c.Reg.Add(d)
		slog.Debug("驱动已注册", "platform", d.Name(), "route", d.Route())
	}
}

// Start 启动所有已注册的平台驱动
// 参数:
//   - ctx: 用于控制生命周期的上下文
//
// 返回:
//   - error: 启动过程中的错误
func (c *Core) Start(ctx context.Context) error {
	drivers := c.Reg.All()
	total := len(drivers)
	if total == 0 {
		return fmt.Errorf("未注册驱动")
	}

	slog.Info("开始启动驱动", "count", total)

	// 并发启动所有驱动以提升启动速度
	type result struct {
		name string
		err  error
	}

	results := make(chan result, total)
	var wg sync.WaitGroup
	wg.Add(total)

	for name, d := range drivers {
		go func(n string, drv Driver) {
			defer wg.Done()
			slog.Debug("启动驱动", "platform", n)
			results <- result{name: n, err: drv.Start(ctx)}
		}(name, d)
	}

	// 等待所有驱动启动完成
	go func() {
		wg.Wait()
		close(results)
	}()

	// 收集启动结果
	successCount := 0
	for res := range results {
		if res.err != nil {
			// 如果是中心平台启动失败，直接返回错误
			if c.Cfg.Mode == "hub" && c.Cfg.Hub == res.name {
				slog.Error("中心平台启动失败", "platform", res.name, "error", res.err)
				return fmt.Errorf("启动中心 %s 失败: %w", res.name, res.err)
			}
			// 其他平台启动失败仅记录，不影响整体启动
			slog.Warn("驱动启动失败", "platform", res.name, "error", res.err)
		} else {
			successCount++
			slog.Info("驱动启动成功", "platform", res.name)
		}
	}

	// 至少需要一个平台成功启动
	if successCount == 0 {
		return fmt.Errorf("无激活平台")
	}

	slog.Info("驱动启动完成", "success", successCount, "total", total)
	return nil
}

// Stop 停止所有已注册的平台驱动
// 使用 WaitGroup 确保所有驱动都完成关闭
// 参数:
//   - ctx: 用于控制关闭超时的上下文
//
// 返回:
//   - error: 停止过程中的错误
func (c *Core) Stop(ctx context.Context) error {
	slog.Info("停止所有驱动")
	var wg sync.WaitGroup
	// 并发停止所有驱动
	for name, d := range c.Reg.All() {
		wg.Add(1)
		go func(n string, drv Driver) {
			defer wg.Done()
			slog.Debug("停止驱动", "platform", n)
			if err := drv.Stop(ctx); err != nil {
				slog.Warn("驱动停止失败", "platform", n, "error", err)
			} else {
				slog.Info("驱动已停止", "platform", n)
			}
		}(name, d)
	}

	wg.Wait() // 等待所有驱动停止

	// 关闭数据库连接
	slog.Debug("关闭数据库连接")
	if err := c.Store.Close(); err != nil {
		slog.Error("关闭数据库失败", "error", err)
		return err
	}
	slog.Info("数据库已关闭")
	return nil
}
