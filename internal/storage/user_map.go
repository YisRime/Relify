// Package storage 提供持久化存储功能
package storage

import (
	"fmt"
	"time"

	"Relify/internal/logger"
)

// UserMapping 用户映射关系
type UserMapping struct {
	SourcePlatform string
	SourceUserID   string
	TargetPlatform string
	TargetUserID   string
	DisplayName    string
	Username       string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// UserMapStore 用户映射存储
type UserMapStore struct {
	*BaseStore
}

// NewUserMapStore 创建用户映射存储
func NewUserMapStore(dbPath string, log *logger.Logger) (*UserMapStore, error) {
	base, err := NewBaseStore(dbPath, log)
	if err != nil {
		return nil, err
	}

	store := &UserMapStore{BaseStore: base}

	if err := store.initSchema(); err != nil {
		base.Close()
		return nil, fmt.Errorf("initialize schema: %w", err)
	}

	log.Info("storage", "UserMapStore initialized")
	return store, nil
}

// initSchema 初始化表结构
func (s *UserMapStore) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS user_mappings (
		source_platform TEXT NOT NULL,
		source_user_id TEXT NOT NULL,
		target_platform TEXT NOT NULL,
		target_user_id TEXT NOT NULL,
		display_name TEXT NOT NULL,
		username TEXT,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (source_platform, source_user_id, target_platform)
	);
	
	CREATE INDEX IF NOT EXISTS idx_user_mappings_lookup 
	ON user_mappings(source_platform, source_user_id, target_platform);
	`

	return s.ExecSchema(schema)
}

// Save 保存或更新用户映射
func (s *UserMapStore) Save(mapping *UserMapping) error {
	if mapping == nil {
		return fmt.Errorf("mapping cannot be nil")
	}

	if mapping.SourcePlatform == "" || mapping.SourceUserID == "" ||
		mapping.TargetPlatform == "" || mapping.TargetUserID == "" {
		return fmt.Errorf("invalid mapping: all platform and user ID fields are required")
	}

	if mapping.DisplayName == "" {
		return fmt.Errorf("display name cannot be empty")
	}

	now := time.Now()
	if mapping.CreatedAt.IsZero() {
		mapping.CreatedAt = now
	}
	mapping.UpdatedAt = now

	query := `
		INSERT INTO user_mappings 
		(source_platform, source_user_id, target_platform, target_user_id, display_name, username, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_platform, source_user_id, target_platform)
		DO UPDATE SET
			target_user_id = excluded.target_user_id,
			display_name = excluded.display_name,
			username = excluded.username,
			updated_at = excluded.updated_at
	`

	s.Lock()
	_, err := s.DB().Exec(query,
		mapping.SourcePlatform,
		mapping.SourceUserID,
		mapping.TargetPlatform,
		mapping.TargetUserID,
		mapping.DisplayName,
		mapping.Username,
		mapping.CreatedAt.Unix(),
		mapping.UpdatedAt.Unix(),
	)
	s.Unlock()

	if err != nil {
		s.logger.Error("storage", "Failed to save user mapping", map[string]interface{}{
			"source_platform": mapping.SourcePlatform,
			"source_user_id":  mapping.SourceUserID,
			"target_platform": mapping.TargetPlatform,
			"error":           err.Error(),
		})
		return fmt.Errorf("save user mapping: %w", err)
	}

	s.logger.Debug("storage", "User mapping saved", map[string]interface{}{
		"source_platform": mapping.SourcePlatform,
		"source_user_id":  mapping.SourceUserID,
		"target_platform": mapping.TargetPlatform,
		"target_user_id":  mapping.TargetUserID,
		"display_name":    mapping.DisplayName,
	})

	return nil
}

// GetTargetUserID 查询目标用户ID
func (s *UserMapStore) GetTargetUserID(sourcePlatform, sourceUserID, targetPlatform string) (string, bool) {
	var targetUserID string
	query := `SELECT target_user_id FROM user_mappings 
		WHERE source_platform = ? AND source_user_id = ? AND target_platform = ?`

	s.RLock()
	err := s.DB().QueryRow(query, sourcePlatform, sourceUserID, targetPlatform).Scan(&targetUserID)
	s.RUnlock()

	return targetUserID, err == nil
}

// GetMapping 获取完整的用户映射
func (s *UserMapStore) GetMapping(sourcePlatform, sourceUserID, targetPlatform string) (*UserMapping, bool) {
	var mapping UserMapping
	var createdAt, updatedAt int64

	query := `
		SELECT source_platform, source_user_id, target_platform, target_user_id, 
		       display_name, username, created_at, updated_at
		FROM user_mappings 
		WHERE source_platform = ? AND source_user_id = ? AND target_platform = ?
	`

	s.RLock()
	err := s.DB().QueryRow(query, sourcePlatform, sourceUserID, targetPlatform).Scan(
		&mapping.SourcePlatform,
		&mapping.SourceUserID,
		&mapping.TargetPlatform,
		&mapping.TargetUserID,
		&mapping.DisplayName,
		&mapping.Username,
		&createdAt,
		&updatedAt,
	)
	s.RUnlock()

	if err != nil {
		s.logger.Debug("storage", "User mapping not found", map[string]interface{}{
			"source_platform": sourcePlatform,
			"source_user_id":  sourceUserID,
			"target_platform": targetPlatform,
		})
		return nil, false
	}

	mapping.CreatedAt = time.Unix(createdAt, 0)
	mapping.UpdatedAt = time.Unix(updatedAt, 0)

	return &mapping, true
}

// Close 关闭数据库
func (s *UserMapStore) Close() error {
	s.logger.Info("storage", "Closing UserMapStore")
	return s.BaseStore.Close()
}
