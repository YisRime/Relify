package qq

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"Relify/internal"
)

// QQ 实现 QQ 平台的驱动
// 使用 OneBot 11 协议与 QQ 客户端通信
type QQ struct {
	cfg    *Config          // QQ 配置
	router *internal.Router // 消息路由器
	client *Client          // OneBot 客户端
}

// NewQQ 创建新的 QQ 驱动实例
// 参数:
//   - props: 配置属性
//   - router: 消息路由器
//
// 返回:
//   - *QQ: QQ 驱动实例
//   - error: 初始化错误
func NewQQ(props internal.Props, router *internal.Router) (*QQ, error) {
	b, _ := json.Marshal(props)
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}
	if cfg.Protocol == "" {
		cfg.Protocol = "ws" // 默认使用 WebSocket 协议
	}

	slog.Debug("初始化 QQ 驱动",
		"protocol", cfg.Protocol,
		"url", cfg.URL,
	)

	q := &QQ{
		cfg:    &cfg,
		router: router,
	}
	q.client = NewClient(&cfg, q.handleMsg) // 创建 OneBot 客户端

	slog.Info("QQ 驱动初始化完成")

	return q, nil
}

// Name 返回驱动名称
func (q *QQ) Name() string { return "qq" }

// Route 返回路由模式（混合模式）
// QQ 将所有桥接消息发送到同一个群组
func (q *QQ) Route() internal.Route { return internal.RouteMix }

// Start 启动 QQ 驱动
// 参数:
//   - ctx: 上下文
//
// 返回:
//   - error: 启动错误
func (q *QQ) Start(ctx context.Context) error {
	go q.client.Connect(ctx) // 异步连接 OneBot 服务
	return nil
}

// Stop 停止 QQ 驱动
// 参数:
//   - ctx: 上下文
//
// 返回:
//   - error: 停止错误
func (q *QQ) Stop(ctx context.Context) error {
	q.client.Close() // 关闭客户端连接
	return nil
}

// Info 获取 QQ 群组或用户的信息
// 参数:
//   - ctx: 上下文
//   - room: 房间 ID（群号或 "p:用户QQ号"）
//
// 返回:
//   - *internal.Info: 群组或用户信息
//   - error: 获取错误
func (q *QQ) Info(ctx context.Context, room string) (*internal.Info, error) {
	info := &internal.Info{ID: room, Name: room}

	// 检查是否为私聊
	isPrivate := strings.HasPrefix(room, "p:")
	realID := strings.TrimPrefix(room, "p:")

	if !isPrivate {
		// 尝试获取群组信息
		if err := q.getGroupInfo(ctx, realID, info); err == nil {
			return info, nil
		}
	}

	// 尝试获取用户信息
	if err := q.getUserInfo(ctx, realID, info); err == nil {
		return info, nil
	}

	return info, nil
}

// getGroupInfo 获取 QQ 群组信息
// 参数:
//   - ctx: 上下文
//   - groupID: 群号
//   - info: 用于填充的信息对象
//
// 返回:
//   - error: 获取错误
func (q *QQ) getGroupInfo(ctx context.Context, groupID string, info *internal.Info) error {
	resp, err := q.client.Call(ctx, "get_group_info", map[string]any{
		"group_id": groupID,
		"no_cache": true, // 不使用缓存，获取最新信息
	})
	if err != nil {
		return err
	}

	var d struct {
		Data struct {
			GroupName string `json:"group_name"`
			GroupID   int64  `json:"group_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp, &d); err != nil || d.Data.GroupName == "" {
		return fmt.Errorf("无效响应")
	}

	info.Name = d.Data.GroupName
	info.Topic = fmt.Sprintf("群组: %d", d.Data.GroupID)
	info.Avatar = fmt.Sprintf("https://p.qlogo.cn/gh/%s/%s/640", groupID, groupID) // QQ 群头像 URL
	return nil
}

// getUserInfo 获取 QQ 用户信息
// 参数:
//   - ctx: 上下文
//   - userID: 用户 QQ 号
//   - info: 用于填充的信息对象
//
// 返回:
//   - error: 获取错误
func (q *QQ) getUserInfo(ctx context.Context, userID string, info *internal.Info) error {
	resp, err := q.client.Call(ctx, "get_stranger_info", map[string]any{
		"user_id":  userID,
		"no_cache": true, // 不使用缓存
	})
	if err != nil {
		return err
	}

	var d struct {
		Data struct {
			Nickname string `json:"nickname"`
			UserID   int64  `json:"user_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp, &d); err != nil || d.Data.Nickname == "" {
		return fmt.Errorf("无效响应")
	}

	info.Name = d.Data.Nickname
	info.Topic = fmt.Sprintf("用户: %d", d.Data.UserID)
	info.Avatar = fmt.Sprintf("https://q1.qlogo.cn/g?b=qq&nk=%s&s=640", userID) // QQ 用户头像 URL
	info.ID = "p:" + userID                                                     // 标记为私聊
	return nil
}

// Make 获取或返回目标房间 ID
// 参数:
//   - ctx: 上下文
//   - info: 房间信息（混合模式下可为 nil）
//
// 返回:
//   - string: 房间 ID
//   - error: 错误
func (q *QQ) Make(ctx context.Context, info *internal.Info) (string, error) {
	// 如果提供了房间信息，直接返回
	if info != nil && info.ID != "" {
		return info.ID, nil
	}

	// 否则使用配置中的默认群组
	if q.cfg.Group != "" {
		return q.cfg.Group, nil
	}
	return "", fmt.Errorf("需要配置'group'字段")
}
