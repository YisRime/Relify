package internal

import (
	"context"
	"time"
)

// EventType 定义了事件的高层业务分类。
type EventType string

const (
	// TypeMessage 代表普通消息事件（包含文本、图片、文件、引用回复等）。
	TypeMessage EventType = "message"
	// TypeNotice 代表系统提示或通知（如“某人加入了群聊”）。
	TypeNotice EventType = "notice"
	// TypeRevoke 代表撤回操作。配合 Event.RefID 指向被撤回的消息。
	TypeRevoke EventType = "revoke"
	// TypeEdit 代表编辑操作。配合 Event.RefID 指向被编辑的消息。
	TypeEdit EventType = "edit"
	// TypeReaction 代表互动/表态操作（如点赞）。配合 Event.RefID 指向被表态的消息。
	TypeReaction EventType = "reaction"
)

// SegmentType 定义了消息内容片段的具体类型。
type SegmentType string

const (
	// SegText 纯文本内容。
	SegText SegmentType = "text"
	// SegImage 图片内容。
	SegImage SegmentType = "image"
	// SegAudio 语音/音频内容。
	SegAudio SegmentType = "audio"
	// SegVideo 视频内容。
	SegVideo SegmentType = "video"
	// SegFile 通用文件内容。
	SegFile SegmentType = "file"
	// SegMention 提及某人 (@用户)。
	SegMention SegmentType = "mention"
	// SegReaction 表情表态 (Emoji)。
	SegReaction SegmentType = "reaction"
)

// SenderType 定义了发送者的实体类型。
type SenderType string

const (
	// SenderUser 代表普通人类用户。
	SenderUser SenderType = "user"
	// SenderBot 代表机器人或自动化程序。
	SenderBot SenderType = "bot"
	// SenderSystem 代表系统本身（如系统通知消息）。
	SenderSystem SenderType = "system"
)

// Properties 是一个通用的键值对映射，用于存储非结构化的配置、权限标志或原始数据。
type Properties map[string]any

// Sender 扁平化地定义了事件触发者（发送者）的信息。
type Sender struct {
	// ID 是用户在源平台的唯一标识符。
	ID string `json:"id"`
	// Name 是用户的显示名称或昵称。
	Name string `json:"name"`
	// Type 标识发送者的类型（用户、机器人、系统）。
	Type SenderType `json:"type"`
	// Avatar 是用户的头像 URL。
	Avatar string `json:"avatar,omitempty"`
	// Role 存储用户的角色标签、权限集或其他身份元数据。
	Role Properties `json:"role,omitempty"`
}

// FileInfo 定义了标准化的文件元数据，用于图片、视频、语音或普通文件。
type FileInfo struct {
	// ID 是文件在源平台的唯一标识（如有）。
	ID string `json:"id,omitempty"`
	// URL 是文件的下载或访问链接。
	URL string `json:"url,omitempty"`
	// Name 是原始文件名。
	Name string `json:"name,omitempty"`
	// MimeType 是文件的 MIME 类型 (如 image/jpeg)。
	MimeType string `json:"mime,omitempty"`
	// Size 是文件大小（字节）。
	Size int64 `json:"size,omitempty"`
	// Duration 是音视频的时长（秒）。
	Duration int `json:"duration,omitempty"`
	// Width 是图片或视频的宽度（像素）。
	Width int `json:"width,omitempty"`
	// Height 是图片或视频的高度（像素）。
	Height int `json:"height,omitempty"`
}

// Segment 代表消息内容的一个片段。
// 这是一个多态结构，通过 Type 字段决定 ID 和 Text 字段的具体含义，极度减少了嵌套层级。
type Segment struct {
	// Type 标识片段的类型。
	Type SegmentType `json:"type"`

	// ID 是通用标识符字段，含义取决于 Type：
	// - SegMention: 被 @ 的用户 ID。
	// - SegImage/File/Video: 文件的 ID (可选)。
	// - SegReaction: 通常为空，但在某些平台可能代表特定 Reaction 实例 ID。
	ID string `json:"id,omitempty"`

	// Text 是通用内容字段，含义取决于 Type：
	// - SegText: 消息文本内容。
	// - SegMention: 被 @ 用户的显示名称。
	// - SegReaction: 表情符号 (如 "👍")。
	Text string `json:"text,omitempty"`

	// File 仅在媒体类型 (Image/Audio/Video/File) 时使用，存储文件元数据。
	File *FileInfo `json:"file,omitempty"`

	// Extra 存储特殊标志或额外数据。
	// 例如：Type 为 SegReaction 时，Extra["remove"] = true 表示这是一个“取消表态”的操作。
	Extra Properties `json:"extra,omitempty"`
}

// Event 代表一个在系统内部流转的标准化事件。
// 所有的业务逻辑（消息、撤回、互动）统一使用此结构，通过 Type 和 RefID 区分意图。
type Event struct {
	// ID 是事件在源平台上的唯一标识符。
	ID string `json:"id"`
	// Type 标识事件的类型（如消息、撤回、互动）。
	Type EventType `json:"type"`
	// Time 是事件发生的时间。
	Time time.Time `json:"time"`
	// Platform 是产生该事件的源平台名称。
	Platform string `json:"platform"`
	// RoomID 是事件发生的房间或群组ID。
	RoomID string `json:"room_id"`

	// Sender 包含触发事件的用户信息。
	Sender *Sender `json:"sender,omitempty"`

	// Segments 包含事件的具体内容负载。
	// - TypeMessage: 包含 [SegText, SegImage, SegMention...]
	// - TypeReaction: 通常包含单个 [SegReaction]
	// - TypeRevoke: 通常为空，或包含一段说明性的 [SegText]
	Segments []Segment `json:"segments,omitempty"`

	// RefID 是通用引用 ID，指向被当前事件操作的“目标对象”。
	// - 消息回复 (TypeMessage + SegReply logic): 指向被回复的 Message ID。
	// - 消息撤回 (TypeRevoke): 指向被撤回的 Message ID。
	// - 表情互动 (TypeReaction): 指向被点赞/表态的 Message ID。
	RefID string `json:"ref_id,omitempty"`

	// Extra 存储特定于平台的额外原始数据。
	Extra Properties `json:"extra,omitempty"`
}

// Reset 重置事件对象的所有字段，以便将其放回 sync.Pool 中复用。
// 这对于高吞吐量的消息系统至关重要，能显著减少 GC 压力。
func (e *Event) Reset() {
	e.ID = ""
	e.Type = ""
	e.Time = time.Time{}
	e.Platform = ""
	e.RoomID = ""
	e.Sender = nil
	e.Segments = e.Segments[:0]
	e.RefID = ""
	e.Extra = nil
}

// BridgeNode 代表桥接关系中的一个端点（平台+房间）。
type BridgeNode struct {
	Platform string     `json:"platform"`
	RoomID   string     `json:"room_id"`
	Config   Properties `json:"config,omitempty"`
}

// BridgeGroup 代表一组互联的房间（即一个桥接组）。
type BridgeGroup struct {
	ID    int64
	Nodes []BridgeNode
}

// RoomInfo 包含从驱动获取的房间基本信息。
type RoomInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Avatar string `json:"avatar,omitempty"`
	Topic  string `json:"topic,omitempty"`
}

// RoutePolicy 定义了驱动的路由策略。
type RoutePolicy string

const (
	// PolicyMirror 镜像模式，通常用于一对一同步，会尝试创建对应的镜像房间。
	PolicyMirror RoutePolicy = "mirror"
	// PolicyMix 混合模式，通常用于将消息聚合到一个公共房间。
	PolicyMix RoutePolicy = "mix"
)

// SendResult 封装了单个消息片段发送的结果。
// 因为一条源 Event 可能被拆分为多条目标消息（例如图文分离），或者部分发送失败。
type SendResult struct {
	// MsgID 是目标平台生成的消息 ID。
	MsgID string `json:"msg_id"`
	// Error 如果发送该部分时出错，则包含具体的错误信息。
	Error error `json:"error,omitempty"`
}

// API 定义了驱动程序可以调用的核心功能接口。
type API interface {
	// FindMapping 查找源消息 ID 对应的目标平台消息 ID。
	FindMapping(srcPlatform, srcMsgID, dstPlatform string) (string, bool)

	// Receive 将从驱动接收到的标准化事件提交给核心路由器进行处理。
	Receive(ctx context.Context, event *Event)
}

// Driver 接口定义了聊天平台适配器必须实现的方法。
type Driver interface {
	// Init 初始化驱动程序。
	Init(ctx context.Context, api API) (string, RoutePolicy, error)

	// Stop 停止驱动程序，清理资源。
	Stop(ctx context.Context) error

	// Send 将标准化事件发送到指定的目标节点。
	// 返回发送结果列表，包含生成的消息 ID 和可能的错误。
	Send(ctx context.Context, node *BridgeNode, event *Event) ([]SendResult, error)

	// GetUserInfo 获取指定用户的详细信息。
	GetUserInfo(ctx context.Context, userID string) (*Sender, error)

	// GetRoomInfo 获取指定房间的信息。
	GetRoomInfo(ctx context.Context, roomID string) (*RoomInfo, error)

	// CreateRoom 根据提供的信息创建一个新房间或获取适配的现有房间 ID。
	CreateRoom(ctx context.Context, info *RoomInfo) (string, error)
}

// Config 定义了应用程序的全局配置结构。
type Config struct {
	LogLevel  string                    `yaml:"log_level"`
	Mode      string                    `yaml:"mode"`
	Hub       string                    `yaml:"hub"`
	RetentDay int                       `yaml:"retent_day"`
	Platforms map[string]PlatformConfig `yaml:"platforms"`
}

// PlatformConfig 定义了单个平台的配置。
type PlatformConfig struct {
	Driver  string     `yaml:"driver"`
	Enabled bool       `yaml:"enabled"`
	Config  Properties `yaml:"config"`
}
