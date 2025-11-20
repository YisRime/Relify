package storage

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// MessageMapStore ID 映射存储
type MessageMapStore struct {
	db *sql.DB
	mu sync.RWMutex
}

// MessageMapping 消息映射关系
type MessageMapping struct {
	SourceDriver string    `json:"source_driver"`
	SourceMsgID  string    `json:"source_msg_id"`
	TargetDriver string    `json:"target_driver"`
	TargetMsgID  string    `json:"target_msg_id"`
	CreatedAt    time.Time `json:"created_at"`
}

// NewMessageMapStore 创建映射存储实例
func NewMessageMapStore(dbPath string) (*MessageMapStore, error) {
	db, err := sql.Open("sqlite", dbPath+"?_journal=WAL&_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	store := &MessageMapStore{db: db}
	if err := store.initSchema(); err != nil {
		return nil, err
	}

	// 启动清理协程
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

// Save 保存映射关系（异步安全）
func (s *MessageMapStore) Save(mapping *MessageMapping) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `
		INSERT OR REPLACE INTO message_mappings 
		(source_driver, source_msg_id, target_driver, target_msg_id, created_at)
		VALUES (?, ?, ?, ?, ?)
	`

	_, err := s.db.Exec(query,
		mapping.SourceDriver,
		mapping.SourceMsgID,
		mapping.TargetDriver,
		mapping.TargetMsgID,
		mapping.CreatedAt.Unix(),
	)

	return err
}

// GetTargetID 根据来源查找目标消息 ID
func (s *MessageMapStore) GetTargetID(sourceDriver, sourceMsgID, targetDriver string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `
		SELECT target_msg_id FROM message_mappings
		WHERE source_driver = ? AND source_msg_id = ? AND target_driver = ?
	`

	var targetMsgID string
	err := s.db.QueryRow(query, sourceDriver, sourceMsgID, targetDriver).Scan(&targetMsgID)
	if err != nil {
		return "", false
	}

	return targetMsgID, true
}

// GetAllTargets 获取一条消息在所有平台的映射 ID
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
		return nil, err
	}
	defer rows.Close()

	var mappings []MessageMapping
	for rows.Next() {
		var m MessageMapping
		var createdAt int64
		if err := rows.Scan(&m.SourceDriver, &m.SourceMsgID, &m.TargetDriver, &m.TargetMsgID, &createdAt); err != nil {
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

// cleanup 清理过期数据
func (s *MessageMapStore) cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()

	ttl := 48 * time.Hour
	cutoff := time.Now().Add(-ttl).Unix()

	query := `DELETE FROM message_mappings WHERE created_at < ?`
	result, err := s.db.Exec(query, cutoff)
	if err != nil {
		// 日志记录错误（后续集成日志系统）
		return
	}

	if affected, _ := result.RowsAffected(); affected > 0 {
		// 日志记录清理数量
		_ = affected
	}
}

// Close 关闭数据库连接
func (s *MessageMapStore) Close() error {
	return s.db.Close()
}
