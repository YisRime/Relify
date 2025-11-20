package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"

	"Relify/internal/model"

	_ "modernc.org/sqlite"
)

// RouteStore 路由绑定存储（常驻内存）
type RouteStore struct {
	db       *sql.DB
	mu       sync.RWMutex
	bindings map[string]*model.RoomBinding // key: binding_id
	roomMap  map[string][]string           // key: "driver:room_id", value: []binding_id
}

// NewRouteStore 创建路由存储实例
func NewRouteStore(dbPath string) (*RouteStore, error) {
	db, err := sql.Open("sqlite", dbPath+"?_journal=WAL&_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	store := &RouteStore{
		db:       db,
		bindings: make(map[string]*model.RoomBinding),
		roomMap:  make(map[string][]string),
	}

	if err := store.initSchema(); err != nil {
		return nil, err
	}

	// 启动时加载所有绑定到内存
	if err := store.loadAll(); err != nil {
		return nil, err
	}

	return store, nil
}

// initSchema 初始化数据库表结构
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
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var id, bindingType, roomsJSON string
		if err := rows.Scan(&id, &bindingType, &roomsJSON); err != nil {
			return err
		}

		binding := &model.RoomBinding{
			ID:   id,
			Type: model.RouteType(bindingType),
		}

		if err := json.Unmarshal([]byte(roomsJSON), &binding.Rooms); err != nil {
			return err
		}

		s.bindings[id] = binding

		// 构建房间索引
		for _, room := range binding.Rooms {
			key := s.roomKey(room.Driver, room.RoomID)
			s.roomMap[key] = append(s.roomMap[key], id)
		}
	}

	return rows.Err()
}

// SaveBinding 保存绑定关系
func (s *RouteStore) SaveBinding(binding *model.RoomBinding) error {
	roomsJSON, err := json.Marshal(binding.Rooms)
	if err != nil {
		return err
	}

	query := `
		INSERT OR REPLACE INTO room_bindings (id, type, rooms_json)
		VALUES (?, ?, ?)
	`

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.db.Exec(query, binding.ID, string(binding.Type), string(roomsJSON)); err != nil {
		return err
	}

	// 更新内存
	s.bindings[binding.ID] = binding

	// 更新房间索引
	for _, room := range binding.Rooms {
		key := s.roomKey(room.Driver, room.RoomID)
		if !s.containsBinding(s.roomMap[key], binding.ID) {
			s.roomMap[key] = append(s.roomMap[key], binding.ID)
		}
	}

	return nil
}

// GetBinding 获取绑定关系（内存直接读取）
func (s *RouteStore) GetBinding(id string) (*model.RoomBinding, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	binding, exists := s.bindings[id]
	return binding, exists
}

// GetBindingsByRoom 根据房间查找所有相关绑定
func (s *RouteStore) GetBindingsByRoom(driver, roomID string) []*model.RoomBinding {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := s.roomKey(driver, roomID)
	bindingIDs := s.roomMap[key]

	bindings := make([]*model.RoomBinding, 0, len(bindingIDs))
	for _, id := range bindingIDs {
		if binding, exists := s.bindings[id]; exists {
			bindings = append(bindings, binding)
		}
	}

	return bindings
}

// roomKey 生成房间索引键
func (s *RouteStore) roomKey(driver, roomID string) string {
	return driver + ":" + roomID
}

// containsBinding 检查绑定 ID 是否存在于列表中
func (s *RouteStore) containsBinding(list []string, id string) bool {
	for _, item := range list {
		if item == id {
			return true
		}
	}
	return false
}

// DeleteBinding 删除绑定关系
func (s *RouteStore) DeleteBinding(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 从数据库删除
	query := `DELETE FROM room_bindings WHERE id = ?`
	if _, err := s.db.Exec(query, id); err != nil {
		return err
	}

	// 从内存删除
	if binding, exists := s.bindings[id]; exists {
		// 清理房间索引
		for _, room := range binding.Rooms {
			key := s.roomKey(room.Driver, room.RoomID)
			s.roomMap[key] = s.removeBinding(s.roomMap[key], id)
		}
		delete(s.bindings, id)
	}

	return nil
}

// ListBindings 列出所有绑定关系
func (s *RouteStore) ListBindings() []*model.RoomBinding {
	s.mu.RLock()
	defer s.mu.RUnlock()

	bindings := make([]*model.RoomBinding, 0, len(s.bindings))
	for _, binding := range s.bindings {
		bindings = append(bindings, binding)
	}

	return bindings
}

// removeBinding 从列表中移除指定的绑定 ID
func (s *RouteStore) removeBinding(list []string, id string) []string {
	result := make([]string, 0, len(list))
	for _, item := range list {
		if item != id {
			result = append(result, item)
		}
	}
	return result
}

// Close 关闭数据库连接
func (s *RouteStore) Close() error {
	return s.db.Close()
}
