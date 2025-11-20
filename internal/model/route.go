package model

// RouteType 路由类型
type RouteType string

const (
	RouteTypeMirror    RouteType = "mirror"    // 1:1 镜像绑定
	RouteTypeAggregate RouteType = "aggregate" // N:1 聚合路由
)

// RoomBinding 房间绑定关系
type RoomBinding struct {
	ID   string    `json:"id"`   // 绑定 ID
	Type RouteType `json:"type"` // 路由类型

	// 绑定的房间列表
	Rooms []BoundRoom `json:"rooms"`
}

// BoundRoom 绑定的房间信息
type BoundRoom struct {
	Driver string `json:"driver"`  // 驱动名称
	RoomID string `json:"room_id"` // 房间 ID
}
