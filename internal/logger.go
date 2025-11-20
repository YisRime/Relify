package internal

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Level 定义日志级别
type Level int

const (
	DebugLevel Level = iota
	InfoLevel
	WarnLevel
	ErrorLevel
)

// Logger 结构体，支持多路输出
type Logger struct {
	level  Level
	output io.Writer
	mu     sync.Mutex
}

// NewLoggerFromConfig 根据配置创建 Logger，日志文件按日期滚动
func NewLoggerFromConfig(levelStr, logsDir string) (*Logger, error) {
	level := InfoLevel
	switch levelStr {
	case "debug":
		level = DebugLevel
	case "warn":
		level = WarnLevel
	case "error":
		level = ErrorLevel
	}

	// 确保日志目录存在
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return nil, err
	}

	// 简单的按天轮转日志文件命名
	logFile := fmt.Sprintf("%s/%s.log", logsDir, time.Now().Format("2006-01-02"))
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}

	return &Logger{
		level:  level,
		output: io.MultiWriter(f, os.Stderr), // 同时输出到文件和标准错误
	}, nil
}

// Log 核心日志方法，输出 JSON 格式
func (l *Logger) Log(level Level, module, msg string, fields map[string]interface{}) {
	if level < l.level {
		return
	}

	entry := map[string]interface{}{
		"ts":     time.Now().Format(time.RFC3339),
		"lvl":    levelString(level),
		"mod":    module,
		"msg":    msg,
		"fields": fields,
	}

	data, _ := json.Marshal(entry)
	l.mu.Lock()
	fmt.Fprintln(l.output, string(data))
	l.mu.Unlock()
}

// levelString 将日志级别转换为字符串
func levelString(l Level) string {
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

// 全局单例变量
var global *Logger
var once sync.Once

// SetGlobal 设置全局日志实例
func SetGlobal(l *Logger) { global = l }

// GetGlobal 获取全局日志实例，如果未初始化则返回标准输出默认值
func GetGlobal() *Logger {
	once.Do(func() { global = &Logger{level: InfoLevel, output: os.Stdout} })
	return global
}

// 快捷日志辅助函数
func Debug(mod, msg string, f ...map[string]interface{}) { logIt(DebugLevel, mod, msg, f) }
func Info(mod, msg string, f ...map[string]interface{})  { logIt(InfoLevel, mod, msg, f) }
func Warn(mod, msg string, f ...map[string]interface{})  { logIt(WarnLevel, mod, msg, f) }
func Error(mod, msg string, f ...map[string]interface{}) { logIt(ErrorLevel, mod, msg, f) }

// logIt 内部辅助函数，处理可选参数
func logIt(l Level, m, msg string, f []map[string]interface{}) {
	var fields map[string]interface{}
	if len(f) > 0 {
		fields = f[0]
	}
	GetGlobal().Log(l, m, msg, fields)
}
