package matrix

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"Relify/internal"

	"maunium.net/go/mautrix/appservice"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// Matrix 实现 Matrix 平台的驱动
// 使用 AppService 协议与 Matrix 服务器通信
type Matrix struct {
	cfg       *Config                // Matrix 配置
	router    *internal.Router       // 消息路由器
	as        *appservice.AppService // AppService 实例
	botUserID id.UserID              // Bot 用户 ID
	cache     sync.Map               // 缓存（用于存储用户信息、Ghost 配置等）
}

// NewMatrix 创建新的 Matrix 驱动实例
// 参数:
//   - props: 配置属性
//   - router: 消息路由器
//
// 返回:
//   - *Matrix: Matrix 驱动实例
//   - error: 初始化错误
func NewMatrix(props internal.Props, router *internal.Router) (*Matrix, error) {
	cfg, err := parseConfig(props)
	if err != nil {
		return nil, err
	}

	slog.Debug("初始化 Matrix 驱动",
		"domain", cfg.Domain,
		"server", cfg.ServerURL,
	)

	m := &Matrix{
		cfg:    cfg,
		router: router,
	}

	// 初始化 AppService 客户端
	if err := m.initClient(); err != nil {
		return nil, err
	}

	slog.Info("Matrix 驱动初始化完成",
		"bot_user_id", m.botUserID,
	)

	return m, nil
}

// Name 返回驱动名称
func (m *Matrix) Name() string { return "matrix" }

// Route 返回路由模式（镜像模式）
// Matrix 为每个桥接创建独立的房间
func (m *Matrix) Route() internal.Route { return internal.RouteMirror }

// Start 启动 Matrix 驱动
// 参数:
//   - ctx: 上下文
//
// 返回:
//   - error: 启动错误
func (m *Matrix) Start(ctx context.Context) error {
	return m.startServe(ctx)
}

// Stop 停止 Matrix 驱动
// 参数:
//   - ctx: 上下文
//
// 返回:
//   - error: 停止错误
func (m *Matrix) Stop(ctx context.Context) error {
	return m.stopServe(ctx)
}

// Info 获取 Matrix 房间的信息
// 参数:
//   - ctx: 上下文
//   - room: 房间 ID
//
// 返回:
//   - *internal.Info: 房间信息
//   - error: 获取错误
func (m *Matrix) Info(ctx context.Context, room string) (*internal.Info, error) {
	rid := id.RoomID(room)
	info := &internal.Info{ID: room, Name: room}

	slog.Debug("Matrix 获取房间信息", "room_id", room)

	// 获取房间名称
	var nameRes struct{ Name string }
	if err := m.as.BotIntent().StateEvent(ctx, rid, event.StateRoomName, "", &nameRes); err != nil {
		slog.Warn("Matrix 获取房间名称失败",
			"room_id", room,
			"error", err,
		)
	} else if nameRes.Name != "" {
		info.Name = nameRes.Name
		slog.Debug("Matrix 获取房间名称成功",
			"room_id", room,
			"name", nameRes.Name,
		)
	} else {
		slog.Debug("Matrix 房间名称为空", "room_id", room)
	}

	// 获取房间头像
	var avatarRes struct {
		Url string `json:"url"`
	}
	if err := m.as.BotIntent().StateEvent(ctx, rid, event.StateRoomAvatar, "", &avatarRes); err != nil {
		slog.Warn("Matrix 获取房间头像失败",
			"room_id", room,
			"error", err,
		)
	} else if avatarRes.Url != "" {
		slog.Debug("Matrix 获取房间头像成功",
			"room_id", room,
			"mxc", avatarRes.Url,
		)
		info.Avatar = m.mxcToURL(avatarRes.Url) // 转换 mxc:// 为 HTTP URL
		slog.Debug("Matrix 房间头像URL转换",
			"room_id", room,
			"mxc", avatarRes.Url,
			"http_url", info.Avatar,
		)
	} else {
		slog.Debug("Matrix 房间头像为空", "room_id", room)
	}

	slog.Debug("Matrix 房间信息获取完成",
		"room_id", room,
		"name", info.Name,
		"avatar", info.Avatar,
	)

	return info, nil
}

// Make 创建新的 Matrix 房间
// 参数:
//   - ctx: 上下文
//   - info: 房间信息（必需，用于镜像模式）
//
// 返回:
//   - string: 创建的房间 ID
//   - error: 创建错误
func (m *Matrix) Make(ctx context.Context, info *internal.Info) (string, error) {
	if info == nil {
		return "", fmt.Errorf("镜像模式需要info参数")
	}
	roomID, err := m.createRoom(ctx, info)
	return roomID, err
}
