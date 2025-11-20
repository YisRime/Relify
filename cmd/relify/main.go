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
	shutdownTimeout = 10 * time.Second
)

func main() {
	configPath := flag.String("config", "config.yaml", "Path to the configuration file")
	flag.Parse()

	if _, err := os.Stat(*configPath); os.IsNotExist(err) {
		fmt.Printf("Config file '%s' not found. Generating default...\n", *configPath)
		defaultCfg := internal.GenerateDefault()
		if err := internal.SaveConfig(*configPath, defaultCfg); err != nil {
			fatal("Failed to generate default config: %v", err)
		}
		fmt.Printf("Config generated. Please edit '%s' and restart.\n", *configPath)
		os.Exit(0)
	}

	cfg, err := internal.LoadConfig(*configPath)
	if err != nil {
		fatal("Failed to load config: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		fatal("Invalid config: %v", err)
	}

	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		fatal("Failed to create data dir: %v", err)
	}

	logger, err := internal.NewLoggerFromConfig(cfg.LogLevel, cfg.GetLogsDir())
	if err != nil {
		fatal("Failed to init logger: %v", err)
	}
	internal.SetGlobal(logger)
	defer logger.Close()

	core, err := internal.NewCore(&internal.CoreConfig{AppConfig: cfg})
	if err != nil {
		logger.Log(internal.ErrorLevel, "main", "Core initialization failed", map[string]interface{}{"err": err.Error()})
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := core.Start(ctx); err != nil {
		logger.Log(internal.ErrorLevel, "main", "Startup failed", map[string]interface{}{"err": err.Error()})
		os.Exit(1)
	}

	logger.Log(internal.InfoLevel, "main", "Relify is running", map[string]interface{}{"pid": os.Getpid()})

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	sig := <-sigChan
	logger.Log(internal.InfoLevel, "main", "Signal received, shutting down...", map[string]interface{}{"signal": sig.String()})

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()

	if err := core.Stop(shutdownCtx); err != nil {
		logger.Log(internal.ErrorLevel, "main", "Shutdown completed with errors", map[string]interface{}{"err": err.Error()})
		os.Exit(1)
	}

	logger.Log(internal.InfoLevel, "main", "Shutdown successful", nil)
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "[FATAL] "+format+"\n", args...)
	os.Exit(1)
}
