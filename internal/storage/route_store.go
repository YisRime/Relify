// Package storage 提供持久化存储功能
package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"

	"Relify/internal/logger"
	"Relify/internal/model"

	_ "modernc.org/sqlite"
)

// RouteStore 路由绑定存储
type RouteStore struct {
	db       *sql.DB
	mu       sync.RWMutex
	bindings map[string]*model.RoomBinding
	roomMap  map[string][]string
	logger   *logger.Logger
}

// NewRouteStore 创建路由存储
func NewRouteStore(dbPath string, log *logger.Logger) (*RouteStore, error) {
	if dbPath == "" {
		return nil, fmt.Errorf("database path cannot be empty")
	}

	db, err := sql.Open("sqlite", dbPath+"?_journal=WAL&_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if log == nil {
		log = logger.GetGlobal()
	}

	store := &RouteStore{
		db:       db,
		bindings: make(map[string]*model.RoomBinding),
		roomMap:  make(map[string][]string),
		logger:   log,
	}

	if err := store.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize schema: %w", err)
	}

	if err := store.loadAll(); err != nil {
		db.Close()
		return nil, fmt.Errorf("load bindings: %w", err)
	}

	log.Info("storage", "RouteStore initialized", map[string]interface{}{
		"bindings_count": len(store.bindings),
	})

	return store, nil
}

// initSchema 初始化表结构
func (s *RouteStore) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS room_bindings (
		id TEXT PRIMARY KEY,
		type TEXT NOT NULL,
		rooms_json TEXT NOT NULL
	);
	`
	_, err := s.db.Exec(schema)
	return err
}

// loadAll 加载所有绑定到内存
func (s *RouteStore) loadAll() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query("SELECT id, type, rooms_json FROM room_bindings")
	if err != nil {
		s.logger.Error("storage", "Failed to load bindings", map[string]interface{}{"error": err.Error()})
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var id, bindingType, roomsJSON string
		if err := rows.Scan(&id, &bindingType, &roomsJSON); err != nil {
			s.logger.Error("storage", "Failed to scan binding", map[string]interface{}{"error": err.Error()})
			return err
		}

		binding := &model.RoomBinding{ID: id}
		if err := json.Unmarshal([]byte(roomsJSON), &binding.Rooms); err != nil {
			s.logger.Error("storage", "Failed to parse rooms JSON", map[string]interface{}{
				"binding_id": id,
				"error":      err.Error(),
			})
			return err
		}

		s.bindings[id] = binding
		for _, room := range binding.Rooms {
			key := s.roomKey(room.Platform, room.RoomID)
			s.roomMap[key] = append(s.roomMap[key], id)
		}
	}

	return rows.Err()
}

// SaveBinding 保存绑定关系
func (s *RouteStore) SaveBinding(binding *model.RoomBinding) error {
	if binding == nil {
		return fmt.Errorf("binding cannot be nil")
	}
	if binding.ID == "" {
		return fmt.Errorf("binding ID cannot be empty")
	}
	if len(binding.Rooms) == 0 {
		return fmt.Errorf("binding must contain at least one room")
	}

	for i, room := range binding.Rooms {
		if room.Platform == "" {
			return fmt.Errorf("room %d: platform cannot be empty", i)
		}
		if room.RoomID == "" {
			return fmt.Errorf("room %d: room ID cannot be empty", i)
		}
	}

	roomsJSON, err := json.Marshal(binding.Rooms)
	if err != nil {
		s.logger.Error("storage", "Failed to serialize rooms JSON", map[string]interface{}{
			"binding_id": binding.ID,
			"error":      err.Error(),
		})
		return fmt.Errorf("serialize rooms JSON: %w", err)
	}

	query := `INSERT OR REPLACE INTO room_bindings (id, type, rooms_json) VALUES (?, ?, ?)`

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.db.Exec(query, binding.ID, "", string(roomsJSON)); err != nil {
		s.logger.Error("storage", "Failed to save binding", map[string]interface{}{
			"binding_id": binding.ID,
			"error":      err.Error(),
		})
		return fmt.Errorf("save binding: %w", err)
	}

	s.bindings[binding.ID] = binding
	for _, room := range binding.Rooms {
		key := s.roomKey(room.Platform, room.RoomID)
		if !s.containsBinding(s.roomMap[key], binding.ID) {
			s.roomMap[key] = append(s.roomMap[key], binding.ID)
		}
	}

	s.logger.Info("storage", "Binding saved", map[string]interface{}{
		"binding_id": binding.ID,
		"rooms":      len(binding.Rooms),
	})

	return nil
}

// GetBinding 获取绑定
func (s *RouteStore) GetBinding(id string) (*model.RoomBinding, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	binding, exists := s.bindings[id]
	return binding, exists
}

// GetBindingsByRoom 根据房间查找绑定
func (s *RouteStore) GetBindingsByRoom(platform, roomID string) []*model.RoomBinding {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := platform + ":" + roomID
	bindingIDs := s.roomMap[key]

	bindings := make([]*model.RoomBinding, 0, len(bindingIDs))
	for _, id := range bindingIDs {
		if binding, exists := s.bindings[id]; exists {
			bindings = append(bindings, binding)
		}
	}
	return bindings
}

// DeleteBinding 删除绑定
func (s *RouteStore) DeleteBinding(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `DELETE FROM room_bindings WHERE id = ?`
	if _, err := s.db.Exec(query, id); err != nil {
		s.logger.Error("storage", "Failed to delete binding", map[string]interface{}{
			"binding_id": id,
			"error":      err.Error(),
		})
		return err
	}

	if binding, exists := s.bindings[id]; exists {
		for _, room := range binding.Rooms {
			key := s.roomKey(room.Platform, room.RoomID)
			s.roomMap[key] = s.removeBinding(s.roomMap[key], id)
		}
		delete(s.bindings, id)
		s.logger.Info("storage", "Binding deleted", map[string]interface{}{"binding_id": id})
	}

	return nil
}

// ListBindings 列出所有绑定
func (s *RouteStore) ListBindings() []*model.RoomBinding {
	s.mu.RLock()
	defer s.mu.RUnlock()

	bindings := make([]*model.RoomBinding, 0, len(s.bindings))
	for _, binding := range s.bindings {
		bindings = append(bindings, binding)
	}
	return bindings
}

// Close 关闭数据库
func (s *RouteStore) Close() error {
	s.logger.Info("storage", "Closing RouteStore")
	return s.db.Close()
}

// 辅助方法
func (s *RouteStore) roomKey(platform, roomID string) string {
	return platform + ":" + roomID
}

func (s *RouteStore) containsBinding(list []string, id string) bool {
	for _, item := range list {
		if item == id {
			return true
		}
	}
	return false
}

func (s *RouteStore) removeBinding(list []string, id string) []string {
	result := make([]string, 0, len(list))
	for _, item := range list {
		if item != id {
			result = append(result, item)
		}
	}
	return result
}
