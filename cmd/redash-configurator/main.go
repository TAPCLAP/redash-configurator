package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"redash-configurator/internal/config"
	"redash-configurator/internal/redash"
	"redash-configurator/internal/sync"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cfg, err := config.Load()
	if err != nil {
		log.Error("invalid configuration", "err", err)
		os.Exit(1)
	}

	syncer := sync.New(redash.New(cfg.RedashURL, cfg.APIKey), cfg.Sources, log)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	for {
		err := syncer.Run(ctx)
		if ctx.Err() != nil {
			log.Info("shutting down")
			return
		}
		if err != nil {
			log.Error("sync finished with errors", "err", err)
		}
		if !sleep(ctx, cfg.SyncInterval) {
			log.Info("shutting down")
			return
		}
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
