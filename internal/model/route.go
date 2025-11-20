// Package model 定义核心数据模型
// 包含消息、路由、用户等跨平台标准化数据结构
//
// 路由模型定义房间之间的绑定关系，支持镜像和聚合两种路由类型。
package model

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
