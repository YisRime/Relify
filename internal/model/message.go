// Package model 定义核心数据模型
// 包含消息、路由、用户等跨平台标准化数据结构
//
// 消息模型基于 Matrix 标准裁剪，提供统一的跨平台消息表示。
// 支持多种消息类型、富文本、媒体附件、提及、引用等功能。
package model

import "time"

// MessageType 消息类型（基于 Matrix 标准裁剪）
type MessageType string

const (
	MsgTypeText   MessageType = "m.text"   // 纯文本消息
	MsgTypeImage  MessageType = "m.image"  // 图片消息
	MsgTypeFile   MessageType = "m.file"   // 文件消息
	MsgTypeAudio  MessageType = "m.audio"  // 音频消息
	MsgTypeVideo  MessageType = "m.video"  // 视频消息
	MsgTypeNotice MessageType = "m.notice" // 系统通知
	MsgTypeEdit   MessageType = "m.edit"   // 编辑消息
	MsgTypeDelete MessageType = "m.delete" // 撤回消息
)

// Message 统一消息模型
// 跨平台消息的标准化表示，包含所有必要的元数据
type Message struct {
	// 消息指纹 - 全局唯一标识（不加密，简单拼接）
	Fingerprint string `json:"fingerprint"`

	// 来源信息
	SourceDriver string `json:"source_driver"`  // 来源驱动名称，如 "telegram", "discord"
	SourceMsgID  string `json:"source_msg_id"`  // 来源平台的消息 ID
	SourceRoomID string `json:"source_room_id"` // 来源房间/群组 ID

	// 消息元数据
	Type      MessageType `json:"type"`      // 消息类型
	Timestamp time.Time   `json:"timestamp"` // 消息时间戳

	// 发送者信息
	Sender SenderProfile `json:"sender"`

	// 消息内容（标准化后）
	Content MessageContent `json:"content"`

	// 引用/回复信息
	RefSourceID string `json:"ref_source_id,omitempty"` // 引用的原始消息 ID（入站时填充）
	RefTargetID string `json:"ref_target_id,omitempty"` // 引用的目标消息 ID（核心层翻译后填充）

	// 提及信息
	Mentions []Mention `json:"mentions,omitempty"` // 提及的用户列表

	// 媒体信息
	Media *MediaInfo `json:"media,omitempty"` // 媒体附件

	// 编辑/撤回相关
	EditTargetID string `json:"edit_target_id,omitempty"` // 要编辑的目标消息 ID
}

// Mention 提及信息
// 表示消息中 @ 某个用户的信息
type Mention struct {
	UserID      string `json:"user_id"`             // 原始平台的用户 ID
	DisplayName string `json:"display_name"`        // 显示名称
	Username    string `json:"username,omitempty"`  // 用户名（可选）
	Offset      int    `json:"offset,omitempty"`    // 在文本中的位置（可选）
	Length      int    `json:"length,omitempty"`    // 提及文本的长度（可选）
	TargetID    string `json:"target_id,omitempty"` // 翻译后的目标平台用户 ID（核心层填充）
}

// SenderProfile 统一发送者身份信息
type SenderProfile struct {
	UserID      string `json:"user_id"`      // 平台用户 ID
	DisplayName string `json:"display_name"` // 显示名称
	AvatarURL   string `json:"avatar_url"`   // 头像 URL
	Username    string `json:"username"`     // 用户名（可选）
}

// MessageContent 消息内容（清洗后的标准格式）
type MessageContent struct {
	Body          string `json:"body"`                     // 纯文本内容
	FormattedBody string `json:"formatted_body,omitempty"` // 富文本内容（HTML 或标准 Markdown）
	Format        string `json:"format,omitempty"`         // 格式类型："html", "markdown"
}

// MediaInfo 媒体附件信息
type MediaInfo struct {
	URL      string `json:"url"`                // 媒体 URL（可能是代理 URL）
	MimeType string `json:"mime_type"`          // MIME 类型
	Size     int64  `json:"size"`               // 文件大小（字节）
	Filename string `json:"filename,omitempty"` // 文件名

	// 图片/视频特有属性
	Width  int `json:"width,omitempty"`  // 宽度
	Height int `json:"height,omitempty"` // 高度

	// 缩略图
	ThumbnailURL string `json:"thumbnail_url,omitempty"` // 缩略图 URL
}

// MessageEvent 消息事件（驱动到核心层的入站接口）
type MessageEvent struct {
	Message *Message `json:"message"`
}

// OutboundMessage 出站消息（核心层到驱动的出站接口）
type OutboundMessage struct {
	TargetDriver string   `json:"target_driver"`  // 目标驱动名称
	TargetRoomID string   `json:"target_room_id"` // 目标房间 ID
	Message      *Message `json:"message"`        // 消息内容
}

// MessageSendResult 消息发送结果（驱动回调核心层）
type MessageSendResult struct {
	Success      bool   `json:"success"`                 // 是否发送成功
	TargetDriver string `json:"target_driver"`           // 目标驱动名称
	TargetMsgID  string `json:"target_msg_id,omitempty"` // 发送成功后的目标消息 ID
	Error        string `json:"error,omitempty"`         // 错误信息
}
