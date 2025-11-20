package internal

import (
	"context"
	"time"
)

// --- Enums ---

type RouteType string

const (
	RouteTypeMirror    RouteType = "mirror"    // 镜像模式 (如 Discord, Slack)
	RouteTypeAggregate RouteType = "aggregate" // 聚合模式 (如 WeChat, WhatsApp)
)

type MessageType string

const (
	MsgTypeText    MessageType = "text"
	MsgTypeRich    MessageType = "rich" // 含 Embed/Card
	MsgTypeImage   MessageType = "image"
	MsgTypeAudio   MessageType = "audio"
	MsgTypeVideo   MessageType = "video"
	MsgTypeFile    MessageType = "file"
	MsgTypeSticker MessageType = "sticker"
	MsgTypeSystem  MessageType = "system"
)

type RoomType string

const (
	RoomTypeText     RoomType = "text"
	RoomTypeVoice    RoomType = "voice"
	RoomTypeDM       RoomType = "dm"
	RoomTypeCategory RoomType = "category"
)

// --- Core Structs ---

// Event 事件包装器
type Event struct {
	ID        string      `json:"id"`
	Type      MessageType `json:"type"`
	Platform  string      `json:"platform"`
	Timestamp time.Time   `json:"timestamp"`
	Message   *Message    `json:"message,omitempty"`
	Room      *Room       `json:"room,omitempty"`
}

// Message 统一消息模型
type Message struct {
	ID           string `json:"id"`
	RoomID       string `json:"room_id"`
	SenderID     string `json:"sender_id"`
	SenderName   string `json:"sender_name"`
	SenderAvatar string `json:"sender_avatar"`

	Content string   `json:"content"` // 纯文本内容
	Files   []*File  `json:"files,omitempty"`
	Embeds  []*Embed `json:"embeds,omitempty"`

	ReplyToID string   `json:"reply_to_id,omitempty"`
	Mentions  []string `json:"mentions,omitempty"` // ID列表

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
	Footer      *EmbedFooter  `json:"footer,omitempty"`
	Image       *File         `json:"image,omitempty"`
	Thumbnail   *File         `json:"thumbnail,omitempty"`
	Fields      []*EmbedField `json:"fields,omitempty"`
}

type EmbedFooter struct {
	Text    string `json:"text"`
	IconURL string `json:"icon_url,omitempty"`
}

type EmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

// OutboundMessage 出站消息
type OutboundMessage struct {
	TargetPlatform string   `json:"target_platform"`
	TargetRoomID   string   `json:"target_room_id"`
	Message        *Message `json:"message"`
}

// --- Room & Binding ---

type Room struct {
	ID        string                 `json:"id"`
	Platform  string                 `json:"platform"`
	Name      string                 `json:"name"`
	Type      RoomType               `json:"type"`
	AvatarURL string                 `json:"avatar_url,omitempty"`
	Extra     map[string]interface{} `json:"extra,omitempty"`
}

type RoomMember struct {
	UserID      string `json:"user_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url,omitempty"`
	IsBot       bool   `json:"is_bot"`
}

type RoomBinding struct {
	ID    string
	Rooms []BoundRoom
}

type BoundRoom struct {
	Platform string
	RoomID   string
}

// --- Interfaces ---

type Platform interface {
	Name() string
	GetRouteType() RouteType
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	SendMessage(ctx context.Context, msg *OutboundMessage) (string, error)
	UploadFile(ctx context.Context, data []byte, filename string) (string, error)
	GetRoom(ctx context.Context, roomID string) (*Room, error)
	GetRoomMembers(ctx context.Context, roomID string) ([]*RoomMember, error)
}

type InboundHandler interface {
	HandleEvent(ctx context.Context, event *Event) error
}

// --- Registry ---

type PlatformRegistry struct {
	platforms map[string]Platform
}

func NewPlatformRegistry() *PlatformRegistry {
	return &PlatformRegistry{platforms: make(map[string]Platform)}
}

func (r *PlatformRegistry) Register(p Platform) {
	r.platforms[p.Name()] = p
}

func (r *PlatformRegistry) Get(name string) (Platform, bool) {
	p, exists := r.platforms[name]
	return p, exists
}

func (r *PlatformRegistry) All() map[string]Platform {
	return r.platforms
}
