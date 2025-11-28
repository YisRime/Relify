package internal

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"golang.org/x/sync/singleflight"
)

// Router 负责管理跨平台的消息路由逻辑。
// 它处理消息的分发、自动桥接的建立以及防止消息回环（Echo）。
type Router struct {
	config    *Config
	registry  *Registry
	store     *Store
	sf        singleflight.Group
	echoCache *ttlcache.Cache[string, int64]
	eventPool sync.Pool
	workerSem chan struct{}
}

// NewRouter 创建并初始化一个新的 Router 实例。
// 包含：
// - 初始化回声检测缓存 (5分钟 TTL)。
// - 设置 Event 对象池以复用内存。
// - 初始化并发控制信号量。
func NewRouter(cfg *Config, reg *Registry, s *Store) *Router {
	cache := ttlcache.New(
		ttlcache.WithTTL[string, int64](5*time.Minute),
		ttlcache.WithDisableTouchOnHit[string, int64](),
	)
	go cache.Start()

	router := &Router{
		config:    cfg,
		registry:  reg,
		store:     s,
		echoCache: cache,
		eventPool: sync.Pool{
			New: func() any { return &Event{} },
		},
		workerSem: make(chan struct{}, 1000),
	}
	return router
}

// Stop 停止路由器的后台任务，如缓存清理。
func (r *Router) Stop() {
	if r.echoCache != nil {
		r.echoCache.Stop()
	}
}

// FindMapping 实现 API 接口，用于查找消息 ID 映射关系。
func (r *Router) FindMapping(srcPlat, srcMsg, dstPlat string) (string, bool) {
	return r.store.FindMapping(srcPlat, srcMsg, dstPlat)
}

// Receive 是处理接收到的事件的主要入口点。
// 流程：
// 1. 记录调试日志。
// 2. 检查回声缓存，过滤掉自己发出的消息。
// 3. 获取或创建桥接组。
// 4. 并发地将消息分发到桥接组中的其他节点。
func (r *Router) Receive(ctx context.Context, event *Event) {
	senderID := ""
	if event.Sender != nil {
		senderID = event.Sender.ID
	}

	slog.Debug("收到消息",
		"platform", event.Platform,
		"room", event.RoomID,
		"sender", senderID,
		"type", event.Type,
		"msg_id", event.ID,
		"segments", len(event.Segments),
	)

	// 回声检测：如果消息ID在缓存中，说明是本系统转发产生的，应忽略
	if item := r.echoCache.Get(event.Platform + ":" + event.ID); item != nil {
		slog.Debug("过滤回声消息", "platform", event.Platform, "msg_id", event.ID)
		return
	}

	// 获取桥接组
	group := r.store.GetBridge(event.Platform, event.RoomID)

	// 如果没有现有桥接，尝试建立新桥接
	if group == nil {
		srcDriver, ok := r.registry.GetDriver(event.Platform)
		if !ok {
			return
		}

		var err error
		if group, err = r.MatchAndBridge(ctx, event, srcDriver); err != nil {
			slog.Warn("建立桥接失败", "platform", event.Platform, "room", event.RoomID, "err", err)
			return
		}
	}

	// 并发分发消息
	var wg sync.WaitGroup
	for _, node := range group.Nodes {
		// 跳过源平台
		if node.Platform == event.Platform {
			continue
		}
		// 获取目标驱动
		if drv, ok := r.registry.GetDriver(node.Platform); ok {
			wg.Add(1)
			// 申请信号量，控制并发度
			r.workerSem <- struct{}{}

			go func(n BridgeNode, d Driver) {
				defer func() {
					<-r.workerSem
					wg.Done()
				}()
				r.Dispatch(ctx, d, event, &n, group.ID)
			}(node, drv)
		}
	}
	wg.Wait()
}

// MatchAndBridge 执行自动桥接匹配逻辑。
// 使用 singleflight 防止对同一房间的并发建桥请求。
// 逻辑：
// 1. 获取源房间信息。
// 2. 确定目标平台列表（Hub 模式或 Mesh 模式）。
// 3. 在目标平台创建对应的房间。
// 4. 将所有节点保存为新的桥接组。
func (r *Router) MatchAndBridge(ctx context.Context, event *Event, srcDriver Driver) (*BridgeGroup, error) {
	// Hub 模式下，Hub 平台本身不触发建桥
	if r.config.Mode == "hub" && event.Platform == r.config.Hub {
		return nil, nil
	}

	key := event.Platform + ":" + event.RoomID

	result, err, _ := r.sf.Do(key, func() (any, error) {
		// 双重检查：防止在等待锁期间桥接已被创建
		if group := r.store.GetBridge(event.Platform, event.RoomID); group != nil {
			return group, nil
		}

		roomInfo, _ := srcDriver.GetRoomInfo(ctx, event.RoomID)
		if roomInfo == nil {
			roomInfo = &RoomInfo{Name: event.RoomID}
		}

		nodes := []BridgeNode{{Platform: event.Platform, RoomID: event.RoomID}}
		var targetPlatforms []string

		if r.config.Mode == "hub" {
			targetPlatforms = []string{r.config.Hub}
		} else {
			for name := range r.registry.GetAllDrivers() {
				if name != event.Platform {
					targetPlatforms = append(targetPlatforms, name)
				}
			}
		}

		for _, targetName := range targetPlatforms {
			destDriver, ok := r.registry.GetDriver(targetName)
			if !ok {
				continue
			}

			var targetRoomID string
			var err error

			if r.registry.GetRoutePolicy(targetName) == PolicyMix {
				targetRoomID, err = destDriver.CreateRoom(ctx, nil)
			} else {
				targetRoomID, err = destDriver.CreateRoom(ctx, &RoomInfo{
					Name:   fmt.Sprintf("[%s]%s", event.Platform, roomInfo.Name),
					Avatar: roomInfo.Avatar,
					Topic:  "Relify Bridge",
				})
			}

			if err != nil {
				continue
			}
			nodes = append(nodes, BridgeNode{Platform: targetName, RoomID: targetRoomID})
		}

		if len(nodes) < 2 {
			return nil, fmt.Errorf("无可用平台")
		}

		bridge, err := r.store.CreateBridge(nodes)
		if err != nil {
			return nil, err
		}
		slog.Info("创建桥接成功", "platform", event.Platform, "room", event.RoomID, "bridge_id", bridge.ID)
		return bridge, nil
	})

	if err != nil {
		return nil, err
	}
	return result.(*BridgeGroup), nil
}

// Dispatch 将事件处理并发送到目标驱动。
// 流程：
// 1. 从对象池获取 Event 对象并复制源数据。
// 2. 调用目标驱动的 Send 方法。
// 3. 如果发送成功且是消息类型，保存 ID 映射关系并更新回声缓存。
func (r *Router) Dispatch(ctx context.Context, destDriver Driver, srcEvent *Event, node *BridgeNode, bridgeID int64) {
	outEvent := r.eventPool.Get().(*Event)
	defer func() {
		outEvent.Reset()
		r.eventPool.Put(outEvent)
	}()

	r.copyEvent(srcEvent, outEvent)

	results, err := destDriver.Send(ctx, node, outEvent)

	if err != nil {
		slog.Warn("投递消息失败", "err", err, "target", node.Platform, "room", node.RoomID)
		return
	}

	// 收集成功发送的消息ID
	var newIDs []string
	for _, res := range results {
		if res.Error == nil && res.MsgID != "" {
			newIDs = append(newIDs, res.MsgID)
		} else if res.Error != nil {
			slog.Debug("部分消息发送失败", "target", node.Platform, "err", res.Error)
		}
	}

	if len(newIDs) > 0 && srcEvent.Type == TypeMessage {
		r.store.SaveMapping(srcEvent.Platform, srcEvent.ID, node.Platform, newIDs, bridgeID)

		slog.Debug("投递消息成功",
			"to_platform", node.Platform,
			"to_room", node.RoomID,
			"new_ids", newIDs,
		)

		// 将新生成的消息 ID 加入回声缓存，防止死循环
		now := time.Now().Unix()
		for _, nid := range newIDs {
			r.echoCache.Set(node.Platform+":"+nid, now, ttlcache.DefaultTTL)
		}
	}
}

// copyEvent 将源事件的数据深度复制到目标事件对象中。
// 用于在分发给不同驱动时保持数据隔离。
func (r *Router) copyEvent(src *Event, dst *Event) {
	dst.ID = src.ID
	dst.Type = src.Type
	dst.Time = src.Time
	dst.Platform = src.Platform
	dst.RoomID = src.RoomID
	dst.RefID = src.RefID

	if src.Sender != nil {
		dst.Sender = &Sender{
			ID:     src.Sender.ID,
			Name:   src.Sender.Name,
			Type:   src.Sender.Type,
			Avatar: src.Sender.Avatar,
		}
		if len(src.Sender.Role) > 0 {
			dst.Sender.Role = make(Properties, len(src.Sender.Role))
			for k, v := range src.Sender.Role {
				dst.Sender.Role[k] = v
			}
		}
	}

	if len(src.Segments) > 0 {
		// 确保切片容量
		if cap(dst.Segments) < len(src.Segments) {
			dst.Segments = make([]Segment, len(src.Segments))
		} else {
			dst.Segments = dst.Segments[:len(src.Segments)]
		}

		for i, s := range src.Segments {
			dSeg := Segment{
				Type: s.Type,
				ID:   s.ID,
				Text: s.Text,
			}

			if s.File != nil {
				dSeg.File = &FileInfo{
					ID:       s.File.ID,
					URL:      s.File.URL,
					Name:     s.File.Name,
					MimeType: s.File.MimeType,
					Size:     s.File.Size,
					Duration: s.File.Duration,
					Width:    s.File.Width,
					Height:   s.File.Height,
				}
			}

			if len(s.Extra) > 0 {
				dSeg.Extra = make(Properties, len(s.Extra))
				for k, v := range s.Extra {
					dSeg.Extra[k] = v
				}
			}

			dst.Segments[i] = dSeg
		}
	} else {
		dst.Segments = dst.Segments[:0]
	}

	if len(src.Extra) > 0 {
		dst.Extra = make(Properties, len(src.Extra))
		for k, v := range src.Extra {
			dst.Extra[k] = v
		}
	}
}
