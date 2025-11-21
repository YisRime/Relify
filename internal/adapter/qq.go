package adapter

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"Relify/internal"

	"github.com/gorilla/websocket"
	"gopkg.in/yaml.v3"
)

type QQConfig struct {
	Protocol    string `yaml:"protocol"`
	APIURL      string `yaml:"api_url"`
	ListenAddr  string `yaml:"listen_addr"`
	AccessToken string `yaml:"access_token"`
	Secret      string `yaml:"secret"`
	BridgeGroup string `yaml:"bridge_group"`
}

type QQAdapter struct {
	cfg        QQConfig
	handler    internal.InboundHandler
	httpServer *http.Server
	wsConn     *websocket.Conn
	wsMu       sync.Mutex
	httpClient *http.Client
	selfID     string
	selfIDMu   sync.RWMutex
	echoMap    sync.Map
}

func NewQQAdapter(node yaml.Node, handler internal.InboundHandler) (*QQAdapter, error) {
	var cfg QQConfig
	if err := node.Decode(&cfg); err != nil {
		return nil, err
	}
	if cfg.Protocol == "" {
		cfg.Protocol = "http"
	}

	return &QQAdapter{
		cfg:        cfg,
		handler:    handler,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func (q *QQAdapter) Name() string { return "qq" }

func (q *QQAdapter) GetBotUserID() string {
	q.selfIDMu.RLock()
	defer q.selfIDMu.RUnlock()
	if q.selfID == "" {
		return "0"
	}
	return q.selfID
}

func (q *QQAdapter) GetRouteType() internal.RouteType { return internal.RouteTypeAggregate }

func (q *QQAdapter) Start(ctx context.Context) error {
	switch q.cfg.Protocol {
	case "http":
		return q.startHTTP(ctx)
	case "ws":
		return q.startWS(ctx)
	case "reverse_ws":
		return q.startReverseWS(ctx)
	default:
		return fmt.Errorf("unknown protocol: %s", q.cfg.Protocol)
	}
}

func (q *QQAdapter) Stop(ctx context.Context) error {
	if q.httpServer != nil {
		return q.httpServer.Shutdown(ctx)
	}
	q.wsMu.Lock()
	if q.wsConn != nil {
		q.wsConn.Close()
	}
	q.wsMu.Unlock()
	return nil
}

func (q *QQAdapter) fetchBotInfo() {
	go func() {
		for i := 0; i < 10; i++ {
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
					return
				}
			}
			time.Sleep(3 * time.Second)
		}
	}()
}

func (q *QQAdapter) startHTTP(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", q.handleHTTPEvent)
	q.httpServer = &http.Server{Addr: q.cfg.ListenAddr, Handler: mux}

	go func() {
		slog.Info("qq http listening", "addr", q.cfg.ListenAddr)
		q.fetchBotInfo()
		if err := q.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("qq http server error", "err", err)
		}
	}()
	return nil
}

func (q *QQAdapter) handleHTTPEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if q.cfg.Secret != "" {
		sig := r.Header.Get("X-Signature")
		if sig == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		mac := hmac.New(sha1.New, []byte(q.cfg.Secret))
		mac.Write(body)
		expected := "sha1=" + hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(sig), []byte(expected)) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
	}
	q.processEventBytes(context.Background(), body)
	w.WriteHeader(http.StatusNoContent)
}

func (q *QQAdapter) startWS(ctx context.Context) error {
	header := http.Header{}
	if q.cfg.AccessToken != "" {
		header.Set("Authorization", "Bearer "+q.cfg.AccessToken)
	}
	conn, _, err := websocket.DefaultDialer.Dial(q.cfg.APIURL, header)
	if err != nil {
		return err
	}
	q.wsMu.Lock()
	q.wsConn = conn
	q.wsMu.Unlock()

	go q.wsListenLoop()
	q.fetchBotInfo()
	return nil
}

func (q *QQAdapter) startReverseWS(ctx context.Context) error {
	mux := http.NewServeMux()
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if q.cfg.AccessToken != "" {
			auth := r.Header.Get("Authorization")
			token := strings.TrimPrefix(auth, "Bearer ")
			if token != q.cfg.AccessToken && r.URL.Query().Get("access_token") != q.cfg.AccessToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			slog.Error("ws upgrade failed", "err", err)
			return
		}
		q.wsMu.Lock()
		if q.wsConn != nil {
			q.wsConn.Close()
		}
		q.wsConn = conn
		q.wsMu.Unlock()
		slog.Info("qq reverse ws connected", "remote", r.RemoteAddr)
		go q.wsListenLoop()
		q.fetchBotInfo()
	})

	q.httpServer = &http.Server{Addr: q.cfg.ListenAddr, Handler: mux}
	go func() {
		slog.Info("qq reverse ws listening", "addr", q.cfg.ListenAddr)
		if err := q.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("qq reverse ws server error", "err", err)
		}
	}()
	return nil
}

func (q *QQAdapter) wsListenLoop() {
	for {
		q.wsMu.Lock()
		conn := q.wsConn
		q.wsMu.Unlock()
		if conn == nil {
			time.Sleep(time.Second)
			continue
		}
		_, message, err := conn.ReadMessage()
		if err != nil {
			slog.Error("ws read error", "err", err)
			q.wsMu.Lock()
			if q.wsConn == conn {
				q.wsConn.Close()
				q.wsConn = nil
			}
			q.wsMu.Unlock()
			return
		}
		var resp struct {
			Echo string `json:"echo"`
		}
		if json.Unmarshal(message, &resp) == nil && resp.Echo != "" {
			if ch, ok := q.echoMap.Load(resp.Echo); ok {
				ch.(chan []byte) <- message
			}
			continue
		}
		q.processEventBytes(context.Background(), message)
	}
}

type onebotEvent struct {
	PostType    string          `json:"post_type"`
	MessageType string          `json:"message_type"`
	SubType     string          `json:"sub_type"`
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
		return
	}
	if evt.PostType != "message" || evt.MessageType != "group" {
		return
	}

	bridgeGroupID, err := strconv.ParseInt(q.cfg.BridgeGroup, 10, 64)
	if err != nil || evt.GroupID != bridgeGroupID {
		return
	}

	roomID := internal.AggregateRoomKey

	senderName := evt.Sender.Card
	if senderName == "" {
		senderName = evt.Sender.Nickname
	}
	if senderName == "" {
		senderName = strconv.FormatInt(evt.UserID, 10)
	}

	relifyEvt := &internal.Event{
		ID:        strconv.Itoa(int(evt.MessageID)),
		Action:    internal.ActionCreate,
		Platform:  q.Name(),
		Timestamp: time.Unix(evt.Time, 0),
		Message: &internal.Message{
			ID:           strconv.Itoa(int(evt.MessageID)),
			RoomID:       roomID,
			SenderID:     strconv.FormatInt(evt.UserID, 10),
			SenderName:   senderName,
			SenderAvatar: fmt.Sprintf("https://q1.qlogo.cn/g?b=qq&nk=%d&s=640", evt.UserID),
			Body:         q.parseOneBotMessage(evt.Message),
		},
	}
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
	if q.cfg.BridgeGroup == "" {
		return "", fmt.Errorf("bridge_group not configured")
	}

	// Convert BridgeGroup string to int64 for API call
	bridgeGroupID, err := strconv.ParseInt(q.cfg.BridgeGroup, 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid bridge_group: %w", err)
	}

	params := map[string]interface{}{"group_id": bridgeGroupID}

	var obMsg []map[string]interface{}
	if msg.Message.ReplyToID != "" {
		rid, _ := strconv.Atoi(msg.Message.ReplyToID)
		obMsg = append(obMsg, map[string]interface{}{"type": "reply", "data": map[string]string{"id": strconv.Itoa(rid)}})
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

	resp, err := q.callAPI("send_group_msg", params)
	if err != nil {
		return "", err
	}

	var resData struct {
		Data struct {
			MessageID int32 `json:"message_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp, &resData); err != nil {
		return "", err
	}
	return strconv.Itoa(int(resData.Data.MessageID)), nil
}

func (q *QQAdapter) callAPI(action string, params interface{}) ([]byte, error) {
	if q.cfg.Protocol == "http" {
		data, _ := json.Marshal(params)
		u, _ := url.JoinPath(q.cfg.APIURL, action)
		req, _ := http.NewRequest("POST", u, bytes.NewBuffer(data))
		req.Header.Set("Content-Type", "application/json")
		if q.cfg.AccessToken != "" {
			req.Header.Set("Authorization", "Bearer "+q.cfg.AccessToken)
		}
		resp, err := q.httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		return io.ReadAll(resp.Body)
	}

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
	return "", fmt.Errorf("not supported")
}

func (q *QQAdapter) GetRoomInfo(ctx context.Context, roomID string) (*internal.RoomInfo, error) {
	if q.cfg.BridgeGroup == "" {
		return &internal.RoomInfo{ID: roomID, Name: "QQ Bridge"}, nil
	}

	gid, err := strconv.ParseInt(q.cfg.BridgeGroup, 10, 64)
	if err != nil {
		return &internal.RoomInfo{ID: roomID, Name: "QQ Bridge"}, nil
	}

	info := &internal.RoomInfo{
		ID:        roomID,
		Name:      q.cfg.BridgeGroup,
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
