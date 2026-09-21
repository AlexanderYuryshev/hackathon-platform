package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hackaton-platform/backend/internal/config"
)

type teamMetric struct {
	HackathonID    uuid.UUID
	TeamID         uuid.UUID
	MetricID       uuid.UUID
	MetricKey      string
	Normalization  map[string]any
}

func main() {
	cfg := config.Load()
	apiURL := envOr("SIM_API_URL", "http://localhost:8080")
	token := os.Getenv("SIM_TOKEN")
	interval := 30 * time.Second
	if v := os.Getenv("SIM_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			interval = d
		}
	}
	if token == "" {
		fmt.Fprintln(os.Stderr, "SIM_TOKEN is required")
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("db connect failed", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	client := &http.Client{Timeout: 10 * time.Second}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	slog.Info("simulator started", "interval", interval.String())
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	run := func() {
		items, err := loadTargets(ctx, pool)
		if err != nil {
			slog.Error("load targets failed", "err", err)
			return
		}
		sent := 0
		for _, tm := range items {
			if skipMetric(rng, tm) {
				continue
			}
			value := generateValue(rng, tm)
			if err := push(client, apiURL, token, tm, value); err != nil {
				slog.Error("push failed", "metric", tm.MetricKey, "err", err)
				continue
			}
			sent++
		}
		slog.Info("tick done", "targets", len(items), "sent", sent)
	}

	run()
	for {
		select {
		case <-ctx.Done():
			slog.Info("simulator stopped")
			return
		case <-ticker.C:
			run()
		}
	}
}

func loadTargets(ctx context.Context, pool *pgxpool.Pool) ([]teamMetric, error) {
	rows, err := pool.Query(ctx, `
		SELECT t.hackathon_id, t.id, m.id, m.key, m.normalization
		FROM teams t
		JOIN metric_definitions m ON m.hackathon_id = t.hackathon_id AND m.type = 'automated'
		ORDER BY t.id, m.key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []teamMetric{}
	for rows.Next() {
		var tm teamMetric
		if err := rows.Scan(&tm.HackathonID, &tm.TeamID, &tm.MetricID, &tm.MetricKey, &tm.Normalization); err != nil {
			return nil, err
		}
		items = append(items, tm)
	}
	return items, rows.Err()
}

func skipMetric(rng *rand.Rand, tm teamMetric) bool {
	if rng.Float64() < 0.03 {
		return true
	}
	return false
}

func generateValue(rng *rand.Rand, tm teamMetric) float64 {
	kind, _ := tm.Normalization["kind"].(string)
	switch kind {
	case "range":
		min, _ := tm.Normalization["min"].(float64)
		max, _ := tm.Normalization["max"].(float64)
		if max <= min {
			return min
		}
		return min + rng.Float64()*(max-min)
	case "target":
		target, _ := tm.Normalization["target"].(float64)
		return target * (0.9 + rng.Float64()*0.2)
	case "binary":
		if rng.Float64() < 0.9 {
			return 1
		}
		return 0
	default:
		return rng.Float64() * 100
	}
}

func push(client *http.Client, apiURL, token string, tm teamMetric, value float64) error {
	body := map[string]any{
		"hackathon_id":     tm.HackathonID.String(),
		"team_id":          tm.TeamID.String(),
		"metric_key":       tm.MetricKey,
		"value":            value,
		"external_event_id": fmt.Sprintf("sim-%d-%s", time.Now().UnixMilli(), tm.MetricID.String()[:8]),
		"captured_at":      time.Now().UTC().Format(time.RFC3339),
		"metadata":         map[string]any{"simulator": true},
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, apiURL+"/api/v1/ingest/metrics", bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("ingest responded %d", resp.StatusCode)
	}
	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
