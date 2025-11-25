package qq

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"Relify/internal"
)

// Send 向 QQ 发送消息
// 根据事件类型调用相应的发送函数
// 参数:
//   - ctx: 上下文
//   - node: 目标节点（包含房间 ID）
//   - evt: 要发送的事件
//
// 返回:
//   - string: OneBot 消息 ID
//   - error: 错误信息
func (q *QQ) Send(ctx context.Context, node *internal.Node, evt *internal.Event) (string, error) {
	slog.Debug("QQ 发送事件",
		"room", node.Room,
		"kind", evt.Kind,
		"user", evt.User,
	)

	switch evt.Kind {
	case internal.Msg:
		// 普通消息
		return q.sendMsg(ctx, node, evt)
	case internal.Edit:
		// 编辑消息（QQ 不支持编辑，使用删除后重发）
		return q.handleEdit(ctx, node, evt)
	case internal.Note:
		// 通知事件
		if internal.Revoke == evt.Extra["subtype"] {
			// 撤回消息
			return "", q.deleteMsg(ctx, evt.Ref)
		}
	}
	return "", nil
}

// handleEdit 处理编辑消息（删除旧消息 + 发送新消息）
// QQ 不支持消息编辑，所以采用删除后重发的方式
// 参数:
//   - ctx: 上下文
//   - node: 目标节点
//   - evt: 编辑事件
//
// 返回:
//   - string: 新消息 ID
//   - error: 错误信息
func (q *QQ) handleEdit(ctx context.Context, node *internal.Node, evt *internal.Event) (string, error) {
	if evt.Ref == "" {
		return "", fmt.Errorf("编辑事件缺少引用")
	}

	// 删除原消息（忽略错误）
	_ = q.deleteMsg(ctx, evt.Ref)

	// 发送新消息
	return q.sendMsg(ctx, node, evt)
}

// sendMsg 发送消息到 QQ 群或私聊
// 参数:
//   - ctx: 上下文
//   - node: 目标节点
//   - evt: 要发送的事件
//
// 返回:
//   - string: OneBot 消息 ID
//   - error: 错误信息
func (q *QQ) sendMsg(ctx context.Context, node *internal.Node, evt *internal.Event) (string, error) {
	// 判断是否为私聊（房间 ID 以 "p:" 开头）
	isPrivate := strings.HasPrefix(node.Room, "p:")
	roomID := strings.TrimPrefix(node.Room, "p:")

	// 解析房间 ID（群号或 QQ 号）
	idInt, err := strconv.ParseInt(roomID, 10, 64)
	if err != nil {
		return "", fmt.Errorf("无效的房间ID: %s", roomID)
	}

	// 构建 OneBot 消息段
	obMsg := q.buildSegments(evt)

	// 根据聊天类型选择 API 动作
	action := "send_group_msg"
	params := map[string]any{"group_id": idInt, "message": obMsg}

	if isPrivate {
		action = "send_private_msg"
		params = map[string]any{"user_id": idInt, "message": obMsg}
	}

	// 调用 OneBot API
	resp, err := q.client.Call(ctx, action, params)
	if err != nil {
		return "", err
	}

	// 解析响应，提取消息 ID
	var d struct {
		Data struct {
			ID int32 `json:"message_id"`
		} `json:"data"`
	}
	if json.Unmarshal(resp, &d) != nil {
		return "", fmt.Errorf("解析响应失败")
	}

	return strconv.Itoa(int(d.Data.ID)), nil
}

// buildSegments 将内部消息段列表转换为 OneBot 格式
// 参数:
//   - evt: 内部事件
//
// 返回:
//   - []map[string]any: OneBot 消息段数组
func (q *QQ) buildSegments(evt *internal.Event) []map[string]any {
	var obMsg []map[string]any

	// 如果是回复消息，添加 reply 段
	if evt.Ref != "" && evt.Kind == internal.Msg {
		obMsg = append(obMsg, map[string]any{
			"type": "reply",
			"data": map[string]string{"id": evt.Ref},
		})
	}

	// 转换所有消息段
	for _, s := range evt.Segs {
		seg := q.buildSegment(&s)
		if seg != nil {
			obMsg = append(obMsg, seg)
		}
	}

	return obMsg
}

// buildSegment 将单个内部消息段转换为 OneBot 格式
// 参数:
//   - s: 内部消息段
//
// 返回:
//   - map[string]any: OneBot 消息段（如果无法转换则返回 nil）
func (q *QQ) buildSegment(s *internal.Seg) map[string]any {
	switch s.Kind {
	case "text":
		// 文本段
		return map[string]any{
			"type": "text",
			"data": map[string]any{"text": s.Raw["txt"]},
		}

	case "image":
		// 图片段
		return map[string]any{
			"type": "image",
			"data": map[string]any{"file": s.Raw["url"]},
		}

	case "audio":
		// 语音段
		return map[string]any{
			"type": "record",
			"data": map[string]any{"file": s.Raw["url"]},
		}

	case "video":
		// 视频段
		return map[string]any{
			"type": "video",
			"data": map[string]any{"file": s.Raw["url"]},
		}

	case "file":
		// 文件段
		data := map[string]any{"file": s.Raw["url"]}
		if name, ok := s.Raw["name"].(string); ok && name != "" {
			data["name"] = name // 添加文件名
		}
		if size := q.extractSize(s.Raw["size"]); size != 0 {
			data["file_size"] = size // 添加文件大小
		}
		return map[string]any{"type": "file", "data": data}

	case "mention":
		// 提及段（@用户）
		if u, ok := s.Raw["user"].(string); ok {
			qqID := q.extractQQFromMXID(u) // 从 Matrix ID 提取 QQ 号
			return map[string]any{
				"type": "at",
				"data": map[string]any{"qq": qqID},
			}
		}
	}

	return nil
}

// extractSize 提取文件大小（处理不同类型）
// 参数:
//   - size: 文件大小（可能是 int64/float64/string）
//
// 返回:
//   - int64: 文件大小（字节）
func (q *QQ) extractSize(size any) int64 {
	switch v := size.(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	case string:
		if val, err := strconv.ParseInt(v, 10, 64); err == nil {
			return val
		}
	}
	return 0
}

// extractQQFromMXID 从 Matrix 用户 ID 提取 QQ 号
// 参数:
//   - userID: Matrix 用户 ID 或 QQ 号
//
// 返回:
//   - string: QQ 号
func (q *QQ) extractQQFromMXID(userID string) string {
	// 检查是否为 Ghost 用户格式: @relify_qq_<QQ号>:<域名>
	if strings.HasPrefix(userID, "@relify_qq_") && strings.Contains(userID, ":") {
		parts := strings.Split(userID, ":")
		if len(parts) > 0 {
			localpart := parts[0]
			qqID := strings.TrimPrefix(localpart, "@relify_qq_")
			return qqID
		}
	}
	// 否则直接返回（可能已是 QQ 号）
	return userID
}

// deleteMsg 删除（撤回）QQ 消息
// 参数:
//   - ctx: 上下文
//   - msgID: 消息 ID
//
// 返回:
//   - error: 错误信息
func (q *QQ) deleteMsg(ctx context.Context, msgID string) error {
	// 解析消息 ID
	id, err := strconv.Atoi(msgID)
	if err != nil {
		return err
	}

	// 调用 OneBot API 删除消息
	_, err = q.client.Call(ctx, "delete_msg", map[string]any{"message_id": id})
	return err
}
