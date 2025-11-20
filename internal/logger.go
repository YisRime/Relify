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
	closer io.Closer
	mu     sync.Mutex
}

func NewLoggerFromConfig(levelStr, logsDir string) (*Logger, error) {
	lvl := InfoLevel
	switch levelStr {
	case "debug":
		lvl = DebugLevel
	case "warn":
		lvl = WarnLevel
	case "error":
		lvl = ErrorLevel
	}

	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return nil, err
	}

	filename := fmt.Sprintf("%s/%s.log", logsDir, time.Now().Format("2006-01-02"))
	f, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}

	return &Logger{
		level:  lvl,
		output: io.MultiWriter(f, os.Stdout),
		closer: f,
	}, nil
}

func (l *Logger) Close() error {
	if l.closer != nil {
		return l.closer.Close()
	}
	return nil
}

func (l *Logger) Log(level Level, mod, msg string, fields map[string]interface{}) {
	if level < l.level {
		return
	}

	entry := map[string]interface{}{
		"ts":  time.Now().Format(time.RFC3339),
		"lvl": levelString(level),
		"mod": mod,
		"msg": msg,
	}
	if len(fields) > 0 {
		entry["dat"] = fields
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

var global *Logger
var once sync.Once

func SetGlobal(l *Logger) { global = l }

func GetGlobal() *Logger {
	once.Do(func() {
		if global == nil {
			global = &Logger{level: InfoLevel, output: os.Stdout}
		}
	})
	return global
}
