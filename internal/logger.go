// Package internal 提供统一的日志系统
package internal

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Level 日志级别
type Level int

const (
	DebugLevel Level = iota
	InfoLevel
	WarnLevel
	ErrorLevel
)

// String 返回日志级别的字符串表示
func (l Level) String() string {
	switch l {
	case DebugLevel:
		return "DEBUG"
	case InfoLevel:
		return "INFO"
	case WarnLevel:
		return "WARN"
	case ErrorLevel:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// ParseLevel 从字符串解析日志级别
func ParseLevel(s string) Level {
	switch s {
	case "debug":
		return DebugLevel
	case "info":
		return InfoLevel
	case "warn":
		return WarnLevel
	case "error":
		return ErrorLevel
	default:
		return InfoLevel
	}
}

// Format 日志格式
type Format int

const (
	JSONFormat Format = iota
	TextFormat
)

// Logger 日志记录器
type Logger struct {
	level  Level
	format Format
	output io.Writer
	mu     sync.Mutex
}

// LogEntry 日志条目（用于 JSON 格式）
type LogEntry struct {
	Timestamp string                 `json:"timestamp"`
	Level     string                 `json:"level"`
	Module    string                 `json:"module,omitempty"`
	Message   string                 `json:"message"`
	Fields    map[string]interface{} `json:"fields,omitempty"`
}

// NewLogger 创建新的日志记录器 (原名 New)
func NewLogger(level Level, format Format, output io.Writer) *Logger {
	return &Logger{
		level:  level,
		format: format,
		output: output,
	}
}

// NewLoggerFromConfig 从配置创建日志记录器
func NewLoggerFromConfig(levelStr, logsDir string) (*Logger, error) {
	level := ParseLevel(levelStr)
	format := JSONFormat

	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	logFile := fmt.Sprintf("%s/%s.log", logsDir, timestamp)

	// 打开日志文件
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}

	// 同时输出到文件和 stderr
	output := io.MultiWriter(f, os.Stderr)
	return NewLogger(level, format, output), nil
}

// Debug 记录 DEBUG 级别日志
func (l *Logger) Debug(module, message string, fields ...map[string]interface{}) {
	l.log(DebugLevel, module, message, fields...)
}

// Info 记录 INFO 级别日志
func (l *Logger) Info(module, message string, fields ...map[string]interface{}) {
	l.log(InfoLevel, module, message, fields...)
}

// Warn 记录 WARN 级别日志
func (l *Logger) Warn(module, message string, fields ...map[string]interface{}) {
	l.log(WarnLevel, module, message, fields...)
}

// Error 记录 ERROR 级别日志
func (l *Logger) Error(module, message string, fields ...map[string]interface{}) {
	l.log(ErrorLevel, module, message, fields...)
}

// log 内部日志记录方法
func (l *Logger) log(level Level, module, message string, fields ...map[string]interface{}) {
	if level < l.level {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	timestamp := time.Now().Format(time.RFC3339)

	if l.format == JSONFormat {
		entry := LogEntry{
			Timestamp: timestamp,
			Level:     level.String(),
			Module:    module,
			Message:   message,
		}
		if len(fields) > 0 && fields[0] != nil {
			entry.Fields = fields[0]
		}
		data, err := json.Marshal(entry)
		if err != nil {
			return
		}
		fmt.Fprintf(l.output, "%s\n", data)
	} else {
		// Text format
		fmt.Fprintf(l.output, "[%s] %s", timestamp, level.String())
		if module != "" {
			fmt.Fprintf(l.output, " [%s]", module)
		}
		fmt.Fprintf(l.output, " %s", message)
		if len(fields) > 0 && fields[0] != nil {
			fmt.Fprintf(l.output, " %v", fields[0])
		}
		fmt.Fprintln(l.output)
	}
}

// SetLevel 设置日志级别
func (l *Logger) SetLevel(level Level) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// GetLevel 获取当前日志级别
func (l *Logger) GetLevel() Level {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.level
}

// With 创建带有预设字段的子记录器
type SubLogger struct {
	logger *Logger
	module string
	fields map[string]interface{}
}

// With 创建子记录器
func (l *Logger) With(module string, fields map[string]interface{}) *SubLogger {
	return &SubLogger{
		logger: l,
		module: module,
		fields: fields,
	}
}

// Debug 记录 DEBUG 日志
func (s *SubLogger) Debug(message string, extraFields ...map[string]interface{}) {
	fields := s.mergeFields(extraFields...)
	s.logger.Debug(s.module, message, fields)
}

// Info 记录 INFO 日志
func (s *SubLogger) Info(message string, extraFields ...map[string]interface{}) {
	fields := s.mergeFields(extraFields...)
	s.logger.Info(s.module, message, fields)
}

// Warn 记录 WARN 日志
func (s *SubLogger) Warn(message string, extraFields ...map[string]interface{}) {
	fields := s.mergeFields(extraFields...)
	s.logger.Warn(s.module, message, fields)
}

// Error 记录 ERROR 日志
func (s *SubLogger) Error(message string, extraFields ...map[string]interface{}) {
	fields := s.mergeFields(extraFields...)
	s.logger.Error(s.module, message, fields)
}

// mergeFields 合并预设字段和额外字段
func (s *SubLogger) mergeFields(extraFields ...map[string]interface{}) map[string]interface{} {
	merged := make(map[string]interface{})

	// 复制预设字段
	for k, v := range s.fields {
		merged[k] = v
	}

	// 合并额外字段
	if len(extraFields) > 0 && extraFields[0] != nil {
		for k, v := range extraFields[0] {
			merged[k] = v
		}
	}

	if len(merged) == 0 {
		return nil
	}

	return merged
}

// Global 全局日志记录器实例
var (
	global     *Logger
	globalOnce sync.Once
)

func initGlobal() {
	global = NewLogger(InfoLevel, JSONFormat, os.Stdout)
}

// SetGlobal 设置全局日志记录器
func SetGlobal(logger *Logger) {
	global = logger
}

// GetGlobal 获取全局日志记录器
func GetGlobal() *Logger {
	globalOnce.Do(initGlobal)
	return global
}

// 全局便捷方法

// Debug 全局 DEBUG 日志
func Debug(module, message string, fields ...map[string]interface{}) {
	global.Debug(module, message, fields...)
}

// Info 全局 INFO 日志
func Info(module, message string, fields ...map[string]interface{}) {
	global.Info(module, message, fields...)
}

// Warn 全局 WARN 日志
func Warn(module, message string, fields ...map[string]interface{}) {
	global.Warn(module, message, fields...)
}

// Error 全局 ERROR 日志
func Error(module, message string, fields ...map[string]interface{}) {
	global.Error(module, message, fields...)
}
