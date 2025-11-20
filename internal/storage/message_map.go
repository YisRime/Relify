// Package storage 提供持久化存储功能
package storage

import (
	"fmt"
	"time"

	"Relify/internal/logger"
)

// MessageMapStore 消息ID映射存储
type MessageMapStore struct {
	*BaseStore
}

// MessageMapping 消息映射
type MessageMapping struct {
	SourcePlatform string    `json:"source_platform"`
	SourceMsgID    string    `json:"source_msg_id"`
	TargetPlatform string    `json:"target_platform"`
	TargetMsgID    string    `json:"target_msg_id"`
	CreatedAt      time.Time `json:"created_at"`
}

// NewMessageMapStore 创建消息映射存储
func NewMessageMapStore(dbPath string, log *logger.Logger) (*MessageMapStore, error) {
	base, err := NewBaseStore(dbPath, log)
	if err != nil {
		return nil, err
	}

	store := &MessageMapStore{BaseStore: base}

	if err := store.initSchema(); err != nil {
		base.Close()
		return nil, fmt.Errorf("initialize schema: %w", err)
	}

	log.Info("storage", "MessageMapStore initialized")
	go store.cleanupLoop()

	return store, nil
}

// initSchema 初始化表结构
func (s *MessageMapStore) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS message_mappings (
		source_platform TEXT NOT NULL,
		source_msg_id TEXT NOT NULL,
		target_platform TEXT NOT NULL,
		target_msg_id TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		PRIMARY KEY (source_platform, source_msg_id, target_platform)
	);

	CREATE INDEX IF NOT EXISTS idx_created_at ON message_mappings(created_at);
	CREATE INDEX IF NOT EXISTS idx_target_lookup ON message_mappings(target_platform, target_msg_id);
	`

	return s.ExecSchema(schema)
}

// Save 保存消息 ID 映射关系
func (s *MessageMapStore) Save(mapping *MessageMapping) error {
	if mapping == nil {
		return fmt.Errorf("mapping cannot be nil")
	}
	if mapping.SourcePlatform == "" || mapping.SourceMsgID == "" ||
		mapping.TargetPlatform == "" || mapping.TargetMsgID == "" {
		return fmt.Errorf("invalid mapping: all fields are required")
	}

	query := `INSERT OR REPLACE INTO message_mappings 
		(source_platform, source_msg_id, target_platform, target_msg_id, created_at)
		VALUES (?, ?, ?, ?, ?)`

	s.Lock()
	_, err := s.DB().Exec(query, mapping.SourcePlatform, mapping.SourceMsgID,
		mapping.TargetPlatform, mapping.TargetMsgID, mapping.CreatedAt.Unix())
	s.Unlock()

	if err != nil {
		return fmt.Errorf("save message mapping: %w", err)
	}

	return nil
}

// GetTargetID 根据来源消息查找目标平台的消息 ID
func (s *MessageMapStore) GetTargetID(sourcePlatform, sourceMsgID, targetPlatform string) (string, bool) {
	var targetMsgID string
	query := `SELECT target_msg_id FROM message_mappings
		WHERE source_platform = ? AND source_msg_id = ? AND target_platform = ?`

	s.RLock()
	err := s.DB().QueryRow(query, sourcePlatform, sourceMsgID, targetPlatform).Scan(&targetMsgID)
	s.RUnlock()

	return targetMsgID, err == nil
}

// GetAllTargets 获取一条消息在所有目标平台的映射 ID
func (s *MessageMapStore) GetAllTargets(sourcePlatform, sourceMsgID string) ([]MessageMapping, error) {
	query := `SELECT source_platform, source_msg_id, target_platform, target_msg_id, created_at
		FROM message_mappings WHERE source_platform = ? AND source_msg_id = ?`

	s.RLock()
	rows, err := s.DB().Query(query, sourcePlatform, sourceMsgID)
	s.RUnlock()

	if err != nil {
		s.logger.Error("storage", "Failed to query message mappings", map[string]interface{}{
			"source_platform": sourcePlatform,
			"source_msg_id":   sourceMsgID,
			"error":           err.Error(),
		})
		return nil, err
	}
	defer rows.Close()

	var mappings []MessageMapping
	for rows.Next() {
		var m MessageMapping
		var createdAt int64
		if err := rows.Scan(&m.SourcePlatform, &m.SourceMsgID, &m.TargetPlatform, &m.TargetMsgID, &createdAt); err != nil {
			s.logger.Error("storage", "Failed to scan message mapping", map[string]interface{}{"error": err.Error()})
			return nil, err
		}
		m.CreatedAt = time.Unix(createdAt, 0)
		mappings = append(mappings, m)
	}

	return mappings, rows.Err()
}

// cleanupLoop 定期清理过期映射（TTL: 48 小时）
func (s *MessageMapStore) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		s.cleanup()
	}
}

// cleanup 清理过期数据（TTL: 48 小时）
func (s *MessageMapStore) cleanup() {
	ttl := 48 * time.Hour
	cutoff := time.Now().Add(-ttl).Unix()

	query := `DELETE FROM message_mappings WHERE created_at < ?`

	s.Lock()
	result, err := s.DB().Exec(query, cutoff)
	s.Unlock()

	if err != nil {
		s.logger.Error("storage", "Failed to cleanup expired message mappings", map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	if affected, _ := result.RowsAffected(); affected > 0 {
		s.logger.Info("storage", "Cleaned up expired message mappings", map[string]interface{}{
			"count": affected,
		})
	}
}

// Close 关闭数据库连接
func (s *MessageMapStore) Close() error {
	s.logger.Info("storage", "Closing MessageMapStore")
	return s.BaseStore.Close()
}
