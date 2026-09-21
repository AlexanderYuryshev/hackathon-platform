package scoring

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hackaton-platform/backend/internal/audit"
	"github.com/hackaton-platform/backend/internal/auth"
	"github.com/hackaton-platform/backend/internal/httpx"
	"github.com/hackaton-platform/backend/internal/tracks"
)

func mapRunError(err error) error {
	if errors.Is(err, ErrScoringLocked) {
		return httpx.NewStatusError(http.StatusConflict, "scoring_locked", err.Error(), nil)
	}
	if errors.Is(err, ErrAlreadyFinalized) {
		return httpx.NewStatusError(http.StatusConflict, "conflict", err.Error(), nil)
	}
	return httpx.NewStatusError(http.StatusInternalServerError, "scoring_failed", err.Error(), err)
}

type Handler struct {
	pool   *pgxpool.Pool
	runner *Runner
}

func NewHandler(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool, runner: NewRunner(pool)}
}

func (h *Handler) Recalculate(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	hackathonID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid hackathon id")
		return
	}
	summary, err := h.runTrack(r, hackathonID, r.URL.Query().Get("track"), "live")
	if err != nil {
		httpx.WriteErr(w, mapRunError(err))
		return
	}
	audit.Record(r.Context(), h.pool, audit.Event{
		HackathonID: &hackathonID,
		ActorID:     &user.ID,
		ActorRole:   "organizer",
		Action:      "scoring.recalculate",
		EntityType:  "score_run",
		EntityID:    summary.RunID.String(),
		Payload:     summary,
	})
	httpx.WriteJSON(w, http.StatusOK, summary)
}

func (h *Handler) runTrack(r *http.Request, hackathonID uuid.UUID, trackKey, mode string) (*RunSummary, error) {
	_, track, err := tracks.ResolveTrackID(r.Context(), h.pool, hackathonID, trackKey)
	if err != nil {
		return nil, err
	}
	return h.runner.Run(r.Context(), hackathonID, track.ID, mode)
}

func (h *Handler) Finalize(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	hackathonID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid hackathon id")
		return
	}

	var status string
	if err := h.pool.QueryRow(r.Context(),
		`SELECT status FROM hackathons WHERE id = $1`, hackathonID).Scan(&status); err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusNotFound, "not_found", "hackathon not found", err))
		return
	}
	if status == "finalized" {
		httpx.WriteError(w, http.StatusConflict, "conflict", "hackathon is already finalized")
		return
	}

	// Serialize whole-hackathon finalization and flip the status atomically
	// with the per-track final runs: a second finalize (or a straggler live
	// run, which aborts on finalized status inside its own transaction) cannot
	// interleave with or overwrite the immutable results.
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to start tx", err))
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := tx.Exec(r.Context(),
		`SELECT pg_advisory_xact_lock(hashtext($1))`, "finalize:"+hackathonID.String()); err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to lock", err))
		return
	}
	var locked string
	if err := tx.QueryRow(r.Context(),
		`SELECT status FROM hackathons WHERE id = $1`, hackathonID).Scan(&locked); err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusNotFound, "not_found", "hackathon not found", err))
		return
	}
	if locked == "finalized" {
		httpx.WriteError(w, http.StatusConflict, "conflict", "hackathon is already finalized")
		return
	}

	// Finalize is hackathon-wide: every track gets a final-mode run on the
	// full metric set, then the hackathon flips to finalized and all tracks lock.
	trackRows, err := tx.Query(r.Context(),
		`SELECT id FROM tracks WHERE hackathon_id = $1 ORDER BY sort_order, created_at`, hackathonID)
	if err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to load tracks", err))
		return
	}
	var trackIDs []uuid.UUID
	for trackRows.Next() {
		var tid uuid.UUID
		if err := trackRows.Scan(&tid); err == nil {
			trackIDs = append(trackIDs, tid)
		}
	}
	trackRows.Close()
	if err := trackRows.Err(); err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to load tracks", err))
		return
	}

	summaries := make([]*RunSummary, 0, len(trackIDs))
	for _, tid := range trackIDs {
		summary, err := h.runner.Run(r.Context(), hackathonID, tid, "final")
		if err != nil {
			httpx.WriteErr(w, mapRunError(err))
			return
		}
		summaries = append(summaries, summary)
	}

	tag, err := tx.Exec(r.Context(), `
		UPDATE hackathons SET status = 'finalized', finalized_at = now()
		WHERE id = $1 AND status <> 'finalized'`, hackathonID)
	if err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to finalize hackathon", err))
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.WriteError(w, http.StatusConflict, "conflict", "hackathon is already finalized")
		return
	}
	if _, err := tx.Exec(r.Context(),
		`UPDATE tracks SET scoring_locked = true WHERE hackathon_id = $1`, hackathonID); err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to lock tracks", err))
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to commit", err))
		return
	}

	for _, summary := range summaries {
		audit.Record(r.Context(), h.pool, audit.Event{
			HackathonID: &hackathonID,
			ActorID:     &user.ID,
			ActorRole:   "organizer",
			Action:      "hackathon.finalized",
			EntityType:  "score_run",
			EntityID:    summary.RunID.String(),
			Payload:     summary,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"runs": summaries})
}

func (h *Handler) ListRuns(w http.ResponseWriter, r *http.Request) {
	hackathonID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid hackathon id")
		return
	}
	trackFilter := strings.TrimSpace(r.URL.Query().Get("track"))
	rows, err := h.pool.Query(r.Context(), `
		SELECT sr.id, sr.version, sr.config_hash, sr.mode, sr.status, sr.error, sr.started_at, sr.completed_at,
			tr.key,
			(SELECT count(*) FROM team_scores ts WHERE ts.score_run_id = sr.id)
		FROM score_runs sr
		JOIN tracks tr ON tr.id = sr.track_id
		WHERE sr.hackathon_id = $1 AND ($2 = '' OR tr.key = $2)
		ORDER BY sr.started_at DESC
		LIMIT 50`, hackathonID, trackFilter)
	if err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to list runs", err))
		return
	}
	defer rows.Close()

	type runRow struct {
		ID          uuid.UUID  `json:"id"`
		Version     int        `json:"version"`
		ConfigHash  string     `json:"config_hash"`
		Mode        string     `json:"mode"`
		Status      string     `json:"status"`
		Error       *string    `json:"error"`
		StartedAt   *time.Time `json:"started_at"`
		CompletedAt *time.Time `json:"completed_at"`
		TrackKey    string     `json:"track_key"`
		TeamsScored int        `json:"teams_scored"`
	}
	runs := []runRow{}
	for rows.Next() {
		var run runRow
		var teams int64
		if err := rows.Scan(&run.ID, &run.Version, &run.ConfigHash, &run.Mode, &run.Status, &run.Error, &run.StartedAt, &run.CompletedAt, &run.TrackKey, &teams); err != nil {
			httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to scan run", err))
			return
		}
		run.TeamsScored = int(teams)
		runs = append(runs, run)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (h *Handler) SetScoringLock(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	hackathonID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid hackathon id")
		return
	}
	var req struct {
		Locked bool   `json:"locked"`
		Reason string `json:"reason"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	var status string
	if err := h.pool.QueryRow(r.Context(),
		`SELECT status FROM hackathons WHERE id = $1`, hackathonID).Scan(&status); err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusNotFound, "not_found", "hackathon not found", err))
		return
	}
	if status == "finalized" {
		httpx.WriteError(w, http.StatusConflict, "conflict", "hackathon is finalized")
		return
	}
	trackID, track, err := tracks.ResolveTrackID(r.Context(), h.pool, hackathonID, r.URL.Query().Get("track"))
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	action := "scoring.unlocked"
	if req.Locked {
		action = "scoring.locked"
	}
	if _, err := h.pool.Exec(r.Context(),
		`UPDATE tracks SET scoring_locked = $2 WHERE id = $1`, trackID, req.Locked); err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to update lock", err))
		return
	}
	audit.Record(r.Context(), h.pool, audit.Event{
		HackathonID: &hackathonID,
		ActorID:     &user.ID,
		ActorRole:   "organizer",
		Action:      action,
		EntityType:  "track",
		EntityID:    trackID.String(),
		Reason:      req.Reason,
		Payload:     map[string]string{"track": track.Key},
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"locked": req.Locked})
}
