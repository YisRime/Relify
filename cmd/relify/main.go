// Relify - 跨平台消息桥接系统
// 基于六边形架构设计，支持 Telegram、Discord、Matrix 等多平台消息同步
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"Relify/internal/config"
	"Relify/internal/core"
	"Relify/internal/logger"
)

func main() {
	// 解析命令行参数
	configPath := flag.String("config", "config.yaml", "配置文件路径")
	flag.Parse()

	// 加载配置文件
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// 验证配置有效性
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Invalid config: %v\n", err)
		os.Exit(1)
	}

	// 初始化数据库路径
	dbPath := cfg.Database.Path
	if dbPath == "" {
		dbPath = "./data/relify.db"
	}

	// 确保数据库目录存在
	dbDir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create database directory: %v\n", err)
		os.Exit(1)
	}

	// 创建核心实例
	coreInst, err := core.NewCore(&core.Config{
		DatabasePath: dbPath,
		AppConfig:    cfg,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize core: %v\n", err)
		os.Exit(1)
	}

	// TODO: 注册驱动（后续实现 Telegram、Discord、Matrix 驱动）
	// 示例：
	// telegramDriver := telegram.NewDriver(cfg.Drivers["telegram"])
	// coreInst.RegisterDriver(telegramDriver)

	// 启动核心层
	ctx := context.Background()
	if err := coreInst.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start core: %v\n", err)
		os.Exit(1)
	}

	logger.Info("main", "Relify started successfully")
	logger.Info("main", "Press Ctrl+C to stop")

	// 优雅关闭信号处理
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	logger.Info("main", "Shutting down...")

	// 停止核心层（带超时）
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := coreInst.Stop(shutdownCtx); err != nil {
		logger.Error("main", "Error during shutdown", map[string]interface{}{
			"error": err.Error(),
		})
		os.Exit(1)
	}

	logger.Info("main", "Shutdown complete")
}
