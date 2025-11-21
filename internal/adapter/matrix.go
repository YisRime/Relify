package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"Relify/internal"

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

type MatrixAdapter struct {
	cfg          MatrixConfig
	bindAddr     string
	handler      internal.InboundHandler
	as           *appservice.AppService
	server       *http.Server
	botUserID    id.UserID
	profileCache sync.Map
}

func NewMatrixAdapter(rawCfg map[string]interface{}, handler internal.InboundHandler) (*MatrixAdapter, error) {
	cfgBytes, _ := json.Marshal(rawCfg)
	var cfg MatrixConfig
	if err := json.Unmarshal(cfgBytes, &cfg); err != nil {
		return nil, err
	}

	if cfg.UserNamespace == "" {
		cfg.UserNamespace = "relify_"
	}
	if cfg.Listen == "" {
		return nil, fmt.Errorf("matrix listen address is required")
	}
	listenURL, err := url.Parse(cfg.Listen)
	if err != nil {
		return nil, fmt.Errorf("invalid listen URL: %w", err)
	}

	if cfg.Domain == "" {
		u, err := url.Parse(cfg.HomeserverURL)
		if err != nil {
			return nil, fmt.Errorf("invalid homeserver_url: %w", err)
		}
		cfg.Domain = u.Hostname()
	}
	cfg.HomeserverURL = strings.TrimRight(cfg.HomeserverURL, "/")
	botUserID := id.NewUserID(cfg.BotLocalpart, cfg.Domain)

	reg := &appservice.Registration{
		ID:              "relify-bridge",
		URL:             cfg.Listen,
		AppToken:        cfg.ASToken,
		ServerToken:     cfg.HSToken,
		SenderLocalpart: cfg.BotLocalpart,
		Namespaces: appservice.Namespaces{
			UserIDs: appservice.NamespaceList{{
				Exclusive: true,
				Regex:     fmt.Sprintf("@%s.*", cfg.UserNamespace),
			}},
		},
	}

	as := appservice.Create()
	as.Registration = reg
	as.HomeserverDomain = cfg.Domain

	adapter := &MatrixAdapter{
		cfg:       cfg,
		bindAddr:  listenURL.Host,
		handler:   handler,
		as:        as,
		botUserID: botUserID,
	}
	return adapter, nil
}

func (m *MatrixAdapter) Name() string                     { return "matrix" }
func (m *MatrixAdapter) GetBotUserID() string             { return m.botUserID.String() }
func (m *MatrixAdapter) GetRouteType() internal.RouteType { return internal.RouteTypeMirror }

func (m *MatrixAdapter) Start(ctx context.Context) error {
	go m.handleEvents()
	m.server = &http.Server{Addr: m.bindAddr, Handler: m.as.Router}
	go func() {
		slog.Info("matrix appservice listening", "url", m.cfg.Listen)
		if err := m.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("matrix http server error", "err", err)
		}
	}()
	go func() {
		time.Sleep(1 * time.Second)
		if err := m.as.BotIntent().EnsureRegistered(context.Background()); err != nil {
			slog.Warn("bot reg check failed", "err", err)
		}
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
	for evt := range m.as.Events {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		switch evt.Type {
		case event.EventMessage:
			m.handleMessage(ctx, evt)
		case event.EventRedaction:
			m.handleRedaction(ctx, evt)
		case event.StateMember:
			m.handleMembership(ctx, evt)
		}
		cancel()
	}
}

func (m *MatrixAdapter) handleMessage(ctx context.Context, evt *event.Event) {
	if m.isMe(evt.Sender) {
		return
	}

	content := evt.Content.AsMessage()
	relifyEvt := &internal.Event{
		Platform:  m.Name(),
		Timestamp: time.UnixMilli(evt.Timestamp),
		Message: &internal.Message{
			RoomID:     evt.RoomID.String(),
			SenderID:   evt.Sender.String(),
			SenderName: evt.Sender.String(),
			Body:       []internal.Segment{},
		},
	}

	if content.NewContent != nil && content.RelatesTo != nil && content.RelatesTo.Type == event.RelReplace {
		relifyEvt.Action = internal.ActionUpdate
		relifyEvt.Message.ID = content.RelatesTo.EventID.String()
		content = content.NewContent
	} else {
		relifyEvt.Action = internal.ActionCreate
		relifyEvt.ID = evt.ID.String()
		relifyEvt.Message.ID = evt.ID.String()
	}

	if content.RelatesTo != nil && content.RelatesTo.InReplyTo != nil {
		relifyEvt.Message.ReplyToID = content.RelatesTo.InReplyTo.EventID.String()
	}

	switch content.MsgType {
	case event.MsgImage, event.MsgVideo, event.MsgAudio, event.MsgFile:
		if content.URL != "" {
			mxc, _ := content.URL.Parse()
			httpURL := fmt.Sprintf("%s/_matrix/media/v3/download/%s/%s",
				m.cfg.HomeserverURL, mxc.Homeserver, mxc.FileID)

			segType := internal.TypeFile
			if content.MsgType == event.MsgImage {
				segType = internal.TypeImage
			} else if content.MsgType == event.MsgVideo {
				segType = internal.TypeVideo
			} else if content.MsgType == event.MsgAudio {
				segType = internal.TypeAudio
			}

			relifyEvt.Message.Body = append(relifyEvt.Message.Body, internal.Segment{
				Type: segType,
				Data: map[string]interface{}{
					"url":  httpURL,
					"name": content.Body,
					"mxc":  string(content.URL),
					"size": content.Info.Size,
				},
				Fallback: fmt.Sprintf("[%s: %s]", segType, content.Body),
			})
		}
	}

	isMedia := content.MsgType == event.MsgImage || content.MsgType == event.MsgVideo ||
		content.MsgType == event.MsgAudio || content.MsgType == event.MsgFile

	if !isMedia || (isMedia && content.Body != "" && content.Body != content.FileName) {
		textBody := content.Body
		if content.RelatesTo != nil && content.RelatesTo.InReplyTo != nil {
			textBody = m.stripReplyFallback(textBody)
		}
		if textBody != "" {
			relifyEvt.Message.Body = append(relifyEvt.Message.Body, internal.Segment{
				Type: internal.TypeText,
				Data: map[string]interface{}{
					"text":   textBody,
					"format": "plain",
				},
			})
		}
	}

	if err := m.handler.HandleEvent(ctx, relifyEvt); err != nil {
		slog.Error("handler error", "err", err)
	}
}

func (m *MatrixAdapter) SendMessage(ctx context.Context, msg *internal.OutMessage) (string, error) {
	intent := m.getGhostIntent(msg.Message.SenderID, msg.Message.SenderName, msg.Message.SenderAvatar)
	roomID := id.RoomID(msg.TargetRoomID)
	var htmlBuilder strings.Builder
	var plainBuilder strings.Builder

	for _, seg := range msg.Message.Body {
		switch seg.Type {
		case internal.TypeText:
			text := internal.GetString(seg.Data, "text")
			safeText := html.EscapeString(text)
			htmlBuilder.WriteString(strings.ReplaceAll(safeText, "\n", "<br>"))
			plainBuilder.WriteString(text)
			plainBuilder.WriteString("\n")

		case internal.TypeImage, internal.TypeVideo, internal.TypeAudio, internal.TypeFile:
			urlStr := internal.GetString(seg.Data, "url")
			name := internal.GetString(seg.Data, "name")
			if name == "" {
				name = "Attachment"
			}
			htmlBuilder.WriteString(fmt.Sprintf(`<a href="%s">[File: %s]</a><br>`, urlStr, name))
			if seg.Type == internal.TypeImage {
				htmlBuilder.WriteString(fmt.Sprintf(`<img src="%s" alt="%s" height="200"><br>`, urlStr, name))
			}
			plainBuilder.WriteString(fmt.Sprintf("[%s: %s]\n", seg.Type, urlStr))

		case internal.TypeMention:
			name := internal.GetString(seg.Data, "name")
			htmlBuilder.WriteString("<b>@" + html.EscapeString(name) + "</b> ")
			plainBuilder.WriteString("@" + name + " ")

		default:
			val := seg.Fallback
			if val == "" {
				val = fmt.Sprintf("[%s]", seg.Type)
			}
			htmlBuilder.WriteString("<blockquote>" + html.EscapeString(val) + "</blockquote>")
			plainBuilder.WriteString(val + "\n")
		}
	}

	content := &event.MessageEventContent{
		MsgType:       event.MsgText,
		Body:          strings.TrimSpace(plainBuilder.String()),
		Format:        event.FormatHTML,
		FormattedBody: htmlBuilder.String(),
	}

	if msg.Message.ReplyToID != "" {
		content.RelatesTo = &event.RelatesTo{
			InReplyTo: &event.InReplyTo{
				EventID: id.EventID(msg.Message.ReplyToID),
			},
		}
	}

	resp, err := intent.SendMessageEvent(ctx, roomID, event.EventMessage, content)
	if err != nil {
		return "", err
	}
	return resp.EventID.String(), nil
}

func (m *MatrixAdapter) EditMessage(ctx context.Context, msg *internal.OutMessage) error {
	intent := m.getGhostIntent(msg.Message.SenderID, msg.Message.SenderName, msg.Message.SenderAvatar)
	roomID := id.RoomID(msg.TargetRoomID)
	var htmlBuilder strings.Builder
	var plainBuilder strings.Builder

	for _, seg := range msg.Message.Body {
		switch seg.Type {
		case internal.TypeText:
			text := internal.GetString(seg.Data, "text")
			safeText := html.EscapeString(text)
			htmlBuilder.WriteString(strings.ReplaceAll(safeText, "\n", "<br>"))
			plainBuilder.WriteString(text)
			plainBuilder.WriteString("\n")
		case internal.TypeImage, internal.TypeVideo, internal.TypeFile:
			urlStr := internal.GetString(seg.Data, "url")
			name := internal.GetString(seg.Data, "name")
			if name == "" {
				name = "Attachment"
			}
			htmlBuilder.WriteString(fmt.Sprintf(`<a href="%s">[File: %s]</a><br>`, urlStr, name))
			if seg.Type == internal.TypeImage {
				htmlBuilder.WriteString(fmt.Sprintf(`<img src="%s" alt="%s" height="200"><br>`, urlStr, name))
			}
			plainBuilder.WriteString(fmt.Sprintf("[%s: %s]\n", seg.Type, urlStr))
		case internal.TypeMention:
			name := internal.GetString(seg.Data, "name")
			htmlBuilder.WriteString("<b>@" + html.EscapeString(name) + "</b> ")
			plainBuilder.WriteString("@" + name + " ")
		default:
			val := seg.Fallback
			if val == "" {
				val = fmt.Sprintf("[%s]", seg.Type)
			}
			htmlBuilder.WriteString("<blockquote>" + html.EscapeString(val) + "</blockquote>")
			plainBuilder.WriteString(val + "\n")
		}
	}

	newContent := &event.MessageEventContent{
		MsgType:       event.MsgText,
		Body:          strings.TrimSpace(plainBuilder.String()),
		Format:        event.FormatHTML,
		FormattedBody: htmlBuilder.String(),
	}

	content := &event.MessageEventContent{
		MsgType:    event.MsgText,
		Body:       "* " + newContent.Body,
		NewContent: newContent,
		RelatesTo: &event.RelatesTo{
			Type:    event.RelReplace,
			EventID: id.EventID(msg.TargetMessageID),
		},
	}

	_, err := intent.SendMessageEvent(ctx, roomID, event.EventMessage, content)
	return err
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
	intent := m.as.BotIntent()
	req := &mautrix.ReqCreateRoom{
		Name:       info.Name,
		Topic:      info.Topic,
		Preset:     "private_chat",
		Visibility: "private",
		IsDirect:   false,
		CreationContent: map[string]interface{}{
			"m.federate": true,
		},
	}
	alias := m.sanitizeLocalpart(info.Name)
	if alias != "" {
		req.RoomAliasName = alias
	}

	resp, err := intent.CreateRoom(ctx, req)
	if err != nil {
		if strings.Contains(err.Error(), "taken") {
			req.RoomAliasName = ""
			resp, err = intent.CreateRoom(ctx, req)
		}
		if err != nil {
			return "", err
		}
	}

	if m.cfg.AutoInviteUser != "" {
		go func() {
			targetUser := id.UserID(m.cfg.AutoInviteUser)
			intent.InviteUser(context.Background(), resp.RoomID, &mautrix.ReqInviteUser{UserID: targetUser})
		}()
	}
	return resp.RoomID.String(), nil
}

func (m *MatrixAdapter) GetRoomInfo(ctx context.Context, roomID string) (*internal.RoomInfo, error) {
	return &internal.RoomInfo{ID: roomID, Name: "Matrix Room"}, nil
}

func (m *MatrixAdapter) handleRedaction(ctx context.Context, evt *event.Event) {
	if m.isMe(evt.Sender) {
		return
	}
	relifyEvt := &internal.Event{
		Platform:  m.Name(),
		Action:    internal.ActionDelete,
		Timestamp: time.UnixMilli(evt.Timestamp),
		ID:        evt.Redacts.String(),
		Message: &internal.Message{
			ID:     evt.Redacts.String(),
			RoomID: evt.RoomID.String(),
		},
	}
	m.handler.HandleEvent(ctx, relifyEvt)
}

func (m *MatrixAdapter) handleMembership(ctx context.Context, evt *event.Event) {
	content := evt.Content.AsMember()
	if content.Membership == event.MembershipInvite && evt.StateKey != nil && *evt.StateKey == m.botUserID.String() {
		m.as.BotIntent().JoinRoom(ctx, evt.RoomID.String(), nil)
	}
}

func (m *MatrixAdapter) getGhostIntent(originalID, name, avatar string) *appservice.IntentAPI {
	safeLocal := m.sanitizeLocalpart(originalID)
	userID := id.NewUserID(fmt.Sprintf("%s%s", m.cfg.UserNamespace, safeLocal), m.cfg.Domain)
	intent := m.as.Intent(userID)

	if _, loaded := m.profileCache.Load(userID); !loaded {
		displayName := name
		if displayName == "" {
			displayName = originalID
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			intent.EnsureRegistered(ctx)
			intent.SetDisplayName(ctx, displayName)
			if strings.HasPrefix(avatar, "mxc://") {
				mxc, _ := id.ParseContentURI(avatar)
				intent.SetAvatarURL(ctx, mxc)
			}
		}()
		m.profileCache.Store(userID, true)
	}
	return intent
}

func (m *MatrixAdapter) sanitizeLocalpart(s string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '.' || r == '=' || r == '_' {
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
	return sender == m.botUserID || strings.HasPrefix(sender.String(), fmt.Sprintf("@%s", m.cfg.UserNamespace))
}
