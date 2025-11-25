package qq

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"Relify/internal"
)

// onebotEvent OneBot 事件结构
type onebotEvent struct {
	Time     int64  `json:"time"`      // 事件时间戳
	SelfID   int64  `json:"self_id"`   // Bot 自身 QQ 号
	PostType string `json:"post_type"` // 事件类型: message/notice/request

	// 消息事件字段
	MsgType string          `json:"message_type"` // 消息类型: group/private
	SubType string          `json:"sub_type"`     // 子类型
	MsgID   int32           `json:"message_id"`   // 消息 ID
	GroupID int64           `json:"group_id"`     // 群号
	UserID  int64           `json:"user_id"`      // 用户 QQ 号
	Message json.RawMessage `json:"message"`      // 消息内容（段数组）
	Sender  senderInfo      `json:"sender"`       // 发送者信息

	// 通知事件字段
	NoticeType string   `json:"notice_type"` // 通知类型
	OperatorID int64    `json:"operator_id"` // 操作者 QQ 号
	TargetID   int64    `json:"target_id"`   // 目标 QQ 号
	File       fileInfo `json:"file"`        // 文件信息

	// 请求事件字段
	RequestType string `json:"request_type"` // 请求类型
	Comment     string `json:"comment"`      // 附加消息
	Flag        string `json:"flag"`         // 请求标识
}

// senderInfo 发送者信息
type senderInfo struct {
	Nickname string `json:"nickname"` // 昵称
	Card     string `json:"card"`     // 群名片
}

// fileInfo 文件信息
type fileInfo struct {
	ID   string `json:"id"`   // 文件 ID
	Name string `json:"name"` // 文件名
	Size int64  `json:"size"` // 文件大小
	Url  string `json:"url"`  // 下载链接
}

// handleMsg 处理 OneBot 事件消息
// 参数:
//   - data: OneBot 事件 JSON 数据
func (q *QQ) handleMsg(data []byte) {
	var evt onebotEvent
	if err := json.Unmarshal(data, &evt); err != nil {
		slog.Warn("QQ 解析事件失败", "error", err)
		return
	}

	// 忽略元事件（心跳等）
	if evt.PostType == "meta_event" {
		return
	}

	slog.Debug("QQ 接收事件",
		"post_type", evt.PostType,
		"msg_type", evt.MsgType,
		"group_id", evt.GroupID,
	)

	ctx := context.Background()
	// 构建基础事件
	base := &internal.Event{
		Time: time.Unix(evt.Time, 0),
		Plat: q.Name(),
		Extra: internal.Props{
			"self_id": evt.SelfID,
		},
	}

	// 根据事件类型分发处理
	switch evt.PostType {
	case "message", "message_sent":
		q.handleMessage(ctx, &evt, base)
	case "notice":
		q.handleNotice(ctx, &evt, base)
	case "request":
		q.handleRequest(ctx, &evt, base)
	}
}

// handleMessage 处理消息事件
// 参数:
//   - ctx: 上下文
//   - src: OneBot 事件
//   - dst: 内部事件（将被填充）
func (q *QQ) handleMessage(ctx context.Context, src *onebotEvent, dst *internal.Event) {
	dst.ID = strconv.Itoa(int(src.MsgID))
	dst.Kind = internal.Msg
	dst.User = strconv.FormatInt(src.UserID, 10)
	dst.Avatar = fmt.Sprintf("https://q1.qlogo.cn/g?b=qq&nk=%d&s=640", src.UserID) // QQ 头像 URL

	// 获取发送者昵称（优先使用群名片）
	dst.Name = src.Sender.Card
	if dst.Name == "" {
		dst.Name = src.Sender.Nickname
	}
	if dst.Name == "" {
		dst.Name = dst.User
	}

	// 区分群聊和私聊
	if src.MsgType == "group" {
		dst.Room = strconv.FormatInt(src.GroupID, 10)
		dst.Extra["chat_type"] = "group"
	} else {
		dst.Room = fmt.Sprintf("p:%d", src.UserID) // 私聊房间 ID 使用 "p:" 前缀
		dst.Extra["chat_type"] = "private"
	}

	slog.Debug("QQ 处理消息",
		"id", dst.ID,
		"user", dst.User,
		"room", dst.Room,
		"type", dst.Extra["chat_type"],
	)

	// 解析消息段（提取回复引用）
	var refID string
	dst.Segs, refID = q.parseSegs(ctx, src.Message)
	if refID != "" {
		dst.Ref = refID
	}

	q.router.Handle(ctx, dst)
}

// handleNotice 处理通知事件
// 参数:
//   - ctx: 上下文
//   - src: OneBot 事件
//   - dst: 内部事件（将被填充）
func (q *QQ) handleNotice(ctx context.Context, src *onebotEvent, dst *internal.Event) {
	dst.Kind = internal.Note
	if src.UserID != 0 {
		dst.User = strconv.FormatInt(src.UserID, 10)
	}

	// 设置房间 ID
	if src.GroupID != 0 {
		dst.Room = strconv.FormatInt(src.GroupID, 10)
	} else if src.UserID != 0 {
		dst.Room = fmt.Sprintf("p:%d", src.UserID)
	}

	// 根据通知类型处理
	switch src.NoticeType {
	case "group_recall", "friend_recall":
		q.handleRecallNotice(src, dst) // 撤回消息
	case "notify":
		q.handleNotifyEvent(src, dst) // 戳一戳等通知
	case "group_upload":
		q.handleFileUpload(src, dst) // 文件上传
	case "friend_add":
		dst.Segs = []internal.Seg{{Kind: "text", Raw: internal.Props{"txt": "成为好友"}}}
	}

	// 只有有内容或引用的通知才转发
	if len(dst.Segs) > 0 || dst.Ref != "" {
		q.router.Handle(ctx, dst)
	}
}

// handleRecallNotice 处理撤回消息通知
// 参数:
//   - src: OneBot 事件
//   - dst: 内部事件（将被填充）
func (q *QQ) handleRecallNotice(src *onebotEvent, dst *internal.Event) {
	dst.Extra["subtype"] = internal.Revoke
	if src.OperatorID != 0 {
		dst.User = strconv.FormatInt(src.OperatorID, 10) // 撤回操作者
	}
	dst.Ref = strconv.Itoa(int(src.MsgID))  // 被撤回的消息 ID
	dst.ID = fmt.Sprintf("rev_%s", dst.Ref) // 撤回事件 ID
	dst.Segs = []internal.Seg{{Kind: "text", Raw: internal.Props{"txt": "撤回消息"}}}
}

// handleNotifyEvent 处理戳一戳等通知事件
// 参数:
//   - src: OneBot 事件
//   - dst: 内部事件（将被填充）
func (q *QQ) handleNotifyEvent(src *onebotEvent, dst *internal.Event) {
	switch src.SubType {
	case "poke":
		dst.Segs = []internal.Seg{{Kind: "text", Raw: internal.Props{"txt": fmt.Sprintf("戳了戳 %d", src.TargetID)}}}
	case "lucky_king":
		dst.Segs = []internal.Seg{{Kind: "text", Raw: internal.Props{"txt": "成为运气王"}}}
	}
}

// handleFileUpload 处理文件上传通知
// 参数:
//   - src: OneBot 事件
//   - dst: 内部事件（将被填充）
func (q *QQ) handleFileUpload(src *onebotEvent, dst *internal.Event) {
	dst.Segs = []internal.Seg{
		{Kind: "text", Raw: internal.Props{"txt": fmt.Sprintf("[文件] %s (%d 字节)", src.File.Name, src.File.Size)}},
	}
	// 如果有下载链接，添加文件段
	if src.File.Url != "" {
		dst.Segs = append(dst.Segs, internal.Seg{
			Kind: "file",
			Raw:  internal.Props{"url": src.File.Url, "name": src.File.Name},
		})
	}
}

// handleRequest 处理请求事件（加好友/加群等）
// 参数:
//   - ctx: 上下文
//   - src: OneBot 事件
//   - dst: 内部事件（将被填充）
func (q *QQ) handleRequest(ctx context.Context, src *onebotEvent, dst *internal.Event) {
	dst.Kind = internal.Note
	dst.User = strconv.FormatInt(src.UserID, 10)
	if src.GroupID != 0 {
		dst.Room = strconv.FormatInt(src.GroupID, 10)
	} else {
		dst.Room = fmt.Sprintf("p:%d", src.UserID)
	}

	txt := fmt.Sprintf("请求 [%s]: %s (标识: %s)", src.RequestType, src.Comment, src.Flag)
	dst.Segs = []internal.Seg{{Kind: "text", Raw: internal.Props{"txt": txt}}}

	q.router.Handle(ctx, dst)
}

// parseSegs 解析 OneBot 消息段数组
// 参数:
//   - ctx: 上下文
//   - raw: OneBot 消息段 JSON
//
// 返回:
//   - []internal.Seg: 内部消息段列表
//   - string: 回复引用的消息 ID（如果有）
func (q *QQ) parseSegs(ctx context.Context, raw json.RawMessage) ([]internal.Seg, string) {
	var arr []segmentItem

	// 如果解析失败，视为纯文本
	if json.Unmarshal(raw, &arr) != nil {
		return []internal.Seg{{Kind: "text", Raw: internal.Props{"txt": string(raw)}}}, ""
	}

	var segs []internal.Seg
	var refID string

	for _, item := range arr {
		seg, ref := q.parseSegment(ctx, item)
		if seg.Kind != "" {
			segs = append(segs, seg)
		}
		if ref != "" {
			refID = ref // 提取回复引用
		}
	}
	return segs, refID
}

// segmentItem OneBot 消息段结构
type segmentItem struct {
	Type string         `json:"type"` // 段类型
	Data map[string]any `json:"data"` // 段数据
}

// parseSegment 解析单个 OneBot 消息段
// 参数:
//   - ctx: 上下文
//   - item: OneBot 消息段
//
// 返回:
//   - internal.Seg: 内部消息段
//   - string: 回复引用的消息 ID（仅 reply 类型返回）
func (q *QQ) parseSegment(ctx context.Context, item segmentItem) (internal.Seg, string) {
	switch item.Type {
	case "text":
		// 文本段
		if t, ok := item.Data["text"].(string); ok {
			return internal.Seg{Kind: "text", Raw: internal.Props{"txt": t}}, ""
		}

	case "image", "flash":
		// 图片段（包括闪照）
		return internal.Seg{
			Kind: "image",
			Raw: internal.Props{
				"url":  item.Data["url"],
				"name": item.Data["file"],
				"size": item.Data["file_size"],
			},
		}, ""

	case "record":
		// 语音段
		return internal.Seg{
			Kind: "audio",
			Raw: internal.Props{
				"url":  item.Data["url"],
				"name": item.Data["file"],
			},
		}, ""

	case "video":
		// 视频段
		return internal.Seg{
			Kind: "video",
			Raw: internal.Props{
				"url":  item.Data["url"],
				"name": item.Data["file"],
			},
		}, ""

	case "file":
		// 文件段
		return internal.Seg{
			Kind: "file",
			Raw: internal.Props{
				"url":  item.Data["url"],
				"name": item.Data["name"],
				"size": item.Data["file_size"],
			},
		}, ""

	case "face":
		// 表情段
		return internal.Seg{
			Kind: "text",
			Raw:  internal.Props{"txt": fmt.Sprintf("[表情:%v]", item.Data["id"])},
		}, ""

	case "reply":
		// 回复段：返回被回复的消息 ID
		if id, ok := item.Data["id"]; ok {
			return internal.Seg{}, fmt.Sprintf("%v", id)
		}

	case "at":
		// @提及段
		return internal.Seg{
			Kind: "mention",
			Raw:  internal.Props{"user": fmt.Sprintf("%v", item.Data["qq"])},
		}, ""

	case "forward":
		// 转发消息段：递归获取内容
		if id, ok := item.Data["id"].(string); ok {
			content := q.fetchForwardMsg(ctx, id, 0)
			return internal.Seg{Kind: "text", Raw: internal.Props{"txt": content}}, ""
		}
		return internal.Seg{Kind: "text", Raw: internal.Props{"txt": "[转发消息]"}}, ""

	case "node":
		// 转发节点段
		return internal.Seg{Kind: "text", Raw: internal.Props{"txt": "[转发节点]"}}, ""

	default:
		// 未知类型：序列化为 JSON 显示
		bs, _ := json.Marshal(item.Data)
		return internal.Seg{
			Kind: "text",
			Raw:  internal.Props{"txt": fmt.Sprintf("[%s: %s]", item.Type, string(bs))},
		}, ""
	}

	return internal.Seg{}, ""
}

// fetchForwardMsg 递归获取转发消息内容
// 参数:
//   - ctx: 上下文
//   - resID: 转发消息 ID
//   - depth: 当前递归深度（最大 3 层）
//
// 返回:
//   - string: 格式化的转发消息内容
func (q *QQ) fetchForwardMsg(ctx context.Context, resID string, depth int) string {
	// 限制递归深度
	if depth >= 3 {
		return " [嵌套转发] "
	}

	// 调用 OneBot API 获取转发消息
	resp, err := q.client.Call(ctx, "get_forward_msg", map[string]any{
		"message_id": resID,
	})
	if err != nil {
		return fmt.Sprintf("[获取转发消息失败: %v]", err)
	}

	var res struct {
		Data struct {
			Messages []map[string]any `json:"messages"`
		} `json:"data"`
	}

	if json.Unmarshal(resp, &res) != nil || len(res.Data.Messages) == 0 {
		return "[内容为空]"
	}

	// 格式化转发消息内容
	var sb strings.Builder
	indent := strings.Repeat("  ", depth) // 缩进（根据层级）
	sb.WriteString(fmt.Sprintf("\n%s--- 转发消息 (层级 %d) ---\n", indent, depth+1))

	for _, msg := range res.Data.Messages {
		nickname := q.extractNickname(msg)
		contentStr := q.extractMessageContent(ctx, msg, depth)
		sb.WriteString(fmt.Sprintf("%s%s: %s\n", indent, nickname, contentStr))
	}
	sb.WriteString(fmt.Sprintf("%s------------------------", indent))

	return sb.String()
}

// extractNickname 提取消息发送者昵称
// 参数:
//   - msg: 消息数据
//
// 返回:
//   - string: 昵称
func (q *QQ) extractNickname(msg map[string]any) string {
	if sender, ok := msg["sender"].(map[string]any); ok {
		if nick, ok := sender["nickname"].(string); ok && nick != "" {
			return nick
		}
	}
	return "未知用户"
}

// extractMessageContent 提取消息内容
// 参数:
//   - ctx: 上下文
//   - msg: 消息数据
//   - depth: 当前递归深度
//
// 返回:
//   - string: 格式化的消息内容
func (q *QQ) extractMessageContent(ctx context.Context, msg map[string]any, depth int) string {
	// 尝试 "content" 字段
	if content, ok := msg["content"]; ok {
		contentBytes, _ := json.Marshal(content)
		return q.parseContentRecursive(ctx, contentBytes, depth)
	}
	// 尝试 "message" 字段
	if message, ok := msg["message"]; ok {
		contentBytes, _ := json.Marshal(message)
		return q.parseContentRecursive(ctx, contentBytes, depth)
	}
	return "[无内容]"
}

// parseContentRecursive 递归解析消息内容（用于转发消息）
// 参数:
//   - ctx: 上下文
//   - raw: 消息段 JSON
//   - depth: 当前递归深度
//
// 返回:
//   - string: 格式化的消息内容
func (q *QQ) parseContentRecursive(ctx context.Context, raw json.RawMessage, depth int) string {
	var arr []struct {
		Type string         `json:"type"`
		Data map[string]any `json:"data"`
	}
	if json.Unmarshal(raw, &arr) != nil {
		return "[内容格式错误]"
	}

	var sb strings.Builder
	for _, item := range arr {
		switch item.Type {
		case "text":
			if t, ok := item.Data["text"].(string); ok {
				sb.WriteString(t)
			}
		case "image":
			sb.WriteString("[图片]")
		case "record":
			sb.WriteString("[语音]")
		case "video":
			sb.WriteString("[视频]")
		case "file":
			if name, ok := item.Data["name"].(string); ok {
				sb.WriteString(fmt.Sprintf("[文件: %s]", name))
			} else {
				sb.WriteString("[文件]")
			}
		case "at":
			sb.WriteString(fmt.Sprintf(" @%v ", item.Data["qq"]))
		case "face":
			sb.WriteString(fmt.Sprintf("[表情:%v]", item.Data["id"]))
		case "forward":
			// 嵌套转发消息（递归获取）
			if id, ok := item.Data["id"].(string); ok {
				sb.WriteString(q.fetchForwardMsg(ctx, id, depth+1))
			} else {
				sb.WriteString("[嵌套转发]")
			}
		default:
			sb.WriteString(fmt.Sprintf("[%s]", item.Type))
		}
	}
	return sb.String()
}
