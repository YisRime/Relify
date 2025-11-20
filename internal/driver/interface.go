package driver

import (
	"context"

	"Relify/internal/model"
)

// Driver 平台驱动统一接口（六边形架构的端口）
type Driver interface {
	// Name 返回驱动名称（如 "telegram", "discord"）
	Name() string

	// Start 启动驱动（连接平台、监听事件）
	Start(ctx context.Context) error

	// Stop 停止驱动
	Stop(ctx context.Context) error

	// SendMessage 发送消息（出站，异步执行，立即返回）
	// 返回值仅表示是否成功提交，实际发送结果通过 callback 回调
	SendMessage(ctx context.Context, msg *model.OutboundMessage, callback SendCallback) error
}

// SendCallback 消息发送结果回调（用于 ID 映射）
type SendCallback func(result *model.MessageSendResult)

// InboundHandler 入站消息处理器（核心层实现，驱动调用）
type InboundHandler interface {
	// HandleMessage 处理入站消息（驱动调用此方法将消息传递给核心层）
	HandleMessage(ctx context.Context, event *model.MessageEvent) error
}

// Registry 驱动注册表
type Registry struct {
	drivers map[string]Driver
}

// NewRegistry 创建驱动注册表
func NewRegistry() *Registry {
	return &Registry{
		drivers: make(map[string]Driver),
	}
}

// Register 注册驱动
func (r *Registry) Register(driver Driver) {
	r.drivers[driver.Name()] = driver
}

// Get 获取驱动
func (r *Registry) Get(name string) (Driver, bool) {
	driver, exists := r.drivers[name]
	return driver, exists
}

// All 获取所有驱动
func (r *Registry) All() map[string]Driver {
	return r.drivers
}
