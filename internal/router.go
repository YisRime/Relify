package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
)

// Router 负责在不同平台之间路由和转发消息
// 管理消息映射、用户映射和房间映射
type Router struct {
	cfg        *Config   // 应用配置
	reg        *Registry // 驱动注册表
	store      *Store    // 数据存储
	matchLocks sync.Map  // 房间匹配锁，防止并发创建桥接
}

// NewRouter 创建新的路由器实例
// 参数:
//   - cfg: 应用配置
//   - reg: 驱动注册表
//   - s: 数据存储
//
// 返回:
//   - *Router: 新的路由器实例
func NewRouter(cfg *Config, reg *Registry, s *Store) *Router {
	return &Router{cfg: cfg, reg: reg, store: s}
}

// Mode 返回当前的运行模式（hub 或 peer）
// 返回:
//   - string: 运行模式
func (r *Router) Mode() string {
	if r.cfg == nil {
		return ""
	}
	return r.cfg.Mode
}

// MapUser 查找用户在不同平台间的映射
// 参数:
//   - srcPlat: 源平台名称
//   - srcUser: 源平台用户 ID
//   - dstPlat: 目标平台名称
//
// 返回:
//   - string: 目标平台的用户 ID
//   - bool: 是否找到映射
func (r *Router) MapUser(srcPlat, srcUser, dstPlat string) (string, bool) {
	return r.store.FindUser(srcPlat, srcUser, dstPlat)
}

// MapMessage 查找消息在不同平台间的映射
// 参数:
//   - srcPlat: 源平台名称
//   - srcMsg: 源平台消息 ID
//   - dstPlat: 目标平台名称
//
// 返回:
//   - string: 目标平台的消息 ID
//   - bool: 是否找到映射
func (r *Router) MapMessage(srcPlat, srcMsg, dstPlat string) (string, bool) {
	return r.store.Seek(srcPlat, srcMsg, dstPlat)
}

// MapRoom 查找房间在不同平台间的映射
// 参数:
//   - srcPlat: 源平台名称
//   - srcRoom: 源平台房间 ID
//   - dstPlat: 目标平台名称
//
// 返回:
//   - string: 目标平台的房间 ID
//   - bool: 是否找到映射
func (r *Router) MapRoom(srcPlat, srcRoom, dstPlat string) (string, bool) {
	return r.store.TargetRoom(srcPlat, srcRoom, dstPlat)
}

// Handle 处理来自平台的事件并转发到其他平台
// 参数:
//   - ctx: 上下文
//   - e: 要处理的事件
//
// 返回:
//   - error: 处理过程中的错误
func (r *Router) Handle(ctx context.Context, e *Event) error {
	// 快速路径：提前过滤无效事件
	if !r.isValidEvent(e) {
		slog.Debug("忽略无效事件", "platform", e.Plat, "room", e.Room, "kind", e.Kind)
		return nil
	}

	slog.Debug("接收事件",
		"platform", e.Plat,
		"room", e.Room,
		"user", e.User,
		"kind", e.Kind,
		"id", e.ID,
		"raw", func() string {
			if data, err := json.Marshal(e); err == nil {
				return string(data)
			}
			return ""
		}(),
	)

	// 获取源平台驱动
	src, ok := r.reg.Get(e.Plat)
	if !ok {
		slog.Warn("未找到源平台驱动", "platform", e.Plat)
		return nil
	}

	// 查找该房间的桥接配置
	binds := r.store.Find(e.Plat, e.Room)

	// 如果没有找到现有桥接，尝试自动匹配创建
	if len(binds) == 0 {
		slog.Info("尝试自动匹配房间",
			"platform", e.Plat,
			"room", e.Room,
		)
		var err error
		if binds, err = r.Match(ctx, e, src); err != nil {
			slog.Warn("房间匹配失败",
				"platform", e.Plat,
				"room", e.Room,
				"error", err,
			)
			return nil // 匹配失败，不处理该事件
		}
		slog.Info("房间匹配成功",
			"platform", e.Plat,
			"room", e.Room,
			"binds", len(binds),
		)
	}

	// 收集目标平台和驱动，避免重复查询
	targets := r.collectTargets(e.Plat, binds)
	if len(targets) == 0 {
		slog.Debug("没有有效的目标平台", "platform", e.Plat, "room", e.Room)
		return nil // 没有有效的目标平台
	}

	slog.Debug("转发事件",
		"platform", e.Plat,
		"targets", len(targets),
		"kind", e.Kind,
	)

	// 预先查询消息引用的跨平台映射
	refMappings := r.prepareRefMappings(e, targets)

	// 并发转发到所有目标平台
	var wg sync.WaitGroup
	wg.Add(len(targets))
	for _, t := range targets {
		go func(target targetInfo) {
			defer wg.Done()
			r.Push(ctx, target.driver, e, &target.node, target.bindID, refMappings)
		}(t)
	}

	wg.Wait() // 等待所有转发完成
	return nil
}

// targetInfo 封装目标平台的所有信息
type targetInfo struct {
	node   Node
	driver Driver
	bindID int64
}

// isValidEvent 快速检查事件是否有效
func (r *Router) isValidEvent(e *Event) bool {
	// 忽略空事件或空消息
	if e == nil || (e.Kind == Msg && len(e.Segs) == 0) {
		return false
	}
	// 检查是否是回声消息
	return !r.store.Echo(e.Plat, e.ID)
}

// collectTargets 收集所有有效的目标平台信息
func (r *Router) collectTargets(srcPlat string, binds []*Group) []targetInfo {
	// 预分配切片容量以减少内存分配
	targets := make([]targetInfo, 0, r.estimateTargetCount(binds))

	for _, b := range binds {
		for _, node := range b.Nodes {
			// 跳过源平台（不回发）
			if node.Plat == srcPlat {
				continue
			}
			// 获取目标平台驱动
			if dst, ok := r.reg.Get(node.Plat); ok {
				targets = append(targets, targetInfo{
					node:   node,
					driver: dst,
					bindID: b.ID,
				})
			}
		}
	}

	return targets
}

// estimateTargetCount 估算目标数量以优化内存分配
func (r *Router) estimateTargetCount(binds []*Group) int {
	count := 0
	for _, b := range binds {
		count += len(b.Nodes)
	}
	// 减去源平台，通常每个 bind 都有一个源平台节点
	if count > len(binds) {
		count -= len(binds)
	}
	return count
}

// prepareRefMappings 预先查询所有目标平台的消息引用映射
// 避免在并发 Push 时重复查询同一个源消息的映射
func (r *Router) prepareRefMappings(e *Event, targets []targetInfo) map[string]string {
	// 判断是否需要转换消息引用
	if !r.needRefConvert(e) || e.Ref == "" {
		return nil // 返回 nil 而不是空 map，节省内存
	}

	// 预分配 map 容量
	refMappings := make(map[string]string, len(targets))

	// 为所有目标平台预查询映射
	for _, target := range targets {
		if tid, ok := r.store.Seek(e.Plat, e.Ref, target.node.Plat); ok {
			refMappings[target.node.Plat] = tid
		}
	}

	// 如果没有找到任何映射，返回 nil
	if len(refMappings) == 0 {
		return nil
	}

	return refMappings
}

// needRefConvert 判断事件是否需要转换消息引用
func (r *Router) needRefConvert(e *Event) bool {
	isRevokeCmd := e.Kind == Note && isRevoke(e)
	isEditMsg := e.Kind == Edit
	return isRevokeCmd || isEditMsg || (e.Kind == Msg && e.Ref != "")
}

// Match 自动匹配并创建桥接配置
// 当收到来自未桥接房间的消息时，自动创建到其他平台的桥接
// 参数:
//   - ctx: 上下文
//   - e: 触发匹配的事件
//   - src: 源平台驱动
//
// 返回:
//   - []*Group: 创建的桥接组列表
//   - error: 匹配过程中的错误
func (r *Router) Match(ctx context.Context, e *Event, src Driver) ([]*Group, error) {
	// 在中心模式下，忽略来自中心平台的消息（避免循环）
	if r.cfg.Mode == "hub" && e.Plat == r.cfg.Hub {
		return nil, nil
	}

	// 使用房间特定的锁防止并发创建重复的桥接
	mu := r.getRoomLock(e.Plat, e.Room)
	mu.Lock()
	defer mu.Unlock()

	// 双重检查：获取锁后再次检查是否已存在桥接
	if binds := r.store.Find(e.Plat, e.Room); len(binds) > 0 {
		return binds, nil
	}

	// 延迟获取房间信息（仅在需要时获取）
	var srcInfo *Info
	getInfo := func() *Info {
		if srcInfo == nil {
			// 从源平台获取房间信息
			i, _ := src.Info(ctx, e.Room)
			if i == nil {
				i = &Info{Name: e.Room} // 如果获取失败，使用房间 ID 作为名称
			}
			srcInfo = i
		}
		return srcInfo
	}

	// 初始化节点列表，包含源房间
	nodes := []Node{{Plat: e.Plat, Room: e.Room}}

	// 根据运行模式获取目标平台和桥接名称
	tPlats, name := r.getTargetPlatforms(e.Plat)
	if len(tPlats) == 0 {
		slog.Warn("无可用目标平台", "platform", e.Plat)
		return nil, fmt.Errorf("无可用目标平台")
	}

	slog.Info("创建新桥接",
		"source", e.Plat,
		"room", e.Room,
		"targets", tPlats,
		"name", name,
	)

	// 为每个目标平台创建或获取房间
	for _, tn := range tPlats {
		dst, ok := r.reg.Get(tn)
		if !ok {
			slog.Warn("目标平台驱动未找到", "platform", tn)
			if r.cfg.Mode == "hub" {
				return nil, fmt.Errorf("中心 %s 离线", tn)
			}
			continue
		}

		tid, err := r.createTargetRoom(ctx, dst, src, getInfo)
		if err != nil {
			slog.Warn("创建目标房间失败",
				"platform", tn,
				"error", err,
			)
			if r.cfg.Mode == "hub" {
				return nil, err
			}
			continue
		}

		slog.Info("目标房间已创建",
			"platform", tn,
			"room", tid,
		)
		nodes = append(nodes, Node{Plat: tn, Room: tid})
	}

	// 至少需要两个节点才能形成桥接
	if len(nodes) < 2 {
		slog.Error("节点数量不足，无法创建桥接",
			"nodes", len(nodes),
			"required", 2,
		)
		return nil, fmt.Errorf("节点数量不足")
	}

	// 将桥接配置保存到数据库
	b, err := r.store.Add(name, nodes)
	if err != nil {
		slog.Error("保存桥接配置失败", "error", err)
		return nil, err
	}

	slog.Info("桥接已创建",
		"id", b.ID,
		"name", name,
		"nodes", len(nodes),
	)

	return []*Group{b}, nil
}

// getRoomLock 获取房间特定的互斥锁
func (r *Router) getRoomLock(plat, room string) *sync.Mutex {
	roomKey := plat + ":" + room
	lockValue, _ := r.matchLocks.LoadOrStore(roomKey, &sync.Mutex{})
	return lockValue.(*sync.Mutex)
}

// getTargetPlatforms 根据运行模式获取目标平台列表和桥接名称
func (r *Router) getTargetPlatforms(srcPlat string) ([]string, string) {
	if r.cfg.Mode == "hub" {
		// 中心模式：只桥接到中心平台
		return []string{r.cfg.Hub}, fmt.Sprintf("中心: %s <-> %s", srcPlat, r.cfg.Hub)
	}

	// 对等模式：桥接到所有其他平台
	tPlats := make([]string, 0, len(r.reg.All())-1)
	for n := range r.reg.All() {
		if n != srcPlat {
			tPlats = append(tPlats, n)
		}
	}
	return tPlats, fmt.Sprintf("对等: %s -> 全部", srcPlat)
}

// createTargetRoom 在目标平台创建或获取房间
func (r *Router) createTargetRoom(ctx context.Context, dst, src Driver, getInfo func() *Info) (string, error) {
	// 根据目标平台的路由模式创建房间
	if dst.Route() == RouteMix {
		// 混合模式：使用现有房间或获取默认房间
		return dst.Make(ctx, nil)
	}

	// 镜像模式：为每个桥接创建新房间
	info := getInfo()
	newInfo := &Info{
		Name:   fmt.Sprintf("[%s]%s", src.Name(), info.Name),
		Avatar: info.Avatar,
	}
	if info.Topic != "" {
		newInfo.Topic = "通过 Relify"
	}
	return dst.Make(ctx, newInfo)
}

// Push 将事件推送到目标平台（使用预查询的引用映射）
// 处理事件修复、发送和消息映射
func (r *Router) Push(ctx context.Context, dst Driver, srcEvt *Event, node *Node, bid int64, refMappings map[string]string) {
	// 修复事件以适配目标平台
	out := r.Fix(srcEvt, node, refMappings)
	if out == nil {
		slog.Debug("事件修复失败，跳过推送",
			"platform", node.Plat,
			"room", node.Room,
		)
		return // 如果修复失败，放弃推送
	}

	// 发送到目标平台
	nid, err := dst.Send(ctx, node, out)
	if err != nil {
		slog.Warn("发送事件失败",
			"platform", node.Plat,
			"room", node.Room,
			"error", err,
		)
		return // 发送失败，忽略错误
	}

	slog.Debug("事件已发送",
		"source", srcEvt.Plat,
		"target", node.Plat,
		"id", nid,
	)

	// 如果是消息事件且发送成功，保存消息映射
	if out.Kind == Msg && nid != "" {
		r.store.Map(srcEvt.Plat, srcEvt.ID, node.Plat, nid, bid)
		slog.Debug("消息映射已保存",
			"source", srcEvt.Plat,
			"source_id", srcEvt.ID,
			"target", node.Plat,
			"target_id", nid,
		)
	}
}

// Fix 修复事件以适配目标平台，使用预查询的引用映射
func (r *Router) Fix(src *Event, node *Node, refMappings map[string]string) *Event {
	// 判断是否需要转换消息引用
	if !r.needRefConvert(src) {
		return copyEvt(src) // 不需要引用转换，直接拷贝返回
	}

	// 检查关键操作（撤回和编辑）是否缺少必要的引用
	isRevokeCmd := src.Kind == Note && isRevoke(src)
	isEditMsg := src.Kind == Edit
	isCriticalOp := isRevokeCmd || isEditMsg

	if src.Ref == "" {
		// 关键操作必须有引用
		if isCriticalOp {
			return nil
		}
		return copyEvt(src) // 普通消息可以没有引用
	}

	// 复制事件
	dst := copyEvt(src)

	// 查找目标平台的引用映射
	var tid string
	var found bool

	if refMappings != nil {
		tid, found = refMappings[node.Plat]
	} else {
		// 如果没有提供映射，实时查询
		tid, found = r.MapMessage(src.Plat, src.Ref, node.Plat)
	}

	if found {
		dst.Ref = tid
	} else {
		// 未找到映射的处理
		if isCriticalOp {
			return nil // 关键操作必须找到引用映射
		}
		dst.Ref = "" // 普通回复可以移除引用
	}

	return dst
}

// isRevoke 检查事件是否为撤回通知
func isRevoke(e *Event) bool {
	if v, ok := e.Extra["subtype"]; ok {
		if s, ok := v.(string); ok {
			return s == Revoke
		}
	}
	return false
}

// copyEvt 深拷贝事件对象
func copyEvt(src *Event) *Event {
	if src == nil {
		return nil
	}

	dst := &Event{
		Kind:  src.Kind,
		Plat:  src.Plat,
		Room:  src.Room,
		User:  src.User,
		ID:    src.ID,
		Ref:   src.Ref,
		Time:  src.Time,
		Segs:  copySegs(src.Segs),
		Extra: copyMap(src.Extra),
	}
	return dst
}

// copySegs 深拷贝消息段切片
func copySegs(src []Seg) []Seg {
	if src == nil {
		return nil
	}

	dst := make([]Seg, len(src))
	for i, s := range src {
		dst[i] = Seg{
			Kind: s.Kind,
			Raw:  copyMap(s.Raw),
		}
	}
	return dst
}

// copyMap 深拷贝 Props 映射
func copyMap(src Props) Props {
	if src == nil {
		return nil
	}
	dst := make(Props, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
