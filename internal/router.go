package internal

import (
	"context"
)

type Router struct {
	registry *PlatformRegistry
	store    *Store
	logger   *Logger
}

func NewRouter(reg *PlatformRegistry, store *Store, log *Logger) *Router {
	return &Router{registry: reg, store: store, logger: log}
}

func (r *Router) HandleEvent(ctx context.Context, event *Event) error {
	if event == nil || event.Message == nil {
		return nil
	}

	// 1. 查找绑定
	bindings := r.store.GetBindingsByRoom(event.Platform, event.Message.RoomID)
	if len(bindings) == 0 {
		return nil
	}

	// 2. 分发
	for _, b := range bindings {
		r.route(ctx, event, b)
	}
	return nil
}

func (r *Router) route(ctx context.Context, event *Event, b *RoomBinding) {
	srcMsg := event.Message
	srcPlat := event.Platform

	for _, targetRoom := range b.Rooms {
		// 跳过回环
		if targetRoom.Platform == srcPlat && targetRoom.RoomID == srcMsg.RoomID {
			continue
		}

		// 获取适配器
		adapter, ok := r.registry.Get(targetRoom.Platform)
		if !ok {
			continue
		}

		// 3. 消息克隆 (Deep Copy) - 确保并发安全
		// Adapter 可能会修改 Embed 内容或文件列表，必须传副本
		outMsg := r.cloneMessage(srcMsg)

		// 4. ID 映射 (Reply & Mentions)
		if outMsg.ReplyToID != "" {
			if tgtID, found := r.store.GetTargetMessageID(srcPlat, outMsg.ReplyToID, targetRoom.Platform); found {
				outMsg.ReplyToID = tgtID
			} else {
				outMsg.ReplyToID = "" // 引用断开
			}
		}

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

		// 5. 异步发送
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

			if newID != "" {
				r.store.SaveMessageMapping(srcPlat, srcMsg.ID, tPlat, newID, b.ID)
			}
		}(targetRoom.Platform, targetRoom.RoomID, outMsg)
	}
}

// cloneMessage 执行深拷贝，防止 Adapter 间的数据竞争
func (r *Router) cloneMessage(m *Message) *Message {
	cp := *m // 浅拷贝结构体

	// 深拷贝 slice
	if m.Files != nil {
		cp.Files = make([]*File, len(m.Files))
		for i, f := range m.Files {
			v := *f
			cp.Files[i] = &v
		}
	}
	if m.Embeds != nil {
		cp.Embeds = make([]*Embed, len(m.Embeds))
		for i, e := range m.Embeds {
			v := *e
			cp.Embeds[i] = &v
			// Fields 也要拷贝
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
	// Mentions 在 route 中会重新分配，这里不需要拷

	return &cp
}
