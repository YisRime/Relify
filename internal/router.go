package internal

import (
	"context"
)

// Router 负责消息的分发和转换
type Router struct {
	registry *PlatformRegistry
	store    *Store
	logger   *Logger
}

// NewRouter 创建路由器实例
func NewRouter(reg *PlatformRegistry, store *Store, log *Logger) *Router {
	return &Router{registry: reg, store: store, logger: log}
}

// HandleEvent 是消息处理的入口
func (r *Router) HandleEvent(ctx context.Context, event *Event) error {
	if event == nil || event.Message == nil {
		return nil
	}

	// 在存储层查找与当前房间相关联的所有绑定
	bindings := r.store.GetBindingsByRoom(event.Platform, event.Message.RoomID)
	if len(bindings) == 0 {
		return nil
	}

	// 遍历所有绑定进行分发
	for _, b := range bindings {
		r.route(ctx, event, b)
	}
	return nil
}

// route 处理单个绑定的转发逻辑
func (r *Router) route(ctx context.Context, event *Event, b *RoomBinding) {
	srcMsg := event.Message
	srcPlat := event.Platform

	for _, targetRoom := range b.Rooms {
		// 防止回环：如果目标房间就是源房间，则跳过
		if targetRoom.Platform == srcPlat && targetRoom.RoomID == srcMsg.RoomID {
			continue
		}

		// 从注册表中获取目标平台的适配器
		adapter, ok := r.registry.Get(targetRoom.Platform)
		if !ok {
			continue
		}

		// 深拷贝消息对象，防止多线程环境下适配器修改原始数据造成冲突
		outMsg := r.cloneMessage(srcMsg)

		// 处理引用消息ID的映射 (ReplyTo)
		if outMsg.ReplyToID != "" {
			if tgtID, found := r.store.GetTargetMessageID(srcPlat, outMsg.ReplyToID, targetRoom.Platform); found {
				outMsg.ReplyToID = tgtID
			} else {
				// 如果找不到对应的目标ID，清除引用以避免错误
				outMsg.ReplyToID = ""
			}
		}

		// 处理提及用户 (Mentions) 的ID映射
		if len(outMsg.Mentions) > 0 {
			newMentions := make([]string, 0, len(outMsg.Mentions))
			for _, uid := range outMsg.Mentions {
				if tgtUID, found := r.store.GetTargetUserID(srcPlat, uid, targetRoom.Platform); found {
					newMentions = append(newMentions, tgtUID)
				} else {
					newMentions = append(newMentions, uid)
				}
			}
			outMsg.Mentions = newMentions
		}

		// 异步发送消息，避免阻塞主线程
		go func(tPlat, tRoom string, m *Message) {
			outbound := &OutboundMessage{
				TargetPlatform: tPlat,
				TargetRoomID:   tRoom,
				Message:        m,
			}

			newID, err := adapter.SendMessage(ctx, outbound)
			if err != nil {
				r.logger.Log(ErrorLevel, "router", "send failed", map[string]interface{}{
					"target": tPlat, "err": err.Error(),
				})
				return
			}

			// 发送成功后，保存源消息ID和新消息ID的映射关系
			if newID != "" {
				r.store.SaveMessageMapping(srcPlat, srcMsg.ID, tPlat, newID, b.ID)
			}
		}(targetRoom.Platform, targetRoom.RoomID, outMsg)
	}
}

// cloneMessage 执行消息结构体的深拷贝
func (r *Router) cloneMessage(m *Message) *Message {
	cp := *m // 基础类型浅拷贝

	// 深拷贝 Files 切片
	if m.Files != nil {
		cp.Files = make([]*File, len(m.Files))
		for i, f := range m.Files {
			v := *f
			cp.Files[i] = &v
		}
	}
	// 深拷贝 Embeds 切片
	if m.Embeds != nil {
		cp.Embeds = make([]*Embed, len(m.Embeds))
		for i, e := range m.Embeds {
			v := *e
			cp.Embeds[i] = &v
			// 递归拷贝 Embed 内部的 Fields
			if e.Fields != nil {
				v.Fields = make([]*EmbedField, len(e.Fields))
				for j, f := range e.Fields {
					vf := *f
					v.Fields[j] = &vf
				}
				cp.Embeds[i] = &v
			}
		}
	}
	// Mentions 在 route 方法中会重新构建，此处不拷贝

	return &cp
}
