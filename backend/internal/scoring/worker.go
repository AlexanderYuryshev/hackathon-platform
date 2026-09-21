package scoring

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	dirtyChannel   = "score_dirty"
	updatedChannel = "score_updated"
	debounceDelay  = 5 * time.Second
)

type Worker struct {
	pool   *pgxpool.Pool
	runner *Runner
}

func NewWorker(pool *pgxpool.Pool) *Worker {
	return &Worker{pool: pool, runner: NewRunner(pool)}
}

func (w *Worker) Start(ctx context.Context, interval time.Duration) {
	dirty := make(chan uuid.UUID, 256)
	go w.dispatch(ctx, dirty)
	go w.listenDirty(ctx, dirty)
	go w.periodicRecalc(ctx, interval)
	go w.cleanupExpired(ctx, time.Hour)
}

func (w *Worker) cleanupExpired(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := w.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now()`); err != nil {
				slog.Error("worker: session cleanup failed", "err", err)
			}
		}
	}
}

func (w *Worker) periodicRecalc(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	w.recalcActive(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.recalcActive(ctx)
		}
	}
}

func (w *Worker) recalcActive(ctx context.Context) {
	rows, err := w.pool.Query(ctx, `
		SELECT tr.hackathon_id, tr.id FROM tracks tr
		JOIN hackathons h ON h.id = tr.hackathon_id
		WHERE h.status IN ('running', 'judging') AND NOT tr.scoring_locked`)
	if err != nil {
		slog.Error("worker: load tracks failed", "err", err)
		return
	}
	defer rows.Close()
	type target struct {
		hackathon, track uuid.UUID
	}
	var targets []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.hackathon, &t.track); err == nil {
			targets = append(targets, t)
		}
	}
	if err := rows.Err(); err != nil {
		slog.Error("worker: iterate tracks failed", "err", err)
		return
	}
	for _, t := range targets {
		if _, err := w.runner.Run(ctx, t.hackathon, t.track, "live"); err != nil {
			slog.Error("worker: recalc failed", "hackathon", t.hackathon, "track", t.track, "err", err)
			continue
		}
		slog.Info("worker: recalc done", "hackathon", t.hackathon, "track", t.track)
	}
}

func (w *Worker) 	dispatch(ctx context.Context, dirty <-chan uuid.UUID) {
	pending := map[uuid.UUID]time.Time{}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case trackID := <-dirty:
			pending[trackID] = time.Now().Add(debounceDelay)
		case <-ticker.C:
			now := time.Now()
			for trackID, due := range pending {
				if due.After(now) {
					continue
				}
				delete(pending, trackID)
				go func(trackID uuid.UUID) {
					runCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
					defer cancel()
					var hackathonID uuid.UUID
					var locked bool
					var status string
					if err := w.pool.QueryRow(runCtx, `
						SELECT tr.hackathon_id, tr.scoring_locked, h.status FROM tracks tr
						JOIN hackathons h ON h.id = tr.hackathon_id
						WHERE tr.id = $1`, trackID,
					).Scan(&hackathonID, &locked, &status); err == nil && (locked || status == "finalized") {
						return
					}
					if _, err := w.runner.Run(runCtx, hackathonID, trackID, "live"); err != nil {
						if errors.Is(err, ErrScoringLocked) {
							return
						}
						slog.Error("worker: debounced recalc failed", "track", trackID, "err", err)
						return
					}
					slog.Info("worker: debounced recalc done", "track", trackID)
				}(trackID)
			}
		}
	}
}

func (w *Worker) listenDirty(ctx context.Context, dirty chan<- uuid.UUID) {
	for {
		if ctx.Err() != nil {
			return
		}
		if err := w.listenOnce(ctx, dirty); err != nil && ctx.Err() == nil {
			slog.Error("worker: listen failed", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
		}
	}
}

func (w *Worker) listenOnce(ctx context.Context, dirty chan<- uuid.UUID) error {
	conn, err := w.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), "UNLISTEN "+dirtyChannel)
		conn.Release()
	}()

	if _, err := conn.Exec(ctx, "LISTEN "+dirtyChannel); err != nil {
		return err
	}
	slog.Info("worker: listening for dirty notifications")
	for {
		notification, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		id, err := uuid.Parse(notification.Payload)
		if err != nil {
			continue
		}
		select {
		case dirty <- id:
		default:
		}
	}
}

// NotifyDirty signals that new raw data arrived for a track. The payload
// carries the track ID; the worker resolves the hackathon from it.
func NotifyDirty(ctx context.Context, pool *pgxpool.Pool, trackID uuid.UUID) {
	pool.Exec(ctx, `SELECT pg_notify($1, $2)`, dirtyChannel, trackID.String())
}
