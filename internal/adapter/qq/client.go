package qq

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Config QQ 适配器的配置
type Config struct {
	Protocol string `json:"protocol" yaml:"protocol"` // 协议类型: "ws" 或 "http"
	URL      string `json:"url" yaml:"url"`           // OneBot 服务器地址
	Listen   string `json:"listen" yaml:"listen"`     // HTTP 监听地址（HTTP 模式）
	Secret   string `json:"secret" yaml:"secret"`     // 鉴权密钥
	Group    string `json:"group" yaml:"group"`       // 群组 ID 列表（逗号分隔）
}

// Client OneBot 协议客户端
// 支持 WebSocket 和 HTTP 两种通信方式
type Client struct {
	cfg     *Config
	handler func([]byte) // 事件处理函数

	conn    *websocket.Conn // WebSocket 连接
	mu      sync.Mutex      // 连接锁
	echos   sync.Map        // API 调用响应通道 map[string]chan []byte
	closeCh chan struct{}   // 关闭信号
}

// NewClient 创建 OneBot 客户端
// 参数:
//   - cfg: 配置信息
//   - handler: 事件处理函数
//
// 返回:
//   - *Client: 客户端实例
func NewClient(cfg *Config, handler func([]byte)) *Client {
	return &Client{
		cfg:     cfg,
		handler: handler,
		closeCh: make(chan struct{}),
	}
}

// Connect 连接到 OneBot 服务器
// 根据协议类型选择 WebSocket 或 HTTP 模式
// 参数:
//   - ctx: 上下文
func (c *Client) Connect(ctx context.Context) {
	if c.cfg.Protocol == "http" {
		c.startHTTPServer(ctx) // HTTP 模式：启动 HTTP 服务器
	} else {
		c.startWSClient(ctx) // WebSocket 模式：连接到 WebSocket 服务器
	}
}

// startWSClient 启动 WebSocket 客户端（带自动重连）
// 参数:
//   - ctx: 上下文
func (c *Client) startWSClient(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute) // 重连间隔
	defer ticker.Stop()

	for {
		// 检查上下文或关闭信号
		select {
		case <-ctx.Done():
			return
		case <-c.closeCh:
			return
		default:
		}

		slog.Info("QQ 尝试连接", "url", c.cfg.URL)

		// 设置鉴权头
		header := http.Header{}
		if c.cfg.Secret != "" {
			header.Set("Authorization", "Bearer "+c.cfg.Secret)
		}

		// 连接到 WebSocket 服务器
		conn, _, err := websocket.DefaultDialer.Dial(c.cfg.URL, header)
		if err != nil {
			slog.Warn("QQ 连接失败", "error", err)
			// 连接失败，等待重试
			select {
			case <-ticker.C:
				continue
			case <-ctx.Done():
				return
			case <-c.closeCh:
				return
			}
		}

		// 保存连接
		c.mu.Lock()
		c.conn = conn
		c.mu.Unlock()

		slog.Info("QQ 连接成功")

		// 读取消息循环
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				slog.Warn("QQ 连接断开", "error", err)
				break // 连接断开，重新连接
			}
			c.processMessage(msg) // 处理消息
		}

		// 清除连接
		c.mu.Lock()
		c.conn = nil
		c.mu.Unlock()

		// 等待重试
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		case <-c.closeCh:
			return
		}
	}
}

// startHTTPServer 启动 HTTP 服务器（接收 OneBot 事件推送）
// 参数:
//   - ctx: 上下文
func (c *Client) startHTTPServer(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", c.handleHTTPRequest)

	addr := c.cfg.Listen
	if addr == "" {
		addr = ":8080" // 默认监听端口
	}

	server := &http.Server{Addr: addr, Handler: mux}

	// 监听上下文取消，关闭服务器
	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
	}()

	_ = server.ListenAndServe()
}

// handleHTTPRequest 处理 HTTP 请求（OneBot 事件推送）
// 参数:
//   - w: HTTP 响应
//   - r: HTTP 请求
func (c *Client) handleHTTPRequest(w http.ResponseWriter, r *http.Request) {
	// 仅接受 POST 请求
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// 读取请求体
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// 验证签名（如果配置了密钥）
	if c.cfg.Secret != "" && !c.verifySignature(r.Header.Get("X-Signature"), body) {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	// 异步处理事件
	if c.handler != nil {
		go c.handler(body)
	}
	w.WriteHeader(http.StatusNoContent)
}

// verifySignature 验证 OneBot HTTP 请求签名
// 使用 HMAC-SHA1 算法验证消息完整性
// 参数:
//   - signature: 签名字符串（格式: "sha1=<hex>"）
//   - body: 请求体
//
// 返回:
//   - bool: 签名是否有效
func (c *Client) verifySignature(signature string, body []byte) bool {
	if signature == "" {
		return false
	}

	// 移除 "sha1=" 前缀
	if len(signature) > 5 {
		signature = signature[5:]
	}

	// 计算 HMAC-SHA1
	mac := hmac.New(sha1.New, []byte(c.cfg.Secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(signature), []byte(expected))
}

// processMessage 处理收到的消息
// 区分 API 响应和事件推送
// 参数:
//   - msg: 消息内容
func (c *Client) processMessage(msg []byte) {
	// 尝试解析为 API 响应（包含 echo 字段）
	var resp struct {
		Echo string `json:"echo"`
	}
	if json.Unmarshal(msg, &resp) == nil && resp.Echo != "" {
		// 查找对应的响应通道
		if ch, ok := c.echos.Load(resp.Echo); ok {
			ch.(chan []byte) <- msg
		}
		return
	}

	// 否则视为事件推送
	if c.handler != nil {
		go c.handler(msg)
	}
}

// Close 关闭客户端
func (c *Client) Close() {
	close(c.closeCh)
	c.mu.Lock()
	if c.conn != nil {
		_ = c.conn.Close()
	}
	c.mu.Unlock()
}

// Call 调用 OneBot API
// 根据协议类型选择 WebSocket 或 HTTP
// 参数:
//   - ctx: 上下文
//   - action: API 动作名称
//   - params: 参数
//
// 返回:
//   - []byte: 响应数据
//   - error: 错误信息
func (c *Client) Call(ctx context.Context, action string, params any) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 1*time.Minute)
	defer cancel()

	if c.cfg.Protocol == "http" {
		return c.callHTTP(ctx, action, params)
	}
	return c.callWS(ctx, action, params)
}

// callWS 通过 WebSocket 调用 API
// 参数:
//   - ctx: 上下文
//   - action: API 动作名称
//   - params: 参数
//
// 返回:
//   - []byte: 响应数据
//   - error: 错误信息
func (c *Client) callWS(ctx context.Context, action string, params any) ([]byte, error) {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return nil, fmt.Errorf("WebSocket未连接")
	}

	// 生成唯一 echo ID（用于匹配响应）
	echo := strconv.FormatInt(time.Now().UnixNano(), 10)
	req := map[string]any{
		"action": action,
		"params": params,
		"echo":   echo,
	}

	// 创建响应通道
	resCh := make(chan []byte, 1)
	c.echos.Store(echo, resCh)
	defer c.echos.Delete(echo)

	// 发送请求
	if err := conn.WriteJSON(req); err != nil {
		return nil, err
	}

	// 等待响应
	select {
	case res := <-resCh:
		return res, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// callHTTP 通过 HTTP 调用 API
// 参数:
//   - ctx: 上下文
//   - action: API 动作名称
//   - params: 参数
//
// 返回:
//   - []byte: 响应数据
//   - error: 错误信息
func (c *Client) callHTTP(ctx context.Context, action string, params any) ([]byte, error) {
	// 构建 API URL
	url := fmt.Sprintf("%s/%s", c.cfg.URL, action)
	body, _ := json.Marshal(params)

	// 创建 HTTP 请求
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.Secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.Secret)
	}

	// 发送请求
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// 检查状态码
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP状态码: %d", resp.StatusCode)
	}

	// 读取响应
	result, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return result, nil
}
