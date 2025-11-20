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

// 定义优雅退出的超时时间
const (
	shutdownTimeout = 10 * time.Second
)

func main() {
	// 定义命令行参数，默认为 config.yaml
	configPath := flag.String("config", "config.yaml", "Path to the configuration file")
	flag.Parse()

	// 检查配置文件是否存在，若不存在则生成默认配置
	if _, err := os.Stat(*configPath); os.IsNotExist(err) {
		fmt.Printf("Config file '%s' not found. Generating default config...\n", *configPath)

		defaultCfg := internal.GenerateDefault()
		// 尝试写入默认配置到磁盘
		if err := internal.SaveConfig(*configPath, defaultCfg); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to write default config file: %v\n", err)
			os.Exit(1)
		}

		// 提示用户修改配置后退出程序
		fmt.Printf("Default config generated!\nPlease edit '%s' with valid Tokens/API Keys and restart the program.\n", *configPath)
		os.Exit(0)
	}

	// 加载并解析配置文件
	cfg, err := internal.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// 验证配置文件的合法性（如模式匹配等）
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Invalid config: %v\n", err)
		os.Exit(1)
	}

	// 确保数据目录存在
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating data dir: %v\n", err)
		os.Exit(1)
	}

	// 根据配置初始化日志系统
	logger, err := internal.NewLoggerFromConfig(cfg.LogLevel, cfg.GetLogsDir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing logger: %v\n", err)
		os.Exit(1)
	}
	// 设置全局日志单例
	internal.SetGlobal(logger)

	// 初始化核心应用逻辑
	coreInst, err := internal.NewCore(&internal.CoreConfig{
		AppConfig: cfg,
	})
	if err != nil {
		internal.Error("main", "Failed to initialize core", map[string]interface{}{
			"error": err.Error(),
		})
		os.Exit(1)
	}

	// 此处应进行具体的平台注册逻辑，通常根据配置动态注册
	// 示例: if cfg.Platforms["discord"].Enabled { coreInst.Register(...) }

	// 创建带有取消功能的上下文，用于控制生命周期
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 启动核心服务及所有已注册的平台
	if err := coreInst.Start(ctx); err != nil {
		internal.Error("main", "Startup failed", map[string]interface{}{
			"error": err.Error(),
		})
		os.Exit(1)
	}

	internal.Info("main", "Relify is running. Press Ctrl+C to stop.", nil)

	// 创建信号通道，监听中断信号 (Ctrl+C) 和终止信号
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// 阻塞直到收到信号
	<-sigChan

	internal.Info("main", "Shutting down...", nil)

	// 创建一个新的带超时的上下文用于优雅关闭
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()

	// 执行核心组件的停止逻辑
	if err := coreInst.Stop(shutdownCtx); err != nil {
		internal.Error("main", "Shutdown error", map[string]interface{}{
			"error": err.Error(),
		})
		os.Exit(1)
	}

	internal.Info("main", "Shutdown complete", nil)
}
