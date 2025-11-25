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
	"Relify/internal/adapter/matrix"
	"Relify/internal/adapter/qq"
)

// main 是 Relify 应用程序的入口函数
// 负责初始化配置、启动核心服务以及各平台适配器
func main() {
	// 创建数据目录用于存储配置文件和数据库
	if err := os.MkdirAll("data", 0755); err != nil {
		fmt.Fprintf(os.Stderr, "创建数据目录失败: %v\n", err)
		os.Exit(1)
	}

	// 配置文件路径
	path := filepath.Join("data", "config.yaml")

	// 如果配置文件不存在，则生成默认配置并退出
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := internal.Save(path, internal.Default()); err != nil {
			fmt.Fprintf(os.Stderr, "保存默认配置失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("已生成默认配置文件:", path)
		return
	}

	// 加载配置文件
	cfg, err := internal.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		os.Exit(1)
	}

	// 检查配置文件的有效性
	if err := cfg.Check(); err != nil {
		fmt.Fprintf(os.Stderr, "配置检查失败: %v\n", err)
		os.Exit(1)
	}

	// 初始化日志系统
	logDir := filepath.Join("data", "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "创建日志目录失败: %v\n", err)
		os.Exit(1)
	}
	logFile := filepath.Join(logDir, fmt.Sprintf("relify_%s.log", time.Now().Format("2006-01-02_15-04-05")))
	file, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		fmt.Fprintf(os.Stderr, "打开日志文件失败: %v\n", err)
		os.Exit(1)
	}
	defer file.Close()

	// 解析日志级别
	var logLevel slog.Level
	switch cfg.Level {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	// 设置日志处理器（同时输出到控制台和文件）
	handler := slog.NewJSONHandler(io.MultiWriter(os.Stdout, file), &slog.HandlerOptions{
		Level: logLevel,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{Key: a.Key, Value: slog.StringValue(a.Value.Time().Format("2006-01-02 15:04:05.000"))}
			}
			return a
		},
	})
	slog.SetDefault(slog.New(handler))

	slog.Info("Relify 启动中", "mode", cfg.Mode, "hub", cfg.Hub)

	// 初始化核心服务，包括路由器和数据库
	app, err := internal.NewCore(cfg)
	if err != nil {
		slog.Error("初始化核心失败", "error", err)
		os.Exit(1)
	}

	// 如果启用了 Matrix 平台，则创建并添加 Matrix 适配器
	if mc, ok := cfg.Plats["matrix"]; ok && mc.Enabled {
		if ma, err := matrix.NewMatrix(mc.Cfg, app.Router); err == nil {
			app.Add(ma)
			slog.Info("Matrix 适配器已加载")
		} else {
			slog.Warn("Matrix 适配器加载失败", "error", err)
		}
	}

	// 如果启用了 QQ 平台，则创建并添加 QQ 适配器
	if qc, ok := cfg.Plats["qq"]; ok && qc.Enabled {
		if qa, err := qq.NewQQ(qc.Cfg, app.Router); err == nil {
			app.Add(qa)
			slog.Info("QQ 适配器已加载")
		} else {
			slog.Warn("QQ 适配器加载失败", "error", err)
		}
	}

	// 创建可取消的上下文用于优雅关闭
	ctx, cancel := context.WithCancel(context.Background())

	// 启动所有已注册的平台适配器
	if err := app.Start(ctx); err != nil {
		slog.Error("启动应用失败", "error", err)
		cancel()
		os.Exit(1)
	}

	slog.Info("Relify 已启动，等待事件...")

	// 监听系统信号以实现优雅关闭
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	<-sig // 等待信号

	slog.Info("收到关闭信号，正在停止...")
	cancel() // 取消上下文

	// 设置超时上下文用于停止所有服务
	stopCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()

	if err := app.Stop(stopCtx); err != nil {
		slog.Error("停止应用时出错", "error", err)
	} else {
		slog.Info("Relify 已停止")
	}
}
