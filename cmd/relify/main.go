package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"Relify/internal"
	"Relify/internal/adapter"
)

func main() {
	cfgPath := flag.String("config", "config.yaml", "Path to configuration file")
	flag.Parse()

	if _, err := os.Stat(*cfgPath); os.IsNotExist(err) {
		if err := internal.SaveConfig(*cfgPath, internal.GenerateDefault()); err != nil {
			panic(fmt.Sprintf("Failed to generate default config: %v", err))
		}
		fmt.Printf("Default config generated at %s. Please edit and restart.\n", *cfgPath)
		return
	}

	cfg, err := internal.LoadConfig(*cfgPath)
	if err != nil {
		panic(fmt.Sprintf("Config load failed: %v", err))
	}
	if err := cfg.Validate(); err != nil {
		panic(fmt.Sprintf("Config validation failed: %v", err))
	}

	core, err := internal.NewCore(cfg)
	if err != nil {
		panic(fmt.Sprintf("Core init failed: %v", err))
	}

	if matCfg, ok := cfg.Platforms["matrix"]; ok && matCfg.Enabled {
		matAdapter, err := adapter.NewMatrixAdapter(matCfg.Config, core.Router)
		if err != nil {
			panic(fmt.Sprintf("Failed to create matrix adapter: %v", err))
		}
		core.RegisterPlatform(matAdapter)
	}

	ctx, cancel := context.WithCancel(context.Background())
	if err := core.Start(ctx); err != nil {
		fmt.Printf("Core start failed: %v\n", err)
		cancel()
		os.Exit(1)
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	sig := <-sigChan
	slog.Info("received signal, shutting down...", "signal", sig)
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := core.Stop(shutdownCtx); err != nil {
		slog.Error("shutdown error", "err", err)
	} else {
		slog.Info("shutdown complete")
	}
}
