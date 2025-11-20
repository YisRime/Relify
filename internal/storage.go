package internal

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db           *sql.DB
	bindingCache map[string]*RoomBinding
	roomIndex    map[string][]string
	mu           sync.RWMutex
	logger       *Logger
	closeOnce    sync.Once
	done         chan struct{}
}

func NewStore(dbPath string, log *Logger) (*Store, error) {
	dsn := fmt.Sprintf("%s?_journal=WAL&_timeout=5000&_sync=NORMAL", dbPath)
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
		done:         make(chan struct{}),
	}

	if err := s.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}

	if err := s.loadCache(); err != nil {
		_ = db.Close()
		return nil, err
	}

	go s.cleanupLoop()
	return s, nil
}

func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		close(s.done)
	})
	return s.db.Close()
}

func (s *Store) initSchema() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS bindings (id TEXT PRIMARY KEY, name TEXT, created_at INTEGER);`,
		`CREATE TABLE IF NOT EXISTS binding_rooms (binding_id TEXT, platform TEXT, room_id TEXT, config_json TEXT, PRIMARY KEY (binding_id, platform, room_id));`,
		`CREATE INDEX IF NOT EXISTS idx_binding_lookup ON binding_rooms(platform, room_id);`,
		`CREATE TABLE IF NOT EXISTS message_mappings (
			source_plat TEXT, source_msg TEXT,
			target_plat TEXT, target_msg TEXT,
			binding_id TEXT, created_at INTEGER,
			PRIMARY KEY (source_plat, source_msg, target_plat)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_mapping_echo ON message_mappings(target_plat, target_msg);`,
		`CREATE TABLE IF NOT EXISTS users (platform TEXT, user_id TEXT, display_name TEXT, avatar_url TEXT, updated_at INTEGER, PRIMARY KEY (platform, user_id));`,
	}

	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("schema error: %w", err)
		}
	}
	return nil
}

func (s *Store) loadCache() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	bRows, err := s.db.Query("SELECT id, name FROM bindings")
	if err != nil {
		return err
	}
	defer bRows.Close()

	for bRows.Next() {
		var b RoomBinding
		if err := bRows.Scan(&b.ID, &b.Name); err != nil {
			continue
		}
		s.bindingCache[b.ID] = &b
	}

	rRows, err := s.db.Query("SELECT binding_id, platform, room_id, config_json FROM binding_rooms")
	if err != nil {
		return err
	}
	defer rRows.Close()

	for rRows.Next() {
		var bid, plat, rid, cfgStr string
		if err := rRows.Scan(&bid, &plat, &rid, &cfgStr); err != nil {
			continue
		}

		if binding, exists := s.bindingCache[bid]; exists {
			var cfg map[string]interface{}
			_ = json.Unmarshal([]byte(cfgStr), &cfg)

			binding.Rooms = append(binding.Rooms, BoundRoom{
				Platform: plat,
				RoomID:   rid,
				Config:   cfg,
			})

			key := plat + ":" + rid
			s.roomIndex[key] = append(s.roomIndex[key], bid)
		}
	}
	return nil
}

func (s *Store) GetBindingsByRoom(platform, roomID string) []*RoomBinding {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := s.roomIndex[platform+":"+roomID]
	result := make([]*RoomBinding, 0, len(ids))
	for _, id := range ids {
		if b, ok := s.bindingCache[id]; ok {
			result = append(result, b)
		}
	}
	return result
}

func (s *Store) IsEventEcho(platform, msgID string) bool {
	var dummy int
	err := s.db.QueryRow("SELECT 1 FROM message_mappings WHERE target_plat=? AND target_msg=? LIMIT 1", platform, msgID).Scan(&dummy)
	return err == nil
}

func (s *Store) GetTargetMessageID(srcPlat, srcMsg, tgtPlat string) (string, bool) {
	var tgtMsg string
	err := s.db.QueryRow("SELECT target_msg FROM message_mappings WHERE source_plat=? AND source_msg=? AND target_plat=?", srcPlat, srcMsg, tgtPlat).Scan(&tgtMsg)
	return tgtMsg, err == nil
}

func (s *Store) SaveMessageMapping(srcPlat, srcMsg, tgtPlat, tgtMsg, bindID string) {
	go func() {
		_, err := s.db.Exec(
			"INSERT OR IGNORE INTO message_mappings (source_plat, source_msg, target_plat, target_msg, binding_id, created_at) VALUES (?, ?, ?, ?, ?, ?)",
			srcPlat, srcMsg, tgtPlat, tgtMsg, bindID, time.Now().Unix(),
		)
		if err != nil {
			s.logger.Log(WarnLevel, "store", "failed to save mapping", map[string]interface{}{"err": err.Error()})
		}
	}()
}

func (s *Store) UpdateUserCache(platform, userID, name, avatar string) {
	go func() {
		_, _ = s.db.Exec(
			"INSERT OR REPLACE INTO users (platform, user_id, display_name, avatar_url, updated_at) VALUES (?, ?, ?, ?, ?)",
			platform, userID, name, avatar, time.Now().Unix(),
		)
	}()
}

func (s *Store) cleanupLoop() {
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			expire := time.Now().Add(-7 * 24 * time.Hour).Unix()
			if _, err := s.db.Exec("DELETE FROM message_mappings WHERE created_at < ?", expire); err != nil {
				s.logger.Log(ErrorLevel, "store", "cleanup failed", map[string]interface{}{"err": err.Error()})
			}
		}
	}
}
