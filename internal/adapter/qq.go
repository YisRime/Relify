package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"Relify/internal"

	"github.com/gorilla/websocket"
	"gopkg.in/yaml.v3"
)

type QQConfig struct {
	Protocol string `yaml:"protocol"`
	URL      string `yaml:"url"`
	Secret   string `yaml:"secret"`
	Group    string `yaml:"group"`
}

type QQAdapter struct {
	cfg      QQConfig
	handler  internal.InboundHandler
	wsConn   *websocket.Conn
	wsMu     sync.Mutex
	selfID   string
	selfIDMu sync.RWMutex
	echoMap  sync.Map
	closeCh  chan struct{}
}

func NewQQAdapter(node yaml.Node, handler internal.InboundHandler) (*QQAdapter, error) {
	var cfg QQConfig
	if err := node.Decode(&cfg); err != nil {
		return nil, err
	}
	if cfg.Protocol == "" {
		cfg.Protocol = "ws"
	}

	return &QQAdapter{
		cfg:     cfg,
		handler: handler,
		closeCh: make(chan struct{}),
	}, nil
}

func (q *QQAdapter) Name() string { return "qq" }

func (q *QQAdapter) GetBotUserID() string {
	q.selfIDMu.RLock()
	defer q.selfIDMu.RUnlock()
	return q.selfID
}

func (q *QQAdapter) GetRouteType() internal.RouteType { return internal.RouteTypeAggregate }

func (q *QQAdapter) Start(ctx context.Context) error {
	if q.cfg.Protocol == "ws" || strings.HasPrefix(q.cfg.URL, "ws") {
		go q.connectWS(ctx)
		return nil
	}
	return fmt.Errorf("only ws protocol is fully supported in this simplified mode")
}

func (q *QQAdapter) Stop(ctx context.Context) error {
	close(q.closeCh)
	q.wsMu.Lock()
	if q.wsConn != nil {
		q.wsConn.Close()
	}
	q.wsMu.Unlock()
	return nil
}

func (q *QQAdapter) connectWS(ctx context.Context) {
	for {
		select {
		case <-q.closeCh:
			return
		case <-ctx.Done():
			return
		default:
		}

		slog.Info("connecting to qq onebot", "url", q.cfg.URL)
		header := http.Header{}
		if q.cfg.Secret != "" {
			header.Set("Authorization", "Bearer "+q.cfg.Secret)
		}

		conn, _, err := websocket.DefaultDialer.Dial(q.cfg.URL, header)
		if err != nil {
			slog.Error("failed to connect qq ws", "err", err)
			time.Sleep(5 * time.Second)
			continue
		}

		q.wsMu.Lock()
		q.wsConn = conn
		q.wsMu.Unlock()
		slog.Info("qq ws connected")

		q.fetchBotInfo()

		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				slog.Error("qq ws disconnected", "err", err)
				break
			}

			slog.Debug("qq ws message received", "size", len(message))

			var resp struct {
				Echo string `json:"echo"`
			}
			if json.Unmarshal(message, &resp) == nil && resp.Echo != "" {
				slog.Debug("qq api response received", "echo", resp.Echo)
				if ch, ok := q.echoMap.Load(resp.Echo); ok {
					ch.(chan []byte) <- message
				}
				continue
			}
			slog.Debug("qq processing event", "data", string(message))
			go q.processEventBytes(context.Background(), message)
		}

		q.wsMu.Lock()
		q.wsConn = nil
		q.wsMu.Unlock()
		time.Sleep(3 * time.Second)
	}
}

func (q *QQAdapter) fetchBotInfo() {
	go func() {
		time.Sleep(1 * time.Second)
		resp, err := q.callAPI("get_login_info", nil)
		if err == nil {
			var res struct {
				Data struct {
					UserID int64 `json:"user_id"`
				} `json:"data"`
			}
			if json.Unmarshal(resp, &res) == nil && res.Data.UserID != 0 {
				q.selfIDMu.Lock()
				q.selfID = strconv.FormatInt(res.Data.UserID, 10)
				q.selfIDMu.Unlock()
				slog.Info("qq bot info fetched", "id", q.selfID)
			}
		}
	}()
}

type onebotEvent struct {
	PostType    string          `json:"post_type"`
	MessageType string          `json:"message_type"`
	Time        int64           `json:"time"`
	UserID      int64           `json:"user_id"`
	GroupID     int64           `json:"group_id"`
	MessageID   int32           `json:"message_id"`
	Message     json.RawMessage `json:"message"`
	Sender      struct {
		Nickname string `json:"nickname"`
		Card     string `json:"card"`
	} `json:"sender"`
}

func (q *QQAdapter) processEventBytes(ctx context.Context, data []byte) {
	var evt onebotEvent
	if err := json.Unmarshal(data, &evt); err != nil {
		slog.Debug("qq failed to unmarshal event", "err", err)
		return
	}

	slog.Debug("qq event parsed", "post_type", evt.PostType, "message_type", evt.MessageType, "group_id", evt.GroupID, "user_id", evt.UserID)

	if evt.PostType != "message" || evt.MessageType != "group" {
		slog.Debug("qq event ignored", "reason", "not group message")
		return
	}

	bridgeGroupID, err := strconv.ParseInt(q.cfg.Group, 10, 64)
	if err != nil || evt.GroupID != bridgeGroupID {
		slog.Debug("qq event ignored", "reason", "group not matched", "expected", q.cfg.Group, "got", evt.GroupID)
		return
	}

	slog.Debug("qq processing group message", "message_id", evt.MessageID, "sender", evt.Sender.Nickname)

	senderName := evt.Sender.Card
	if senderName == "" {
		senderName = evt.Sender.Nickname
	}
	if senderName == "" {
		senderName = strconv.FormatInt(evt.UserID, 10)
	}

	slog.Debug("qq building relify event", "sender", senderName, "message_segments", len(evt.Message))

	relifyEvt := &internal.Event{
		ID:        strconv.Itoa(int(evt.MessageID)),
		Action:    internal.ActionCreate,
		Platform:  q.Name(),
		Timestamp: time.Unix(evt.Time, 0),
		Message: &internal.Message{
			ID:           strconv.Itoa(int(evt.MessageID)),
			RoomID:       q.cfg.Group,
			SenderID:     strconv.FormatInt(evt.UserID, 10),
			SenderName:   senderName,
			SenderAvatar: fmt.Sprintf("https://q1.qlogo.cn/g?b=qq&nk=%d&s=640", evt.UserID),
			Body:         q.parseOneBotMessage(evt.Message),
		},
	}
	slog.Debug("qq calling handler", "event_id", relifyEvt.ID, "segments", len(relifyEvt.Message.Body))
	q.handler.HandleEvent(ctx, relifyEvt)
}

func (q *QQAdapter) parseOneBotMessage(raw json.RawMessage) []internal.Segment {
	var rawSegs []map[string]interface{}
	if json.Unmarshal(raw, &rawSegs) != nil {
		return []internal.Segment{{Type: internal.TypeText, Data: map[string]interface{}{"text": string(raw)}}}
	}
	var res []internal.Segment
	for _, s := range rawSegs {
		t, _ := s["type"].(string)
		d, _ := s["data"].(map[string]interface{})
		switch t {
		case "text":
			res = append(res, internal.Segment{Type: internal.TypeText, Data: map[string]interface{}{"text": d["text"]}})
		case "image":
			res = append(res, internal.Segment{Type: internal.TypeImage, Data: map[string]interface{}{"url": d["url"], "file": d["file"]}})
		case "record":
			res = append(res, internal.Segment{Type: internal.TypeAudio, Data: map[string]interface{}{"url": d["url"], "file": d["file"]}})
		case "video":
			res = append(res, internal.Segment{Type: internal.TypeVideo, Data: map[string]interface{}{"url": d["url"], "file": d["file"]}})
		case "at":
			res = append(res, internal.Segment{Type: internal.TypeMention, Data: map[string]interface{}{"id": d["qq"], "name": d["qq"]}})
		}
	}
	return res
}

func (q *QQAdapter) SendMessage(ctx context.Context, msg *internal.OutMessage) (string, error) {
	slog.Debug("qq sending message", "target_room", msg.TargetRoomID, "segments", len(msg.Message.Body))

	groupID, err := strconv.ParseInt(msg.TargetRoomID, 10, 64)
	if err != nil {
		slog.Debug("qq send failed", "err", "invalid target room id", "room", msg.TargetRoomID)
		return "", fmt.Errorf("invalid target room id: %s", msg.TargetRoomID)
	}

	params := map[string]interface{}{"group_id": groupID}

	var obMsg []map[string]interface{}
	if msg.Message.ReplyToID != "" {
		rid, _ := strconv.Atoi(msg.Message.ReplyToID)
		obMsg = append(obMsg, map[string]interface{}{"type": "reply", "data": map[string]string{"id": strconv.Itoa(rid)}})
		slog.Debug("qq adding reply", "reply_to", rid)
	}

	for _, seg := range msg.Message.Body {
		switch seg.Type {
		case internal.TypeText:
			obMsg = append(obMsg, map[string]interface{}{"type": "text", "data": map[string]string{"text": internal.GetString(seg.Data, "text")}})
		case internal.TypeImage:
			obMsg = append(obMsg, map[string]interface{}{"type": "image", "data": map[string]string{"file": internal.GetString(seg.Data, "url")}})
		case internal.TypeAudio:
			obMsg = append(obMsg, map[string]interface{}{"type": "record", "data": map[string]string{"file": internal.GetString(seg.Data, "url")}})
		case internal.TypeVideo:
			obMsg = append(obMsg, map[string]interface{}{"type": "video", "data": map[string]string{"file": internal.GetString(seg.Data, "url")}})
		}
	}
	params["message"] = obMsg

	slog.Debug("qq calling send_group_msg api", "segments", len(obMsg))

	resp, err := q.callAPI("send_group_msg", params)
	if err != nil {
		slog.Debug("qq send api failed", "err", err)
		return "", err
	}

	var resData struct {
		Data struct {
			MessageID int32 `json:"message_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp, &resData); err != nil {
		slog.Debug("qq send response parse failed", "err", err)
		return "", err
	}
	msgID := strconv.Itoa(int(resData.Data.MessageID))
	slog.Debug("qq message sent", "message_id", msgID)
	return msgID, nil
}

func (q *QQAdapter) callAPI(action string, params interface{}) ([]byte, error) {
	q.wsMu.Lock()
	conn := q.wsConn
	q.wsMu.Unlock()
	if conn == nil {
		return nil, fmt.Errorf("ws not connected")
	}

	echo := strconv.FormatInt(time.Now().UnixNano(), 10)
	payload := map[string]interface{}{"action": action, "params": params, "echo": echo}
	respChan := make(chan []byte, 1)
	q.echoMap.Store(echo, respChan)
	defer q.echoMap.Delete(echo)

	q.wsMu.Lock()
	err := conn.WriteJSON(payload)
	q.wsMu.Unlock()
	if err != nil {
		return nil, err
	}

	select {
	case res := <-respChan:
		return res, nil
	case <-time.After(10 * time.Second):
		return nil, fmt.Errorf("timeout")
	}
}

func (q *QQAdapter) EditMessage(ctx context.Context, msg *internal.OutMessage) error { return nil }

func (q *QQAdapter) DeleteMessage(ctx context.Context, roomID, msgID string) error {
	mid, _ := strconv.Atoi(msgID)
	_, err := q.callAPI("delete_msg", map[string]interface{}{"message_id": mid})
	return err
}

func (q *QQAdapter) UploadFile(ctx context.Context, data []byte, filename string) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func (q *QQAdapter) CreateRoom(ctx context.Context, info *internal.RoomInfo) (string, error) {
	if q.cfg.Group == "" {
		return "", fmt.Errorf("qq group not configured")
	}
	return q.cfg.Group, nil
}

func (q *QQAdapter) GetRoomInfo(ctx context.Context, roomID string) (*internal.RoomInfo, error) {
	gid, err := strconv.ParseInt(roomID, 10, 64)
	if err != nil {
		return &internal.RoomInfo{ID: roomID, Name: "QQ Group"}, nil
	}

	info := &internal.RoomInfo{
		ID:        roomID,
		Name:      roomID,
		AvatarURL: fmt.Sprintf("https://p.qlogo.cn/gh/%d/%d/100", gid, gid),
	}

	resp, err := q.callAPI("get_group_info", map[string]interface{}{"group_id": gid})
	if err == nil {
		var d struct {
			Data struct {
				GroupName string `json:"group_name"`
			} `json:"data"`
		}
		if json.Unmarshal(resp, &d) == nil && d.Data.GroupName != "" {
			info.Name = d.Data.GroupName
		}
	}
	return info, nil
}
