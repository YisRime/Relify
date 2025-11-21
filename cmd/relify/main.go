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
	cfgPath := flag.String("config", "config.yaml", "Path to config")
	flag.Parse()

	if _, err := os.Stat(*cfgPath); os.IsNotExist(err) {
		if err := internal.SaveConfig(*cfgPath, internal.GenerateDefault()); err != nil {
			fmt.Printf("Failed to generate config: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Configuration file generated: %s\n", *cfgPath)
		fmt.Println("After configuration, run the program again to start.")
		return
	}

	cfg, err := internal.LoadConfig(*cfgPath)
	if err != nil {
		panic(err)
	}
	if err := cfg.Validate(); err != nil {
		panic(err)
	}

	core, err := internal.NewCore(cfg)
	if err != nil {
		panic(err)
	}

	if mc, ok := cfg.Platforms["matrix"]; ok && mc.Enabled {
		slog.Debug("initializing matrix adapter")
		if ma, err := adapter.NewMatrixAdapter(mc.Config, core.Router); err == nil {
			core.RegisterPlatform(ma)
		} else {
			slog.Error("failed to init matrix", "err", err)
		}
	}

	if qc, ok := cfg.Platforms["qq"]; ok && qc.Enabled {
		slog.Debug("initializing qq adapter")
		if qa, err := adapter.NewQQAdapter(qc.Config, core.Router); err == nil {
			core.RegisterPlatform(qa)
		} else {
			slog.Error("failed to init qq", "err", err)
		}
	}

	slog.Debug("starting core", "platforms", len(core.Registry.All()))
	ctx, cancel := context.WithCancel(context.Background())
	if err := core.Start(ctx); err != nil {
		fmt.Println(err)
		cancel()
		os.Exit(1)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig

	slog.Info("stopping...")
	cancel()

	shutdownCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()

	if err := core.Stop(shutdownCtx); err != nil {
		slog.Error("shutdown failed", "err", err)
	}
}
