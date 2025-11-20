// Package driver 定义平台驱动接口
// 六边形架构的端口层，所有平台适配器必须实现 Driver 接口
package driver

import (
	"context"

	"Relify/internal/model"
)

// Driver 平台驱动统一接口（六边形架构的端口）
// 所有平台适配器必须实现此接口
type Driver interface {
	// Name 返回驱动名称（如 "telegram", "discord", "matrix"）
	Name() string

	// Start 启动驱动（连接平台、监听事件）
	// 参数：
	//   - ctx: 上下文对象
	// 返回：
	//   - error: 启动错误
	Start(ctx context.Context) error

	// Stop 停止驱动（断开连接、清理资源）
	// 参数：
	//   - ctx: 上下文对象
	// 返回：
	//   - error: 停止错误
	Stop(ctx context.Context) error

	// SendMessage 发送消息（出站，异步执行，立即返回）
	// 返回值仅表示是否成功提交任务，实际发送结果通过 callback 回调
	// 参数：
	//   - ctx: 上下文对象
	//   - msg: 出站消息
	//   - callback: 发送结果回调函数
	// 返回：
	//   - error: 提交错误
	SendMessage(ctx context.Context, msg *model.OutboundMessage, callback SendCallback) error
}

// SendCallback 消息发送结果回调（用于 ID 映射）
type SendCallback func(result *model.MessageSendResult)

// InboundHandler 入站消息处理器（核心层实现，驱动调用）
type InboundHandler interface {
	// HandleMessage 处理入站消息（驱动调用此方法将消息传递给核心层）
	// 参数：
	//   - ctx: 上下文对象
	//   - event: 消息事件
	// 返回：
	//   - error: 处理错误
	HandleMessage(ctx context.Context, event *model.MessageEvent) error
}

// Registry 驱动注册表
// 管理所有已注册的平台驱动
type Registry struct {
	drivers map[string]Driver
}

// NewRegistry 创建驱动注册表实例
func NewRegistry() *Registry {
	return &Registry{
		drivers: make(map[string]Driver),
	}
}

// Register 注册驱动到注册表
// 参数：
//   - driver: 驱动实例
func (r *Registry) Register(driver Driver) {
	r.drivers[driver.Name()] = driver
}

// Get 根据名称获取驱动
// 参数：
//   - name: 驱动名称
//
// 返回：
//   - Driver: 驱动实例
//   - bool: 是否存在
func (r *Registry) Get(name string) (Driver, bool) {
	driver, exists := r.drivers[name]
	return driver, exists
}

// All 获取所有已注册的驱动
// 返回：
//   - map[string]Driver: 驱动映射表
func (r *Registry) All() map[string]Driver {
	return r.drivers
}
