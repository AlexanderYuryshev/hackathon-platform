package leaderboard

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/go-chi/chi/v5"

	"github.com/hackaton-platform/backend/internal/httpx"
	"github.com/hackaton-platform/backend/internal/tracks"
)

type Hub struct {
	pool *pgxpool.Pool
	mu   sync.Mutex
	// Subscriptions are keyed by "hackathonID:trackID": each track has its
	// own leaderboard stream.
	subs map[string]map[chan hubEvent]struct{}
	checkBlind func(http.ResponseWriter, *http.Request, uuid.UUID) error
}

type hubEvent struct {
	Type    string         `json:"type"`
	Payload map[string]any `json:"payload"`
}

func NewHub(pool *pgxpool.Pool) *Hub {
	return &Hub{
		pool: pool,
		subs: map[string]map[chan hubEvent]struct{}{},
	}
}

func (h *Hub) SetBlindnessCheck(fn func(http.ResponseWriter, *http.Request, uuid.UUID) error) {
	h.checkBlind = fn
}

func (h *Hub) Start(ctx context.Context) {
	go h.listen(ctx)
}

func subKey(hackathonID, trackID uuid.UUID) string {
	return hackathonID.String() + ":" + trackID.String()
}

func (h *Hub) subscribe(hackathonID, trackID uuid.UUID) chan hubEvent {
	ch := make(chan hubEvent, 16)
	key := subKey(hackathonID, trackID)
	h.mu.Lock()
	if h.subs[key] == nil {
		h.subs[key] = map[chan hubEvent]struct{}{}
	}
	h.subs[key][ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *Hub) unsubscribe(hackathonID, trackID uuid.UUID, ch chan hubEvent) {
	key := subKey(hackathonID, trackID)
	h.mu.Lock()
	if m, ok := h.subs[key]; ok {
		delete(m, ch)
		if len(m) == 0 {
			delete(h.subs, key)
		}
	}
	h.mu.Unlock()
}

func (h *Hub) broadcast(hackathonID, trackID uuid.UUID, ev hubEvent) {
	key := subKey(hackathonID, trackID)
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[key] {
		select {
		case ch <- ev:
		default:
		}
	}
}

func (h *Hub) listen(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		if err := h.listenOnce(ctx); err != nil && ctx.Err() == nil {
			slog.Error("hub: listen failed", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
		}
	}
}

func (h *Hub) listenOnce(ctx context.Context) error {
	conn, err := h.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), "UNLISTEN score_updated")
		conn.Release()
	}()

	if _, err := conn.Exec(ctx, "LISTEN score_updated"); err != nil {
		return err
	}
	slog.Info("hub: listening for score updates")
	for {
		notification, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		var payload struct {
			HackathonID string `json:"hackathon_id"`
			TrackID     string `json:"track_id"`
			RunID       string `json:"run_id"`
			Mode        string `json:"mode"`
			Status      string `json:"status"`
		}
		if err := json.Unmarshal([]byte(notification.Payload), &payload); err != nil {
			continue
		}
		hackathonID, err := uuid.Parse(payload.HackathonID)
		if err != nil {
			continue
		}
		trackID, err := uuid.Parse(payload.TrackID)
		if err != nil {
			continue
		}
		data := map[string]any{
			"hackathon_id": payload.HackathonID,
			"track_id":     payload.TrackID,
			"run_id":       payload.RunID,
			"mode":         payload.Mode,
		}
		switch payload.Status {
		case "error":
			h.broadcast(hackathonID, trackID, hubEvent{Type: "scoring.error", Payload: data})
		default:
			h.broadcast(hackathonID, trackID, hubEvent{Type: "scoring.completed", Payload: data})
			h.broadcast(hackathonID, trackID, hubEvent{Type: "leaderboard.updated", Payload: data})
			h.broadcast(hackathonID, trackID, hubEvent{Type: "team.updated", Payload: data})
		}
	}
}

const heartbeatInterval = 15 * time.Second

func (h *Hub) Events(w http.ResponseWriter, r *http.Request) {
	hackathonID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid hackathon id")
		return
	}
	if h.checkBlind != nil {
		if err := h.checkBlind(w, r, hackathonID); err != nil {
			return
		}
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		httpx.WriteError(w, http.StatusInternalServerError, "streaming_unsupported", "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	trackID, _, err := tracks.ResolveTrackID(r.Context(), h.pool, hackathonID, r.URL.Query().Get("track"))
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}

	ch := h.subscribe(hackathonID, trackID)
	defer h.unsubscribe(hackathonID, trackID, ch)

	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()

	writeEvent := func(ev hubEvent) bool {
		data, err := json.Marshal(ev)
		if err != nil {
			return true
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, data); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	connected := hubEvent{Type: "heartbeat", Payload: map[string]any{"ts": time.Now().UTC()}}
	if !writeEvent(connected) {
		return
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if !writeEvent(hubEvent{Type: "heartbeat", Payload: map[string]any{"ts": time.Now().UTC()}}) {
				return
			}
		case ev := <-ch:
			if !writeEvent(ev) {
				return
			}
		}
	}
}
