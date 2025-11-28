package matrix

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"Relify/internal"

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// processEvent 处理从 Matrix 接收的事件
// 过滤掉 Bot 和 Ghost 用户的事件，处理消息和撤回事件
// 参数:
//   - evt: Matrix 事件
func (m *Matrix) processEvent(evt *event.Event) {
	// 忽略 Bot 自己发送的事件
	if evt.Sender == m.botUserID {
		return
	}

	// 忽略 Ghost 用户发送的事件（由其他平台桥接过来的）
	prefix := "@" + m.cfg.AppService.Namespace
	if strings.HasPrefix(evt.Sender.String(), prefix) {
		return
	}

	slog.Debug("Matrix 接收事件",
		"type", evt.Type,
		"sender", evt.Sender,
		"room", evt.RoomID,
		"raw", func() string {
			if data, err := json.Marshal(evt); err == nil {
				return string(data)
			}
			return ""
		}(),
	)

	// 根据事件类型分发处理
	switch evt.Type {
	case event.EventMessage:
		m.handleMessage(evt) // 处理消息事件
	case event.EventRedaction:
		m.handleRedaction(evt) // 处理撤回事件
	}
}

// handleMessage 处理 Matrix 消息事件
// 转换为统一的内部事件格式并路由到其他平台
// 参数:
//   - evt: Matrix 消息事件
func (m *Matrix) handleMessage(evt *event.Event) {
	content := evt.Content.AsMessage()

	isEdit := false             // 是否为编辑消息
	originID := evt.ID.String() // 原始消息 ID

	// 检查是否为编辑消息（Matrix 使用 m.relates_to 表示关系）
	if content.RelatesTo != nil && content.RelatesTo.Type == event.RelReplace {
		isEdit = true
		originID = content.RelatesTo.EventID.String()
		// 使用新内容（如果存在）
		if content.NewContent != nil {
			content = content.NewContent
		}
	}

	// 获取发送者的显示名称和头像
	name, avatar := m.getMemberInfo(evt.Sender, evt.RoomID)

	slog.Debug("Matrix 处理消息",
		"id", originID,
		"is_edit", isEdit,
		"user", evt.Sender,
		"room", evt.RoomID,
	)

	// 构建内部事件结构
	e := &internal.Event{
		ID:     originID,
		Kind:   internal.Msg,
		Time:   time.UnixMilli(evt.Timestamp),
		Plat:   m.Name(),
		Room:   evt.RoomID.String(),
		User:   evt.Sender.String(),
		Name:   name,
		Avatar: avatar,
	}

	// 设置编辑标记
	if isEdit {
		e.Kind = internal.Edit
		e.Ref = originID
	}

	// 处理回复消息（不是编辑的情况下）
	if !isEdit && content.RelatesTo != nil && content.RelatesTo.InReplyTo != nil {
		e.Ref = content.RelatesTo.InReplyTo.EventID.String()
	}

	// 解析消息内容为段列表
	e.Segs = m.parseMessageContent(content)

	// 发送到路由器处理
	m.router.Handle(context.Background(), e)
}

// getMemberInfo 获取房间成员的显示信息
// 使用缓存减少 API 调用
// 参数:
//   - userID: 用户 ID
//   - roomID: 房间 ID
//
// 返回:
//   - name: 显示名称
//   - avatar: 头像 URL
func (m *Matrix) getMemberInfo(userID id.UserID, roomID id.RoomID) (name, avatar string) {
	name = userID.String() // 默认使用用户 ID
	cacheKey := "member_" + userID.String()

	// 先查缓存
	if cached, ok := m.cache.Load(cacheKey); ok {
		if memberData, ok := cached.(map[string]string); ok {
			return memberData["name"], memberData["avatar"]
		}
	}

	// 从 Matrix 获取成员信息
	member := m.as.BotIntent().Member(context.Background(), roomID, userID)
	if member != nil {
		slog.Debug("Matrix 成功获取成员信息",
			"user_id", userID,
			"displayname", member.Displayname,
			"avatar_url", member.AvatarURL,
		)

		if member.Displayname != "" {
			name = member.Displayname // 使用显示名称
		}
		if member.AvatarURL != "" {
			avatar = m.mxcToURL(string(member.AvatarURL)) // 转换头像 URL
			slog.Debug("Matrix 转换头像URL",
				"mxc", member.AvatarURL,
				"http_url", avatar,
			)
		}

		// 缓存成员信息
		m.cache.Store(cacheKey, map[string]string{
			"name":   name,
			"avatar": avatar,
		})
	} else {
		slog.Warn("Matrix 获取成员信息失败",
			"user_id", userID,
			"room_id", roomID,
			"使用默认值", name,
		)
	}
	return name, avatar
}

// parseMessageContent 解析 Matrix 消息内容为内部段格式
// 参数:
//   - content: Matrix 消息内容
//
// 返回:
//   - []internal.Seg: 消息段列表
func (m *Matrix) parseMessageContent(content *event.MessageEventContent) []internal.Seg {
	switch content.MsgType {
	case event.MsgText, event.MsgNotice, event.MsgEmote:
		// 文本类消息
		body := stripFallback(content.Body) // 去除回复引用部分
		if content.MsgType == event.MsgEmote {
			body = "* " + body // Emote 消息添加前缀
		}
		return []internal.Seg{{
			Kind: "text",
			Raw:  internal.Props{"txt": body},
		}}

	case event.MsgImage, event.MsgVideo, event.MsgAudio, event.MsgFile:
		// 媒体类消息
		kind := map[event.MessageType]string{
			event.MsgImage: "image",
			event.MsgVideo: "video",
			event.MsgAudio: "audio",
			event.MsgFile:  "file",
		}[content.MsgType]

		url := m.mxcToURL(string(content.URL)) // 转换 MXC URL 为 HTTP URL
		fileName := content.Body
		if content.FileName != "" {
			fileName = content.FileName // 使用文件名（如果有）
		}

		props := internal.Props{"url": url, "name": fileName}
		if content.Info != nil && content.Info.Size > 0 {
			props["size"] = content.Info.Size // 添加文件大小
		}

		return []internal.Seg{{Kind: kind, Raw: props}}

	default:
		// 未知消息类型
		return []internal.Seg{{
			Kind: "text",
			Raw:  internal.Props{"txt": fmt.Sprintf("[Matrix: %s]", content.MsgType)},
		}}
	}
}

// handleRedaction 处理 Matrix 撤回事件
// 转换为内部通知事件
// 参数:
//   - evt: Matrix 撤回事件
func (m *Matrix) handleRedaction(evt *event.Event) {
	e := &internal.Event{
		ID:   evt.ID.String(),
		Kind: internal.Note,
		Time: time.UnixMilli(evt.Timestamp),
		Plat: m.Name(),
		Room: evt.RoomID.String(),
		User: evt.Sender.String(),
		Ref:  evt.Redacts.String(), // 被撤回的消息 ID
		Extra: internal.Props{
			"subtype": internal.Revoke,
		},
	}
	e.Segs = []internal.Seg{{Kind: "text", Raw: internal.Props{"txt": "撤回消息"}}}

	m.router.Handle(context.Background(), e)
}

// stripFallback 去除 Matrix 回复消息的引用部分
// Matrix 回复消息包含被回复消息的引用文本（以 '>' 开头）
// 参数:
//   - s: 原始消息文本
//
// 返回:
//   - string: 去除引用后的文本
func stripFallback(s string) string {
	if len(s) > 0 && s[0] == '>' {
		// 查找引用部分的结束（双换行符）
		if idx := len(s); idx > 0 {
			for i := 0; i < len(s)-1; i++ {
				if s[i] == '\n' && s[i+1] == '\n' {
					if i+2 < len(s) {
						return s[i+2:] // 返回引用后的内容
					}
					return ""
				}
			}
		}
	}
	return s
}

// mxcToURL 将 Matrix MXC URI 转换为 HTTP URL
// 参数:
//   - mxc: MXC URI (mxc://服务器/媒体ID)
//
// 返回:
//   - string: HTTP URL 或原始 MXC（如果格式不正确）
func (m *Matrix) mxcToURL(mxc string) string {
	if len(mxc) > 6 && mxc[:6] == "mxc://" {
		uri, err := id.ParseContentURI(mxc)
		if err != nil {
			slog.Warn("Matrix 解析MXC URI失败",
				"mxc", mxc,
				"error", err,
			)
			return mxc
		}
		// 构建媒体下载 URL
		httpURL := fmt.Sprintf("https://%s/_matrix/media/v3/download/%s/%s", m.cfg.ServerDomain, uri.Homeserver, uri.FileID)
		slog.Debug("Matrix MXC转HTTP URL",
			"mxc", mxc,
			"homeserver", uri.Homeserver,
			"file_id", uri.FileID,
			"http_url", httpURL,
			"server_domain", m.cfg.ServerDomain,
		)
		return httpURL
	}
	slog.Debug("Matrix MXC格式无效，返回原值", "mxc", mxc)
	return mxc
}
