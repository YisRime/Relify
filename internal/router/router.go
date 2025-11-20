// Package router 提供消息路由引擎
// 负责消息分发、ID 翻译、提及转换等核心路由逻辑
package router

import (
	"context"
	"fmt"
	"time"

	"Relify/internal/driver"
	"Relify/internal/logger"
	"Relify/internal/model"
	"Relify/internal/storage"
)

// Router 路由引擎（核心层主逻辑）
type Router struct {
	driverRegistry  *driver.Registry
	routeStore      *storage.RouteStore
	messageMapStore *storage.MessageMapStore
	userMapStore    *storage.UserMapStore
	logger          *logger.Logger
}

// NewRouter 创建路由引擎实例
// 参数：
//   - driverRegistry: 驱动注册表
//   - routeStore: 路由存储
//   - messageMapStore: 消息映射存储
//   - userMapStore: 用户映射存储
//   - log: 日志记录器
//
// 返回：
//   - *Router: 路由引擎实例
func NewRouter(
	driverRegistry *driver.Registry,
	routeStore *storage.RouteStore,
	messageMapStore *storage.MessageMapStore,
	userMapStore *storage.UserMapStore,
	log *logger.Logger,
) *Router {
	return &Router{
		driverRegistry:  driverRegistry,
		routeStore:      routeStore,
		messageMapStore: messageMapStore,
		userMapStore:    userMapStore,
		logger:          log,
	}
}

// HandleMessage 处理入站消息（实现 InboundHandler 接口）
func (r *Router) HandleMessage(ctx context.Context, event *model.MessageEvent) error {
	if event == nil || event.Message == nil {
		return fmt.Errorf("invalid message event: event or message is nil")
	}

	msg := event.Message

	// 验证必要字段
	if msg.SourceDriver == "" || msg.SourceRoomID == "" || msg.SourceMsgID == "" {
		return fmt.Errorf("missing required fields: source_driver, source_room_id, or source_msg_id")
	}

	// 生成消息指纹
	if msg.Fingerprint == "" {
		msg.Fingerprint = r.generateFingerprint(msg)
	}

	// 查找该房间的绑定关系
	bindings := r.routeStore.GetBindingsByRoom(msg.SourceDriver, msg.SourceRoomID)
	if len(bindings) == 0 {
		return nil // 无绑定关系，静默忽略
	}

	r.logger.Info("router", "Routing message", map[string]interface{}{
		"fingerprint": msg.Fingerprint,
		"driver":      msg.SourceDriver,
		"room_id":     msg.SourceRoomID,
		"bindings":    len(bindings),
	})

	// 对每个绑定执行路由分发
	for _, binding := range bindings {
		r.routeToBinding(ctx, msg, binding)
	}

	// 即发即弃：立即返回，不等待下游结果
	return nil
}

// routeToBinding 将消息路由到绑定的所有目标房间
func (r *Router) routeToBinding(ctx context.Context, msg *model.Message, binding *model.RoomBinding) {
	for _, targetRoom := range binding.Rooms {
		// 跳过来源房间（避免回声）
		if targetRoom.Driver == msg.SourceDriver && targetRoom.RoomID == msg.SourceRoomID {
			continue
		}

		targetDriver, exists := r.driverRegistry.Get(targetRoom.Driver)
		if !exists {
			r.logger.Error("router", "Target driver not found", map[string]interface{}{
				"driver": targetRoom.Driver,
			})
			continue
		}

		// ID 翻译并构造出站消息
		outbound := &model.OutboundMessage{
			TargetDriver: targetRoom.Driver,
			TargetRoomID: targetRoom.RoomID,
			Message:      r.translateMessage(msg, targetRoom.Driver, binding),
		}

		// 异步发送消息
		go r.sendMessageAsync(ctx, targetDriver, outbound, msg)
	}
}

// translateMessage 翻译消息中的 ID 引用
func (r *Router) translateMessage(msg *model.Message, targetDriver string, binding *model.RoomBinding) *model.Message {
	translated := *msg

	// 处理回复/引用消息
	if msg.RefSourceID != "" {
		if targetMsgID, found := r.messageMapStore.GetTargetID(msg.SourceDriver, msg.RefSourceID, targetDriver); found {
			translated.RefTargetID = targetMsgID
		} else {
			// 未找到映射，降级为普通消息
			translated.RefSourceID = ""
			translated.RefTargetID = ""
			r.logger.Debug("router", "Reply reference not found, cleared", map[string]interface{}{
				"source_id": msg.RefSourceID,
			})
		}
	}

	// 处理编辑消息
	if msg.Type == model.MsgTypeEdit && msg.EditTargetID != "" {
		if targetMsgID, found := r.messageMapStore.GetTargetID(msg.SourceDriver, msg.EditTargetID, targetDriver); found {
			translated.EditTargetID = targetMsgID
		} else {
			// 无法编辑，转为普通消息
			translated.Type = model.MsgTypeText
			translated.EditTargetID = ""
		}
	}

	// 处理提及转换
	if len(msg.Mentions) > 0 {
		translated.Mentions = r.translateMentions(msg.Mentions, msg.SourceDriver, targetDriver)
	}

	return &translated
}

// translateMentions 翻译提及信息
func (r *Router) translateMentions(mentions []model.Mention, sourceDriver, targetDriver string) []model.Mention {
	translated := make([]model.Mention, len(mentions))
	for i, mention := range mentions {
		translated[i] = mention
		if targetUserID, found := r.userMapStore.GetTargetUserID(sourceDriver, mention.UserID, targetDriver); found {
			translated[i].TargetID = targetUserID
		}
	}
	return translated
}

// sendMessageAsync 异步发送消息到目标平台
func (r *Router) sendMessageAsync(ctx context.Context, targetDriver driver.Driver, outbound *model.OutboundMessage, originalMsg *model.Message) {
	callback := func(result *model.MessageSendResult) {
		if !result.Success || result.TargetMsgID == "" {
			r.logger.Error("router", "Message send failed", map[string]interface{}{
				"target_driver": result.TargetDriver,
				"error":         result.Error,
			})
			return
		}

		// 异步写入 ID 映射
		go func() {
			mapping := &storage.MessageMapping{
				SourceDriver: originalMsg.SourceDriver,
				SourceMsgID:  originalMsg.SourceMsgID,
				TargetDriver: result.TargetDriver,
				TargetMsgID:  result.TargetMsgID,
				CreatedAt:    time.Now(),
			}
			if err := r.messageMapStore.Save(mapping); err != nil {
				r.logger.Error("router", "Failed to save message mapping", map[string]interface{}{"error": err.Error()})
			}
		}()
	}

	if err := targetDriver.SendMessage(ctx, outbound, callback); err != nil {
		r.logger.Error("router", "Failed to submit message", map[string]interface{}{
			"driver": targetDriver.Name(),
			"error":  err.Error(),
		})
	}
}

// generateFingerprint 生成消息指纹（简单拼接，无加密）
func (r *Router) generateFingerprint(msg *model.Message) string {
	return fmt.Sprintf("%s:%s:%s:%d", msg.SourceDriver, msg.SourceRoomID, msg.SourceMsgID, msg.Timestamp.UnixNano())
}
