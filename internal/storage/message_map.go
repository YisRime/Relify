// Package storage 提供持久化存储功能
// 包含消息映射、路由绑定、用户映射等存储实现
package storage

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	"Relify/internal/logger"

	_ "modernc.org/sqlite"
)

// MessageMapStore 消息 ID 映射存储
// 用于存储跨平台的消息 ID 映射关系，支持引用和编辑功能
type MessageMapStore struct {
	db     *sql.DB
	mu     sync.RWMutex
	logger *logger.Logger
}

// MessageMapping 消息映射关系
type MessageMapping struct {
	SourceDriver string    `json:"source_driver"`
	SourceMsgID  string    `json:"source_msg_id"`
	TargetDriver string    `json:"target_driver"`
	TargetMsgID  string    `json:"target_msg_id"`
	CreatedAt    time.Time `json:"created_at"`
}

// NewMessageMapStore 创建消息 ID 映射存储实例
func NewMessageMapStore(dbPath string, log *logger.Logger) (*MessageMapStore, error) {
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

	store := &MessageMapStore{db: db, logger: log}

	if err := store.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	log.Info("storage", "MessageMapStore initialized")
	go store.cleanupLoop()

	return store, nil
}

// initSchema 初始化数据库表结构
func (s *MessageMapStore) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS message_mappings (
		source_driver TEXT NOT NULL,
		source_msg_id TEXT NOT NULL,
		target_driver TEXT NOT NULL,
		target_msg_id TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		PRIMARY KEY (source_driver, source_msg_id, target_driver)
	);

	CREATE INDEX IF NOT EXISTS idx_created_at ON message_mappings(created_at);
	CREATE INDEX IF NOT EXISTS idx_target_lookup ON message_mappings(target_driver, target_msg_id);
	`

	_, err := s.db.Exec(schema)
	return err
}

// Save 保存消息 ID 映射关系
func (s *MessageMapStore) Save(mapping *MessageMapping) error {
	if mapping == nil {
		return fmt.Errorf("mapping cannot be nil")
	}
	if mapping.SourceDriver == "" || mapping.SourceMsgID == "" ||
		mapping.TargetDriver == "" || mapping.TargetMsgID == "" {
		return fmt.Errorf("invalid mapping: all fields are required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	query := `INSERT OR REPLACE INTO message_mappings 
		(source_driver, source_msg_id, target_driver, target_msg_id, created_at)
		VALUES (?, ?, ?, ?, ?)`

	_, err := s.db.Exec(query, mapping.SourceDriver, mapping.SourceMsgID,
		mapping.TargetDriver, mapping.TargetMsgID, mapping.CreatedAt.Unix())
	if err != nil {
		return fmt.Errorf("save message mapping: %w", err)
	}

	return nil
}

// GetTargetID 根据来源消息查找目标平台的消息 ID
func (s *MessageMapStore) GetTargetID(sourceDriver, sourceMsgID, targetDriver string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var targetMsgID string
	query := `SELECT target_msg_id FROM message_mappings
		WHERE source_driver = ? AND source_msg_id = ? AND target_driver = ?`

	err := s.db.QueryRow(query, sourceDriver, sourceMsgID, targetDriver).Scan(&targetMsgID)
	return targetMsgID, err == nil
}

// GetAllTargets 获取一条消息在所有目标平台的映射 ID
// 参数：
//   - sourceDriver: 来源驱动名称
//   - sourceMsgID: 来源消息 ID
//
// 返回：
//   - []MessageMapping: 所有映射关系列表
//   - error: 错误信息
func (s *MessageMapStore) GetAllTargets(sourceDriver, sourceMsgID string) ([]MessageMapping, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `
		SELECT source_driver, source_msg_id, target_driver, target_msg_id, created_at
		FROM message_mappings
		WHERE source_driver = ? AND source_msg_id = ?
	`

	rows, err := s.db.Query(query, sourceDriver, sourceMsgID)
	if err != nil {
		s.logger.Error("storage", "Failed to query message mappings", map[string]interface{}{
			"source_driver": sourceDriver,
			"source_msg_id": sourceMsgID,
			"error":         err.Error(),
		})
		return nil, err
	}
	defer rows.Close()

	var mappings []MessageMapping
	for rows.Next() {
		var m MessageMapping
		var createdAt int64
		if err := rows.Scan(&m.SourceDriver, &m.SourceMsgID, &m.TargetDriver, &m.TargetMsgID, &createdAt); err != nil {
			s.logger.Error("storage", "Failed to scan message mapping", map[string]interface{}{
				"error": err.Error(),
			})
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
	s.mu.Lock()
	defer s.mu.Unlock()

	ttl := 48 * time.Hour
	cutoff := time.Now().Add(-ttl).Unix()

	query := `DELETE FROM message_mappings WHERE created_at < ?`
	result, err := s.db.Exec(query, cutoff)
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
	return s.db.Close()
}
