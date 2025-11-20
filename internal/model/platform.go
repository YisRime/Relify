// Package model 定义核心数据模型
package model

import (
	"context"

	"Relify/internal/config"
)

// Platform 平台适配器接口
type Platform interface {
	Name() string
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	SendMessage(ctx context.Context, msg *OutboundMessage, callback SendCallback) error
	GetRouteType() config.RouteType
}

// SendCallback 消息发送结果回调
type SendCallback func(result *MessageSendResult)

// InboundHandler 入站消息处理器
type InboundHandler interface {
	HandleMessage(ctx context.Context, event *MessageEvent) error
}

// PlatformRegistry 平台注册表
type PlatformRegistry struct {
	platforms map[string]Platform
}

// NewPlatformRegistry 创建平台注册表
func NewPlatformRegistry() *PlatformRegistry {
	return &PlatformRegistry{
		platforms: make(map[string]Platform),
	}
}

// Register 注册平台
func (r *PlatformRegistry) Register(p Platform) {
	r.platforms[p.Name()] = p
}

// Get 获取平台
func (r *PlatformRegistry) Get(name string) (Platform, bool) {
	p, exists := r.platforms[name]
	return p, exists
}

// All 获取所有平台
func (r *PlatformRegistry) All() map[string]Platform {
	return r.platforms
}
