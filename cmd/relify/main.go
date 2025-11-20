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
	// "Relify/platforms/discord"
)

const (
	shutdownTimeout = 10 * time.Second
)

func main() {
	// 1. 解析命令行参数
	configPath := flag.String("config", "config.yaml", "配置文件路径")
	flag.Parse()

	// 2. 自动检测配置是否存在
	if _, err := os.Stat(*configPath); os.IsNotExist(err) {
		fmt.Printf("配置文件 '%s' 未找到，正在生成默认配置...\n", *configPath)

		defaultCfg := internal.GenerateDefault()
		if err := internal.SaveConfig(*configPath, defaultCfg); err != nil {
			fmt.Fprintf(os.Stderr, "❌ 无法写入默认配置文件: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("✅ 默认配置已生成！\n请编辑 '%s' 填入正确的 Token 或 API Key，然后再次运行程序。\n", *configPath)
		// 生成后直接退出，提示用户去修改。
		os.Exit(0)
	}

	// 3. 加载配置
	cfg, err := internal.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// 4. 校验配置
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Invalid config: %v\n", err)
		os.Exit(1)
	}

	// 5. 创建数据目录
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating data dir: %v\n", err)
		os.Exit(1)
	}

	// 6. 初始化日志
	logger, err := internal.NewLoggerFromConfig(cfg.LogLevel, cfg.GetLogsDir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing logger: %v\n", err)
		os.Exit(1)
	}
	internal.SetGlobal(logger)

	// 7. 初始化核心
	coreInst, err := internal.NewCore(&internal.CoreConfig{
		AppConfig: cfg,
	})
	if err != nil {
		internal.Error("main", "Failed to initialize core", map[string]interface{}{
			"error": err.Error(),
		})
		os.Exit(1)
	}

	// 8. 注册平台 (示例)
	// if cfg.Platforms["discord_example"].Enabled {
	//     coreInst.RegisterPlatform(discord.New(cfg.Platforms["discord_example"]))
	// }

	// 9. 启动
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := coreInst.Start(ctx); err != nil {
		internal.Error("main", "Startup failed", map[string]interface{}{
			"error": err.Error(),
		})
		os.Exit(1)
	}

	internal.Info("main", "Relify is running. Press Ctrl+C to stop.", nil)

	// 10. 监听退出信号
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	internal.Info("main", "Shutting down...", nil)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()

	if err := coreInst.Stop(shutdownCtx); err != nil {
		internal.Error("main", "Shutdown error", map[string]interface{}{
			"error": err.Error(),
		})
		os.Exit(1)
	}

	internal.Info("main", "Shutdown complete", nil)
}
