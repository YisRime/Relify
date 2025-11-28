package internal

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jellydator/ttlcache/v3"
	_ "modernc.org/sqlite"
)

// Operation 定义了在数据库事务上下文中执行的函数签名。
// 所有的写操作都应封装为此类型，以便通过 worker 进行批量处理。
type Operation func(*sql.Tx) error

// Store 负责应用程序的数据持久化存储管理。
// 它使用 SQLite 作为后端数据库，并实现了以下特性：
// - 基于通道的异步写操作队列。
// - 批量事务提交以优化 I/O 性能。
// - 内存缓存 (TTL Cache) 用于加速热点数据读取。
// - 启动时的数据预热。
type Store struct {
	db         *sql.DB
	cache      *ttlcache.Cache[string, *BridgeGroup]
	operations chan Operation
	stopChan   chan struct{}
	waitGroup  sync.WaitGroup
}

// NewStore 初始化并返回一个新的 Store 实例。
// 该函数会执行以下操作：
// 1. 打开 SQLite 数据库连接并配置 WAL 模式。
// 2. 创建必要的数据表和索引 (bridges, mappings)。
// 3. 启动后台 worker 协程用于处理写操作。
// 4. 启动后台定时任务用于清理过期的消息映射。
// 5. 执行缓存预热。
func NewStore(path string, retentionDays int) (*Store, error) {
	db, err := sql.Open("sqlite", fmt.Sprintf("%s?_journal=WAL&_timeout=5000", path))
	if err != nil {
		return nil, err
	}

	queries := []string{
		`CREATE TABLE IF NOT EXISTS bridges (
			id INTEGER, 
			platform TEXT, 
			room_id TEXT, 
			config TEXT, 
			PRIMARY KEY (platform, room_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_bridge_id ON bridges(id)`,
		`CREATE TABLE IF NOT EXISTS mappings (
			src_platform TEXT, 
			src_msg_id TEXT, 
			dst_platform TEXT, 
			dst_msg_id TEXT, 
			bridge_id INTEGER, 
			timestamp INTEGER, 
			PRIMARY KEY (src_platform, src_msg_id, dst_platform, dst_msg_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_mapping_time ON mappings(timestamp)`,
	}

	for _, q := range queries {
		if _, err := db.Exec(q); err != nil {
			db.Close()
			return nil, err
		}
	}

	// 初始化泛型缓存
	cache := ttlcache.New(
		ttlcache.WithDisableTouchOnHit[string, *BridgeGroup](),
	)

	store := &Store{
		db:         db,
		cache:      cache,
		operations: make(chan Operation, 2000),
		stopChan:   make(chan struct{}),
	}

	if err := store.preload(); err != nil {
		slog.Warn("缓存预热失败", "err", err)
	}

	store.waitGroup.Add(1)
	go store.worker()

	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		for range ticker.C {
			expireTime := time.Now().Add(time.Duration(-retentionDays) * 24 * time.Hour).Unix()
			store.PushOperation(func(tx *sql.Tx) error {
				_, err := tx.Exec("DELETE FROM mappings WHERE timestamp < ?", expireTime)
				return err
			})
		}
	}()

	return store, nil
}

// preload 从数据库加载所有的桥接配置 (bridges) 并填充到内存缓存中。
// 这可以减少运行时的数据库查询频率，提高路由匹配速度。
func (s *Store) preload() error {
	rows, err := s.db.Query("SELECT id, platform, room_id, config FROM bridges")
	if err != nil {
		return err
	}
	defer rows.Close()

	tempMap := make(map[int64]*BridgeGroup)

	for rows.Next() {
		var id int64
		var p, r, c string
		if err := rows.Scan(&id, &p, &r, &c); err != nil {
			continue
		}

		node := BridgeNode{Platform: p, RoomID: r}
		if c != "" {
			json.Unmarshal([]byte(c), &node.Config)
		}

		if _, ok := tempMap[id]; !ok {
			tempMap[id] = &BridgeGroup{ID: id}
		}
		tempMap[id].Nodes = append(tempMap[id].Nodes, node)
	}

	for _, group := range tempMap {
		for _, node := range group.Nodes {
			s.cache.Set(node.Platform+":"+node.RoomID, group, ttlcache.NoTTL)
		}
	}
	slog.Info("缓存预热完成", "bridge_count", len(tempMap))
	return nil
}

// Close 优雅地关闭 Store。
// 它会停止缓存，关闭 worker 通道，等待所有待处理的写操作完成，最后关闭数据库连接。
func (s *Store) Close() error {
	s.cache.Stop()
	close(s.stopChan)
	s.waitGroup.Wait()
	return s.db.Close()
}

// worker 是一个后台协程，负责从操作队列中提取请求并批量执行。
// 它会在累积一定数量的操作或达到时间间隔后，在一个数据库事务中提交这些操作。
func (s *Store) worker() {
	defer s.waitGroup.Done()
	batch := make([]Operation, 0, 100)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	executeBatch := func() {
		if len(batch) == 0 {
			return
		}
		if tx, err := s.db.Begin(); err == nil {
			for _, op := range batch {
				if err := op(tx); err != nil {
					slog.Error("执行事务失败", "err", err)
				}
			}
			if err := tx.Commit(); err != nil {
				slog.Error("提交事务失败", "err", err)
			}
		} else {
			slog.Error("开启事务失败", "err", err)
		}
		batch = batch[:0]
	}

	for {
		select {
		case op := <-s.operations:
			batch = append(batch, op)
			if len(batch) >= 100 {
				executeBatch()
			}
		case <-ticker.C:
			executeBatch()
		case <-s.stopChan:
			executeBatch()
			return
		}
	}
}

// PushOperation 将一个数据库写操作添加到异步处理队列中。
// 该操作是非阻塞的，除非队列已满。
func (s *Store) PushOperation(op Operation) {
	s.operations <- op
}

// SaveMapping 异步保存源消息 ID 与目标消息 ID 之间的映射关系。
// 用于后续的消息引用（如回复）或撤回操作。
func (s *Store) SaveMapping(srcPlat, srcMsgID, dstPlat string, dstMsgIDs []string, bridgeID int64) {
	if len(dstMsgIDs) == 0 {
		return
	}
	ts := time.Now().Unix()
	s.PushOperation(func(tx *sql.Tx) error {
		for _, dstMsgID := range dstMsgIDs {
			_, _ = tx.Exec(
				"INSERT OR IGNORE INTO mappings (src_platform, src_msg_id, dst_platform, dst_msg_id, bridge_id, timestamp) VALUES (?, ?, ?, ?, ?, ?)",
				srcPlat, srcMsgID, dstPlat, dstMsgID, bridgeID, ts,
			)
		}
		return nil
	})
}

// FindMapping 根据源平台、源消息 ID 和目标平台，查找对应的目标消息 ID。
// 返回目标消息 ID 和一个布尔值（表示是否找到）。
func (s *Store) FindMapping(srcPlat, srcMsgID, dstPlat string) (string, bool) {
	var dstMsgID string
	err := s.db.QueryRow(
		"SELECT dst_msg_id FROM mappings WHERE src_platform=? AND src_msg_id=? AND dst_platform=? ORDER BY rowid DESC LIMIT 1",
		srcPlat, srcMsgID, dstPlat,
	).Scan(&dstMsgID)
	return dstMsgID, err == nil
}

// GetBridge 从缓存中检索指定平台和房间所属的桥接组信息。
// 如果缓存未命中，返回 nil。
func (s *Store) GetBridge(platform, roomID string) *BridgeGroup {
	if item := s.cache.Get(platform + ":" + roomID); item != nil {
		return item.Value()
	}
	return nil
}

// CreateBridge 在数据库中注册一个新的桥接组，并同步更新内存缓存。
// 该操作在事务中执行，确保数据一致性。
func (s *Store) CreateBridge(nodes []BridgeNode) (*BridgeGroup, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	bridgeID := time.Now().UnixNano()
	group := &BridgeGroup{ID: bridgeID, Nodes: nodes}

	for _, node := range nodes {
		bytes, _ := json.Marshal(node.Config)
		if _, err := tx.Exec("INSERT INTO bridges (id, platform, room_id, config) VALUES (?, ?, ?, ?)", bridgeID, node.Platform, node.RoomID, string(bytes)); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	for _, node := range nodes {
		s.cache.Set(node.Platform+":"+node.RoomID, group, ttlcache.NoTTL)
	}

	return group, nil
}
