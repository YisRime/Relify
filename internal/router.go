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
	return &Router{cfg: cfg, registry: reg, store: store}
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
	if err := event.Message.Validate(); err != nil {
		slog.Warn("invalid event message", "err", err, "platform", event.Platform)
		return nil
	}
	if err := r.store.SaveEvent(event); err != nil {
		slog.Warn("failed to save event", "err", err)
	}
	r.store.UpdateUserCache(event.Platform, event.Message.SenderID, event.Message.SenderName, event.Message.SenderAvatar)

	lookupKey := event.Message.RoomID
	sourceAdapter, ok := r.registry.Get(event.Platform)
	if !ok {
		return nil
	}

	if sourceAdapter.GetRouteType() == RouteTypeAggregate {
		lookupKey = AggregateRoomKey
	}

	bindings := r.store.GetBindingsByRoom(event.Platform, lookupKey)
	if len(bindings) == 0 {
		var err error
		bindings, err = r.resolveBinding(ctx, event, sourceAdapter)
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
			if targetRoom.Platform == event.Platform && targetRoom.RoomID == lookupKey {
				continue
			}
			adapterInstance, ok := r.registry.Get(targetRoom.Platform)
			if !ok {
				continue
			}
			wg.Add(1)
			go func(tr BoundRoom, bid string) {
				defer wg.Done()
				r.dispatch(ctx, adapterInstance, event, tr, bid)
			}(targetRoom, b.ID)
		}
	}
	wg.Wait()
	return nil
}

func (r *Router) resolveBinding(ctx context.Context, event *Event, sourceAdapter Platform) ([]*RoomBinding, error) {
	if r.cfg.Mode != "hub" {
		return r.resolvePeerBinding(ctx, event)
	}
	hubPlatName := r.cfg.HubPlatform
	if event.Platform == hubPlatName {
		return nil, nil
	}
	hubAdapter, ok := r.registry.Get(hubPlatName)
	if !ok {
		return nil, fmt.Errorf("hub offline")
	}

	routeType := sourceAdapter.GetRouteType()
	var targetRoomInfo RoomInfo
	var sourceBoundRoomID string
	bindingName := ""

	if routeType == RouteTypeAggregate {
		sourceBoundRoomID = AggregateRoomKey
		targetRoomInfo = RoomInfo{
			Name:  fmt.Sprintf("All-%s", event.Platform),
			Topic: fmt.Sprintf("Aggregation for all %s chats", event.Platform),
		}
		bindingName = fmt.Sprintf("Agg: %s -> Hub", event.Platform)
	} else {
		sourceBoundRoomID = event.Message.RoomID
		srcRoomInfo, err := sourceAdapter.GetRoomInfo(ctx, event.Message.RoomID)
		if err != nil {
			srcRoomInfo = &RoomInfo{
				ID:   event.Message.RoomID,
				Name: fmt.Sprintf("%s-%s", event.Platform, event.Message.RoomID),
			}
		}
		targetRoomInfo = RoomInfo{
			Name:      fmt.Sprintf("[%s] %s", sourceAdapter.Name(), srcRoomInfo.Name),
			AvatarURL: srcRoomInfo.AvatarURL,
			Topic:     fmt.Sprintf("Mirror of %s (%s)", srcRoomInfo.Name, srcRoomInfo.ID),
		}
		bindingName = fmt.Sprintf("Mirror: %s", srcRoomInfo.Name)
	}

	targetID, err := hubAdapter.CreateRoom(ctx, &targetRoomInfo)
	if err != nil {
		return nil, fmt.Errorf("hub create failed: %w", err)
	}

	rooms := []BoundRoom{
		{Platform: event.Platform, RoomID: sourceBoundRoomID},
		{Platform: hubPlatName, RoomID: targetID},
	}
	b, err := r.store.CreateDynamicBinding(bindingName, rooms)
	return []*RoomBinding{b}, err
}

func (r *Router) resolvePeerBinding(ctx context.Context, event *Event) ([]*RoomBinding, error) {
	targetRooms := []BoundRoom{{Platform: event.Platform, RoomID: event.Message.RoomID}}
	createdCount := 0
	roomName := fmt.Sprintf("%s-%s", event.Platform, event.Message.RoomID)

	for name, adapterInstance := range r.registry.All() {
		if name == event.Platform {
			continue
		}
		info := &RoomInfo{Name: roomName}
		if tid, err := adapterInstance.CreateRoom(ctx, info); err == nil {
			targetRooms = append(targetRooms, BoundRoom{Platform: name, RoomID: tid})
			createdCount++
		}
	}
	if createdCount == 0 {
		return nil, fmt.Errorf("no peer rooms created")
	}
	b, err := r.store.CreateDynamicBinding(fmt.Sprintf("Peer: %s", roomName), targetRooms)
	return []*RoomBinding{b}, err
}

func (r *Router) dispatch(ctx context.Context, adapterInstance Platform, event *Event, tRoom BoundRoom, bindID string) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	var err error
	switch event.Action {
	case ActionCreate:
		err = r.handleCreate(ctx, adapterInstance, event, tRoom, bindID)
	case ActionUpdate:
		if tgtMsgID, found := r.store.GetTargetMessageID(event.Platform, event.Message.ID, tRoom.Platform); found {
			outMsg := r.deepCopyMessage(event.Message)
			err = adapterInstance.EditMessage(ctx, &OutMessage{
				TargetPlatform:  tRoom.Platform,
				TargetRoomID:    tRoom.RoomID,
				TargetMessageID: tgtMsgID,
				Message:         outMsg,
			})
		}
	case ActionDelete:
		if tgtMsgID, found := r.store.GetTargetMessageID(event.Platform, event.Message.ID, tRoom.Platform); found {
			err = adapterInstance.DeleteMessage(ctx, tRoom.RoomID, tgtMsgID)
		}
	}
	if err != nil {
		slog.Warn("delivery failed", "to", tRoom.Platform, "err", err)
	}
}

func (r *Router) handleCreate(ctx context.Context, adapterInstance Platform, event *Event, tRoom BoundRoom, bindID string) error {
	outMsg := r.deepCopyMessage(event.Message)
	if outMsg.ReplyToID != "" {
		if tgtID, found := r.store.GetTargetMessageID(event.Platform, outMsg.ReplyToID, tRoom.Platform); found {
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
	newID, err := adapterInstance.SendMessage(ctx, payload)
	if err == nil && newID != "" {
		r.store.SaveMessageMapping(event.Platform, event.Message.ID, tRoom.Platform, newID, bindID)
	}
	return err
}

func (r *Router) deepCopyMessage(src *Message) *Message {
	dst := *src
	if src.Body != nil {
		dst.Body = make([]Segment, len(src.Body))
		for i, s := range src.Body {
			dst.Body[i] = Segment{
				Type:     s.Type,
				Fallback: s.Fallback,
				Data:     r.deepCopyMap(s.Data),
			}
		}
	}
	if src.Extra != nil {
		dst.Extra = r.deepCopyMap(src.Extra)
	}
	return &dst
}

func (r *Router) deepCopyMap(src map[string]interface{}) map[string]interface{} {
	if src == nil {
		return nil
	}
	dst := make(map[string]interface{})
	for k, v := range src {
		switch val := v.(type) {
		case map[string]interface{}:
			dst[k] = r.deepCopyMap(val)
		case []interface{}:
			dst[k] = r.deepCopySlice(val)
		default:
			dst[k] = val
		}
	}
	return dst
}

func (r *Router) deepCopySlice(src []interface{}) []interface{} {
	if src == nil {
		return nil
	}
	dst := make([]interface{}, len(src))
	for i, v := range src {
		switch val := v.(type) {
		case map[string]interface{}:
			dst[i] = r.deepCopyMap(val)
		case []interface{}:
			dst[i] = r.deepCopySlice(val)
		default:
			dst[i] = val
		}
	}
	return dst
}
