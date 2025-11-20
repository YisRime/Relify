package internal

import (
	"database/sql"
	"encoding/json"
	"sync"
	"time"

	_ "modernc.org/sqlite" // 引入纯 Go 实现的 SQLite 驱动
)

// Store 处理持久化存储和内存缓存
type Store struct {
	db           *sql.DB
	bindingCache map[string]*RoomBinding // 内存缓存：BindingID -> Binding对象
	roomIndex    map[string][]string     // 倒排索引：Platform:RoomID -> BindingID列表
	cacheMu      sync.RWMutex            // 读写锁保护缓存
	logger       *Logger
}

// NewStore 初始化数据库连接并加载缓存
func NewStore(dbPath string, log *Logger) (*Store, error) {
	// 配置 SQLite 连接字符串，启用 WAL 模式以提高并发写入性能
	dsn := dbPath + "?_journal=WAL&_timeout=5000&_busy_timeout=5000&_sync=NORMAL"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	// 设置连接池参数
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)

	s := &Store{
		db:           db,
		logger:       log,
		bindingCache: make(map[string]*RoomBinding),
		roomIndex:    make(map[string][]string),
	}

	// 初始化数据库表结构
	if err := s.initSchema(); err != nil {
		db.Close()
		return nil, err
	}

	// 预热缓存
	if err := s.loadCache(); err != nil {
		db.Close()
		return nil, err
	}

	// 启动后台清理任务
	go s.cleanupLoop()
	return s, nil
}

// Close 关闭数据库连接
func (s *Store) Close() error { return s.db.Close() }

// initSchema 创建必要的数据库表和索引
func (s *Store) initSchema() error {
	query := `
	CREATE TABLE IF NOT EXISTS room_bindings (
		id TEXT PRIMARY KEY,
		rooms_json TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS message_mappings (
		source_plat TEXT, source_msg TEXT,
		target_plat TEXT, target_msg TEXT,
		binding_id TEXT, created_at INTEGER,
		PRIMARY KEY (source_plat, source_msg, target_plat, binding_id)
	);
	CREATE INDEX IF NOT EXISTS idx_mapping_lookup ON message_mappings(source_plat, source_msg);
	CREATE TABLE IF NOT EXISTS user_mappings (
		source_plat TEXT, source_user TEXT,
		target_plat TEXT, target_user TEXT,
		PRIMARY KEY (source_plat, source_user, target_plat)
	);
	`
	_, err := s.db.Exec(query)
	return err
}

// loadCache 从数据库全量加载绑定关系到内存
func (s *Store) loadCache() error {
	rows, err := s.db.Query("SELECT id, rooms_json FROM room_bindings")
	if err != nil {
		return err
	}
	defer rows.Close()

	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	for rows.Next() {
		var id, data string
		if err := rows.Scan(&id, &data); err != nil {
			return err
		}
		b := &RoomBinding{ID: id}
		// 反序列化 JSON 配置
		if json.Unmarshal([]byte(data), &b.Rooms) == nil {
			s.bindingCache[id] = b
			// 构建倒排索引以便快速查找
			for _, r := range b.Rooms {
				key := r.Platform + ":" + r.RoomID
				s.roomIndex[key] = append(s.roomIndex[key], id)
			}
		}
	}
	return nil
}

// GetBindingsByRoom 根据平台和房间ID查找关联的绑定配置
func (s *Store) GetBindingsByRoom(platform, roomID string) []*RoomBinding {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()

	// 查找索引
	ids := s.roomIndex[platform+":"+roomID]
	if len(ids) == 0 {
		return nil
	}

	res := make([]*RoomBinding, 0, len(ids))
	for _, id := range ids {
		if b, ok := s.bindingCache[id]; ok {
			res = append(res, b)
		}
	}
	return res
}

// SaveBinding 保存或更新绑定配置，并刷新缓存
func (s *Store) SaveBinding(b *RoomBinding) error {
	data, _ := json.Marshal(b.Rooms)
	_, err := s.db.Exec("INSERT OR REPLACE INTO room_bindings (id, rooms_json) VALUES (?, ?)", b.ID, string(data))
	if err != nil {
		return err
	}

	// 简单策略：更新后重新全量加载缓存以保证一致性
	return s.loadCache()
}

// GetTargetMessageID 查询消息映射，用于处理回复功能
func (s *Store) GetTargetMessageID(srcPlat, srcMsg, tgtPlat string) (string, bool) {
	var tgtMsg string
	err := s.db.QueryRow(
		"SELECT target_msg FROM message_mappings WHERE source_plat=? AND source_msg=? AND target_plat=?",
		srcPlat, srcMsg, tgtPlat,
	).Scan(&tgtMsg)
	return tgtMsg, err == nil
}

// SaveMessageMapping 记录源消息与目标消息的映射关系
func (s *Store) SaveMessageMapping(srcPlat, srcMsg, tgtPlat, tgtMsg, bindID string) error {
	_, err := s.db.Exec(
		"INSERT OR IGNORE INTO message_mappings VALUES (?, ?, ?, ?, ?, ?)",
		srcPlat, srcMsg, tgtPlat, tgtMsg, bindID, time.Now().Unix(),
	)
	return err
}

// GetTargetUserID 查询用户映射（如果存在）
func (s *Store) GetTargetUserID(srcPlat, srcUser, tgtPlat string) (string, bool) {
	var tgtUser string
	err := s.db.QueryRow(
		"SELECT target_user FROM user_mappings WHERE source_plat=? AND source_user=? AND target_plat=?",
		srcPlat, srcUser, tgtPlat,
	).Scan(&tgtUser)
	return tgtUser, err == nil
}

// cleanupLoop 定期清理过期的消息映射记录
func (s *Store) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Hour)
	for range ticker.C {
		// 设置过期时间为 3 天前
		expire := time.Now().Add(-72 * time.Hour).Unix()
		s.db.Exec("DELETE FROM message_mappings WHERE created_at < ?", expire)
	}
}
