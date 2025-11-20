package internal

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

type Router struct {
	config     *Config
	registry   *PlatformRegistry
	store      *Store
	logger     *Logger
	httpClient *http.Client
}

func NewRouter(cfg *Config, reg *PlatformRegistry, store *Store, log *Logger) *Router {
	return &Router{
		config:   cfg,
		registry: reg,
		store:    store,
		logger:   log,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (r *Router) HandleEvent(ctx context.Context, event *Event) error {
	if event == nil || event.Message == nil {
		return nil
	}

	if adapter, ok := r.registry.Get(event.Platform); ok {
		if botID := adapter.GetBotUserID(); botID != "" && event.Message.SenderID == botID {
			return nil
		}
	}

	if r.store.IsEventEcho(event.Platform, event.Message.ID) {
		return nil
	}

	r.store.UpdateUserCache(event.Platform, event.Message.SenderID, event.Message.SenderName, event.Message.SenderAvatar)

	bindings := r.store.GetBindingsByRoom(event.Platform, event.Message.RoomID)

	if len(bindings) == 0 {
		var err error
		if r.config.Mode == "hub" {
			bindings, err = r.ensureHubBinding(ctx, event)
		} else {
			bindings, err = r.ensurePeerBinding(ctx, event)
		}

		if err != nil {
			r.logger.Log(ErrorLevel, "router", "auto-binding failed", map[string]interface{}{"err": err.Error()})
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
			go func(tr BoundRoom, bindID string) {
				defer wg.Done()
				r.processRoute(ctx, adapter, event, tr, bindID)
			}(targetRoom, b.ID)
		}
	}
	wg.Wait()
	return nil
}

func (r *Router) ensureHubBinding(ctx context.Context, event *Event) ([]*RoomBinding, error) {
	hubPlatName := r.config.HubPlatform
	fallbackRoomID := r.config.HubRoomID

	if event.Platform == hubPlatName {
		return nil, nil
	}

	hubAdapter, ok := r.registry.Get(hubPlatName)
	if !ok {
		return nil, fmt.Errorf("hub platform %s not active", hubPlatName)
	}

	var targetRoomID string
	var usedFallback bool

	newRoomName := fmt.Sprintf("%s-%s", event.Platform, event.Message.RoomID)
	createdID, err := hubAdapter.CreateRoom(ctx, newRoomName)

	if err == nil {
		targetRoomID = createdID
	} else {
		if fallbackRoomID != "" {
			r.logger.Log(WarnLevel, "router", "hub create failed, using fallback", map[string]interface{}{"err": err.Error()})
			targetRoomID = fallbackRoomID
			usedFallback = true
		} else {
			return nil, fmt.Errorf("hub room creation failed: %w", err)
		}
	}

	bindingName := fmt.Sprintf("Auto-Hub: %s/%s", event.Platform, event.Message.RoomID)
	if usedFallback {
		bindingName += " (Fallback)"
	}

	rooms := []BoundRoom{
		{Platform: event.Platform, RoomID: event.Message.RoomID},
		{Platform: hubPlatName, RoomID: targetRoomID},
	}

	r.logger.Log(InfoLevel, "router", "created hub binding", map[string]interface{}{"name": bindingName})
	b, err := r.store.CreateDynamicBinding(bindingName, rooms)
	if err != nil {
		return nil, err
	}
	return []*RoomBinding{b}, nil
}

func (r *Router) ensurePeerBinding(ctx context.Context, event *Event) ([]*RoomBinding, error) {
	allPlats := r.registry.All()

	if len(allPlats) <= 1 {
		return nil, nil
	}

	rooms := []BoundRoom{
		{Platform: event.Platform, RoomID: event.Message.RoomID},
	}

	targetRoomName := fmt.Sprintf("%s-%s", event.Platform, event.Message.RoomID)

	createdCount := 0

	for name, adapter := range allPlats {
		if name == event.Platform {
			continue
		}

		targetID, err := adapter.CreateRoom(ctx, targetRoomName)
		if err != nil {
			r.logger.Log(WarnLevel, "router", "peer create failed", map[string]interface{}{
				"plat": name,
				"err":  err.Error(),
			})
			continue
		}

		rooms = append(rooms, BoundRoom{Platform: name, RoomID: targetID})
		createdCount++
	}

	if createdCount == 0 {
		return nil, fmt.Errorf("failed to create counterpart rooms on any other platform")
	}

	bindingName := fmt.Sprintf("Auto-Peer: %s/%s (%d targets)", event.Platform, event.Message.RoomID, createdCount)

	r.logger.Log(InfoLevel, "router", "created peer binding", map[string]interface{}{
		"name":    bindingName,
		"targets": createdCount,
	})

	b, err := r.store.CreateDynamicBinding(bindingName, rooms)
	if err != nil {
		return nil, err
	}
	return []*RoomBinding{b}, nil
}

func (r *Router) processRoute(ctx context.Context, adapter Platform, event *Event, tRoom BoundRoom, bindID string) {
	routeCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var err error
	switch event.Action {
	case ActionCreate:
		err = r.handleCreate(routeCtx, adapter, event, tRoom, bindID)
	case ActionUpdate:
		err = r.handleUpdate(routeCtx, adapter, event, tRoom)
	case ActionDelete:
		err = r.handleDelete(routeCtx, adapter, event, tRoom)
	}

	if err != nil {
		r.logger.Log(WarnLevel, "router", "delivery failed", map[string]interface{}{
			"to_plat": tRoom.Platform,
			"to_room": tRoom.RoomID,
			"err":     err.Error(),
		})
	}
}

func (r *Router) handleCreate(ctx context.Context, adapter Platform, event *Event, tRoom BoundRoom, bindID string) error {
	outMsg := r.constructOutboundPayload(event.Message)

	if outMsg.ReplyToID != "" {
		if tgtID, found := r.store.GetTargetMessageID(event.Platform, outMsg.ReplyToID, tRoom.Platform); found {
			outMsg.ReplyToID = tgtID
		} else {
			outMsg.ReplyToID = ""
		}
	}

	if len(outMsg.Files) > 0 {
		if err := r.rehostFiles(ctx, adapter, outMsg); err != nil {
			return err
		}
	}

	payload := &OutboundMessage{
		TargetPlatform: tRoom.Platform,
		TargetRoomID:   tRoom.RoomID,
		TargetConfig:   tRoom.Config,
		Message:        outMsg,
	}

	newID, err := adapter.SendMessage(ctx, payload)
	if err != nil {
		return err
	}

	if newID != "" {
		r.store.SaveMessageMapping(event.Platform, event.Message.ID, tRoom.Platform, newID, bindID)
	}
	return nil
}

func (r *Router) handleUpdate(ctx context.Context, adapter Platform, event *Event, tRoom BoundRoom) error {
	tgtMsgID, found := r.store.GetTargetMessageID(event.Platform, event.Message.ID, tRoom.Platform)
	if !found {
		return nil
	}

	outMsg := r.constructOutboundPayload(event.Message)
	outMsg.Files = nil

	return adapter.EditMessage(ctx, &OutboundMessage{
		TargetPlatform:  tRoom.Platform,
		TargetRoomID:    tRoom.RoomID,
		TargetMessageID: tgtMsgID,
		Message:         outMsg,
	})
}

func (r *Router) handleDelete(ctx context.Context, adapter Platform, event *Event, tRoom BoundRoom) error {
	tgtMsgID, found := r.store.GetTargetMessageID(event.Platform, event.Message.ID, tRoom.Platform)
	if !found {
		return nil
	}
	return adapter.DeleteMessage(ctx, tRoom.RoomID, tgtMsgID)
}

func (r *Router) constructOutboundPayload(src *Message) *Message {
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
	return &dst
}

func (r *Router) rehostFiles(ctx context.Context, adapter Platform, msg *Message) error {
	n := 0
	for _, file := range msg.Files {
		data, err := r.downloadFile(ctx, file.URL)
		if err != nil {
			r.logger.Log(WarnLevel, "router", "dl failed", map[string]interface{}{"url": file.URL, "err": err.Error()})
			continue
		}

		newURL, err := adapter.UploadFile(ctx, data, file.Name)
		if err != nil {
			r.logger.Log(WarnLevel, "router", "ul failed", map[string]interface{}{"plat": adapter.Name(), "err": err.Error()})
			continue
		}

		file.URL = newURL
		msg.Files[n] = file
		n++
	}
	msg.Files = msg.Files[:n]
	return nil
}

func (r *Router) downloadFile(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http status %d", resp.StatusCode)
	}

	const maxLimit = 16 * 1024 * 1024
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxLimit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxLimit {
		return nil, fmt.Errorf("file too large")
	}
	return data, nil
}
