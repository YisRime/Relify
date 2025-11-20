package internal

import (
	"context"
	"time"
)

// RouteType 定义路由模式
type RouteType string

const (
	RouteTypeMirror    RouteType = "mirror"    // 镜像模式 (如 Discord, Slack)
	RouteTypeAggregate RouteType = "aggregate" // 聚合模式 (如 WeChat, WhatsApp)
)

// MessageType 定义消息内容类型
type MessageType string

const (
	MsgTypeText    MessageType = "text"
	MsgTypeRich    MessageType = "rich" // 富文本 (含 Embed/Card)
	MsgTypeImage   MessageType = "image"
	MsgTypeAudio   MessageType = "audio"
	MsgTypeVideo   MessageType = "video"
	MsgTypeFile    MessageType = "file"
	MsgTypeSticker MessageType = "sticker"
	MsgTypeSystem  MessageType = "system" // 系统通知
)

// RoomType 定义聊天室类型
type RoomType string

const (
	RoomTypeText     RoomType = "text"
	RoomTypeVoice    RoomType = "voice"
	RoomTypeDM       RoomType = "dm"
	RoomTypeCategory RoomType = "category"
)

// Event 是系统内部传递的通用事件结构
type Event struct {
	ID        string      `json:"id"`
	Type      MessageType `json:"type"`
	Platform  string      `json:"platform"`
	Timestamp time.Time   `json:"timestamp"`
	Message   *Message    `json:"message,omitempty"`
	Room      *Room       `json:"room,omitempty"`
}

// Message 统一的消息模型，屏蔽平台差异
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
	Mentions  []string `json:"mentions,omitempty"` // 被提及的 User ID 列表

	Extra map[string]interface{} `json:"extra,omitempty"` // 存储平台特有的原始数据
}

// File 代表附件
type File struct {
	Name         string `json:"name"`
	URL          string `json:"url"`
	MimeType     string `json:"mime_type"`
	Size         int64  `json:"size"`
	ThumbnailURL string `json:"thumbnail_url,omitempty"`
}

// Embed 代表嵌入式富文本内容 (类似于 Discord Embeds)
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

// OutboundMessage 封装发送到目标平台的消息
type OutboundMessage struct {
	TargetPlatform string   `json:"target_platform"`
	TargetRoomID   string   `json:"target_room_id"`
	Message        *Message `json:"message"`
}

// Room 代表聊天室/频道信息
type Room struct {
	ID        string                 `json:"id"`
	Platform  string                 `json:"platform"`
	Name      string                 `json:"name"`
	Type      RoomType               `json:"type"`
	AvatarURL string                 `json:"avatar_url,omitempty"`
	Extra     map[string]interface{} `json:"extra,omitempty"`
}

// RoomMember 代表聊天室成员信息
type RoomMember struct {
	UserID      string `json:"user_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url,omitempty"`
	IsBot       bool   `json:"is_bot"`
}

// RoomBinding 定义了不同平台房间之间的绑定关系
type RoomBinding struct {
	ID    string
	Rooms []BoundRoom
}

type BoundRoom struct {
	Platform string
	RoomID   string
}

// Platform 接口定义了每个适配器必须实现的方法
type Platform interface {
	Name() string
	GetRouteType() RouteType
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	// SendMessage 发送消息并返回新生成的 MessageID
	SendMessage(ctx context.Context, msg *OutboundMessage) (string, error)
	UploadFile(ctx context.Context, data []byte, filename string) (string, error)
	GetRoom(ctx context.Context, roomID string) (*Room, error)
	GetRoomMembers(ctx context.Context, roomID string) ([]*RoomMember, error)
}

// InboundHandler 处理入站事件的接口
type InboundHandler interface {
	HandleEvent(ctx context.Context, event *Event) error
}

// PlatformRegistry 管理所有已注册的平台实例
type PlatformRegistry struct {
	platforms map[string]Platform
}

func NewPlatformRegistry() *PlatformRegistry {
	return &PlatformRegistry{platforms: make(map[string]Platform)}
}

// Register 注册一个新的平台适配器
func (r *PlatformRegistry) Register(p Platform) {
	r.platforms[p.Name()] = p
}

// Get 获取指定名称的平台适配器
func (r *PlatformRegistry) Get(name string) (Platform, bool) {
	p, exists := r.platforms[name]
	return p, exists
}

// All 返回所有已注册的平台
func (r *PlatformRegistry) All() map[string]Platform {
	return r.platforms
}
