package internal

import (
	"context"
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

const AggregateRoomKey = "AGGREGATE"

type EventAction string

const (
	ActionCreate EventAction = "create"
	ActionUpdate EventAction = "update"
	ActionDelete EventAction = "delete"
)

const (
	TypeText    = "text"
	TypeImage   = "image"
	TypeAudio   = "audio"
	TypeVideo   = "video"
	TypeFile    = "file"
	TypeMention = "mention"
)

type RouteType string

const (
	RouteTypeMirror    RouteType = "mirror"
	RouteTypeAggregate RouteType = "aggregate"
)

type Segment struct {
	Type     string                 `json:"type"`
	Data     map[string]interface{} `json:"data"`
	Fallback string                 `json:"fallback,omitempty"`
}

func (s *Segment) Validate() error {
	switch s.Type {
	case TypeImage, TypeVideo, TypeAudio, TypeFile:
		if GetString(s.Data, "url") == "" {
			return fmt.Errorf("missing url")
		}
	case TypeText:
		if GetString(s.Data, "text") == "" {
			return fmt.Errorf("missing text")
		}
	}
	return nil
}

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
	Platform  string      `json:"platform"`
	Timestamp time.Time   `json:"timestamp"`
	Message   *Message    `json:"message,omitempty"`
}

type Message struct {
	ID           string                 `json:"id"`
	RoomID       string                 `json:"room_id"`
	SenderID     string                 `json:"sender_id"`
	SenderName   string                 `json:"sender_name"`
	SenderAvatar string                 `json:"sender_avatar"`
	Body         []Segment              `json:"body"`
	ReplyToID    string                 `json:"reply_to_id,omitempty"`
	Extra        map[string]interface{} `json:"extra,omitempty"`
}

func (m *Message) Validate() error {
	if m.ID == "" || m.RoomID == "" || len(m.Body) == 0 {
		return fmt.Errorf("invalid message structure")
	}
	for _, seg := range m.Body {
		if err := seg.Validate(); err != nil {
			return err
		}
	}
	return nil
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

type Config struct {
	DataDir     string                    `yaml:"data_dir"`
	LogLevel    string                    `yaml:"log_level"`
	Mode        string                    `yaml:"mode"`
	HubPlatform string                    `yaml:"hub_platform"`
	Platforms   map[string]PlatformConfig `yaml:"platforms"`
}

type PlatformConfig struct {
	Type    string    `yaml:"type"`
	Enabled bool      `yaml:"enabled"`
	Config  yaml.Node `yaml:"config"`
}

func GetString(data map[string]interface{}, key string) string {
	if v, ok := data[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
