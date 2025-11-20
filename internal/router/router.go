// Package router 提供消息路由引擎
package router

import (
	"context"
	"fmt"
	"sync"
	"time"

	"Relify/internal/config"
	"Relify/internal/logger"
	"Relify/internal/model"
	"Relify/internal/storage"
)

// Router 路由引擎
type Router struct {
	platformRegistry *model.PlatformRegistry
	routeStore       *storage.RouteStore
	messageMapStore  *storage.MessageMapStore
	userMapStore     *storage.UserMapStore
	logger           *logger.Logger
	mode             string
	hubPlatform      string
	platformConfigs  map[string]config.RouteType
	mu               sync.RWMutex
}

// NewRouter 创建路由引擎
func NewRouter(
	platformRegistry *model.PlatformRegistry,
	routeStore *storage.RouteStore,
	messageMapStore *storage.MessageMapStore,
	userMapStore *storage.UserMapStore,
	mode string,
	hubPlatform string,
	platformConfigs map[string]config.RouteType,
	log *logger.Logger,
) *Router {
	return &Router{
		platformRegistry: platformRegistry,
		routeStore:       routeStore,
		messageMapStore:  messageMapStore,
		userMapStore:     userMapStore,
		logger:           log,
		mode:             mode,
		hubPlatform:      hubPlatform,
		platformConfigs:  platformConfigs,
	}
}

// UpdatePlatformRouteType 更新平台路由类型
func (r *Router) UpdatePlatformRouteType(platformName string, routeType config.RouteType) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.platformConfigs[platformName] = routeType
	r.logger.Info("router", "Platform route type updated", map[string]interface{}{
		"platform":   platformName,
		"route_type": routeType,
	})
}

// HandleMessage 处理入站消息
func (r *Router) HandleMessage(ctx context.Context, event *model.MessageEvent) error {
	if event == nil || event.Message == nil {
		return fmt.Errorf("invalid message event")
	}

	msg := event.Message
	if msg.SourcePlatform == "" || msg.SourceRoomID == "" || msg.SourceMsgID == "" {
		return fmt.Errorf("missing required fields")
	}

	if msg.Fingerprint == "" {
		msg.Fingerprint = fmt.Sprintf("%s:%s:%s:%d", msg.SourcePlatform, msg.SourceRoomID, msg.SourceMsgID, msg.Timestamp.UnixNano())
	}

	bindings := r.routeStore.GetBindingsByRoom(msg.SourcePlatform, msg.SourceRoomID)
	if len(bindings) == 0 {
		return nil
	}

	r.logger.Info("router", "Routing message", map[string]interface{}{
		"fingerprint": msg.Fingerprint,
		"platform":    msg.SourcePlatform,
		"room_id":     msg.SourceRoomID,
		"bindings":    len(bindings),
	})

	for _, binding := range bindings {
		r.routeToBinding(ctx, msg, binding)
	}

	return nil
}

// routeToBinding 路由到绑定的所有目标房间
func (r *Router) routeToBinding(ctx context.Context, msg *model.Message, binding *model.RoomBinding) {
	for _, targetRoom := range binding.Rooms {
		if targetRoom.Platform == msg.SourcePlatform && targetRoom.RoomID == msg.SourceRoomID {
			continue
		}

		targetPlatform, exists := r.platformRegistry.Get(targetRoom.Platform)
		if !exists {
			r.logger.Error("router", "Target platform not found", map[string]interface{}{
				"platform": targetRoom.Platform,
			})
			continue
		}

		outbound := &model.OutboundMessage{
			TargetPlatform: targetRoom.Platform,
			TargetRoomID:   targetRoom.RoomID,
			Message:        r.translateMessage(msg, targetRoom.Platform, binding),
		}

		go r.sendMessageAsync(ctx, targetPlatform, outbound, msg)
	}
}

// translateMessage 翻译消息中的 ID 引用
func (r *Router) translateMessage(msg *model.Message, targetPlatform string, binding *model.RoomBinding) *model.Message {
	translated := *msg

	if msg.RefSourceID != "" {
		if targetMsgID, found := r.messageMapStore.GetTargetID(msg.SourcePlatform, msg.RefSourceID, targetPlatform); found {
			translated.RefTargetID = targetMsgID
		} else {
			translated.RefSourceID = ""
			translated.RefTargetID = ""
			r.logger.Debug("router", "Referenced message mapping not found", map[string]interface{}{
				"source_id": msg.RefSourceID,
			})
		}
	}

	if msg.Type == model.MsgTypeEdit && msg.EditTargetID != "" {
		if targetMsgID, found := r.messageMapStore.GetTargetID(msg.SourcePlatform, msg.EditTargetID, targetPlatform); found {
			translated.EditTargetID = targetMsgID
		} else {
			translated.Type = model.MsgTypeText
			translated.EditTargetID = ""
		}
	}

	if len(msg.Mentions) > 0 {
		translated.Mentions = r.translateMentions(msg.Mentions, msg.SourcePlatform, targetPlatform)
	}

	return &translated
}

// translateMentions 翻译提及信息
func (r *Router) translateMentions(mentions []model.Mention, sourcePlatform, targetPlatform string) []model.Mention {
	translated := make([]model.Mention, len(mentions))
	for i, mention := range mentions {
		translated[i] = mention
		if targetUserID, found := r.userMapStore.GetTargetUserID(sourcePlatform, mention.UserID, targetPlatform); found {
			translated[i].TargetID = targetUserID
		}
	}
	return translated
}

// sendMessageAsync 异步发送消息
func (r *Router) sendMessageAsync(ctx context.Context, targetPlatform model.Platform, outbound *model.OutboundMessage, originalMsg *model.Message) {
	callback := func(result *model.MessageSendResult) {
		if !result.Success || result.TargetMsgID == "" {
			r.logger.Error("router", "Message sending failed", map[string]interface{}{
				"target_platform": result.TargetPlatform,
				"error":           result.Error,
			})
			return
		}

		go func() {
			mapping := &storage.MessageMapping{
				SourcePlatform: originalMsg.SourcePlatform,
				SourceMsgID:    originalMsg.SourceMsgID,
				TargetPlatform: result.TargetPlatform,
				TargetMsgID:    result.TargetMsgID,
				CreatedAt:      time.Now(),
			}
			if err := r.messageMapStore.Save(mapping); err != nil {
				r.logger.Error("router", "Failed to save message mapping", map[string]interface{}{"error": err.Error()})
			}
		}()
	}

	if err := targetPlatform.SendMessage(ctx, outbound, callback); err != nil {
		r.logger.Error("router", "Failed to submit message", map[string]interface{}{
			"platform": targetPlatform.Name(),
			"error":    err.Error(),
		})
	}
}
