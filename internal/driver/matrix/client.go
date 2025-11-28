package matrix

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// AppServiceConfig 定义 Matrix AppService 的配置
type AppServiceConfig struct {
	ID        string `json:"id" yaml:"id"`               // AppService 唯一标识符
	Token     string `json:"token" yaml:"token"`         // 鉴权令牌
	Namespace string `json:"namespace" yaml:"namespace"` // 用户和房间命名空间前缀
	Listen    string `json:"listen" yaml:"listen"`       // HTTP 监听地址
}

// Config 定义 Matrix 适配器的完整配置
type Config struct {
	ServerURL    string           `json:"server_url" yaml:"server_url"`       // Matrix 服务器地址
	Domain       string           `json:"domain" yaml:"domain"`               // Matrix 域名
	ServerDomain string           `json:"server_domain" yaml:"server_domain"` // 服务器域名（用于媒体下载）
	AppService   AppServiceConfig `json:"appservice" yaml:"appservice"`       // AppService 配置
	AutoInvite   string           `json:"auto_invite" yaml:"auto_invite"`     // 自动邀请的用户 ID（中心模式）
}

// parseConfig 解析 Props 为 Config 结构
// 参数:
//   - p: 配置属性映射
//
// 返回:
//   - *Config: 解析后的配置
//   - error: 解析错误
func parseConfig(p internal.Props) (*Config, error) {
	b, _ := json.Marshal(p)
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	// 设置默认命名空间
	if c.AppService.Namespace == "" {
		c.AppService.Namespace = "relify_"
	}
	// 如果未指定服务器域名，使用主域名
	if c.ServerDomain == "" {
		c.ServerDomain = c.Domain
	}
	return &c, nil
}

// initClient 初始化 Matrix AppService 客户端
// 配置 AppService 注册信息、命名空间和状态存储
// 返回:
//   - error: 初始化错误
func (m *Matrix) initClient() error {
	as := appservice.Create()
	as.HomeserverDomain = m.cfg.Domain
	// 配置 AppService 注册信息
	as.Registration = &appservice.Registration{
		ID:              m.cfg.AppService.ID,
		URL:             m.cfg.AppService.Listen,
		AppToken:        m.cfg.AppService.Token, // AppService 到 Homeserver 的令牌
		ServerToken:     m.cfg.AppService.Token, // Homeserver 到 AppService 的令牌
		SenderLocalpart: "relify",               // Bot 用户的本地部分
		Namespaces: appservice.Namespaces{
			// 独占用户命名空间（用于 Ghost 用户）
			UserIDs: appservice.NamespaceList{{Exclusive: true, Regex: fmt.Sprintf("@%s.*", m.cfg.AppService.Namespace)}},
			// 独占房间别名命名空间
			RoomAliases: appservice.NamespaceList{{Exclusive: true, Regex: fmt.Sprintf("#%s.*:%s", m.cfg.AppService.Namespace, m.cfg.Domain)}},
		},
	}

	// 设置 Homeserver URL
	if err := as.SetHomeserverURL(m.cfg.ServerURL); err != nil {
		return err
	}

	// 使用自定义状态存储
	as.StateStore = &AppServiceStateStore{
		StateStore:    mautrix.NewMemoryStateStore(), // 基础内存存储
		registrations: sync.Map{},                    // Ghost 用户注册状态
		joinRules:     sync.Map{},                    // 房间加入规则缓存
	}

	m.as = as
	m.botUserID = id.NewUserID("relify", m.cfg.Domain) // Bot 用户完整 ID

	return nil
}

// startServe 启动 AppService 服务
// 启动事件处理协程和 HTTP 监听
// 参数:
//   - ctx: 上下文
//
// 返回:
//   - error: 启动错误
func (m *Matrix) startServe(ctx context.Context) error {
	slog.Info("Matrix 服务启动中", "listen", m.cfg.AppService.Listen)

	m.as.Events = make(chan *event.Event, 100) // 事件队列缓冲区

	// 启动事件处理协程
	go func() {
		for {
			select {
			case evt := <-m.as.Events:
				if evt != nil {
					m.processEvent(evt) // 处理 Matrix 事件
				}
			case <-ctx.Done():
				slog.Info("Matrix 事件处理协程退出")
				return
			}
		}
	}()

	// 启动 HTTP 服务监听 Homeserver 的事件推送
	// 启动 HTTP 服务监听 Homeserver 的事件推送
	go func() {
		addr := extractPort(m.cfg.AppService.Listen)
		slog.Info("Matrix HTTP 服务启动", "addr", addr)
		if err := http.ListenAndServe(addr, m.as.Router); err != nil {
			slog.Error("Matrix HTTP 服务错误", "error", err)
		}
	}()

	// 延迟确保 Bot 用户已注册
	go func() {
		time.Sleep(2 * time.Second)
		if err := m.as.BotIntent().EnsureRegistered(context.Background()); err != nil {
			slog.Warn("Matrix Bot 注册失败", "error", err)
		} else {
			slog.Info("Matrix Bot 已注册", "user_id", m.botUserID)
		}
	}()

	return nil
}

// stopServe 停止 AppService 服务
// 参数:
//   - ctx: 上下文
//
// 返回:
//   - error: 停止错误
func (m *Matrix) stopServe(ctx context.Context) error {
	return nil
}

// createRoom 创建新的 Matrix 房间
// 参数:
//   - ctx: 上下文
//   - info: 房间信息（名称、主题、头像）
//
// 返回:
//   - string: 创建的房间 ID
//   - error: 创建错误
func (m *Matrix) createRoom(ctx context.Context, info *internal.Info) (string, error) {
	slog.Info("Matrix 创建房间",
		"name", info.Name,
		"topic", info.Topic,
		"avatar", info.Avatar,
	)

	// 根据运行模式设置房间可见性
	visibility := "private"
	if m.router != nil && m.router.Mode() == "peer" {
		visibility = "public" // 对等模式下创建公开房间
	}

	slog.Debug("Matrix 房间配置",
		"visibility", visibility,
		"mode", func() string {
			if m.router != nil {
				return m.router.Mode()
			}
			return "unknown"
		}(),
	)

	req := &mautrix.ReqCreateRoom{
		Name:            info.Name,
		Topic:           info.Topic,
		Visibility:      visibility,
		CreationContent: map[string]any{"m.federate": true}, // 启用联邦
	}

	// 如果提供了头像，设置房间头像
	if info.Avatar != "" {
		slog.Debug("Matrix 设置房间头像", "avatar_url", info.Avatar)
		if err := m.setRoomAvatar(ctx, req, info.Avatar); err != nil {
			slog.Warn("Matrix 设置房间头像失败",
				"avatar_url", info.Avatar,
				"error", err,
			)
		}
	}

	// 生成房间别名（使用命名空间前缀）
	safeName := strings.ReplaceAll(strings.ToLower(info.Name), " ", "_")
	req.RoomAliasName = m.cfg.AppService.Namespace + safeName

	slog.Debug("Matrix 房间别名",
		"alias", req.RoomAliasName,
		"namespace", m.cfg.AppService.Namespace,
	)

	// 尝试创建房间
	resp, err := m.as.BotIntent().CreateRoom(ctx, req)
	if err != nil {
		// 如果别名冲突，去掉别名重试
		slog.Debug("Matrix 房间别名冲突，重试",
			"alias", req.RoomAliasName,
			"error", err,
		)
		req.RoomAliasName = ""
		resp, err = m.as.BotIntent().CreateRoom(ctx, req)
		if err != nil {
			slog.Error("Matrix 创建房间失败", "error", err)
			return "", err
		}
	}

	slog.Info("Matrix 房间已创建",
		"room_id", resp.RoomID,
		"name", info.Name,
	)

	// 在中心模式下自动邀请指定用户
	if m.router != nil && m.router.Mode() == "hub" && m.cfg.AutoInvite != "" {
		slog.Debug("Matrix 中心模式，准备邀请用户",
			"room_id", resp.RoomID,
			"user", m.cfg.AutoInvite,
		)

		_, err := m.as.BotIntent().InviteUser(ctx, resp.RoomID, &mautrix.ReqInviteUser{
			UserID: id.UserID(m.cfg.AutoInvite),
		})
		if err != nil {
			slog.Warn("Matrix 邀请用户失败",
				"room_id", resp.RoomID,
				"user", m.cfg.AutoInvite,
				"error", err,
			)
		} else {
			slog.Info("Matrix 已邀请用户",
				"room_id", resp.RoomID,
				"user", m.cfg.AutoInvite,
			)
		}
	} else {
		slog.Debug("Matrix 跳过用户邀请",
			"mode", func() string {
				if m.router != nil {
					return m.router.Mode()
				}
				return "unknown"
			}(),
			"auto_invite", m.cfg.AutoInvite,
		)
	}

	return resp.RoomID.String(), nil
}

// setRoomAvatar 设置房间头像
// 参数:
//   - ctx: 上下文
//   - req: 房间创建请求
//   - avatarURL: 头像 URL
//
// 返回:
//   - error: 设置错误
func (m *Matrix) setRoomAvatar(ctx context.Context, req *mautrix.ReqCreateRoom, avatarURL string) error {
	slog.Debug("Matrix 开始设置房间头像", "avatar_url", avatarURL)

	// 上传头像到 Matrix 媒体仓库
	mxc, err := m.uploadMedia(ctx, m.as.BotIntent(), avatarURL, "")
	if err != nil {
		slog.Error("Matrix 上传房间头像失败",
			"avatar_url", avatarURL,
			"error", err,
		)
		return err
	}

	if mxc == "" {
		slog.Error("Matrix 上传房间头像返回空MXC", "avatar_url", avatarURL)
		return fmt.Errorf("MXC地址为空")
	}

	// 解析 MXC URI
	avatarURI, err := id.ParseContentURI(mxc)
	if err != nil {
		slog.Error("Matrix 解析房间头像MXC URI失败",
			"mxc", mxc,
			"error", err,
		)
		return err
	}

	// 添加房间头像初始状态事件
	stateKey := ""
	req.InitialState = []*event.Event{
		{
			Type:     event.StateRoomAvatar,
			StateKey: &stateKey,
			Content: event.Content{
				Parsed: &event.RoomAvatarEventContent{
					URL: avatarURI.CUString(),
				},
			},
		},
	}
	return nil
}

// uploadMedia 上传媒体文件到 Matrix
// 参数:
//   - ctx: 上下文
//   - intent: Intent API 实例
//   - urlStr: 源媒体 URL
//   - mimeType: MIME 类型（可选）
//
// 返回:
//   - string: MXC URI
//   - error: 上传错误
func (m *Matrix) uploadMedia(ctx context.Context, intent *appservice.IntentAPI, urlStr, mimeType string) (string, error) {
	slog.Debug("Matrix 开始上传媒体",
		"url", urlStr,
		"mime_type", mimeType,
		"user_id", intent.UserID,
	)

	// 如果已经是 MXC URI，直接返回
	if strings.HasPrefix(urlStr, "mxc://") {
		slog.Debug("Matrix 媒体已是MXC URI，直接使用", "mxc", urlStr)
		return urlStr, nil
	}

	// 下载媒体文件
	downCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(downCtx, "GET", urlStr, nil)
	if err != nil {
		slog.Error("Matrix 创建下载请求失败",
			"url", urlStr,
			"error", err,
		)
		return "", err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Error("Matrix 下载媒体文件失败",
			"url", urlStr,
			"error", err,
		)
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Error("Matrix 下载媒体返回错误状态码",
			"url", urlStr,
			"status_code", resp.StatusCode,
			"status", resp.Status,
		)
		return "", fmt.Errorf("下载状态码 %d", resp.StatusCode)
	}

	slog.Debug("Matrix 媒体下载成功",
		"url", urlStr,
		"content_length", resp.ContentLength,
		"content_type", resp.Header.Get("Content-Type"),
	)

	// 读取文件内容
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		slog.Error("Matrix 读取媒体数据失败",
			"url", urlStr,
			"error", err,
		)
		return "", err
	}

	// 检测 MIME 类型
	if mimeType == "" {
		mimeType = resp.Header.Get("Content-Type")
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
	}

	// 上传到 Matrix 媒体仓库
	slog.Debug("Matrix 开始上传到媒体仓库",
		"size", len(data),
		"mime_type", mimeType,
		"user_id", intent.UserID,
	)

	uploadResp, err := intent.UploadBytes(ctx, data, mimeType)
	if err != nil {
		slog.Error("Matrix 上传到媒体仓库失败",
			"url", urlStr,
			"size", len(data),
			"mime_type", mimeType,
			"error", err,
		)
		return "", err
	}

	mxc := string(uploadResp.ContentURI.CUString())
	slog.Debug("Matrix 媒体上传成功",
		"original_url", urlStr,
		"mxc", mxc,
		"size", len(data),
	)

	return mxc, nil
}

// AppServiceStateStore 扩展的状态存储
// 缓存 Ghost 用户注册状态和房间加入规则
type AppServiceStateStore struct {
	mautrix.StateStore          // 基础状态存储
	registrations      sync.Map // Ghost 用户注册状态缓存
	joinRules          sync.Map // 房间加入规则缓存
}

// IsRegistered 检查用户是否已注册
func (s *AppServiceStateStore) IsRegistered(ctx context.Context, userID id.UserID) (bool, error) {
	val, ok := s.registrations.Load(userID)
	if !ok {
		return false, nil
	}
	return val.(bool), nil
}

// MarkRegistered 标记用户已注册
func (s *AppServiceStateStore) MarkRegistered(ctx context.Context, userID id.UserID) error {
	s.registrations.Store(userID, true)
	return nil
}

// SetJoinRules 设置房间加入规则
func (s *AppServiceStateStore) SetJoinRules(ctx context.Context, roomID id.RoomID, content *event.JoinRulesEventContent) error {
	if content != nil {
		s.joinRules.Store(roomID, content)
	}
	return nil
}

// GetJoinRules 获取房间加入规则
func (s *AppServiceStateStore) GetJoinRules(ctx context.Context, roomID id.RoomID) (*event.JoinRulesEventContent, error) {
	if val, ok := s.joinRules.Load(roomID); ok {
		return val.(*event.JoinRulesEventContent), nil
	}
	return &event.JoinRulesEventContent{JoinRule: event.JoinRuleInvite}, nil // 默认为邀请制
}

// GetPowerLevel 获取用户在房间的权限等级
func (s *AppServiceStateStore) GetPowerLevel(ctx context.Context, roomID id.RoomID, userID id.UserID) (int, error) {
	levels, err := s.GetPowerLevels(ctx, roomID)
	if err != nil || levels == nil {
		return 0, err
	}
	return levels.GetUserLevel(userID), nil
}

// GetPowerLevelRequirement 获取事件类型所需的权限等级
func (s *AppServiceStateStore) GetPowerLevelRequirement(ctx context.Context, roomID id.RoomID, eventType event.Type) (int, error) {
	levels, err := s.GetPowerLevels(ctx, roomID)
	if err != nil || levels == nil {
		return 0, err
	}
	return levels.GetEventLevel(eventType), nil
}

// HasPowerLevel 检查用户是否有足够权限发送指定类型的事件
func (s *AppServiceStateStore) HasPowerLevel(ctx context.Context, roomID id.RoomID, userID id.UserID, eventType event.Type) (bool, error) {
	userLevel, err := s.GetPowerLevel(ctx, roomID, userID)
	if err != nil {
		return false, err
	}
	required, err := s.GetPowerLevelRequirement(ctx, roomID, eventType)
	if err != nil {
		return false, err
	}
	return userLevel >= required, nil
}

// sanitize 将字符串转换为安全的 Matrix localpart
// 只保留小写字母、数字、连字符、点和下划线
func (m *Matrix) sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '.' || r == '_' {
			return r
		}
		return '_'
	}, strings.ToLower(s))
}

// extractPort 从地址字符串提取端口部分
// 参数:
//   - addr: 监听地址（可能包含协议）
//
// 返回:
//   - string: 端口部分或原始地址
func extractPort(addr string) string {
	u, err := url.Parse(addr)
	if err == nil && u.Host != "" {
		return u.Host
	}
	return addr
}
