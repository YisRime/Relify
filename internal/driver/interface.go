// Package driver 定义平台驱动接口
// 六边形架构的端口层，所有平台适配器必须实现 Driver 接口
package driver

import (
	"context"

	"Relify/internal/model"
)

// Driver 平台驱动统一接口（六边形架构的端口）
type Driver interface {
	Name() string
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	SendMessage(ctx context.Context, msg *model.OutboundMessage, callback SendCallback) error
}

// SendCallback 消息发送结果回调（用于 ID 映射）
type SendCallback func(result *model.MessageSendResult)

// InboundHandler 入站消息处理器（核心层实现，驱动调用）
type InboundHandler interface {
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
func (r *Registry) Register(driver Driver) {
	r.drivers[driver.Name()] = driver
}

// Get 根据名称获取驱动
func (r *Registry) Get(name string) (Driver, bool) {
	driver, exists := r.drivers[name]
	return driver, exists
}

// All 获取所有已注册的驱动
func (r *Registry) All() map[string]Driver {
	return r.drivers
}
