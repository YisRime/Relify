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

// NewRouter 创建路由引擎
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
	msg := event.Message

	r.logger.Debug("router", "Received message", map[string]interface{}{
		"driver":  msg.SourceDriver,
		"room_id": msg.SourceRoomID,
		"msg_id":  msg.SourceMsgID,
		"type":    msg.Type,
	})

	// 生成消息指纹（简单拼接，不加密）
	if msg.Fingerprint == "" {
		msg.Fingerprint = r.generateFingerprint(msg)
	}

	// 查找该房间的绑定关系
	bindings := r.routeStore.GetBindingsByRoom(msg.SourceDriver, msg.SourceRoomID)
	if len(bindings) == 0 {
		r.logger.Debug("router", "No bindings found for room", map[string]interface{}{
			"driver":  msg.SourceDriver,
			"room_id": msg.SourceRoomID,
		})
		// 无绑定关系，直接返回（不处理）
		return nil
	}

	r.logger.Info("router", "Routing message", map[string]interface{}{
		"fingerprint": msg.Fingerprint,
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
	// 为该绑定内的每个目标房间分发消息
	for _, targetRoom := range binding.Rooms {
		// 跳过来源房间（避免回声）
		if targetRoom.Driver == msg.SourceDriver && targetRoom.RoomID == msg.SourceRoomID {
			continue
		}

		// ID 翻译：处理引用/回复
		translatedMsg := r.translateMessage(msg, targetRoom.Driver, binding)

		// 构造出站消息
		outbound := &model.OutboundMessage{
			TargetDriver: targetRoom.Driver,
			TargetRoomID: targetRoom.RoomID,
			Message:      translatedMsg,
		}

		// 获取目标驱动
		targetDriver, exists := r.driverRegistry.Get(targetRoom.Driver)
		if !exists {
			r.logger.Error("router", "Target driver not found", map[string]interface{}{
				"driver": targetRoom.Driver,
			})
			continue
		}

		r.logger.Debug("router", "Sending message to target", map[string]interface{}{
			"target_driver": targetRoom.Driver,
			"target_room":   targetRoom.RoomID,
		})

		// 异步发送消息（在独立 Goroutine 中执行）
		go r.sendMessageAsync(ctx, targetDriver, outbound, msg)
	}
}

// translateMessage ID 翻译：将引用 ID 转换为目标平台的消息 ID
func (r *Router) translateMessage(msg *model.Message, targetDriver string, binding *model.RoomBinding) *model.Message {
	// 复制消息对象，避免修改原始消息
	translated := *msg

	// 处理回复/引用消息
	if msg.RefSourceID != "" {
		// 查询 ID 映射表：找到引用消息在目标平台的 ID
		targetMsgID, found := r.messageMapStore.GetTargetID(
			msg.SourceDriver,
			msg.RefSourceID,
			targetDriver,
		)

		if found {
			// 找到映射，填充目标引用 ID
			translated.RefTargetID = targetMsgID
			r.logger.Debug("router", "Translated reply reference", map[string]interface{}{
				"source_id": msg.RefSourceID,
				"target_id": targetMsgID,
			})
		} else {
			// 未找到映射，降级为普通消息（清空引用字段）
			translated.RefSourceID = ""
			translated.RefTargetID = ""
			r.logger.Warn("router", "Reply reference not found in mapping", map[string]interface{}{
				"source_id": msg.RefSourceID,
			})
		}
	}

	// 处理编辑消息
	if msg.Type == model.MsgTypeEdit && msg.EditTargetID != "" {
		// 查询要编辑的消息在目标平台的 ID
		targetMsgID, found := r.messageMapStore.GetTargetID(
			msg.SourceDriver,
			msg.EditTargetID,
			targetDriver,
		)

		if found {
			translated.EditTargetID = targetMsgID
		} else {
			// 无法编辑，转为普通消息
			translated.Type = model.MsgTypeText
			translated.EditTargetID = ""
			r.logger.Warn("router", "Edit target not found, downgrading to text", map[string]interface{}{
				"edit_target_id": msg.EditTargetID,
			})
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

		// 查询用户 ID 映射
		targetUserID, found := r.userMapStore.GetTargetUserID(
			sourceDriver,
			mention.UserID,
			targetDriver,
		)

		if found {
			translated[i].TargetID = targetUserID
			r.logger.Debug("router", "Translated mention", map[string]interface{}{
				"source_user_id": mention.UserID,
				"target_user_id": targetUserID,
				"display_name":   mention.DisplayName,
			})
		} else {
			// 未找到映射，保留原始信息，驱动可以选择如何处理
			r.logger.Debug("router", "Mention mapping not found", map[string]interface{}{
				"source_user_id": mention.UserID,
				"display_name":   mention.DisplayName,
			})
		}
	}

	return translated
}

// sendMessageAsync 异步发送消息（全链路异步）
func (r *Router) sendMessageAsync(ctx context.Context, targetDriver driver.Driver, outbound *model.OutboundMessage, originalMsg *model.Message) {
	// 定义回调：记录 ID 映射
	callback := func(result *model.MessageSendResult) {
		if result.Success && result.TargetMsgID != "" {
			r.logger.Debug("router", "Message sent successfully", map[string]interface{}{
				"target_driver": result.TargetDriver,
				"target_msg_id": result.TargetMsgID,
			})

			// 异步写入 ID 映射
			mapping := &storage.MessageMapping{
				SourceDriver: originalMsg.SourceDriver,
				SourceMsgID:  originalMsg.SourceMsgID,
				TargetDriver: result.TargetDriver,
				TargetMsgID:  result.TargetMsgID,
				CreatedAt:    time.Now(),
			}

			// 在新的 Goroutine 中写入数据库（避免阻塞）
			go func() {
				if err := r.messageMapStore.Save(mapping); err != nil {
					r.logger.Error("router", "Failed to save message mapping", map[string]interface{}{
						"error": err.Error(),
					})
				} else {
					r.logger.Debug("router", "Message mapping saved", map[string]interface{}{
						"source_driver": mapping.SourceDriver,
						"source_msg_id": mapping.SourceMsgID,
						"target_driver": mapping.TargetDriver,
						"target_msg_id": mapping.TargetMsgID,
					})
				}
			}()
		} else {
			r.logger.Error("router", "Message send failed", map[string]interface{}{
				"target_driver": result.TargetDriver,
				"error":         result.Error,
			})
		}
	}

	// 调用驱动发送消息
	if err := targetDriver.SendMessage(ctx, outbound, callback); err != nil {
		r.logger.Error("router", "Failed to submit message", map[string]interface{}{
			"target_driver": targetDriver.Name(),
			"error":         err.Error(),
		})
	}
}

// generateFingerprint 生成消息指纹（简单拼接，不加密）
func (r *Router) generateFingerprint(msg *model.Message) string {
	// 简单拼接：驱动:房间:消息ID:时间戳纳秒
	return fmt.Sprintf("%s:%s:%s:%d",
		msg.SourceDriver,
		msg.SourceRoomID,
		msg.SourceMsgID,
		msg.Timestamp.UnixNano(),
	)
}
