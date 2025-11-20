package internal

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	maxRetries = 3                      // 最大重试次数
	retryDelay = 500 * time.Millisecond // 初始重试延迟
)

// Router 路由引擎
type Router struct {
	platformRegistry *PlatformRegistry
	store            *Store
	logger           *Logger
	mode             string
	hubPlatform      string

	// 存储平台的路由属性 (Mirror/Aggregate)
	platformTypes map[string]RouteType
	mu            sync.RWMutex
}

// NewRouter 创建路由引擎
func NewRouter(
	platformRegistry *PlatformRegistry,
	store *Store,
	mode string,
	hubPlatform string,
	log *Logger,
) *Router {
	return &Router{
		platformRegistry: platformRegistry,
		store:            store,
		logger:           log,
		mode:             mode,
		hubPlatform:      hubPlatform,
		platformTypes:    make(map[string]RouteType),
	}
}

// RegisterPlatformType 注册平台的路由类型 (由 Core 调用)
func (r *Router) RegisterPlatformType(platformName string, routeType RouteType) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.platformTypes[platformName] = routeType
	r.logger.Debug("router", "Registered platform type", map[string]interface{}{
		"platform":   platformName,
		"route_type": routeType,
	})
}

// getPlatformType 获取平台的路由类型 (线程安全)
func (r *Router) getPlatformType(platformName string) RouteType {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if t, ok := r.platformTypes[platformName]; ok {
		return t
	}
	// 默认为聚合模式 (最安全，不会尝试伪造身份)
	return RouteTypeAggregate
}

// HandleEvent 处理入站事件 (实现 InboundHandler 接口)
func (r *Router) HandleEvent(ctx context.Context, event *Event) error {
	if event == nil {
		return fmt.Errorf("nil event")
	}

	// 仅处理消息类事件
	switch event.Type {
	case MsgTypeText, MsgTypeImage, MsgTypeAudio,
		MsgTypeVideo, MsgTypeFile, MsgTypeSticker,
		MsgTypeRich:
		// Proceed
	default:
		return nil
	}

	msg := event.Message
	if msg == nil || event.Platform == "" || msg.RoomID == "" || msg.ID == "" {
		return fmt.Errorf("invalid event or message")
	}

	fingerprint := fmt.Sprintf("%s:%s:%s", event.Platform, msg.RoomID, msg.ID)

	// 查找绑定
	bindings := r.store.GetBindingsByRoom(event.Platform, msg.RoomID)
	if len(bindings) == 0 {
		return nil
	}

	r.logger.Info("router", "Routing message", map[string]interface{}{
		"fingerprint": fingerprint,
		"bindings":    len(bindings),
	})

	for _, binding := range bindings {
		r.routeToBinding(ctx, event, binding)
	}

	return nil
}

// routeToBinding 路由到绑定的所有目标房间
func (r *Router) routeToBinding(ctx context.Context, event *Event, binding *RoomBinding) {
	msg := event.Message
	sourcePlatform := event.Platform

	for _, targetRoom := range binding.Rooms {
		// 1. 跳过来源房间 (防止回声)
		if targetRoom.Platform == sourcePlatform && targetRoom.RoomID == msg.RoomID {
			continue
		}

		// 2. 获取目标平台适配器
		targetPlatformAdapter, exists := r.platformRegistry.Get(targetRoom.Platform)
		if !exists {
			r.logger.Error("router", "Target platform not found", map[string]interface{}{
				"platform": targetRoom.Platform,
			})
			continue
		}

		// 3. 获取目标平台路由类型 (Mirror 或 Aggregate)
		targetType := r.getPlatformType(targetRoom.Platform)

		// 4. 消息转换 (深拷贝 + 格式化 + ID映射)
		translatedMsg := r.translateMessage(msg, sourcePlatform, targetRoom.Platform, targetType)

		// 5. 构建出站消息
		outbound := &OutboundMessage{
			TargetPlatform: targetRoom.Platform,
			TargetRoomID:   targetRoom.RoomID,
			Message:        translatedMsg,
		}

		// 6. 异步发送 (带重试)
		go r.processSend(ctx, targetPlatformAdapter, outbound, sourcePlatform, msg.ID, binding.ID)
	}
}

// translateMessage 转换消息：ID映射、内容清洗、DeepCopy、聚合模式前缀处理
func (r *Router) translateMessage(msg *Message, sourcePlatform, targetPlatform string, targetType RouteType) *Message {
	// 1. 深拷贝 (Deep Copy)
	translated := r.deepCopyMessage(msg)

	// 2. 收集用户映射信息 (Map: SourceUserID -> TargetUserID or Name)
	// 用于后续替换文本中的 Mention
	userMapping := make(map[string]string)

	// 处理 Mentions 列表
	if len(translated.Mentions) > 0 {
		var mappedMentions []string
		for _, sourceUserID := range translated.Mentions {
			// 尝试查询数据库映射
			targetUserID, found := r.store.GetTargetUserID(sourcePlatform, sourceUserID, targetPlatform)

			if found {
				mappedMentions = append(mappedMentions, targetUserID)
				userMapping[sourceUserID] = targetUserID
			} else {
				// 如果找不到 ID 映射，尝试获取用户的 DisplayName (如果 Store 里有缓存)
				// 或者保留原 ID。为了用户体验，我们尽量不直接显示 ID。
				// 这里简化处理，保留 ID，但在文本替换时可以考虑更智能的策略
				mappedMentions = append(mappedMentions, sourceUserID)

				// 尝试查询源平台用户信息以获取名字用于替换文本 (Store 需要支持 query user info，这里假设没有，仅用原ID)
				userMapping[sourceUserID] = sourceUserID
			}
		}
		translated.Mentions = mappedMentions
	}

	// 3. 处理正文内容 (Content)

	// 3.1 替换正文中的 Mention (简单的字符串替换，适配器层可能需要更复杂的 Regex)
	// 假设 content 中包含源平台的 ID (e.g. "Hello <@123>"), 我们尝试替换为 "Hello <@456>" 或 "Hello @Name"
	for srcID, targetVal := range userMapping {
		// 注意：这是一种非常粗糙的替换，容易误伤数字。
		// 实际生产中，Adapter 应该在 HandleEvent 时将 Mentions 转换为一种中间格式 (e.g. Relify Mentions)
		// 这里做简单演示：仅当 ID 较长时才替换，或者依赖 Adapter 的特定格式
		if len(srcID) > 5 {
			translated.Content = strings.ReplaceAll(translated.Content, srcID, targetVal)
		}
	}

	// 3.2 根据路由类型调整格式
	if targetType == RouteTypeAggregate {
		// 聚合模式：目标平台无法伪造发送者，需要在正文中显式包含发送者名字
		// 格式: "**DisplayName**: Content"
		prefix := fmt.Sprintf("**%s**: ", msg.SenderName)
		translated.Content = prefix + translated.Content

		// 清空 Sender 信息，防止适配器混淆
		translated.SenderID = ""
		translated.SenderName = ""
		translated.SenderAvatar = ""
	}

	// 4. 处理回复 (ReplyToID)
	if translated.ReplyToID != "" {
		if targetMsgID, found := r.store.GetTargetMessageID(sourcePlatform, translated.ReplyToID, targetPlatform); found {
			translated.ReplyToID = targetMsgID
		} else {
			// 引用丢失时的兜底：在聚合模式下，可以引用文本提示
			if targetType == RouteTypeAggregate {
				translated.Content = "[Reply] " + translated.Content
			}
			translated.ReplyToID = ""
		}
	}

	// 5. 处理 Embeds
	if len(translated.Embeds) > 0 {
		for _, embed := range translated.Embeds {
			r.processEmbed(embed, userMapping)
		}
	}

	return translated
}

// processEmbed 处理 Embed 内的文本替换
func (r *Router) processEmbed(embed *Embed, userMapping map[string]string) {
	if embed == nil {
		return
	}

	// 简单的文本替换函数
	replace := func(s string) string {
		res := s
		for src, dst := range userMapping {
			if len(src) > 5 {
				res = strings.ReplaceAll(res, src, dst)
			}
		}
		return res
	}

	embed.Title = replace(embed.Title)
	embed.Description = replace(embed.Description)

	for _, field := range embed.Fields {
		if field != nil {
			field.Name = replace(field.Name)
			field.Value = replace(field.Value)
		}
	}

	if embed.Footer != nil {
		embed.Footer.Text = replace(embed.Footer.Text)
	}
}

// deepCopyMessage 执行消息的深拷贝 (保持不变)
func (r *Router) deepCopyMessage(msg *Message) *Message {
	if msg == nil {
		return nil
	}

	cp := *msg

	if msg.Files != nil {
		cp.Files = make([]*File, len(msg.Files))
		for i, v := range msg.Files {
			if v != nil {
				f := *v
				cp.Files[i] = &f
			}
		}
	}

	if msg.Embeds != nil {
		cp.Embeds = make([]*Embed, len(msg.Embeds))
		for i, v := range msg.Embeds {
			if v != nil {
				e := r.deepCopyEmbed(v)
				cp.Embeds[i] = e
			}
		}
	}

	if msg.Mentions != nil {
		cp.Mentions = make([]string, len(msg.Mentions))
		copy(cp.Mentions, msg.Mentions)
	}

	if msg.Extra != nil {
		cp.Extra = make(map[string]interface{})
		for k, v := range msg.Extra {
			cp.Extra[k] = v
		}
	}

	return &cp
}

// deepCopyEmbed Embed 深拷贝辅助 (保持不变)
func (r *Router) deepCopyEmbed(src *Embed) *Embed {
	if src == nil {
		return nil
	}
	dst := *src

	if src.Footer != nil {
		f := *src.Footer
		dst.Footer = &f
	}
	if src.Image != nil {
		f := *src.Image
		dst.Image = &f
	}
	if src.Thumbnail != nil {
		f := *src.Thumbnail
		dst.Thumbnail = &f
	}

	if src.Fields != nil {
		dst.Fields = make([]*EmbedField, len(src.Fields))
		for i, v := range src.Fields {
			if v != nil {
				f := *v
				dst.Fields[i] = &f
			}
		}
	}

	return &dst
}

// processSend 发送并保存映射 (增加重试机制)
func (r *Router) processSend(
	ctx context.Context,
	targetPlatform Platform,
	outbound *OutboundMessage,
	sourcePlatform string,
	sourceMsgID string,
	bindingID string,
) {
	var targetMsgID string
	var err error

	// 重试循环
	for i := 0; i <= maxRetries; i++ {
		// 检查 Context 是否已取消
		if ctx.Err() != nil {
			r.logger.Warn("router", "Message sending cancelled", map[string]interface{}{
				"target": outbound.TargetPlatform,
			})
			return
		}

		targetMsgID, err = targetPlatform.SendMessage(ctx, outbound)
		if err == nil {
			break // 发送成功
		}

		// 最后一次尝试失败，不再重试
		if i == maxRetries {
			r.logger.Error("router", "Failed to send message after retries", map[string]interface{}{
				"target_platform": outbound.TargetPlatform,
				"target_room":     outbound.TargetRoomID,
				"retries":         maxRetries,
				"error":           err.Error(),
			})
			return
		}

		// 等待后重试 (简单的指数退避)
		delay := retryDelay * time.Duration(1<<i)
		r.logger.Warn("router", "Failed to send message, retrying...", map[string]interface{}{
			"target":  outbound.TargetPlatform,
			"attempt": i + 1,
			"error":   err.Error(),
			"delay":   delay.String(),
		})

		select {
		case <-time.After(delay):
			continue
		case <-ctx.Done():
			return
		}
	}

	// 发送成功，保存 ID 映射关系
	if targetMsgID != "" {
		err = r.store.SaveMessageMapping(
			sourcePlatform,
			sourceMsgID,
			outbound.TargetPlatform,
			targetMsgID,
			bindingID,
		)
		if err != nil {
			r.logger.Warn("router", "Failed to save msg mapping", map[string]interface{}{"error": err.Error()})
		} else {
			r.logger.Debug("router", "Message sent and mapped", map[string]interface{}{
				"source": fmt.Sprintf("%s/%s", sourcePlatform, sourceMsgID),
				"target": fmt.Sprintf("%s/%s", outbound.TargetPlatform, targetMsgID),
			})
		}
	}
}
