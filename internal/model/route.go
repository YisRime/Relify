// Package model 定义核心数据模型
// 包含消息、路由、用户等跨平台标准化数据结构
//
// 路由模型定义房间之间的绑定关系，支持镜像和聚合两种路由类型。
// 用户模型定义跨平台用户映射关系，用于身份转换和提及翻译。
package model

import "time"

// RouteType 路由类型
type RouteType string

const (
	RouteTypeMirror    RouteType = "mirror"    // 1:1 镜像绑定（双向同步）
	RouteTypeAggregate RouteType = "aggregate" // N:1 聚合路由（多对一）
)

// RoomBinding 房间绑定关系
// 定义哪些房间应该互相同步消息
type RoomBinding struct {
	ID   string    `json:"id"`   // 绑定唯一标识符
	Type RouteType `json:"type"` // 路由类型

	// 绑定的房间列表
	Rooms []BoundRoom `json:"rooms"`
}

// BoundRoom 绑定的房间信息
type BoundRoom struct {
	Driver string `json:"driver"`  // 驱动名称（如 "telegram", "discord"）
	RoomID string `json:"room_id"` // 房间/群组 ID
}

// UserMapping 用户 ID 映射关系
// 用于跨平台用户身份转换，支持 @ 提及的智能翻译
type UserMapping struct {
	// 来源用户信息
	SourceDriver string `json:"source_driver"`  // 来源驱动名称
	SourceUserID string `json:"source_user_id"` // 来源平台的用户 ID

	// 目标用户信息
	TargetDriver string `json:"target_driver"`  // 目标驱动名称
	TargetUserID string `json:"target_user_id"` // 目标平台的用户 ID

	// 元数据
	DisplayName string    `json:"display_name,omitempty"` // 显示名称（可选，用于日志）
	CreatedAt   time.Time `json:"created_at"`             // 创建时间
	UpdatedAt   time.Time `json:"updated_at"`             // 更新时间
}
