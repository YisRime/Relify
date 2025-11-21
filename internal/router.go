package internal

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

type Router struct {
	cfg      *Config
	registry *PlatformRegistry
	store    *Store
}

func NewRouter(cfg *Config, reg *PlatformRegistry, store *Store) *Router {
	return &Router{
		cfg:      cfg,
		registry: reg,
		store:    store,
	}
}

func (r *Router) HandleEvent(ctx context.Context, event *Event) error {
	if event == nil || event.Message == nil {
		return nil
	}

	if p, ok := r.registry.Get(event.Platform); ok && event.Message.SenderID == p.GetBotUserID() {
		return nil
	}
	if r.store.IsEventEcho(event.Platform, event.Message.ID) {
		return nil
	}

	r.store.UpdateUserCache(event.Platform, event.Message.SenderID, event.Message.SenderName, event.Message.SenderAvatar)

	bindings := r.store.GetBindingsByRoom(event.Platform, event.Message.RoomID)
	if len(bindings) == 0 {
		var err error
		bindings, err = r.resolveBinding(ctx, event)
		if err != nil {
			slog.Warn("auto-binding failed", "err", err, "plat", event.Platform)
			return nil
		}
	}

	if len(bindings) == 0 {
		return nil
	}

	var wg sync.WaitGroup
	for _, b := range bindings {
		for _, targetRoom := range b.Rooms {
			if targetRoom.Platform == event.Platform && targetRoom.RoomID == event.Message.RoomID {
				continue
			}

			adapter, ok := r.registry.Get(targetRoom.Platform)
			if !ok {
				continue
			}

			wg.Add(1)
			go func(tr BoundRoom, bid string) {
				defer wg.Done()
				r.dispatch(ctx, adapter, event, tr, bid)
			}(targetRoom, b.ID)
		}
	}
	wg.Wait()
	return nil
}

func (r *Router) resolveBinding(ctx context.Context, event *Event) ([]*RoomBinding, error) {
	// Mode 1: Hub (Star Topology)
	// If event is from a Spoke, create a mirror room on the Hub.
	// If event is from the Hub, we don't auto-bind (Hub doesn't know where to send).
	if r.cfg.Mode == "hub" {
		hubPlat := r.cfg.HubPlatform
		if event.Platform == hubPlat {
			return nil, nil
		}

		adapter, ok := r.registry.Get(hubPlat)
		if !ok {
			return nil, fmt.Errorf("hub platform %s offline", hubPlat)
		}

		// Generate room name: e.g. "telegram-12345678"
		newName := fmt.Sprintf("%s-%s", event.Platform, event.Message.RoomID)

		// Strictly require creation. No fallback.
		targetID, err := adapter.CreateRoom(ctx, newName)
		if err != nil {
			return nil, fmt.Errorf("hub mirror create failed: %w", err)
		}

		rooms := []BoundRoom{
			{Platform: event.Platform, RoomID: event.Message.RoomID},
			{Platform: hubPlat, RoomID: targetID},
		}
		b, err := r.store.CreateDynamicBinding(fmt.Sprintf("Hub-Mirror: %s", newName), rooms)
		return []*RoomBinding{b}, err
	}

	// Mode 2: Peer (Mesh Topology)
	// Create mirror rooms on ALL other active platforms.
	targetRooms := []BoundRoom{{Platform: event.Platform, RoomID: event.Message.RoomID}}
	created := 0
	roomName := fmt.Sprintf("%s-%s", event.Platform, event.Message.RoomID)

	for name, adapter := range r.registry.All() {
		if name == event.Platform {
			continue
		}
		// Try to create room on peer
		if tid, err := adapter.CreateRoom(ctx, roomName); err == nil {
			targetRooms = append(targetRooms, BoundRoom{Platform: name, RoomID: tid})
			created++
		}
	}

	if created == 0 {
		return nil, fmt.Errorf("no peer rooms created")
	}

	b, err := r.store.CreateDynamicBinding(fmt.Sprintf("Peer-Mirror: %s", roomName), targetRooms)
	return []*RoomBinding{b}, err
}

func (r *Router) dispatch(ctx context.Context, adapter Platform, event *Event, tRoom BoundRoom, bindID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var err error
	switch event.Action {
	case ActionCreate:
		err = r.handleCreate(ctx, adapter, event, tRoom, bindID)
	case ActionUpdate:
		if tgtMsgID, found := r.store.GetTargetMessageID(event.Platform, event.Message.ID, tRoom.Platform); found {
			outMsg := r.deepCopyMessage(event.Message)
			outMsg.Files = nil
			err = adapter.EditMessage(ctx, &OutboundMessage{
				TargetPlatform: tRoom.Platform, TargetRoomID: tRoom.RoomID, TargetMessageID: tgtMsgID, Message: outMsg,
			})
		}
	case ActionDelete:
		if tgtMsgID, found := r.store.GetTargetMessageID(event.Platform, event.Message.ID, tRoom.Platform); found {
			err = adapter.DeleteMessage(ctx, tRoom.RoomID, tgtMsgID)
		}
	}

	if err != nil {
		slog.Warn("delivery failed", "to", tRoom.Platform, "err", err)
	}
}

func (r *Router) handleCreate(ctx context.Context, adapter Platform, event *Event, tRoom BoundRoom, bindID string) error {
	outMsg := r.deepCopyMessage(event.Message)

	if outMsg.ReplyToID != "" {
		if tgtID, found := r.store.GetTargetMessageID(event.Platform, outMsg.ReplyToID, tRoom.Platform); found {
			outMsg.ReplyToID = tgtID
		} else {
			outMsg.ReplyToID = ""
		}
	}

	payload := &OutboundMessage{
		TargetPlatform: tRoom.Platform,
		TargetRoomID:   tRoom.RoomID,
		TargetConfig:   tRoom.Config,
		Message:        outMsg,
	}

	newID, err := adapter.SendMessage(ctx, payload)
	if err == nil && newID != "" {
		r.store.SaveMessageMapping(event.Platform, event.Message.ID, tRoom.Platform, newID, bindID)
	}
	return err
}

func (r *Router) deepCopyMessage(src *Message) *Message {
	dst := *src
	if src.Mentions != nil {
		dst.Mentions = make([]string, len(src.Mentions))
		copy(dst.Mentions, src.Mentions)
	}
	if src.Files != nil {
		dst.Files = make([]*File, len(src.Files))
		for i, f := range src.Files {
			val := *f
			dst.Files[i] = &val
		}
	}
	if src.Embeds != nil {
		dst.Embeds = make([]*Embed, len(src.Embeds))
		for i, e := range src.Embeds {
			val := *e
			if e.Fields != nil {
				val.Fields = make([]*EmbedField, len(e.Fields))
				for j, field := range e.Fields {
					fVal := *field
					val.Fields[j] = &fVal
				}
			}
			dst.Embeds[i] = &val
		}
	}
	return &dst
}
