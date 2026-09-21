package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hackaton-platform/backend/internal/config"
	"github.com/hackaton-platform/backend/internal/db"
	"github.com/hackaton-platform/backend/internal/scoring"
)

func main() {
	cfg := config.Load()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("db connect failed", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	interval := 5 * time.Minute
	if v := os.Getenv("RECALC_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			interval = d
		}
	}

	worker := scoring.NewWorker(pool)
	worker.Start(ctx, interval)

	slog.Info("worker started", "interval", interval.String())
	<-ctx.Done()
	slog.Info("worker stopped")
}
