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

type dbOp func(*sql.Tx) error

type Store struct {
	db           *sql.DB
	bindingCache sync.Map
	opChan       chan dbOp
	closeChan    chan struct{}
	wg           sync.WaitGroup
}

func NewStore(dbPath string) (*Store, error) {
	dsn := fmt.Sprintf("%s?_journal=WAL&_timeout=5000&_sync=NORMAL", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	sqls := []string{
		`CREATE TABLE IF NOT EXISTS bindings (id TEXT PRIMARY KEY, name TEXT, created_at INTEGER)`,
		`CREATE TABLE IF NOT EXISTS binding_rooms (binding_id TEXT, platform TEXT, room_id TEXT, config_json TEXT, PRIMARY KEY (binding_id, platform, room_id))`,
		`CREATE INDEX IF NOT EXISTS idx_binding_lookup ON binding_rooms(platform, room_id)`,
		`CREATE TABLE IF NOT EXISTS message_mappings (source_plat TEXT, source_msg TEXT, target_plat TEXT, target_msg TEXT, binding_id TEXT, created_at INTEGER, PRIMARY KEY (source_plat, source_msg, target_plat))`,
		`CREATE INDEX IF NOT EXISTS idx_mapping_cleanup ON message_mappings(created_at)`,
		`CREATE TABLE IF NOT EXISTS users (platform TEXT, user_id TEXT, display_name TEXT, avatar_url TEXT, updated_at INTEGER, PRIMARY KEY (platform, user_id))`,
		`CREATE TABLE IF NOT EXISTS events (id TEXT PRIMARY KEY, action TEXT, platform TEXT, room_id TEXT, timestamp INTEGER, event_json TEXT)`,
		`CREATE INDEX IF NOT EXISTS idx_events_ts ON events(timestamp)`,
	}
	for _, sqlStmt := range sqls {
		if _, err := db.Exec(sqlStmt); err != nil {
			db.Close()
			return nil, err
		}
	}

	s := &Store{
		db:        db,
		opChan:    make(chan dbOp, 2000),
		closeChan: make(chan struct{}),
	}
	s.wg.Add(1)
	go s.writerLoop()
	go s.cleanupLoop()
	return s, nil
}

func (s *Store) Close() error {
	close(s.closeChan)
	s.wg.Wait()
	return s.db.Close()
}

func (s *Store) writerLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	var batch []dbOp
	execBatch := func() {
		if len(batch) == 0 {
			return
		}
		tx, err := s.db.Begin()
		if err != nil {
			slog.Error("tx begin failed", "err", err)
			batch = nil
			return
		}
		for _, op := range batch {
			if err := op(tx); err != nil {
				slog.Warn("batch op failed", "err", err)
			}
		}
		if err := tx.Commit(); err != nil {
			slog.Error("tx commit failed", "err", err)
		}
		batch = nil
		batch = make([]dbOp, 0, 100)
	}

	for {
		select {
		case op := <-s.opChan:
			batch = append(batch, op)
			if len(batch) >= 100 {
				execBatch()
			}
		case <-ticker.C:
			execBatch()
		case <-s.closeChan:
			execBatch()
			return
		}
	}
}

func (s *Store) enqueueAsync(op dbOp) {
	select {
	case s.opChan <- op:
	default:
		slog.Warn("db buffer full, dropping write")
	}
}

func (s *Store) SaveEvent(event *Event) {
	if event == nil || event.Message == nil {
		return
	}
	data, _ := json.Marshal(event)
	s.enqueueAsync(func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO events (id, action, platform, room_id, timestamp, event_json) VALUES (?, ?, ?, ?, ?, ?)",
			event.ID, string(event.Action), event.Platform, event.Message.RoomID, event.Timestamp.UnixMilli(), string(data))
		return err
	})
}

func (s *Store) SaveMessageMapping(srcPlat, srcMsg, tgtPlat, tgtMsg, bindID string) {
	s.enqueueAsync(func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT OR IGNORE INTO message_mappings VALUES (?, ?, ?, ?, ?, ?)",
			srcPlat, srcMsg, tgtPlat, tgtMsg, bindID, time.Now().Unix())
		return err
	})
}

func (s *Store) UpdateUserCache(platform, userID, name, avatar string) {
	s.enqueueAsync(func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT OR REPLACE INTO users VALUES (?, ?, ?, ?, ?)",
			platform, userID, name, avatar, time.Now().Unix())
		return err
	})
}

func (s *Store) GetBindingsByRoom(platform, roomID string) []*RoomBinding {
	key := platform + ":" + roomID
	if val, ok := s.bindingCache.Load(key); ok {
		return val.([]*RoomBinding)
	}

	rows, err := s.db.Query("SELECT binding_id FROM binding_rooms WHERE platform=? AND room_id=?", platform, roomID)
	if err != nil {
		return nil
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
		return []*RoomBinding{}
	}

	var result []*RoomBinding
	for _, bid := range ids {
		if b, err := s.getBindingByID(bid); err == nil {
			result = append(result, b)
		}
	}
	s.bindingCache.Store(key, result)
	return result
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

func (s *Store) cleanupLoop() {
	ticker := time.NewTicker(4 * time.Hour)
	for range ticker.C {
		expire := time.Now().Add(-48 * time.Hour).Unix()
		s.enqueueAsync(func(tx *sql.Tx) error {
			tx.Exec("DELETE FROM message_mappings WHERE created_at < ?", expire)
			tx.Exec("DELETE FROM events WHERE timestamp < ?", expire*1000)
			return nil
		})
	}
}
