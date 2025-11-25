package internal

import (
	"context"
	"time"
)

// Kind 表示事件的类型
type Kind string

const (
	Msg  Kind = "msg"  // 消息事件
	Note Kind = "note" // 通知事件（如撤回、群组变更等）
)

// 事件子类型常量
const (
	Revoke = "revoke" // 撤回消息
	Edit   = "edit"   // 编辑消息
)

// Props 是通用的属性映射类型，用于存储任意键值对
type Props map[string]any

// Event 表示跨平台的统一事件结构
// 用于在不同聊天平台之间传递消息和通知
type Event struct {
	ID     string    `json:"id"`               // 事件 ID（平台内唯一标识）
	Kind   Kind      `json:"kind"`             // 事件类型（消息或通知）
	Time   time.Time `json:"time"`             // 事件时间戳
	Plat   string    `json:"plat"`             // 来源平台名称
	Room   string    `json:"room"`             // 房间/群组 ID
	User   string    `json:"user,omitempty"`   // 发送者用户 ID
	Name   string    `json:"name,omitempty"`   // 发送者昵称
	Avatar string    `json:"avatar,omitempty"` // 发送者头像 URL
	Segs   []Seg     `json:"segs,omitempty"`   // 消息内容段列表
	Ref    string    `json:"ref,omitempty"`    // 引用的消息 ID（用于回复、编辑、撤回等）
	Extra  Props     `json:"extra,omitempty"`  // 额外的平台特定属性
}

// Seg 表示消息的一个内容段
// 消息可以由多个不同类型的段组成（如文本、图片、提及等）
type Seg struct {
	Kind string `json:"kind"` // 段类型（text、image、audio、video、file、mention 等）
	Raw  Props  `json:"raw"`  // 段的原始数据
}

// Node 表示一个聊天平台中的节点（房间/群组）
// 用于桥接配置
type Node struct {
	Plat string `json:"plat"`          // 平台名称
	Room string `json:"room"`          // 房间/群组 ID
	Cfg  Props  `json:"cfg,omitempty"` // 节点特定配置
}

// Group 表示一个桥接组，连接多个平台的房间
type Group struct {
	ID    int64  // 桥接组的数据库 ID
	Name  string // 桥接组名称
	Nodes []Node // 包含的节点列表
}

// Info 表示房间或用户的基本信息
type Info struct {
	ID     string `json:"id"`               // 房间/用户 ID
	Name   string `json:"name"`             // 显示名称
	Avatar string `json:"avatar,omitempty"` // 头像 URL
	Topic  string `json:"topic,omitempty"`  // 主题/描述
}

// Route 表示平台的路由模式
type Route string

const (
	RouteMirror Route = "mirror" // 镜像模式：为每个桥接创建独立房间
	RouteMix    Route = "mix"    // 混合模式：所有桥接消息发送到同一房间
)

// Driver 定义平台适配器的接口
// 每个聊天平台需要实现此接口以集成到 Relify
type Driver interface {
	// Name 返回平台的唯一名称
	Name() string

	// Route 返回平台的路由模式
	Route() Route

	// Start 启动平台适配器
	// 参数: ctx - 用于控制生命周期的上下文
	// 返回: 启动过程中的错误
	Start(ctx context.Context) error

	// Stop 停止平台适配器
	// 参数: ctx - 用于控制关闭超时的上下文
	// 返回: 停止过程中的错误
	Stop(ctx context.Context) error

	// Send 向指定节点发送事件
	// 参数:
	//   - ctx: 上下文
	//   - node: 目标节点
	//   - evt: 要发送的事件
	// 返回:
	//   - string: 发送后的消息 ID
	//   - error: 发送过程中的错误
	Send(ctx context.Context, node *Node, evt *Event) (string, error)

	// Info 获取房间或用户的信息
	// 参数:
	//   - ctx: 上下文
	//   - room: 房间/用户 ID
	// 返回:
	//   - *Info: 房间/用户信息
	//   - error: 获取过程中的错误
	Info(ctx context.Context, room string) (*Info, error)

	// Make 创建新房间
	// 参数:
	//   - ctx: 上下文
	//   - info: 房间信息（可为 nil，用于混合模式）
	// 返回:
	//   - string: 创建的房间 ID
	//   - error: 创建过程中的错误
	Make(ctx context.Context, info *Info) (string, error)
}

// Config 表示应用程序的配置
type Config struct {
	Level string              `yaml:"level"` // 日志级别
	Mode  string              `yaml:"mode"`  // 运行模式（hub 或 peer）
	Hub   string              `yaml:"hub"`   // 中心平台名称（仅在 hub 模式下使用）
	Plats map[string]PlatConf `yaml:"plats"` // 各平台的配置
}

// PlatConf 表示单个平台的配置
type PlatConf struct {
	Type    string `yaml:"type"`    // 平台类型
	Enabled bool   `yaml:"enabled"` // 是否启用
	Cfg     Props  `yaml:"cfg"`     // 平台特定配置
}

// GetString 从 Props 中安全地获取字符串值
// 参数:
//   - p: 属性映射
//   - key: 要获取的键
//
// 返回:
//   - string: 键对应的字符串值，如果不存在或类型不匹配则返回空字符串
func GetString(p Props, key string) string {
	if v, ok := p[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
		return ""
	}
	return ""
}
