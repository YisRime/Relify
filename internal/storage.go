package internal

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

type Store struct {
	db     *sql.DB
	logger *Logger

	bindingCache sync.Map

	closeOnce sync.Once
	done      chan struct{}
}

func NewStore(dbPath string, log *Logger) (*Store, error) {
	dsn := fmt.Sprintf("%s?_journal=WAL&_timeout=5000&_sync=NORMAL", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(1 * time.Hour)

	s := &Store{
		db:     db,
		logger: log,
		done:   make(chan struct{}),
	}

	if err := s.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}

	go s.cleanupLoop()
	return s, nil
}

func (s *Store) Close() error {
	s.closeOnce.Do(func() { close(s.done) })
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
		`CREATE INDEX IF NOT EXISTS idx_mapping_cleanup ON message_mappings(created_at);`,
		`CREATE TABLE IF NOT EXISTS users (platform TEXT, user_id TEXT, display_name TEXT, avatar_url TEXT, updated_at INTEGER, PRIMARY KEY (platform, user_id));`,
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, q := range queries {
		if _, err := tx.Exec(q); err != nil {
			return fmt.Errorf("schema error: %w", err)
		}
	}
	return tx.Commit()
}

func (s *Store) GetBindingsByRoom(platform, roomID string) []*RoomBinding {
	key := platform + ":" + roomID

	if val, ok := s.bindingCache.Load(key); ok {
		return val.([]*RoomBinding)
	}

	bindings, err := s.loadBindingsFromDB(platform, roomID)
	if err != nil {
		s.logger.Log(ErrorLevel, "store", "load bindings failed", map[string]interface{}{"err": err.Error()})
		return nil
	}

	s.bindingCache.Store(key, bindings)
	return bindings
}

func (s *Store) loadBindingsFromDB(platform, roomID string) ([]*RoomBinding, error) {
	rows, err := s.db.Query("SELECT binding_id FROM binding_rooms WHERE platform=? AND room_id=?", platform, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var bindingIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			bindingIDs = append(bindingIDs, id)
		}
	}

	if len(bindingIDs) == 0 {
		return []*RoomBinding{}, nil
	}

	var result []*RoomBinding
	for _, bid := range bindingIDs {
		b, err := s.GetBindingByID(bid)
		if err == nil && b != nil {
			result = append(result, b)
		}
	}
	return result, nil
}

func (s *Store) GetBindingByID(id string) (*RoomBinding, error) {
	var b RoomBinding
	err := s.db.QueryRow("SELECT id, name FROM bindings WHERE id=?", id).Scan(&b.ID, &b.Name)
	if err != nil {
		return nil, err
	}

	rRows, err := s.db.Query("SELECT platform, room_id, config_json FROM binding_rooms WHERE binding_id=?", id)
	if err != nil {
		return nil, err
	}
	defer rRows.Close()

	for rRows.Next() {
		var plat, rid, cfgStr string
		if err := rRows.Scan(&plat, &rid, &cfgStr); err == nil {
			var cfg map[string]interface{}
			_ = json.Unmarshal([]byte(cfgStr), &cfg)
			b.Rooms = append(b.Rooms, BoundRoom{Platform: plat, RoomID: rid, Config: cfg})
		}
	}
	return &b, nil
}

func (s *Store) CreateDynamicBinding(name string, rooms []BoundRoom) (*RoomBinding, error) {
	id := uuid.New().String()

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("INSERT INTO bindings (id, name, created_at) VALUES (?, ?, ?)", id, name, time.Now().Unix()); err != nil {
		return nil, err
	}

	for _, r := range rooms {
		cfgBytes, _ := json.Marshal(r.Config)
		if _, err := tx.Exec("INSERT INTO binding_rooms (binding_id, platform, room_id, config_json) VALUES (?, ?, ?, ?)",
			id, r.Platform, r.RoomID, string(cfgBytes)); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	binding := &RoomBinding{ID: id, Name: name, Rooms: rooms}

	for _, r := range rooms {
		s.bindingCache.Delete(r.Platform + ":" + r.RoomID)
	}

	return binding, nil
}

func (s *Store) IsEventEcho(platform, msgID string) bool {
	var dummy int
	return s.db.QueryRow("SELECT 1 FROM message_mappings WHERE target_plat=? AND target_msg=? LIMIT 1", platform, msgID).Scan(&dummy) == nil
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
			s.logger.Log(WarnLevel, "store", "mapping save failed", map[string]interface{}{"err": err.Error()})
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
	ticker := time.NewTicker(12 * time.Hour)
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
