// Package model 定义核心数据模型
package model

import "time"

// MessageType 消息类型
type MessageType string

const (
	MsgTypeText   MessageType = "m.text"
	MsgTypeImage  MessageType = "m.image"
	MsgTypeFile   MessageType = "m.file"
	MsgTypeAudio  MessageType = "m.audio"
	MsgTypeVideo  MessageType = "m.video"
	MsgTypeNotice MessageType = "m.notice"
	MsgTypeEdit   MessageType = "m.edit"
	MsgTypeDelete MessageType = "m.delete"
)

// Message 统一消息模型
type Message struct {
	Fingerprint    string
	SourcePlatform string
	SourceMsgID    string
	SourceRoomID   string
	Type           MessageType
	Timestamp      time.Time
	Sender         SenderProfile
	Content        MessageContent
	RefSourceID    string
	RefTargetID    string
	Mentions       []Mention
	Media          *MediaInfo
	EditTargetID   string
}

// Mention 提及信息
type Mention struct {
	UserID      string
	DisplayName string
	Username    string
	Offset      int
	Length      int
	TargetID    string
}

// SenderProfile 发送者信息
type SenderProfile struct {
	UserID      string
	DisplayName string
	AvatarURL   string
	Username    string
}

// MessageContent 消息内容
type MessageContent struct {
	Body          string
	FormattedBody string
	Format        string
}

// MediaInfo 媒体附件信息
type MediaInfo struct {
	URL          string
	MimeType     string
	Size         int64
	Filename     string
	Width        int
	Height       int
	ThumbnailURL string
}

// MessageEvent 消息事件
type MessageEvent struct {
	Message    *Message
	EventType  string
	Timestamp  time.Time
	RetryCount int
}

// OutboundMessage 出站消息
type OutboundMessage struct {
	TargetPlatform string
	TargetRoomID   string
	Message        *Message
}

// MessageSendResult 消息发送结果
type MessageSendResult struct {
	Success        bool
	TargetPlatform string
	TargetMsgID    string
	Error          string
}
