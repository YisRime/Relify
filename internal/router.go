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
	if event == nil || event.Message == nil || r.store.IsEventEcho(event.Platform, event.Message.ID) {
		return nil
	}
	if p, ok := r.registry.Get(event.Platform); ok && event.Message.SenderID == p.GetBotUserID() {
		return nil
	}
	if err := event.Message.Validate(); err != nil {
		return nil
	}

	r.store.SaveEvent(event)
	r.store.UpdateUserCache(event.Platform, event.Message.SenderID, event.Message.SenderName, event.Message.SenderAvatar)

	lookupKey := event.Message.RoomID
	srcAdapter, ok := r.registry.Get(event.Platform)
	if !ok {
		return nil
	}

	if srcAdapter.GetRouteType() == RouteTypeAggregate {
		lookupKey = AggregateRoomKey
	}

	bindings := r.store.GetBindingsByRoom(event.Platform, lookupKey)
	if len(bindings) == 0 {
		var err error
		if bindings, err = r.resolveBinding(ctx, event, srcAdapter); err != nil {
			slog.Warn("binding failed", "err", err)
			return nil
		}
	}

	var wg sync.WaitGroup
	for _, b := range bindings {
		for _, tr := range b.Rooms {
			if tr.Platform == event.Platform && tr.RoomID == lookupKey {
				continue
			}
			if adapter, ok := r.registry.Get(tr.Platform); ok {
				wg.Add(1)
				go func(tr BoundRoom, bid string) {
					defer wg.Done()
					r.dispatch(ctx, adapter, event, tr, bid)
				}(tr, b.ID)
			}
		}
	}
	wg.Wait()
	return nil
}

func (r *Router) resolveBinding(ctx context.Context, event *Event, srcAdapter Platform) ([]*RoomBinding, error) {
	if r.cfg.Mode != "hub" {
		return r.resolvePeerBinding(ctx, event)
	}

	hubName := r.cfg.HubPlatform
	if event.Platform == hubName {
		return nil, nil
	}

	hub, ok := r.registry.Get(hubName)
	if !ok {
		return nil, fmt.Errorf("hub offline")
	}

	var targetName string
	srcInfo, _ := srcAdapter.GetRoomInfo(ctx, event.Message.RoomID)
	if srcInfo == nil {
		srcInfo = &RoomInfo{Name: event.Message.RoomID}
	}

	if srcAdapter.GetRouteType() == RouteTypeAggregate {
		targetName = fmt.Sprintf("All-%s", event.Platform)
	} else {
		targetName = fmt.Sprintf("[%s] %s", srcAdapter.Name(), srcInfo.Name)
	}

	tid, err := hub.CreateRoom(ctx, &RoomInfo{Name: targetName, Topic: "Bridged via Relify"})
	if err != nil {
		return nil, err
	}

	rooms := []BoundRoom{
		{Platform: event.Platform, RoomID: event.Message.RoomID},
		{Platform: hubName, RoomID: tid},
	}
	if srcAdapter.GetRouteType() == RouteTypeAggregate {
		rooms[0].RoomID = AggregateRoomKey
	}

	b, err := r.store.CreateDynamicBinding(fmt.Sprintf("Auto: %s", targetName), rooms)
	return []*RoomBinding{b}, err
}

func (r *Router) resolvePeerBinding(ctx context.Context, event *Event) ([]*RoomBinding, error) {
	targetRooms := []BoundRoom{{Platform: event.Platform, RoomID: event.Message.RoomID}}
	roomName := fmt.Sprintf("%s-%s", event.Platform, event.Message.RoomID)

	for name, p := range r.registry.All() {
		if name == event.Platform {
			continue
		}
		if tid, err := p.CreateRoom(ctx, &RoomInfo{Name: roomName}); err == nil {
			targetRooms = append(targetRooms, BoundRoom{Platform: name, RoomID: tid})
		}
	}
	if len(targetRooms) < 2 {
		return nil, fmt.Errorf("no peers")
	}
	b, err := r.store.CreateDynamicBinding("Peer: "+roomName, targetRooms)
	return []*RoomBinding{b}, err
}

func (r *Router) dispatch(ctx context.Context, adapter Platform, event *Event, tRoom BoundRoom, bindID string) {
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	case <-ctx.Done():
		return
	}

	outMsg := r.deepCopyMessage(event.Message)
	if outMsg.ReplyToID != "" {
		outMsg.ReplyToID, _ = r.store.GetTargetMessageID(event.Platform, outMsg.ReplyToID, tRoom.Platform)
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
			return
		}

		switch event.Action {
		case ActionCreate:
			var newID string
			if newID, err = adapter.SendMessage(ctx, payload); err == nil && newID != "" {
				r.store.SaveMessageMapping(event.Platform, event.Message.ID, tRoom.Platform, newID, bindID)
				return
			}
		case ActionUpdate:
			if tgtID, ok := r.store.GetTargetMessageID(event.Platform, event.Message.ID, tRoom.Platform); ok {
				payload.TargetMessageID = tgtID
				if err = adapter.EditMessage(ctx, payload); err == nil {
					return
				}
			} else {
				return
			}
		case ActionDelete:
			if tgtID, ok := r.store.GetTargetMessageID(event.Platform, event.Message.ID, tRoom.Platform); ok {
				if err = adapter.DeleteMessage(ctx, tRoom.RoomID, tgtID); err == nil {
					return
				}
			} else {
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
