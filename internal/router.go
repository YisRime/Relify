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
	sem      chan struct{}
}

func NewRouter(cfg *Config, reg *PlatformRegistry, store *Store) *Router {
	return &Router{
		cfg:      cfg,
		registry: reg,
		store:    store,
		sem:      make(chan struct{}, 50),
	}
}

func (r *Router) HandleEvent(ctx context.Context, event *Event) error {
	slog.Debug("router received event", "platform", event.Platform, "action", event.Action)

	if event == nil || event.Message == nil || r.store.IsEventEcho(event.Platform, event.Message.ID) {
		slog.Debug("router event ignored", "reason", "nil or echo")
		return nil
	}
	if p, ok := r.registry.Get(event.Platform); ok && event.Message.SenderID == p.GetBotUserID() {
		slog.Debug("router event ignored", "reason", "sent by bot", "sender", event.Message.SenderID)
		return nil
	}
	if err := event.Message.Validate(); err != nil {
		slog.Debug("router event ignored", "reason", "validation failed", "err", err)
		return nil
	}

	slog.Debug("router processing event", "platform", event.Platform, "room", event.Message.RoomID, "sender", event.Message.SenderName)

	r.store.SaveEvent(event)
	r.store.UpdateUserCache(event.Platform, event.Message.SenderID, event.Message.SenderName, event.Message.SenderAvatar)

	srcAdapter, ok := r.registry.Get(event.Platform)
	if !ok {
		return nil
	}

	bindings := r.store.GetBindingsByRoom(event.Platform, event.Message.RoomID)
	slog.Debug("router found bindings", "count", len(bindings), "platform", event.Platform, "room", event.Message.RoomID)

	if len(bindings) == 0 {
		slog.Debug("router creating new binding", "platform", event.Platform, "room", event.Message.RoomID)
		var err error
		if bindings, err = r.resolveBinding(ctx, event, srcAdapter); err != nil {
			slog.Warn("binding failed", "err", err)
			return nil
		}
		slog.Debug("router binding created", "count", len(bindings))
	}

	var wg sync.WaitGroup
	dispatchCount := 0
	for _, b := range bindings {
		for _, tr := range b.Rooms {
			if tr.Platform == event.Platform {
				continue
			}

			if adapter, ok := r.registry.Get(tr.Platform); ok {
				dispatchCount++
				slog.Debug("router dispatching message", "to_platform", tr.Platform, "to_room", tr.RoomID, "binding", b.ID)
				wg.Add(1)
				go func(tr BoundRoom, bid string) {
					defer wg.Done()
					r.dispatch(ctx, adapter, event, tr, bid)
				}(tr, b.ID)
			}
		}
	}
	slog.Debug("router waiting for dispatch", "count", dispatchCount)
	wg.Wait()
	slog.Debug("router dispatch completed")
	return nil
}

func (r *Router) resolveBinding(ctx context.Context, event *Event, srcAdapter Platform) ([]*RoomBinding, error) {
	if r.cfg.Mode == "hub" && event.Platform == r.cfg.HubPlatform {
		return nil, nil
	}

	srcInfo, _ := srcAdapter.GetRoomInfo(ctx, event.Message.RoomID)
	if srcInfo == nil {
		srcInfo = &RoomInfo{Name: event.Message.RoomID}
	}

	boundRooms := []BoundRoom{
		{Platform: event.Platform, RoomID: event.Message.RoomID},
	}
	bindingName := ""

	if r.cfg.Mode == "hub" {
		hubName := r.cfg.HubPlatform
		hub, ok := r.registry.Get(hubName)
		if !ok {
			return nil, fmt.Errorf("hub platform %s offline", hubName)
		}

		targetName := fmt.Sprintf("[%s] %s", srcAdapter.Name(), srcInfo.Name)
		tid, err := hub.CreateRoom(ctx, &RoomInfo{Name: targetName, Topic: "Bridged via Relify (Hub Mode)"})
		if err != nil {
			return nil, err
		}
		boundRooms = append(boundRooms, BoundRoom{Platform: hubName, RoomID: tid})
		bindingName = fmt.Sprintf("Hub: %s <-> %s", event.Platform, hubName)

	} else {
		baseName := fmt.Sprintf("%s-%s", event.Platform, srcInfo.Name)

		for name, p := range r.registry.All() {
			if name == event.Platform {
				continue
			}

			targetRoomName := baseName
			if p.GetRouteType() == RouteTypeAggregate {
				targetRoomName = "Aggregate Group"
			}

			tid, err := p.CreateRoom(ctx, &RoomInfo{Name: targetRoomName, Topic: "Bridged via Relify (Peer Mode)"})
			if err == nil {
				boundRooms = append(boundRooms, BoundRoom{Platform: name, RoomID: tid})
			} else {
				slog.Warn("failed to bridge to peer", "platform", name, "err", err)
			}
		}
		bindingName = fmt.Sprintf("Peer: %s -> All", event.Platform)
	}

	if len(boundRooms) < 2 {
		return nil, fmt.Errorf("not enough targets to bind")
	}

	b, err := r.store.CreateDynamicBinding(bindingName, boundRooms)
	if err != nil {
		return nil, err
	}
	return []*RoomBinding{b}, nil
}

func (r *Router) dispatch(ctx context.Context, adapter Platform, event *Event, tRoom BoundRoom, bindID string) {
	slog.Debug("router dispatch started", "platform", adapter.Name(), "room", tRoom.RoomID)

	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	case <-ctx.Done():
		slog.Debug("router dispatch cancelled", "reason", "context done")
		return
	}

	outMsg := r.deepCopyMessage(event.Message)
	slog.Debug("router message copied", "segments", len(outMsg.Body))

	if outMsg.ReplyToID != "" {
		if tgtID, ok := r.store.GetTargetMessageID(event.Platform, outMsg.ReplyToID, tRoom.Platform); ok {
			outMsg.ReplyToID = tgtID
		} else {
			outMsg.ReplyToID = ""
		}
	}

	payload := &OutMessage{
		TargetPlatform: tRoom.Platform,
		TargetRoomID:   tRoom.RoomID,
		TargetConfig:   tRoom.Config,
		Message:        outMsg,
	}

	var err error
	for i := 0; i < 3; i++ {
		if ctx.Err() != nil {
			slog.Debug("router dispatch aborted", "reason", "context cancelled")
			return
		}

		switch event.Action {
		case ActionCreate:
			slog.Debug("router sending message", "platform", adapter.Name(), "room", tRoom.RoomID, "attempt", i+1)
			var newID string
			if newID, err = adapter.SendMessage(ctx, payload); err == nil && newID != "" {
				slog.Debug("router message sent", "platform", adapter.Name(), "new_id", newID)
				r.store.SaveMessageMapping(event.Platform, event.Message.ID, tRoom.Platform, newID, bindID)
				return
			}
			slog.Debug("router send failed", "platform", adapter.Name(), "err", err, "attempt", i+1)
		case ActionUpdate:
			if tgtID, ok := r.store.GetTargetMessageID(event.Platform, event.Message.ID, tRoom.Platform); ok {
				slog.Debug("router updating message", "platform", adapter.Name(), "target_id", tgtID)
				payload.TargetMessageID = tgtID
				if err = adapter.EditMessage(ctx, payload); err == nil {
					slog.Debug("router message updated", "platform", adapter.Name())
					return
				}
				slog.Debug("router update failed", "platform", adapter.Name(), "err", err)
			} else {
				slog.Debug("router update skipped", "reason", "target message not found")
				return
			}
		case ActionDelete:
			if tgtID, ok := r.store.GetTargetMessageID(event.Platform, event.Message.ID, tRoom.Platform); ok {
				slog.Debug("router deleting message", "platform", adapter.Name(), "target_id", tgtID)
				if err = adapter.DeleteMessage(ctx, tRoom.RoomID, tgtID); err == nil {
					slog.Debug("router message deleted", "platform", adapter.Name())
					return
				}
				slog.Debug("router delete failed", "platform", adapter.Name(), "err", err)
			} else {
				slog.Debug("router delete skipped", "reason", "target message not found")
				return
			}
		}

		time.Sleep(time.Duration(1<<i) * time.Second)
	}
	slog.Warn("dispatch failed", "to", tRoom.Platform, "err", err)
}

func (r *Router) deepCopyMessage(src *Message) *Message {
	dst := *src
	dst.Body = make([]Segment, len(src.Body))
	for i, s := range src.Body {
		dst.Body[i] = Segment{Type: s.Type, Fallback: s.Fallback, Data: make(map[string]interface{}, len(s.Data))}
		for k, v := range s.Data {
			dst.Body[i].Data[k] = v
		}
	}
	return &dst
}
