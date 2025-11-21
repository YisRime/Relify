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
)

func main() {
	cfgPath := flag.String("config", "config.yaml", "Config file path")
	flag.Parse()

	if _, err := os.Stat(*cfgPath); os.IsNotExist(err) {
		internal.SaveConfig(*cfgPath, internal.GenerateDefault())
		fmt.Println("Config generated. Please edit and restart.")
		return
	}

	cfg, err := internal.LoadConfig(*cfgPath)
	if err != nil {
		panic(err)
	}

	core, err := internal.NewCore(cfg)
	if err != nil {
		panic(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	if err := core.Start(ctx); err != nil {
		fmt.Printf("Start error: %v\n", err)
		os.Exit(1)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig

	slog.Info("shutting down...")
	cancel()

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	core.Stop(stopCtx)
}
