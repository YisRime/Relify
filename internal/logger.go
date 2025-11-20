package internal

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

type Level int

const (
	DebugLevel Level = iota
	InfoLevel
	WarnLevel
	ErrorLevel
)

type Logger struct {
	level  Level
	output io.Writer
	mu     sync.Mutex
}

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

	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return nil, err
	}

	logFile := fmt.Sprintf("%s/%s.log", logsDir, time.Now().Format("2006-01-02"))
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}

	return &Logger{
		level:  level,
		output: io.MultiWriter(f, os.Stderr),
	}, nil
}

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

// 全局单例与辅助方法
var global *Logger
var once sync.Once

func SetGlobal(l *Logger) { global = l }
func GetGlobal() *Logger {
	once.Do(func() { global = &Logger{level: InfoLevel, output: os.Stdout} })
	return global
}

func Debug(mod, msg string, f ...map[string]interface{}) { logIt(DebugLevel, mod, msg, f) }
func Info(mod, msg string, f ...map[string]interface{})  { logIt(InfoLevel, mod, msg, f) }
func Warn(mod, msg string, f ...map[string]interface{})  { logIt(WarnLevel, mod, msg, f) }
func Error(mod, msg string, f ...map[string]interface{}) { logIt(ErrorLevel, mod, msg, f) }

func logIt(l Level, m, msg string, f []map[string]interface{}) {
	var fields map[string]interface{}
	if len(f) > 0 {
		fields = f[0]
	}
	GetGlobal().Log(l, m, msg, fields)
}
