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
	// 可以在这里导入具体的平台适配器包
	// "Relify/platforms/discord"
	// "Relify/platforms/telegram"
)

const (
	shutdownTimeout = 10 * time.Second
)

func main() {
	// 1. 解析命令行参数
	configPath := flag.String("config", "config.yaml", "配置文件路径")
	flag.Parse()

	// 2. 加载并校验配置
	cfg, err := internal.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Invalid config: %v\n", err)
		os.Exit(1)
	}

	// 3. 确保数据目录存在
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating data dir: %v\n", err)
		os.Exit(1)
	}

	// 4. 初始化全局日志
	logger, err := internal.NewLoggerFromConfig(cfg.LogLevel, cfg.GetLogsDir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing logger: %v\n", err)
		os.Exit(1)
	}
	internal.SetGlobal(logger)

	// 5. 初始化核心层 (Core)
	// NewCore 内部会自动初始化 Store (Database) 和 Router
	coreInst, err := internal.NewCore(&internal.CoreConfig{
		AppConfig: cfg,
	})
	if err != nil {
		internal.Error("main", "Failed to initialize core", map[string]interface{}{
			"error": err.Error(),
		})
		os.Exit(1)
	}

	// 6. 注册平台适配器 (TODO: 根据实际引入的包进行注册)
	// 示例:
	// if cfg.Platforms["discord"].Enabled {
	//     discordAdapter := discord.New(cfg.Platforms["discord"])
	//     coreInst.RegisterPlatform(discordAdapter)
	// }
	// if cfg.Platforms["telegram"].Enabled {
	//     tgAdapter := telegram.New(cfg.Platforms["telegram"])
	//     coreInst.RegisterPlatform(tgAdapter)
	// }

	// 7. 启动系统
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := coreInst.Start(ctx); err != nil {
		internal.Error("main", "Startup failed", map[string]interface{}{
			"error": err.Error(),
		})
		os.Exit(1)
	}

	internal.Info("main", "Relify is running. Press Ctrl+C to stop.", nil)

	// 8. 等待中断信号
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	internal.Info("main", "Shutting down...", nil)

	// 9. 优雅关闭
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()

	if err := coreInst.Stop(shutdownCtx); err != nil {
		internal.Error("main", "Shutdown error", map[string]interface{}{
			"error": err.Error(),
		})
		// 即使关闭出错也退出
		os.Exit(1)
	}

	internal.Info("main", "Shutdown complete", nil)
}
