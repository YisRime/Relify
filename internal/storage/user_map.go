package storage

import (
	"database/sql"
	"fmt"
	"sync"
	"time"
)

// UserMapping 用户 ID 映射
type UserMapping struct {
	SourceDriver string    // 来源驱动
	SourceUserID string    // 来源用户 ID
	TargetDriver string    // 目标驱动
	TargetUserID string    // 目标用户 ID
	DisplayName  string    // 显示名称（用于回退）
	Username     string    // 用户名（可选）
	CreatedAt    time.Time // 创建时间
	UpdatedAt    time.Time // 更新时间
}

// UserMapStore 用户 ID 映射存储
type UserMapStore struct {
	db *sql.DB
	mu sync.RWMutex
}

// NewUserMapStore 创建用户映射存储
func NewUserMapStore(dbPath string) (*UserMapStore, error) {
	db, err := sql.Open("sqlite", dbPath+"?_journal=WAL&_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	store := &UserMapStore{
		db: db,
	}

	if err := store.initSchema(); err != nil {
		return nil, err
	}

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

// Save 保存或更新用户映射
func (s *UserMapStore) Save(mapping *UserMapping) error {
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

	return err
}

// GetTargetUserID 查询目标平台的用户 ID
func (s *UserMapStore) GetTargetUserID(sourceDriver, sourceUserID, targetDriver string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var targetUserID string
	query := `
		SELECT target_user_id 
		FROM user_mappings 
		WHERE source_driver = ? AND source_user_id = ? AND target_driver = ?
	`

	err := s.db.QueryRow(query, sourceDriver, sourceUserID, targetDriver).Scan(&targetUserID)
	if err != nil {
		return "", false
	}

	return targetUserID, true
}

// GetMapping 获取完整的用户映射信息
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
		return nil, false
	}

	mapping.CreatedAt = time.Unix(createdAt, 0)
	mapping.UpdatedAt = time.Unix(updatedAt, 0)

	return &mapping, true
}

// Close 关闭数据库连接
func (s *UserMapStore) Close() error {
	return s.db.Close()
}
