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

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config")
	flag.Parse()

	if _, err := os.Stat(*configPath); os.IsNotExist(err) {
		internal.SaveConfig(*configPath, internal.GenerateDefault())
		fmt.Printf("Config generated at %s. Edit and restart.\n", *configPath)
		os.Exit(0)
	}

	cfg, err := internal.LoadConfig(*configPath)
	if err != nil {
		fatal("Config load failed: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		fatal("Config invalid: %v", err)
	}

	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		fatal("Data dir error: %v", err)
	}

	logger, err := internal.NewLoggerFromConfig(cfg.LogLevel, cfg.GetLogsDir())
	if err != nil {
		fatal("Logger error: %v", err)
	}
	internal.SetGlobal(logger)
	defer logger.Close()

	core, err := internal.NewCore(cfg)
	if err != nil {
		logger.Log(internal.ErrorLevel, "main", "Init failed", map[string]interface{}{"err": err.Error()})
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := core.Start(ctx); err != nil {
		logger.Log(internal.ErrorLevel, "main", "Start failed", map[string]interface{}{"err": err.Error()})
		os.Exit(1)
	}

	logger.Log(internal.InfoLevel, "main", "Running", map[string]interface{}{"pid": os.Getpid()})

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	shutdownCtx, sCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer sCancel()
	core.Stop(shutdownCtx)
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "[FATAL] "+format+"\n", args...)
	os.Exit(1)
}
