// Relify - 跨平台消息桥接系统
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"Relify/internal"
)

const (
	shutdownTimeout = 30 * time.Second
	signalBuffer    = 1
)

func main() {
	configPath := flag.String("config", "config.yaml", "配置文件路径")
	flag.Parse()

	// 加载配置
	cfg, err := internal.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Invalid config: %v\n", err)
		os.Exit(1)
	}

	// 创建数据目录
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create data directory: %v\n", err)
		os.Exit(1)
	}

	// 初始化日志系统
	log, err := internal.NewLoggerFromConfig(cfg.LogLevel, cfg.GetLogsDir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	internal.SetGlobal(log)

	// 初始化核心业务层
	coreInst, err := internal.NewCore(&internal.CoreConfig{
		DatabasePath: cfg.GetDatabasePath(),
		AppConfig:    cfg,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize core: %v\n", err)
		os.Exit(1)
	}

	// TODO: 在此处注册具体平台的适配器
	// 例如: coreInst.RegisterPlatform(discord.NewAdapter(...))

	// 启动系统
	ctx := context.Background()
	if err := coreInst.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start: %v\n", err)
		os.Exit(1)
	}

	internal.Info("main", "Relify started successfully")
	internal.Info("main", "Press Ctrl+C to stop")

	// 等待中断信号
	sigChan := make(chan os.Signal, signalBuffer)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	internal.Info("main", "Shutting down...")

	// 优雅关闭
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := coreInst.Stop(shutdownCtx); err != nil {
		internal.Error("main", "Error during shutdown", map[string]interface{}{
			"error": err.Error(),
		})
		os.Exit(1)
	}

	internal.Info("main", "Shutdown complete")
}
