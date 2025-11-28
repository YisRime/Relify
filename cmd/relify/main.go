package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"Relify/internal"
	_ "Relify/internal/driver/matrix"
	_ "Relify/internal/driver/qq"
)

// main 是应用程序的入口函数。
// 主要流程包括：
// 1. 调用 setup 初始化环境和配置。
// 2. 创建并启动核心服务 (Core)。
// 3. 阻塞监听系统中断信号 (如 Ctrl+C, SIGTERM)。
// 4. 接收到信号后执行优雅关闭流程。
func main() {
	config := setup()
	ctx, cancel := context.WithCancel(context.Background())

	app, err := internal.NewCore(config)
	if err == nil {
		err = app.Start(ctx)
	}

	if err != nil {
		slog.Error("启动失败", "err", err)
		cancel()
		os.Exit(1)
	}

	// 监听中断信号 (Ctrl+C) 或终止信号
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	slog.Info("停止中...")
	cancel()

	// 设置超时上下文以确保清理操作不会无限期挂起
	stopCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	app.Stop(stopCtx)
}

// setup 负责应用程序的基础设施初始化。
// 功能包括：
// - 创建日志目录。
// - 加载或生成默认配置文件。
// - 初始化结构化日志记录器 (slog)，配置日志级别、输出格式及多端输出 (控制台+文件)。
func setup() *internal.Config {
	os.MkdirAll("data/logs", 0755)
	configPath := filepath.Join("data", "config.yaml")

	// 检查配置文件是否存在，不存在则生成默认配置
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if internal.SaveConfig(configPath, internal.DefaultConfig()) == nil {
			slog.Info("生成配置成功", "path", configPath)
			os.Exit(0)
		}
	}

	config, err := internal.LoadConfig(configPath)
	if err != nil || config.Check() != nil {
		slog.Error("加载配置失败", "err", err)
		os.Exit(1)
	}

	// 设置日志文件输出，文件名包含当前时间戳
	logFile, _ := os.OpenFile(
		filepath.Join("data/logs", fmt.Sprintf("relify_%s.log", time.Now().Format("2006-01-02_15-04-05"))),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0666,
	)

	// 根据配置设置日志级别
	logLevel := slog.LevelInfo
	switch config.LogLevel {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	}

	// 配置 slog 使用 JSON 格式，并同时输出到控制台和文件
	slog.SetDefault(slog.New(slog.NewJSONHandler(io.MultiWriter(os.Stdout, logFile), &slog.HandlerOptions{
		Level: logLevel,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			// 格式化时间戳为可读格式
			if a.Key == slog.TimeKey {
				return slog.Attr{Key: a.Key, Value: slog.StringValue(a.Value.Time().Format("2006-01-02 15:04:05.000"))}
			}
			return a
		},
	})))

	return config
}
