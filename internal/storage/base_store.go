// Package storage 提供持久化存储功能
package storage

import (
	"database/sql"
	"fmt"
	"sync"

	"Relify/internal/logger"

	_ "modernc.org/sqlite"
)

// BaseStore 基础存储，提供公共的数据库操作
type BaseStore struct {
	db     *sql.DB
	mu     sync.RWMutex
	logger *logger.Logger
}

// NewBaseStore 创建基础存储
func NewBaseStore(dbPath string, log *logger.Logger) (*BaseStore, error) {
	if dbPath == "" {
		return nil, fmt.Errorf("database path cannot be empty")
	}

	db, err := sql.Open("sqlite", dbPath+"?_journal=WAL&_timeout=5000&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// 设置连接池参数以提高并发性能
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)

	if log == nil {
		log = logger.GetGlobal()
	}

	return &BaseStore{
		db:     db,
		logger: log,
	}, nil
}

// DB 获取数据库连接
func (s *BaseStore) DB() *sql.DB {
	return s.db
}

// Lock 获取写锁
func (s *BaseStore) Lock() {
	s.mu.Lock()
}

// Unlock 释放写锁
func (s *BaseStore) Unlock() {
	s.mu.Unlock()
}

// RLock 获取读锁
func (s *BaseStore) RLock() {
	s.mu.RLock()
}

// RUnlock 释放读锁
func (s *BaseStore) RUnlock() {
	s.mu.RUnlock()
}

// Close 关闭数据库连接
func (s *BaseStore) Close() error {
	return s.db.Close()
}

// ExecSchema 执行Schema初始化
func (s *BaseStore) ExecSchema(schema string) error {
	_, err := s.db.Exec(schema)
	return err
}
