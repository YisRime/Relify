package internal

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

type Store struct {
	db           *sql.DB
	bindingCache sync.Map
}

func NewStore(dbPath string) (*Store, error) {
	dsn := fmt.Sprintf("%s?_journal=WAL&_timeout=5000&_sync=NORMAL", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	schema := `
	CREATE TABLE IF NOT EXISTS bindings (id TEXT PRIMARY KEY, name TEXT, created_at INTEGER);
	CREATE TABLE IF NOT EXISTS binding_rooms (binding_id TEXT, platform TEXT, room_id TEXT, config_json TEXT, PRIMARY KEY (binding_id, platform, room_id));
	CREATE INDEX IF NOT EXISTS idx_binding_lookup ON binding_rooms(platform, room_id);
	CREATE TABLE IF NOT EXISTS message_mappings (
		source_plat TEXT, source_msg TEXT, target_plat TEXT, target_msg TEXT,
		binding_id TEXT, created_at INTEGER,
		PRIMARY KEY (source_plat, source_msg, target_plat)
	);
	CREATE INDEX IF NOT EXISTS idx_mapping_cleanup ON message_mappings(created_at);
	CREATE TABLE IF NOT EXISTS users (platform TEXT, user_id TEXT, display_name TEXT, avatar_url TEXT, updated_at INTEGER, PRIMARY KEY (platform, user_id));
	`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}

	s := &Store{db: db}
	go s.cleanupLoop()
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) GetBindingsByRoom(platform, roomID string) []*RoomBinding {
	key := platform + ":" + roomID
	if val, ok := s.bindingCache.Load(key); ok {
		return val.([]*RoomBinding)
	}

	bindings, err := s.loadBindingsFromDB(platform, roomID)
	if err != nil {
		slog.Error("db load failed", "err", err)
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

	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}

	if len(ids) == 0 {
		return []*RoomBinding{}, nil
	}

	var result []*RoomBinding
	for _, bid := range ids {
		if b, err := s.getBindingByID(bid); err == nil {
			result = append(result, b)
		}
	}
	return result, nil
}

func (s *Store) getBindingByID(id string) (*RoomBinding, error) {
	b := &RoomBinding{ID: id}
	if err := s.db.QueryRow("SELECT name FROM bindings WHERE id=?", id).Scan(&b.Name); err != nil {
		return nil, err
	}

	rows, err := s.db.Query("SELECT platform, room_id, config_json FROM binding_rooms WHERE binding_id=?", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var plat, rid, cfgStr string
		if rows.Scan(&plat, &rid, &cfgStr) == nil {
			var cfg map[string]interface{}
			json.Unmarshal([]byte(cfgStr), &cfg)
			b.Rooms = append(b.Rooms, BoundRoom{Platform: plat, RoomID: rid, Config: cfg})
		}
	}
	return b, nil
}

func (s *Store) CreateDynamicBinding(name string, rooms []BoundRoom) (*RoomBinding, error) {
	id := uuid.New().String()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("INSERT INTO bindings VALUES (?, ?, ?)", id, name, time.Now().Unix()); err != nil {
		return nil, err
	}

	for _, r := range rooms {
		cfgBytes, _ := json.Marshal(r.Config)
		if _, err := tx.Exec("INSERT INTO binding_rooms VALUES (?, ?, ?, ?)", id, r.Platform, r.RoomID, string(cfgBytes)); err != nil {
			return nil, err
		}
		s.bindingCache.Delete(r.Platform + ":" + r.RoomID)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &RoomBinding{ID: id, Name: name, Rooms: rooms}, nil
}

func (s *Store) IsEventEcho(platform, msgID string) bool {
	var i int
	return s.db.QueryRow("SELECT 1 FROM message_mappings WHERE target_plat=? AND target_msg=? LIMIT 1", platform, msgID).Scan(&i) == nil
}

func (s *Store) GetTargetMessageID(srcPlat, srcMsg, tgtPlat string) (string, bool) {
	var tgtMsg string
	err := s.db.QueryRow("SELECT target_msg FROM message_mappings WHERE source_plat=? AND source_msg=? AND target_plat=?", srcPlat, srcMsg, tgtPlat).Scan(&tgtMsg)
	return tgtMsg, err == nil
}

func (s *Store) SaveMessageMapping(srcPlat, srcMsg, tgtPlat, tgtMsg, bindID string) {
	go s.db.Exec("INSERT OR IGNORE INTO message_mappings VALUES (?, ?, ?, ?, ?, ?)", srcPlat, srcMsg, tgtPlat, tgtMsg, bindID, time.Now().Unix())
}

func (s *Store) UpdateUserCache(platform, userID, name, avatar string) {
	go s.db.Exec("INSERT OR REPLACE INTO users VALUES (?, ?, ?, ?, ?)", platform, userID, name, avatar, time.Now().Unix())
}

func (s *Store) cleanupLoop() {
	ticker := time.NewTicker(12 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		expire := time.Now().Add(-7 * 24 * time.Hour).Unix()
		s.db.Exec("DELETE FROM message_mappings WHERE created_at < ?", expire)
	}
}
