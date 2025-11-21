package internal

import (
	"context"
	"time"
)

const AggregateRoomKey = "AGGREGATE"

type EventAction string

const (
	ActionCreate EventAction = "create"
	ActionUpdate EventAction = "update"
	ActionDelete EventAction = "delete"
)

type MessageType string

const (
	MsgTypeText  MessageType = "text"
	MsgTypeImage MessageType = "image"
	MsgTypeAudio MessageType = "audio"
	MsgTypeVideo MessageType = "video"
	MsgTypeFile  MessageType = "file"
)

type RouteType string

const (
	RouteTypeMirror    RouteType = "mirror"
	RouteTypeAggregate RouteType = "aggregate"
)

type Platform interface {
	Name() string
	GetBotUserID() string
	GetRouteType() RouteType

	Start(ctx context.Context) error
	Stop(ctx context.Context) error

	SendMessage(ctx context.Context, msg *OutMessage) (string, error)
	EditMessage(ctx context.Context, msg *OutMessage) error
	DeleteMessage(ctx context.Context, roomID, msgID string) error

	UploadFile(ctx context.Context, data []byte, filename string) (string, error)

	CreateRoom(ctx context.Context, info *RoomInfo) (string, error)
	GetRoomInfo(ctx context.Context, roomID string) (*RoomInfo, error)
}

type InboundHandler interface {
	HandleEvent(ctx context.Context, event *Event) error
}

type Event struct {
	ID        string      `json:"id"`
	Action    EventAction `json:"action"`
	Type      MessageType `json:"type"`
	Platform  string      `json:"platform"`
	Timestamp time.Time   `json:"timestamp"`
	Message   *Message    `json:"message,omitempty"`
}

type Message struct {
	ID           string `json:"id"`
	RoomID       string `json:"room_id"`
	SenderID     string `json:"sender_id"`
	SenderName   string `json:"sender_name"`
	SenderAvatar string `json:"sender_avatar"`

	Content string   `json:"content"`
	Files   []*File  `json:"files,omitempty"`
	Embeds  []*Embed `json:"embeds,omitempty"`

	ReplyToID string   `json:"reply_to_id,omitempty"`
	Mentions  []string `json:"mentions,omitempty"`

	Extra map[string]interface{} `json:"extra,omitempty"`
}

type File struct {
	Name         string `json:"name"`
	URL          string `json:"url"`
	MimeType     string `json:"mime_type"`
	Size         int64  `json:"size"`
	ThumbnailURL string `json:"thumbnail_url,omitempty"`
}

type Embed struct {
	Title       string        `json:"title,omitempty"`
	Description string        `json:"description,omitempty"`
	URL         string        `json:"url,omitempty"`
	Color       int           `json:"color,omitempty"`
	Footer      string        `json:"footer,omitempty"`
	Image       *File         `json:"image,omitempty"`
	Thumbnail   *File         `json:"thumbnail,omitempty"`
	Fields      []*EmbedField `json:"fields,omitempty"`
}

type EmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

type OutMessage struct {
	TargetPlatform  string                 `json:"target_platform"`
	TargetRoomID    string                 `json:"target_room_id"`
	TargetConfig    map[string]interface{} `json:"target_config,omitempty"`
	TargetMessageID string                 `json:"target_message_id,omitempty"`
	Message         *Message               `json:"message"`
}

type RoomInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url,omitempty"`
	Topic     string `json:"topic,omitempty"`
}

type RoomBinding struct {
	ID    string
	Name  string
	Rooms []BoundRoom
}

type BoundRoom struct {
	Platform string
	RoomID   string
	Config   map[string]interface{}
}
