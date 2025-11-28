package matrix

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"strings"

	"Relify/internal"

	"maunium.net/go/mautrix/appservice"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// Send 向 Matrix 发送消息
// 根据事件类型调用相应的发送函数
// 参数:
//   - ctx: 上下文
//   - node: 目标节点（包含房间 ID）
//   - evt: 要发送的事件
//
// 返回:
//   - string: Matrix 事件 ID
//   - error: 错误信息
func (m *Matrix) Send(ctx context.Context, node *internal.Node, evt *internal.Event) (string, error) {
	slog.Debug("Matrix 发送事件",
		"room", node.Room,
		"kind", evt.Kind,
		"user", evt.User,
		"raw", func() string {
			if data, err := json.Marshal(evt); err == nil {
				return string(data)
			}
			return ""
		}(),
	)

	switch evt.Kind {
	case internal.Msg:
		// 普通消息
		return m.sendMessage(ctx, node.Room, evt)
	case internal.Edit:
		// 编辑消息
		return m.sendEdit(ctx, node.Room, evt)
	case internal.Note:
		// 通知事件
		if internal.Revoke == evt.Extra["subtype"] {
			// 撤回消息
			return "", m.sendRedact(ctx, node.Room, evt.Ref)
		}
	}
	return "", nil
}

// getGhost 获取或创建 Ghost 用户的 Intent API
// Ghost 用户是 AppService 为其他平台用户创建的傀儡账号
// 参数:
//   - evt: 原始事件（包含用户信息）
//
// 返回:
//   - *appservice.IntentAPI: Ghost 用户的操作接口
func (m *Matrix) getGhost(evt *internal.Event) *appservice.IntentAPI {
	// 构建 Ghost 用户的本地部分: namespace_平台_用户ID
	localpart := fmt.Sprintf("%s%s_%s", m.cfg.AppService.Namespace, evt.Plat, m.sanitize(evt.User))
	mxid := id.NewUserID(localpart, m.cfg.Domain)
	intent := m.as.Intent(mxid)

	// 缓存键包含用户名和头像信息（用于检测更新）
	key := fmt.Sprintf("ghost_%s_%s_%s", mxid.String(), evt.Name, evt.Avatar)

	// 如果缓存中不存在，异步更新 Ghost 用户资料
	if _, loaded := m.cache.LoadOrStore(key, true); !loaded {
		go m.updateGhostProfile(intent, evt)
	}
	return intent
}

// updateGhostProfile 更新 Ghost 用户的显示名称和头像
// 此操作异步执行，避免阻塞消息发送
// 参数:
//   - intent: Ghost 用户的操作接口
//   - evt: 包含用户名称和头像的事件
func (m *Matrix) updateGhostProfile(intent *appservice.IntentAPI, evt *internal.Event) {
	ctx := context.Background()

	slog.Debug("Matrix 开始更新Ghost用户资料",
		"user_id", intent.UserID,
		"name", evt.Name,
		"avatar", evt.Avatar,
		"platform", evt.Plat,
		"original_user", evt.User,
	)

	// 确保用户已注册
	if err := intent.EnsureRegistered(ctx); err != nil {
		slog.Error("Matrix Ghost用户注册失败",
			"user_id", intent.UserID,
			"error", err,
		)
		return
	}

	// 设置显示名称
	name := evt.Name
	if name == "" {
		name = evt.User // 如果没有昵称，使用用户 ID
	}

	slog.Debug("Matrix 设置显示名称",
		"user_id", intent.UserID,
		"name", name,
	)

	if err := intent.SetDisplayName(ctx, name); err != nil {
		slog.Error("Matrix 设置显示名称失败",
			"user_id", intent.UserID,
			"name", name,
			"error", err,
		)
	}

	// 设置头像（如果有）
	if evt.Avatar != "" {
		mxc, err := m.uploadMedia(ctx, intent, evt.Avatar, "image/jpeg")
		if err != nil {
			slog.Error("Matrix 上传头像失败",
				"user_id", intent.UserID,
				"avatar_url", evt.Avatar,
				"error", err,
			)
		} else if mxc == "" {
			slog.Warn("Matrix 上传头像返回空MXC",
				"user_id", intent.UserID,
				"avatar_url", evt.Avatar,
			)
		} else {
			avatarURI, err := id.ParseContentURI(mxc)
			if err != nil {
				slog.Error("Matrix 解析MXC URI失败",
					"user_id", intent.UserID,
					"mxc", mxc,
					"error", err,
				)
			} else {
				slog.Debug("Matrix 设置头像URL",
					"user_id", intent.UserID,
					"avatar_uri", avatarURI,
				)

				if err := intent.SetAvatarURL(ctx, avatarURI); err != nil {
					slog.Error("Matrix 设置头像URL失败",
						"user_id", intent.UserID,
						"avatar_uri", avatarURI,
						"error", err,
					)
				}
			}
		}
	}
}

// sendMessage 发送普通消息到 Matrix 房间
// 参数:
//   - ctx: 上下文
//   - roomID: 目标房间 ID
//   - evt: 要发送的事件
//
// 返回:
//   - string: Matrix 事件 ID
//   - error: 错误信息
func (m *Matrix) sendMessage(ctx context.Context, roomID string, evt *internal.Event) (string, error) {
	intent := m.getGhost(evt) // 获取发送者的 Ghost 用户

	// 渲染消息内容（将内部格式转换为 Matrix 格式）
	content, err := m.renderContent(ctx, intent, evt.Segs)
	if err != nil {
		return "", err
	}

	// 如果是回复消息，设置关联关系
	if evt.Ref != "" {
		content.RelatesTo = &event.RelatesTo{
			InReplyTo: &event.InReplyTo{EventID: id.EventID(evt.Ref)},
		}
	}

	// 发送消息事件
	resp, err := intent.SendMessageEvent(ctx, id.RoomID(roomID), event.EventMessage, content)
	if err != nil {
		return "", err
	}
	return resp.EventID.String(), nil
}

// sendEdit 发送编辑消息到 Matrix 房间
// 参数:
//   - ctx: 上下文
//   - roomID: 目标房间 ID
//   - evt: 包含新内容的编辑事件
//
// 返回:
//   - string: Matrix 事件 ID
//   - error: 错误信息
func (m *Matrix) sendEdit(ctx context.Context, roomID string, evt *internal.Event) (string, error) {
	intent := m.getGhost(evt)
	newContent, _ := m.renderContent(ctx, intent, evt.Segs) // 渲染新内容

	// 构建编辑消息（Body 以 "* " 开头表示编辑）
	content := &event.MessageEventContent{
		MsgType:    event.MsgText,
		Body:       "* " + newContent.Body, // 旧客户端显示格式
		NewContent: newContent,             // 新客户端使用的内容
		RelatesTo: &event.RelatesTo{
			Type:    event.RelReplace,    // 替换关系类型
			EventID: id.EventID(evt.Ref), // 被编辑的原始消息 ID
		},
	}

	resp, err := intent.SendMessageEvent(ctx, id.RoomID(roomID), event.EventMessage, content)
	if err != nil {
		return "", err
	}
	return resp.EventID.String(), nil
}

// sendRedact 撤回 Matrix 房间中的消息
// 参数:
//   - ctx: 上下文
//   - roomID: 目标房间 ID
//   - eventID: 要撤回的消息 ID
//
// 返回:
//   - error: 错误信息
func (m *Matrix) sendRedact(ctx context.Context, roomID, eventID string) error {
	// 使用 Bot 账号撤回消息（需要权限）
	_, err := m.as.BotIntent().RedactEvent(ctx, id.RoomID(roomID), id.EventID(eventID))
	return err
}

// renderContent 将内部消息段列表渲染为 Matrix 消息内容
// 参数:
//   - ctx: 上下文
//   - intent: 发送者的 Intent API
//   - segs: 内部消息段列表
//
// 返回:
//   - *event.MessageEventContent: Matrix 消息内容
//   - error: 错误信息
func (m *Matrix) renderContent(ctx context.Context, intent *appservice.IntentAPI, segs []internal.Seg) (*event.MessageEventContent, error) {
	var body strings.Builder     // 纯文本内容
	var htmlBody strings.Builder // HTML 格式内容
	content := &event.MessageEventContent{MsgType: event.MsgText}

	for _, s := range segs {
		switch s.Kind {
		case "text":
			// 文本段
			txt := s.Raw["txt"].(string)
			body.WriteString(txt)
			htmlBody.WriteString(html.EscapeString(txt)) // HTML 转义

		case "image", "file", "video", "audio":
			// 媒体段：上传后添加到消息中
			if err := m.renderMediaSegment(ctx, intent, &s, content, &body, &htmlBody); err != nil {
				// 上传失败时，降级为链接文本
				urlStr := s.Raw["url"].(string)
				name, _ := s.Raw["name"].(string)
				if name == "" {
					name = s.Kind
				}
				link := fmt.Sprintf(" [%s: %s] ", name, urlStr)
				body.WriteString(link)
				htmlBody.WriteString(link)
			}

		case "mention":
			// 提及段：转换为 Matrix 用户提及
			m.renderMention(&s, &body, &htmlBody)
		}
	}

	// 如果消息类型仍为文本（没有媒体），设置文本内容
	if content.MsgType == event.MsgText {
		content.Body = body.String()
		content.Format = event.FormatHTML
		content.FormattedBody = htmlBody.String()
	}

	return content, nil
}

// renderMediaSegment 渲染媒体段（上传并设置消息内容）
// 参数:
//   - ctx: 上下文
//   - intent: 发送者的 Intent API
//   - seg: 媒体段
//   - content: 消息内容（将被修改为媒体消息）
//   - body: 纯文本内容（不使用，用于 API 兼容）
//   - htmlBody: HTML 内容（不使用，用于 API 兼容）
//
// 返回:
//   - error: 错误信息
func (m *Matrix) renderMediaSegment(ctx context.Context, intent *appservice.IntentAPI, seg *internal.Seg, content *event.MessageEventContent, body, htmlBody *strings.Builder) error {
	urlStr := seg.Raw["url"].(string)
	name, _ := seg.Raw["name"].(string)
	if name == "" {
		name = seg.Kind // 如果没有文件名，使用段类型
	}

	// 上传媒体文件
	mxc, err := m.uploadMedia(ctx, intent, urlStr, "")
	if err != nil {
		return err
	}

	// 设置消息内容为媒体消息
	content.URL = id.ContentURIString(mxc)
	content.Body = name
	content.FileName = name

	// 初始化文件信息
	if content.Info == nil {
		content.Info = &event.FileInfo{}
	}

	// 设置文件大小（如果有）
	if size, ok := seg.Raw["size"]; ok {
		switch v := size.(type) {
		case int64:
			content.Info.Size = int(v)
		case int:
			content.Info.Size = v
		case float64:
			content.Info.Size = int(v)
		}
	}

	// 设置消息类型（图片/视频/音频/文件）
	content.MsgType = map[string]event.MessageType{
		"image": event.MsgImage,
		"video": event.MsgVideo,
		"audio": event.MsgAudio,
		"file":  event.MsgFile,
	}[seg.Kind]

	return nil
}

// renderMention 渲染提及段（转换为 Matrix 用户 ID）
// 参数:
//   - seg: 提及段
//   - body: 纯文本内容
//   - htmlBody: HTML 内容（包含超链接）
func (m *Matrix) renderMention(seg *internal.Seg, body, htmlBody *strings.Builder) {
	u := seg.Raw["user"].(string)

	var mxid string
	// 如果是纯数字（QQ 号），构建 Ghost 用户 ID
	if _, err := fmt.Sscanf(u, "%d", new(int64)); err == nil {
		localpart := fmt.Sprintf("%s%s_%s", m.cfg.AppService.Namespace, "qq", u)
		mxid = id.NewUserID(localpart, m.cfg.Domain).String()
	} else {
		// 否则直接使用（可能已是 Matrix ID）
		mxid = u
	}

	// 添加到内容中（HTML 格式包含 matrix.to 链接）
	body.WriteString(mxid + " ")
	htmlBody.WriteString(fmt.Sprintf(`<a href="https://matrix.to/#/%s">%s</a> `, mxid, mxid))
}
