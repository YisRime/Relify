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
	registry   *PlatformRegistry
	store      *Store
	logger     *Logger
	httpClient *http.Client
}

func NewRouter(reg *PlatformRegistry, store *Store, log *Logger) *Router {
	return &Router{
		registry: reg,
		store:    store,
		logger:   log,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
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
		r.logger.Log(DebugLevel, "router", "ignored echo message", map[string]interface{}{
			"id": event.Message.ID,
		})
		return nil
	}

	r.store.UpdateUserCache(event.Platform, event.Message.SenderID, event.Message.SenderName, event.Message.SenderAvatar)

	bindings := r.store.GetBindingsByRoom(event.Platform, event.Message.RoomID)
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

func (r *Router) processRoute(ctx context.Context, adapter Platform, event *Event, tRoom BoundRoom, bindID string) {
	routeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
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
		r.logger.Log(ErrorLevel, "router", "delivery failed", map[string]interface{}{
			"action": event.Action,
			"target": tRoom.Platform,
			"error":  err.Error(),
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
		Message:        outMsg,
	}

	newID, err := r.sendWithRetry(ctx, adapter, payload)
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
		return fmt.Errorf("target message not found")
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
	dst := &Message{
		SenderID:     src.SenderID,
		SenderName:   src.SenderName,
		SenderAvatar: src.SenderAvatar,
		Content:      src.Content,
		ReplyToID:    src.ReplyToID,
		Extra:        src.Extra,
	}

	if len(src.Mentions) > 0 {
		dst.Mentions = make([]string, len(src.Mentions))
		copy(dst.Mentions, src.Mentions)
	}

	if len(src.Files) > 0 {
		dst.Files = make([]*File, len(src.Files))
		for i, f := range src.Files {
			val := *f
			dst.Files[i] = &val
		}
	}

	if len(src.Embeds) > 0 {
		dst.Embeds = make([]*Embed, len(src.Embeds))
		for i, e := range src.Embeds {
			if e == nil {
				continue
			}
			v := *e
			if e.Image != nil {
				img := *e.Image
				v.Image = &img
			}
			if e.Thumbnail != nil {
				thm := *e.Thumbnail
				v.Thumbnail = &thm
			}
			if len(e.Fields) > 0 {
				v.Fields = make([]*EmbedField, len(e.Fields))
				for j, f := range e.Fields {
					if f != nil {
						fv := *f
						v.Fields[j] = &fv
					}
				}
			}
			dst.Embeds[i] = &v
		}
	}

	return dst
}

func (r *Router) rehostFiles(ctx context.Context, adapter Platform, msg *Message) error {
	for _, file := range msg.Files {
		data, err := r.downloadFile(ctx, file.URL)
		if err != nil {
			r.logger.Log(WarnLevel, "router", "download failed, skipping file", map[string]interface{}{"url": file.URL})
			continue
		}

		newURL, err := adapter.UploadFile(ctx, data, file.Name)
		if err != nil {
			r.logger.Log(WarnLevel, "router", "upload failed", map[string]interface{}{"plat": adapter.Name()})
			continue
		}

		file.URL = newURL
	}
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
		return nil, fmt.Errorf("status code %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

func (r *Router) sendWithRetry(ctx context.Context, adapter Platform, msg *OutboundMessage) (string, error) {
	const maxRetries = 3
	var lastErr error

	for i := 0; i <= maxRetries; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(1<<i) * time.Second):
			}
		}

		id, err := adapter.SendMessage(ctx, msg)
		if err == nil {
			return id, nil
		}
		lastErr = err
	}
	return "", fmt.Errorf("retry exhausted: %w", lastErr)
}
