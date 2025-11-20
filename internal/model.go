// Package internal 定义核心数据模型和接口
package internal

import (
	"context"
	"time"
)

// RouteType 路由类型 (平台固有属性)
type RouteType string

const (
	// RouteTypeMirror 镜像模式：机器人有权限创建频道/房间，可以完全复刻源结构 (e.g. Discord, Slack)
	RouteTypeMirror RouteType = "mirror"
	// RouteTypeAggregate 聚合模式：机器人无法创建房间，只能被拉入现有群组 (e.g. WeChat, WhatsApp, TG Personal)
	RouteTypeAggregate RouteType = "aggregate"
)

// MessageType 消息类型枚举
type MessageType string

const (
	MsgTypeText    MessageType = "text"    // 纯文本或Markdown
	MsgTypeRich    MessageType = "rich"    // 富文本 (HTML, Embeds, Cards)
	MsgTypeImage   MessageType = "image"   // 图片
	MsgTypeAudio   MessageType = "audio"   // 音频
	MsgTypeVideo   MessageType = "video"   // 视频
	MsgTypeFile    MessageType = "file"    // 通用文件
	MsgTypeSticker MessageType = "sticker" // 贴纸/表情包
	MsgTypeSystem  MessageType = "system"  // 系统消息 (入群, 退群, 置顶)
)

// --- Event & Message Models ---

// Event 通用事件包装器
type Event struct {
	ID        string      `json:"id"`        // 事件全局唯一ID
	Type      MessageType `json:"type"`      // 事件类型
	Platform  string      `json:"platform"`  // 来源平台
	Timestamp time.Time   `json:"timestamp"` // 事件发生时间

	Message *Message `json:"message,omitempty"` // 消息体 (如果是消息事件)
	Room    *Room    `json:"room,omitempty"`    // 房间信息 (如果是房间更新事件)
}

// Message 统一消息模型
type Message struct {
	ID           string `json:"id"`            // 消息ID
	RoomID       string `json:"room_id"`       // 所属房间ID
	SenderID     string `json:"sender_id"`     // 发送者ID
	SenderName   string `json:"sender_name"`   // 发送者显示名称
	SenderAvatar string `json:"sender_avatar"` // 发送者头像

	// 核心内容区
	Content       string `json:"content"`                  // 纯文本内容 (Fallback)
	Format        string `json:"format,omitempty"`         // 格式标记 (e.g. "markdown", "html")
	FormattedBody string `json:"formatted_body,omitempty"` // 格式化后的内容 (Matrix HTML)

	// 媒体与附件
	Files []*File `json:"files,omitempty"`

	// 富文本组件
	Embeds []*Embed `json:"embeds,omitempty"`

	// 引用与社交关系
	ReplyToID string   `json:"reply_to_id,omitempty"` // 回复的目标消息ID
	Mentions  []string `json:"mentions,omitempty"`    // 被提及(At)的用户ID列表

	// Extra 存储平台原生数据
	Extra map[string]interface{} `json:"extra,omitempty"`
}

// File 通用文件/媒体定义
type File struct {
	ID           string `json:"id,omitempty"`            // 平台侧文件ID (如果有)
	URL          string `json:"url"`                     // 文件下载/访问链接
	Name         string `json:"name"`                    // 文件名
	MimeType     string `json:"mime_type"`               // MIME类型
	Size         int64  `json:"size"`                    // 文件大小(字节)
	Width        int    `json:"width,omitempty"`         // 宽度 (图片/视频)
	Height       int    `json:"height,omitempty"`        // 高度 (图片/视频)
	Duration     int    `json:"duration,omitempty"`      // 时长(秒, 音视频)
	ThumbnailURL string `json:"thumbnail_url,omitempty"` // 缩略图链接
}

// Embed 富文本嵌入
type Embed struct {
	Title       string        `json:"title,omitempty"`
	Description string        `json:"description,omitempty"`
	URL         string        `json:"url,omitempty"`
	Color       int           `json:"color,omitempty"`
	Timestamp   string        `json:"timestamp,omitempty"`
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

// OutboundMessage 出站消息请求结构
type OutboundMessage struct {
	TargetPlatform string   `json:"target_platform"` // 目标平台
	TargetRoomID   string   `json:"target_room_id"`  // 目标房间ID
	Message        *Message `json:"message"`         // 要发送的消息实体

	DisablePreview bool `json:"disable_preview,omitempty"`
	Silent         bool `json:"silent,omitempty"`
}

// --- Platform & Room Models ---

type RoomType string

const (
	RoomTypeText     RoomType = "text"
	RoomTypeVoice    RoomType = "voice"
	RoomTypeDM       RoomType = "dm"
	RoomTypeCategory RoomType = "category"
)

type Room struct {
	ID        string                 `json:"id"`
	Platform  string                 `json:"platform"`
	ParentID  string                 `json:"parent_id,omitempty"`
	Type      RoomType               `json:"type"`
	Name      string                 `json:"name"`
	Topic     string                 `json:"topic,omitempty"`
	AvatarURL string                 `json:"avatar_url,omitempty"`
	Extra     map[string]interface{} `json:"extra,omitempty"`
}

type RoomMember struct {
	UserID      string                 `json:"user_id"`
	Username    string                 `json:"username"`
	DisplayName string                 `json:"display_name"`
	AvatarURL   string                 `json:"avatar_url,omitempty"`
	IsBot       bool                   `json:"is_bot"`
	IsAdmin     bool                   `json:"is_admin"`
	Extra       map[string]interface{} `json:"extra,omitempty"`
}

type RoomBinding struct {
	ID    string
	Rooms []BoundRoom
}

type BoundRoom struct {
	Platform string
	RoomID   string
}

// Platform 平台适配器接口
type Platform interface {
	// Name 返回平台名称 (e.g. "discord", "telegram")
	Name() string

	// GetRouteType 返回该平台的路由类型 (镜像 or 聚合)
	GetRouteType() RouteType

	// Start 启动平台适配器
	Start(ctx context.Context) error

	// Stop 停止平台适配器
	Stop(ctx context.Context) error

	// SendMessage 发送消息
	SendMessage(ctx context.Context, msg *OutboundMessage) (string, error)

	// UploadFile 上传文件/图片
	UploadFile(ctx context.Context, data []byte, filename string) (string, error)

	// GetRoom 获取房间/频道信息
	GetRoom(ctx context.Context, roomID string) (*Room, error)

	// GetRoomMembers 获取房间成员列表
	GetRoomMembers(ctx context.Context, roomID string) ([]*RoomMember, error)

	// GetMessageLink 获取消息跳转链接
	GetMessageLink(roomID, msgID string) string
}

// InboundHandler 入站事件处理器接口
type InboundHandler interface {
	HandleEvent(ctx context.Context, event *Event) error
}

// PlatformRegistry 平台注册表
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
