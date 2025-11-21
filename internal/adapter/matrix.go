package adapter

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"Relify/internal"

	"gopkg.in/yaml.v3"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/appservice"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type MatrixConfig struct {
	HomeserverURL  string `yaml:"homeserver_url"`
	Domain         string `yaml:"domain"`
	ASToken        string `yaml:"as_token"`
	HSToken        string `yaml:"hs_token"`
	BotLocalpart   string `yaml:"bot_localpart"`
	Listen         string `yaml:"listen"`
	UserNamespace  string `yaml:"user_namespace"`
	AutoInviteUser string `yaml:"auto_invite_user"`
}

type cachedProfile struct {
	registered bool
	expiresAt  int64
}

type MatrixAdapter struct {
	cfg          MatrixConfig
	handler      internal.InboundHandler
	as           *appservice.AppService
	server       *http.Server
	botUserID    id.UserID
	profileCache sync.Map
}

func NewMatrixAdapter(node yaml.Node, handler internal.InboundHandler) (*MatrixAdapter, error) {
	var cfg MatrixConfig
	if err := node.Decode(&cfg); err != nil {
		return nil, err
	}

	if cfg.UserNamespace == "" {
		cfg.UserNamespace = "relify_"
	}
	u, err := url.Parse(cfg.HomeserverURL)
	if err != nil {
		return nil, err
	}

	if cfg.Domain == "" {
		cfg.Domain = u.Hostname()
	}
	cfg.HomeserverURL = strings.TrimRight(cfg.HomeserverURL, "/")

	as := appservice.Create()
	if err := as.SetHomeserverURL(cfg.HomeserverURL); err != nil {
		return nil, err
	}
	as.Registration = &appservice.Registration{
		ID:              "relify-bridge",
		URL:             cfg.Listen,
		AppToken:        cfg.ASToken,
		ServerToken:     cfg.HSToken,
		SenderLocalpart: cfg.BotLocalpart,
		Namespaces: appservice.Namespaces{
			UserIDs: appservice.NamespaceList{{Exclusive: true, Regex: fmt.Sprintf("@%s.*", cfg.UserNamespace)}},
		},
	}
	as.HomeserverDomain = cfg.Domain

	return &MatrixAdapter{
		cfg:       cfg,
		handler:   handler,
		as:        as,
		botUserID: id.NewUserID(cfg.BotLocalpart, cfg.Domain),
	}, nil
}

func (m *MatrixAdapter) Name() string                     { return "matrix" }
func (m *MatrixAdapter) GetBotUserID() string             { return m.botUserID.String() }
func (m *MatrixAdapter) GetRouteType() internal.RouteType { return internal.RouteTypeMirror }

func (m *MatrixAdapter) Start(ctx context.Context) error {
	go m.handleEvents()
	m.server = &http.Server{Addr: m.as.Registration.URL, Handler: m.as.Router}
	if u, err := url.Parse(m.cfg.Listen); err == nil {
		m.server.Addr = u.Host
	}

	go func() {
		slog.Info("matrix listening", "addr", m.server.Addr)
		if err := m.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("matrix server error", "err", err)
		}
	}()

	go func() {
		time.Sleep(time.Second)
		m.as.BotIntent().EnsureRegistered(context.Background())
	}()
	return nil
}

func (m *MatrixAdapter) Stop(ctx context.Context) error {
	if m.server != nil {
		return m.server.Shutdown(ctx)
	}
	return nil
}

func (m *MatrixAdapter) handleEvents() {
	slog.Debug("matrix event handler started")
	for evt := range m.as.Events {
		slog.Debug("matrix event received", "type", evt.Type.Type, "room", evt.RoomID, "sender", evt.Sender)
		switch evt.Type {
		case event.EventMessage:
			m.handleMessage(evt)
		case event.EventRedaction:
			m.handleRedaction(evt)
		case event.StateMember:
			m.handleMembership(evt)
		default:
			slog.Debug("matrix event ignored", "type", evt.Type.Type)
		}
	}
	slog.Debug("matrix event handler stopped")
}

func (m *MatrixAdapter) handleMessage(evt *event.Event) {
	slog.Debug("matrix handling message", "event_id", evt.ID, "sender", evt.Sender, "room", evt.RoomID)

	if m.isMe(evt.Sender) {
		slog.Debug("matrix message ignored", "reason", "sent by bot")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	content := evt.Content.AsMessage()
	slog.Debug("matrix message content", "msgtype", content.MsgType, "body", content.Body)

	relifyEvt := &internal.Event{
		Platform: m.Name(), Timestamp: time.UnixMilli(evt.Timestamp),
		Message: &internal.Message{RoomID: evt.RoomID.String(), SenderID: evt.Sender.String(), SenderName: evt.Sender.String(), Body: []internal.Segment{}},
	}

	if content.NewContent != nil && content.RelatesTo != nil && content.RelatesTo.Type == event.RelReplace {
		relifyEvt.Action = internal.ActionUpdate
		relifyEvt.Message.ID = content.RelatesTo.EventID.String()
		content = content.NewContent
	} else {
		relifyEvt.Action = internal.ActionCreate
		relifyEvt.ID, relifyEvt.Message.ID = evt.ID.String(), evt.ID.String()
	}

	if content.RelatesTo != nil && content.RelatesTo.InReplyTo != nil {
		relifyEvt.Message.ReplyToID = content.RelatesTo.InReplyTo.EventID.String()
	}

	if content.URL != "" {
		mxc, _ := content.URL.Parse()
		httpURL := fmt.Sprintf("%s/_matrix/media/v3/download/%s/%s", m.cfg.HomeserverURL, mxc.Homeserver, mxc.FileID)

		segType := internal.TypeFile
		switch content.MsgType {
		case event.MsgImage:
			segType = internal.TypeImage
		case event.MsgVideo:
			segType = internal.TypeVideo
		case event.MsgAudio:
			segType = internal.TypeAudio
		}

		relifyEvt.Message.Body = append(relifyEvt.Message.Body, internal.Segment{
			Type:     segType,
			Data:     map[string]interface{}{"url": httpURL, "name": content.Body, "size": content.Info.Size},
			Fallback: fmt.Sprintf("[%s: %s]", segType, content.Body),
		})
	}

	if content.Body != "" && (content.MsgType == event.MsgText || content.MsgType == event.MsgNotice || content.URL == "") {
		text := content.Body
		if content.RelatesTo != nil {
			text = m.stripReplyFallback(text)
		}
		relifyEvt.Message.Body = append(relifyEvt.Message.Body, internal.Segment{
			Type: internal.TypeText, Data: map[string]interface{}{"text": text},
		})
	}

	m.handler.HandleEvent(ctx, relifyEvt)
}

func (m *MatrixAdapter) SendMessage(ctx context.Context, msg *internal.OutMessage) (string, error) {
	slog.Debug("matrix sending message", "room", msg.TargetRoomID, "sender", msg.Message.SenderName)

	intent := m.getGhostIntent(msg.Message.SenderID, msg.Message.SenderName, msg.Message.SenderAvatar)
	content := m.renderContent(msg.Message)

	if msg.Message.ReplyToID != "" {
		slog.Debug("matrix adding reply", "reply_to", msg.Message.ReplyToID)
		content.RelatesTo = &event.RelatesTo{InReplyTo: &event.InReplyTo{EventID: id.EventID(msg.Message.ReplyToID)}}
	}

	slog.Debug("matrix calling send message event")
	resp, err := intent.SendMessageEvent(ctx, id.RoomID(msg.TargetRoomID), event.EventMessage, content)
	if err != nil {
		slog.Debug("matrix send failed", "err", err)
		return "", err
	}
	slog.Debug("matrix message sent", "event_id", resp.EventID)
	return resp.EventID.String(), nil
}

func (m *MatrixAdapter) EditMessage(ctx context.Context, msg *internal.OutMessage) error {
	intent := m.getGhostIntent(msg.Message.SenderID, msg.Message.SenderName, msg.Message.SenderAvatar)
	newContent := m.renderContent(msg.Message)
	content := &event.MessageEventContent{
		MsgType: event.MsgText, Body: "* " + newContent.Body, NewContent: newContent,
		RelatesTo: &event.RelatesTo{Type: event.RelReplace, EventID: id.EventID(msg.TargetMessageID)},
	}
	_, err := intent.SendMessageEvent(ctx, id.RoomID(msg.TargetRoomID), event.EventMessage, content)
	return err
}

func (m *MatrixAdapter) renderContent(msg *internal.Message) *event.MessageEventContent {
	var htmlB, plainB strings.Builder
	for _, seg := range msg.Body {
		switch seg.Type {
		case internal.TypeText:
			text := internal.GetString(seg.Data, "text")
			htmlB.WriteString(strings.ReplaceAll(html.EscapeString(text), "\n", "<br>"))
			plainB.WriteString(text + "\n")
		case internal.TypeImage, internal.TypeVideo, internal.TypeFile:
			url, name := internal.GetString(seg.Data, "url"), internal.GetString(seg.Data, "name")
			if name == "" {
				name = "File"
			}
			htmlB.WriteString(fmt.Sprintf(`<a href="%s">%s</a><br>`, url, html.EscapeString(name)))
			if seg.Type == internal.TypeImage {
				htmlB.WriteString(fmt.Sprintf(`<img src="%s" height="200"><br>`, url))
			}
			plainB.WriteString(fmt.Sprintf("[%s: %s]\n", seg.Type, url))
		default:
			val := seg.Fallback
			if val == "" {
				val = fmt.Sprintf("[%s]", seg.Type)
			}
			htmlB.WriteString("<blockquote>" + html.EscapeString(val) + "</blockquote>")
			plainB.WriteString(val + "\n")
		}
	}
	return &event.MessageEventContent{
		MsgType: event.MsgText, Body: strings.TrimSpace(plainB.String()),
		Format: event.FormatHTML, FormattedBody: htmlB.String(),
	}
}

func (m *MatrixAdapter) DeleteMessage(ctx context.Context, roomID, msgID string) error {
	_, err := m.as.BotIntent().RedactEvent(ctx, id.RoomID(roomID), id.EventID(msgID), mautrix.ReqRedact{Reason: "Bridged delete"})
	return err
}

func (m *MatrixAdapter) UploadFile(ctx context.Context, data []byte, filename string) (string, error) {
	resp, err := m.as.BotIntent().UploadBytes(ctx, data, "application/octet-stream")
	if err != nil {
		return "", err
	}
	return string(resp.ContentURI.CUString()), nil
}

func (m *MatrixAdapter) CreateRoom(ctx context.Context, info *internal.RoomInfo) (string, error) {
	slog.Debug("matrix creating room", "name", info.Name, "topic", info.Topic)

	intent := m.as.BotIntent()
	req := &mautrix.ReqCreateRoom{
		Name: info.Name, Topic: info.Topic, Preset: "private_chat", Visibility: "private",
		CreationContent: map[string]interface{}{"m.federate": true},
	}
	if alias := m.sanitizeLocalpart(info.Name); alias != "" {
		req.RoomAliasName = alias
		slog.Debug("matrix using room alias", "alias", alias)
	}

	resp, err := intent.CreateRoom(ctx, req)
	if err != nil && strings.Contains(err.Error(), "taken") {
		slog.Debug("matrix alias taken, retrying without alias")
		req.RoomAliasName = ""
		resp, err = intent.CreateRoom(ctx, req)
	}
	if err != nil {
		slog.Debug("matrix room creation failed", "err", err)
		return "", err
	}

	slog.Debug("matrix room created", "room_id", resp.RoomID)

	if m.cfg.AutoInviteUser != "" {
		slog.Debug("matrix auto-inviting user", "user", m.cfg.AutoInviteUser)
		intent.InviteUser(ctx, resp.RoomID, &mautrix.ReqInviteUser{UserID: id.UserID(m.cfg.AutoInviteUser)})
	}
	return resp.RoomID.String(), nil
}

func (m *MatrixAdapter) GetRoomInfo(ctx context.Context, roomID string) (*internal.RoomInfo, error) {
	rid := id.RoomID(roomID)
	info := &internal.RoomInfo{ID: roomID, Name: roomID}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		var nameEvent event.RoomNameEventContent
		if err := m.as.BotIntent().StateEvent(ctx, rid, event.StateRoomName, "", &nameEvent); err == nil && nameEvent.Name != "" {
			info.Name = nameEvent.Name
		}
	}()
	go func() {
		defer wg.Done()
		var avatarEvent event.RoomAvatarEventContent
		if err := m.as.BotIntent().StateEvent(ctx, rid, event.StateRoomAvatar, "", &avatarEvent); err == nil && avatarEvent.URL != "" {
			mxc, _ := avatarEvent.URL.Parse()
			info.AvatarURL = fmt.Sprintf("%s/_matrix/media/v3/download/%s/%s",
				strings.TrimRight(m.cfg.HomeserverURL, "/"), mxc.Homeserver, mxc.FileID)
		}
	}()
	wg.Wait()
	return info, nil
}

func (m *MatrixAdapter) handleRedaction(evt *event.Event) {
	if !m.isMe(evt.Sender) {
		m.handler.HandleEvent(context.Background(), &internal.Event{
			Platform: m.Name(), Action: internal.ActionDelete, Timestamp: time.UnixMilli(evt.Timestamp),
			Message: &internal.Message{ID: evt.Redacts.String(), RoomID: evt.RoomID.String()},
		})
	}
}

func (m *MatrixAdapter) handleMembership(evt *event.Event) {
	if c := evt.Content.AsMember(); c.Membership == event.MembershipInvite && *evt.StateKey == m.botUserID.String() {
		m.as.BotIntent().JoinRoom(context.Background(), evt.RoomID.String(), nil)
	}
}

func (m *MatrixAdapter) getGhostIntent(uid, name, avatar string) *appservice.IntentAPI {
	local := m.sanitizeLocalpart(uid)
	mxid := id.NewUserID(m.cfg.UserNamespace+local, m.cfg.Domain)
	intent := m.as.Intent(mxid)

	now := time.Now().Unix()
	if v, ok := m.profileCache.Load(mxid); ok {
		if p := v.(*cachedProfile); p.registered && now < p.expiresAt {
			return intent
		}
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		intent.EnsureRegistered(ctx)
		intent.SetDisplayName(ctx, name)
		if strings.HasPrefix(avatar, "mxc://") {
			intent.SetAvatarURL(ctx, id.ContentURIString(avatar).ParseOrIgnore())
		}
	}()
	m.profileCache.Store(mxid, &cachedProfile{registered: true, expiresAt: now + 3600})
	return intent
}

func (m *MatrixAdapter) sanitizeLocalpart(s string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '.' || r == '_' {
			return r
		}
		return '_'
	}, strings.ToLower(s))
}

func (m *MatrixAdapter) stripReplyFallback(body string) string {
	lines := strings.Split(body, "\n")
	var output []string
	isFallback := true
	for _, line := range lines {
		clean := strings.TrimSpace(line)
		if isFallback && (strings.HasPrefix(clean, ">") || clean == "") {
			continue
		}
		isFallback = false
		output = append(output, line)
	}
	return strings.Join(output, "\n")
}

func (m *MatrixAdapter) isMe(sender id.UserID) bool {
	return sender == m.botUserID || strings.HasPrefix(sender.String(), "@"+m.cfg.UserNamespace)
}
