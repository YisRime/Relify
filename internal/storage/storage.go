// Package storage 提供持久化存储功能
package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"Relify/internal/logger"
	"Relify/internal/model"

	_ "modernc.org/sqlite"
)

// Store 统一存储，支持peer和hub模式
type Store struct {
	db     *sql.DB
	mu     sync.RWMutex
	logger *logger.Logger

	// 内存缓存
	bindingCache      map[string]*model.RoomBinding  // binding_id -> binding
	roomIndex         map[string][]string            // room_key -> []binding_id
	messageMappingIdx map[string]*MessageMappingNode // source_key -> mapping tree
	cacheMu           sync.RWMutex
}

// MessageMappingNode 消息映射节点（支持多对多）
type MessageMappingNode struct {
	SourcePlatform string
	SourceMsgID    string
	Targets        map[string]*MessageTarget // target_platform -> target info
	UpdatedAt      time.Time
}

// MessageTarget 目标消息信息
type MessageTarget struct {
	Platform  string
	MsgID     string
	BindingID string // 属于哪个绑定
	CreatedAt time.Time
}

// UserMapping 用户ID映射
type UserMapping struct {
	SourcePlatform string
	SourceUserID   string
	TargetPlatform string
	TargetUserID   string
	DisplayName    string
	Username       string
	UpdatedAt      time.Time
}

// NewStore 创建统一存储
func NewStore(dbPath string, log *logger.Logger) (*Store, error) {
	if dbPath == "" {
		return nil, fmt.Errorf("database path cannot be empty")
	}

	db, err := sql.Open("sqlite", dbPath+"?_journal=WAL&_timeout=5000&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// 设置连接池参数以提高并发性能
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)

	if log == nil {
		log = logger.GetGlobal()
	}

	store := &Store{
		db:                db,
		logger:            log,
		bindingCache:      make(map[string]*model.RoomBinding),
		roomIndex:         make(map[string][]string),
		messageMappingIdx: make(map[string]*MessageMappingNode),
	}

	if err := store.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize schema: %w", err)
	}

	if err := store.loadCaches(); err != nil {
		db.Close()
		return nil, fmt.Errorf("load caches: %w", err)
	}

	log.Info("storage", "Store initialized", map[string]interface{}{
		"bindings": len(store.bindingCache),
	})

	go store.cleanupLoop()
	return store, nil
}

// Close 关闭数据库连接
func (s *Store) Close() error {
	return s.db.Close()
}

// initSchema 初始化表结构
func (s *Store) initSchema() error {
	schema := `
	-- 房间绑定表
	CREATE TABLE IF NOT EXISTS room_bindings (
		id TEXT PRIMARY KEY,
		rooms_json TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);

	-- 消息映射表（支持多对多，按binding分区）
	CREATE TABLE IF NOT EXISTS message_mappings (
		source_platform TEXT NOT NULL,
		source_msg_id TEXT NOT NULL,
		target_platform TEXT NOT NULL,
		target_msg_id TEXT NOT NULL,
		binding_id TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		PRIMARY KEY (source_platform, source_msg_id, target_platform, binding_id)
	);

	CREATE INDEX IF NOT EXISTS idx_msg_source_lookup 
		ON message_mappings(source_platform, source_msg_id);
	CREATE INDEX IF NOT EXISTS idx_msg_target_lookup 
		ON message_mappings(target_platform, target_msg_id);
	CREATE INDEX IF NOT EXISTS idx_msg_binding 
		ON message_mappings(binding_id);
	CREATE INDEX IF NOT EXISTS idx_msg_cleanup 
		ON message_mappings(created_at);

	-- 用户映射表（简化版，只存储映射关系）
	CREATE TABLE IF NOT EXISTS user_mappings (
		source_platform TEXT NOT NULL,
		source_user_id TEXT NOT NULL,
		target_platform TEXT NOT NULL,
		target_user_id TEXT NOT NULL,
		display_name TEXT NOT NULL,
		username TEXT,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (source_platform, source_user_id, target_platform)
	);

	CREATE INDEX IF NOT EXISTS idx_user_lookup 
		ON user_mappings(source_platform, source_user_id, target_platform);
	`

	_, err := s.db.Exec(schema)
	return err
}

// loadCaches 加载缓存
func (s *Store) loadCaches() error {
	// 加载房间绑定
	if err := s.loadBindings(); err != nil {
		return fmt.Errorf("load bindings: %w", err)
	}
	return nil
}

// loadBindings 加载房间绑定到缓存
func (s *Store) loadBindings() error {
	rows, err := s.db.Query("SELECT id, rooms_json FROM room_bindings")
	if err != nil {
		return err
	}
	defer rows.Close()

	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	for rows.Next() {
		var id, roomsJSON string
		if err := rows.Scan(&id, &roomsJSON); err != nil {
			return err
		}

		binding := &model.RoomBinding{ID: id}
		if err := json.Unmarshal([]byte(roomsJSON), &binding.Rooms); err != nil {
			return err
		}

		s.bindingCache[id] = binding
		for _, room := range binding.Rooms {
			key := roomKey(room.Platform, room.RoomID)
			s.roomIndex[key] = append(s.roomIndex[key], id)
		}
	}

	return rows.Err()
}

// SaveBinding 保存房间绑定
func (s *Store) SaveBinding(binding *model.RoomBinding) error {
	if binding == nil || binding.ID == "" || len(binding.Rooms) == 0 {
		return fmt.Errorf("invalid binding")
	}

	roomsJSON, err := json.Marshal(binding.Rooms)
	if err != nil {
		return fmt.Errorf("serialize rooms: %w", err)
	}

	now := time.Now().Unix()
	query := `INSERT INTO room_bindings (id, rooms_json, created_at, updated_at) 
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET 
			rooms_json = excluded.rooms_json,
			updated_at = excluded.updated_at`

	s.mu.Lock()
	_, err = s.db.Exec(query, binding.ID, string(roomsJSON), now, now)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("save binding: %w", err)
	}

	// 更新缓存
	s.cacheMu.Lock()
	s.bindingCache[binding.ID] = binding
	// 清理旧索引
	for key := range s.roomIndex {
		s.roomIndex[key] = removeFromSlice(s.roomIndex[key], binding.ID)
	}
	// 重建索引
	for _, room := range binding.Rooms {
		key := roomKey(room.Platform, room.RoomID)
		s.roomIndex[key] = append(s.roomIndex[key], binding.ID)
	}
	s.cacheMu.Unlock()
	s.mu.Unlock()

	return nil
}

// GetBinding 获取绑定
func (s *Store) GetBinding(id string) (*model.RoomBinding, bool) {
	s.cacheMu.RLock()
	binding, exists := s.bindingCache[id]
	s.cacheMu.RUnlock()
	return binding, exists
}

// GetBindingsByRoom 获取房间所属的绑定
func (s *Store) GetBindingsByRoom(platform, roomID string) []*model.RoomBinding {
	key := roomKey(platform, roomID)

	s.cacheMu.RLock()
	bindingIDs := s.roomIndex[key]
	bindings := make([]*model.RoomBinding, 0, len(bindingIDs))
	for _, id := range bindingIDs {
		if b := s.bindingCache[id]; b != nil {
			bindings = append(bindings, b)
		}
	}
	s.cacheMu.RUnlock()

	return bindings
}

// ListBindings 列出所有绑定
func (s *Store) ListBindings() []*model.RoomBinding {
	s.cacheMu.RLock()
	bindings := make([]*model.RoomBinding, 0, len(s.bindingCache))
	for _, binding := range s.bindingCache {
		bindings = append(bindings, binding)
	}
	s.cacheMu.RUnlock()
	return bindings
}

// DeleteBinding 删除绑定
func (s *Store) DeleteBinding(id string) error {
	query := `DELETE FROM room_bindings WHERE id = ?`

	s.mu.Lock()
	_, err := s.db.Exec(query, id)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("delete binding: %w", err)
	}

	s.cacheMu.Lock()
	if binding, exists := s.bindingCache[id]; exists {
		for _, room := range binding.Rooms {
			key := roomKey(room.Platform, room.RoomID)
			s.roomIndex[key] = removeFromSlice(s.roomIndex[key], id)
		}
		delete(s.bindingCache, id)
	}
	s.cacheMu.Unlock()
	s.mu.Unlock()

	return nil
}

// SaveMessageMapping 保存消息映射（支持多对多）
func (s *Store) SaveMessageMapping(
	sourcePlatform, sourceMsgID string,
	targetPlatform, targetMsgID string,
	bindingID string,
) error {
	query := `INSERT OR REPLACE INTO message_mappings 
		(source_platform, source_msg_id, target_platform, target_msg_id, binding_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`

	now := time.Now()
	s.mu.Lock()
	_, err := s.db.Exec(query, sourcePlatform, sourceMsgID, targetPlatform, targetMsgID, bindingID, now.Unix())
	s.mu.Unlock()

	if err != nil {
		return fmt.Errorf("save message mapping: %w", err)
	}

	// 更新内存索引
	s.updateMessageMappingCache(sourcePlatform, sourceMsgID, targetPlatform, targetMsgID, bindingID, now)

	return nil
}

// GetTargetMessageID 获取目标消息ID（优先从缓存查询）
func (s *Store) GetTargetMessageID(sourcePlatform, sourceMsgID, targetPlatform string) (string, bool) {
	key := messageKey(sourcePlatform, sourceMsgID)

	s.cacheMu.RLock()
	node := s.messageMappingIdx[key]
	if node != nil && node.Targets != nil {
		if target := node.Targets[targetPlatform]; target != nil {
			s.cacheMu.RUnlock()
			return target.MsgID, true
		}
	}
	s.cacheMu.RUnlock()

	// 缓存未命中，查询数据库
	return s.queryTargetMessageID(sourcePlatform, sourceMsgID, targetPlatform)
}

// queryTargetMessageID 从数据库查询目标消息ID
func (s *Store) queryTargetMessageID(sourcePlatform, sourceMsgID, targetPlatform string) (string, bool) {
	var targetMsgID string
	query := `SELECT target_msg_id FROM message_mappings
		WHERE source_platform = ? AND source_msg_id = ? AND target_platform = ?
		ORDER BY created_at DESC LIMIT 1`

	s.mu.RLock()
	err := s.db.QueryRow(query, sourcePlatform, sourceMsgID, targetPlatform).Scan(&targetMsgID)
	s.mu.RUnlock()

	return targetMsgID, err == nil
}

// GetAllTargetMessages 获取消息的所有目标映射
func (s *Store) GetAllTargetMessages(sourcePlatform, sourceMsgID string) map[string]string {
	key := messageKey(sourcePlatform, sourceMsgID)
	result := make(map[string]string)

	s.cacheMu.RLock()
	node := s.messageMappingIdx[key]
	if node != nil {
		for platform, target := range node.Targets {
			result[platform] = target.MsgID
		}
	}
	s.cacheMu.RUnlock()

	if len(result) > 0 {
		return result
	}

	// 从数据库加载
	query := `SELECT target_platform, target_msg_id FROM message_mappings
		WHERE source_platform = ? AND source_msg_id = ?`

	s.mu.RLock()
	rows, err := s.db.Query(query, sourcePlatform, sourceMsgID)
	s.mu.RUnlock()

	if err != nil {
		return result
	}
	defer rows.Close()

	for rows.Next() {
		var platform, msgID string
		if err := rows.Scan(&platform, &msgID); err == nil {
			result[platform] = msgID
		}
	}

	return result
}

// SaveUserMapping 保存用户映射
func (s *Store) SaveUserMapping(mapping *UserMapping) error {
	if mapping == nil {
		return fmt.Errorf("mapping cannot be nil")
	}

	if mapping.SourcePlatform == "" || mapping.SourceUserID == "" ||
		mapping.TargetPlatform == "" || mapping.TargetUserID == "" {
		return fmt.Errorf("invalid mapping: all platform and user ID fields are required")
	}

	now := time.Now()
	mapping.UpdatedAt = now

	query := `INSERT INTO user_mappings 
		(source_platform, source_user_id, target_platform, target_user_id, display_name, username, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_platform, source_user_id, target_platform)
		DO UPDATE SET
			target_user_id = excluded.target_user_id,
			display_name = excluded.display_name,
			username = excluded.username,
			updated_at = excluded.updated_at`

	s.mu.Lock()
	_, err := s.db.Exec(query,
		mapping.SourcePlatform,
		mapping.SourceUserID,
		mapping.TargetPlatform,
		mapping.TargetUserID,
		mapping.DisplayName,
		mapping.Username,
		now.Unix(),
	)
	s.mu.Unlock()

	if err != nil {
		return fmt.Errorf("save user mapping: %w", err)
	}

	return nil
}

// GetTargetUserID 查询目标用户ID
func (s *Store) GetTargetUserID(sourcePlatform, sourceUserID, targetPlatform string) (string, bool) {
	var targetUserID string
	query := `SELECT target_user_id FROM user_mappings 
		WHERE source_platform = ? AND source_user_id = ? AND target_platform = ?`

	s.mu.RLock()
	err := s.db.QueryRow(query, sourcePlatform, sourceUserID, targetPlatform).Scan(&targetUserID)
	s.mu.RUnlock()

	return targetUserID, err == nil
}

// GetUserMapping 获取完整的用户映射
func (s *Store) GetUserMapping(sourcePlatform, sourceUserID, targetPlatform string) (*UserMapping, bool) {
	var mapping UserMapping
	var username *string
	var updatedAt int64

	query := `SELECT source_platform, source_user_id, target_platform, target_user_id, 
		display_name, username, updated_at FROM user_mappings 
		WHERE source_platform = ? AND source_user_id = ? AND target_platform = ?`

	s.mu.RLock()
	err := s.db.QueryRow(query, sourcePlatform, sourceUserID, targetPlatform).Scan(
		&mapping.SourcePlatform,
		&mapping.SourceUserID,
		&mapping.TargetPlatform,
		&mapping.TargetUserID,
		&mapping.DisplayName,
		&username,
		&updatedAt,
	)
	s.mu.RUnlock()

	if err != nil {
		return nil, false
	}

	if username != nil {
		mapping.Username = *username
	}
	mapping.UpdatedAt = time.Unix(updatedAt, 0)

	return &mapping, true
}

// updateMessageMappingCache 更新消息映射缓存
func (s *Store) updateMessageMappingCache(
	sourcePlatform, sourceMsgID, targetPlatform, targetMsgID, bindingID string, createdAt time.Time,
) {
	key := messageKey(sourcePlatform, sourceMsgID)

	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	node := s.messageMappingIdx[key]
	if node == nil {
		node = &MessageMappingNode{
			SourcePlatform: sourcePlatform,
			SourceMsgID:    sourceMsgID,
			Targets:        make(map[string]*MessageTarget),
			UpdatedAt:      createdAt,
		}
		s.messageMappingIdx[key] = node
	}

	node.Targets[targetPlatform] = &MessageTarget{
		Platform:  targetPlatform,
		MsgID:     targetMsgID,
		BindingID: bindingID,
		CreatedAt: createdAt,
	}
	node.UpdatedAt = createdAt
}

// cleanupLoop 定期清理过期数据
func (s *Store) cleanupLoop() {
	ticker := time.NewTicker(2 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		s.cleanup()
	}
}

// cleanup 清理过期数据
func (s *Store) cleanup() {
	ttl := 72 * time.Hour
	cutoff := time.Now().Add(-ttl).Unix()

	s.mu.Lock()
	result, err := s.db.Exec("DELETE FROM message_mappings WHERE created_at < ?", cutoff)
	s.mu.Unlock()

	if err != nil {
		s.logger.Error("storage", "Cleanup failed", map[string]interface{}{"error": err.Error()})
		return
	}

	if affected, _ := result.RowsAffected(); affected > 0 {
		s.logger.Info("storage", "Cleanup completed", map[string]interface{}{
			"deleted_mappings": affected,
		})
	}

	// 清理内存缓存中的过期数据
	s.cacheMu.Lock()
	expiry := time.Now().Add(-ttl)
	for key, node := range s.messageMappingIdx {
		if node.UpdatedAt.Before(expiry) {
			delete(s.messageMappingIdx, key)
		}
	}
	s.cacheMu.Unlock()
}

// Helper functions
func roomKey(platform, roomID string) string {
	return platform + ":" + roomID
}

func messageKey(platform, msgID string) string {
	return platform + ":" + msgID
}

func removeFromSlice(slice []string, item string) []string {
	result := make([]string, 0, len(slice))
	for _, s := range slice {
		if s != item {
			result = append(result, s)
		}
	}
	return result
}
