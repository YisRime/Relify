// Package model 定义核心数据模型
package model

// RoomBinding 房间绑定关系
type RoomBinding struct {
	ID    string
	Rooms []BoundRoom
}

// BoundRoom 绑定的房间
type BoundRoom struct {
	Platform string
	RoomID   string
}
