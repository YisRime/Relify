// Package storage 提供持久化存储功能
// 包含消息映射、路由绑定、用户映射等存储实现
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

// RouteStore 路由绑定存储（常驻内存）
// 提供高性能的路由查询，所有绑定关系加载到内存中
type RouteStore struct {
	db       *sql.DB
	mu       sync.RWMutex
	bindings map[string]*model.RoomBinding // key: binding_id
	roomMap  map[string][]string           // key: "driver:room_id", value: []binding_id
	logger   *logger.Logger
}

// NewRouteStore 创建路由存储实例
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
		return nil, fmt.Errorf("init schema: %w", err)
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

// loadAll 从数据库加载所有绑定到内存
func (s *RouteStore) loadAll() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query("SELECT id, type, rooms_json FROM room_bindings")
	if err != nil {
		s.logger.Error("storage", "Failed to load route bindings", map[string]interface{}{
			"error": err.Error(),
		})
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var id, bindingType, roomsJSON string
		if err := rows.Scan(&id, &bindingType, &roomsJSON); err != nil {
			s.logger.Error("storage", "Failed to scan route binding", map[string]interface{}{
				"error": err.Error(),
			})
			return err
		}

		binding := &model.RoomBinding{
			ID:   id,
			Type: model.RouteType(bindingType),
		}

		if err := json.Unmarshal([]byte(roomsJSON), &binding.Rooms); err != nil {
			s.logger.Error("storage", "Failed to unmarshal rooms JSON", map[string]interface{}{
				"binding_id": id,
				"error":      err.Error(),
			})
			return err
		}

		s.bindings[id] = binding

		// 构建房间索引
		for _, room := range binding.Rooms {
			key := s.roomKey(room.Driver, room.RoomID)
			s.roomMap[key] = append(s.roomMap[key], id)
		}

		s.logger.Debug("storage", "Loaded route binding", map[string]interface{}{
			"binding_id": id,
			"type":       bindingType,
			"rooms":      len(binding.Rooms),
		})
	}

	return rows.Err()
}

// SaveBinding 保存绑定关系到数据库并更新内存
// 参数：
//   - binding: 房间绑定关系
//
// 返回：
//   - error: 错误信息
func (s *RouteStore) SaveBinding(binding *model.RoomBinding) error {
	if binding == nil {
		return fmt.Errorf("binding cannot be nil")
	}

	if binding.ID == "" {
		return fmt.Errorf("binding ID cannot be empty")
	}

	if len(binding.Rooms) == 0 {
		return fmt.Errorf("binding must have at least one room")
	}

	// 验证房间配置
	for i, room := range binding.Rooms {
		if room.Driver == "" {
			return fmt.Errorf("room %d: driver cannot be empty", i)
		}
		if room.RoomID == "" {
			return fmt.Errorf("room %d: room_id cannot be empty", i)
		}
	}

	roomsJSON, err := json.Marshal(binding.Rooms)
	if err != nil {
		s.logger.Error("storage", "Failed to marshal rooms JSON", map[string]interface{}{
			"binding_id": binding.ID,
			"error":      err.Error(),
		})
		return fmt.Errorf("marshal rooms JSON: %w", err)
	}

	query := `
		INSERT OR REPLACE INTO room_bindings (id, type, rooms_json)
		VALUES (?, ?, ?)
	`

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.db.Exec(query, binding.ID, string(binding.Type), string(roomsJSON)); err != nil {
		s.logger.Error("storage", "Failed to save route binding", map[string]interface{}{
			"binding_id": binding.ID,
			"error":      err.Error(),
		})
		return fmt.Errorf("save route binding: %w", err)
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

	s.logger.Info("storage", "Route binding saved", map[string]interface{}{
		"binding_id": binding.ID,
		"type":       binding.Type,
		"rooms":      len(binding.Rooms),
	})

	return nil
}

// GetBinding 根据 ID 获取绑定关系（内存直接读取）
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

	key := driver + ":" + roomID
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
// 参数：
//   - id: 绑定 ID
//
// 返回：
//   - error: 错误信息
func (s *RouteStore) DeleteBinding(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 从数据库删除
	query := `DELETE FROM room_bindings WHERE id = ?`
	if _, err := s.db.Exec(query, id); err != nil {
		s.logger.Error("storage", "Failed to delete route binding", map[string]interface{}{
			"binding_id": id,
			"error":      err.Error(),
		})
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

		s.logger.Info("storage", "Route binding deleted", map[string]interface{}{
			"binding_id": id,
		})
	}

	return nil
}

// ListBindings 列出所有绑定关系
// 返回：
//   - []*model.RoomBinding: 所有绑定关系列表
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
	s.logger.Info("storage", "Closing RouteStore")
	return s.db.Close()
}
