package internal

import (
	"database/sql"
	"encoding/json"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db           *sql.DB
	bindingCache map[string]*RoomBinding // cache: binding_id -> binding
	roomIndex    map[string][]string     // index: platform:room_id -> []binding_id
	cacheMu      sync.RWMutex
	logger       *Logger
}

func NewStore(dbPath string, log *Logger) (*Store, error) {
	// 启用 WAL 模式以支持更高并发
	dsn := dbPath + "?_journal=WAL&_timeout=5000&_busy_timeout=5000&_sync=NORMAL"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)

	s := &Store{
		db:           db,
		logger:       log,
		bindingCache: make(map[string]*RoomBinding),
		roomIndex:    make(map[string][]string),
	}

	if err := s.initSchema(); err != nil {
		db.Close()
		return nil, err
	}

	if err := s.loadCache(); err != nil {
		db.Close()
		return nil, err
	}

	go s.cleanupLoop()
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

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

// --- Bindings (Cached) ---

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
		if json.Unmarshal([]byte(data), &b.Rooms) == nil {
			s.bindingCache[id] = b
			for _, r := range b.Rooms {
				key := r.Platform + ":" + r.RoomID
				s.roomIndex[key] = append(s.roomIndex[key], id)
			}
		}
	}
	return nil
}

func (s *Store) GetBindingsByRoom(platform, roomID string) []*RoomBinding {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()

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

func (s *Store) SaveBinding(b *RoomBinding) error {
	data, _ := json.Marshal(b.Rooms)
	_, err := s.db.Exec("INSERT OR REPLACE INTO room_bindings (id, rooms_json) VALUES (?, ?)", b.ID, string(data))
	if err != nil {
		return err
	}

	// Update Cache (简单起见，全量重载或重启生效，这里省略复杂的局部更新逻辑以保持稳定)
	// 在生产环境中，这里应实现精确的缓存更新
	return s.loadCache()
}

// --- Mappings (Direct DB) ---

func (s *Store) GetTargetMessageID(srcPlat, srcMsg, tgtPlat string) (string, bool) {
	var tgtMsg string
	err := s.db.QueryRow(
		"SELECT target_msg FROM message_mappings WHERE source_plat=? AND source_msg=? AND target_plat=?",
		srcPlat, srcMsg, tgtPlat,
	).Scan(&tgtMsg)
	return tgtMsg, err == nil
}

func (s *Store) SaveMessageMapping(srcPlat, srcMsg, tgtPlat, tgtMsg, bindID string) error {
	_, err := s.db.Exec(
		"INSERT OR IGNORE INTO message_mappings VALUES (?, ?, ?, ?, ?, ?)",
		srcPlat, srcMsg, tgtPlat, tgtMsg, bindID, time.Now().Unix(),
	)
	return err
}

func (s *Store) GetTargetUserID(srcPlat, srcUser, tgtPlat string) (string, bool) {
	var tgtUser string
	err := s.db.QueryRow(
		"SELECT target_user FROM user_mappings WHERE source_plat=? AND source_user=? AND target_plat=?",
		srcPlat, srcUser, tgtPlat,
	).Scan(&tgtUser)
	return tgtUser, err == nil
}

func (s *Store) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Hour)
	for range ticker.C {
		// 删除 3 天前的映射
		expire := time.Now().Add(-72 * time.Hour).Unix()
		s.db.Exec("DELETE FROM message_mappings WHERE created_at < ?", expire)
	}
}
