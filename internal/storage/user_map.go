// Package storage 提供持久化存储功能
// 包含消息映射、路由绑定、用户映射等存储实现
package storage

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	"Relify/internal/logger"
)

// UserMapping 用户 ID 映射关系
type UserMapping struct {
	SourceDriver string    // 来源驱动名称
	SourceUserID string    // 来源用户 ID
	TargetDriver string    // 目标驱动名称
	TargetUserID string    // 目标用户 ID
	DisplayName  string    // 显示名称（用于回退）
	Username     string    // 用户名（可选）
	CreatedAt    time.Time // 创建时间
	UpdatedAt    time.Time // 更新时间
}

// UserMapStore 用户 ID 映射存储
// 用于存储跨平台的用户 ID 映射关系，支持提及功能
type UserMapStore struct {
	db     *sql.DB
	mu     sync.RWMutex
	logger *logger.Logger
}

// NewUserMapStore 创建用户 ID 映射存储实例
func NewUserMapStore(dbPath string, log *logger.Logger) (*UserMapStore, error) {
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

	store := &UserMapStore{db: db, logger: log}

	if err := store.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	log.Info("storage", "UserMapStore initialized")
	return store, nil
}

// initSchema 初始化表结构
func (s *UserMapStore) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS user_mappings (
		source_driver TEXT NOT NULL,
		source_user_id TEXT NOT NULL,
		target_driver TEXT NOT NULL,
		target_user_id TEXT NOT NULL,
		display_name TEXT NOT NULL,
		username TEXT,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (source_driver, source_user_id, target_driver)
	);
	
	CREATE INDEX IF NOT EXISTS idx_user_mappings_lookup 
	ON user_mappings(source_driver, source_user_id, target_driver);
	`

	_, err := s.db.Exec(schema)
	return err
}

// Save 保存或更新用户映射关系
// 参数：
//   - mapping: 用户映射关系
//
// 返回：
//   - error: 错误信息
func (s *UserMapStore) Save(mapping *UserMapping) error {
	if mapping == nil {
		return fmt.Errorf("mapping cannot be nil")
	}

	if mapping.SourceDriver == "" || mapping.SourceUserID == "" ||
		mapping.TargetDriver == "" || mapping.TargetUserID == "" {
		return fmt.Errorf("invalid mapping: source_driver, source_user_id, target_driver, and target_user_id are required")
	}

	if mapping.DisplayName == "" {
		return fmt.Errorf("display_name is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if mapping.CreatedAt.IsZero() {
		mapping.CreatedAt = now
	}
	mapping.UpdatedAt = now

	query := `
		INSERT INTO user_mappings 
		(source_driver, source_user_id, target_driver, target_user_id, display_name, username, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_driver, source_user_id, target_driver)
		DO UPDATE SET
			target_user_id = excluded.target_user_id,
			display_name = excluded.display_name,
			username = excluded.username,
			updated_at = excluded.updated_at
	`

	_, err := s.db.Exec(query,
		mapping.SourceDriver,
		mapping.SourceUserID,
		mapping.TargetDriver,
		mapping.TargetUserID,
		mapping.DisplayName,
		mapping.Username,
		mapping.CreatedAt.Unix(),
		mapping.UpdatedAt.Unix(),
	)

	if err != nil {
		s.logger.Error("storage", "Failed to save user mapping", map[string]interface{}{
			"source_driver":  mapping.SourceDriver,
			"source_user_id": mapping.SourceUserID,
			"target_driver":  mapping.TargetDriver,
			"error":          err.Error(),
		})
		return fmt.Errorf("save user mapping: %w", err)
	}

	s.logger.Debug("storage", "User mapping saved", map[string]interface{}{
		"source_driver":  mapping.SourceDriver,
		"source_user_id": mapping.SourceUserID,
		"target_driver":  mapping.TargetDriver,
		"target_user_id": mapping.TargetUserID,
		"display_name":   mapping.DisplayName,
	})

	return nil
}

// GetTargetUserID 查询目标平台的用户 ID
func (s *UserMapStore) GetTargetUserID(sourceDriver, sourceUserID, targetDriver string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var targetUserID string
	query := `SELECT target_user_id FROM user_mappings 
		WHERE source_driver = ? AND source_user_id = ? AND target_driver = ?`

	err := s.db.QueryRow(query, sourceDriver, sourceUserID, targetDriver).Scan(&targetUserID)
	return targetUserID, err == nil
}

// GetMapping 获取完整的用户映射信息
// 参数：
//   - sourceDriver: 来源驱动名称
//   - sourceUserID: 来源用户 ID
//   - targetDriver: 目标驱动名称
//
// 返回：
//   - *UserMapping: 用户映射关系
//   - bool: 是否找到映射
func (s *UserMapStore) GetMapping(sourceDriver, sourceUserID, targetDriver string) (*UserMapping, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var mapping UserMapping
	var createdAt, updatedAt int64

	query := `
		SELECT source_driver, source_user_id, target_driver, target_user_id, 
		       display_name, username, created_at, updated_at
		FROM user_mappings 
		WHERE source_driver = ? AND source_user_id = ? AND target_driver = ?
	`

	err := s.db.QueryRow(query, sourceDriver, sourceUserID, targetDriver).Scan(
		&mapping.SourceDriver,
		&mapping.SourceUserID,
		&mapping.TargetDriver,
		&mapping.TargetUserID,
		&mapping.DisplayName,
		&mapping.Username,
		&createdAt,
		&updatedAt,
	)

	if err != nil {
		s.logger.Debug("storage", "User mapping details not found", map[string]interface{}{
			"source_driver":  sourceDriver,
			"source_user_id": sourceUserID,
			"target_driver":  targetDriver,
		})
		return nil, false
	}

	mapping.CreatedAt = time.Unix(createdAt, 0)
	mapping.UpdatedAt = time.Unix(updatedAt, 0)

	return &mapping, true
}

// Close 关闭数据库连接
func (s *UserMapStore) Close() error {
	s.logger.Info("storage", "Closing UserMapStore")
	return s.db.Close()
}
