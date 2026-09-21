package tracks

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hackaton-platform/backend/internal/audit"
	"github.com/hackaton-platform/backend/internal/auth"
	"github.com/hackaton-platform/backend/internal/httpx"
)

type Handler struct {
	pool *pgxpool.Pool
}

func NewHandler(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool}
}

type Track struct {
	ID               uuid.UUID `json:"id"`
	HackathonID      uuid.UUID `json:"hackathon_id"`
	Key              string    `json:"key"`
	Name             string    `json:"name"`
	Description      string    `json:"description"`
	MinJudgesPerTeam int       `json:"min_judges_per_team"`
	ScoringVersion   int       `json:"scoring_version"`
	ScoringLocked    bool      `json:"scoring_locked"`
	SortOrder        int       `json:"sort_order"`
	TeamCount        int       `json:"team_count"`
	CreatedAt        time.Time `json:"created_at"`
}

var keyRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9_-]{0,62}[a-z0-9])?$`)

// ValidKey reports whether a track key is well-formed.
func ValidKey(key string) bool {
	return keyRe.MatchString(key)
}

// ResolveTrackID maps a ?track=<key> parameter (or empty for the default
// track) to a track of the hackathon. Every track-scoped endpoint uses it,
// so single-track hackathons keep working with no parameter at all.
func ResolveTrackID(ctx context.Context, pool *pgxpool.Pool, hackathonID uuid.UUID, key string) (uuid.UUID, *Track, error) {
	var t Track
	var q string
	var args []any
	if strings.TrimSpace(key) == "" {
		q = `SELECT id, hackathon_id, key, name, description, min_judges_per_team,
				scoring_version, scoring_locked, sort_order, created_at
			FROM tracks WHERE hackathon_id = $1 ORDER BY sort_order, created_at LIMIT 1`
		args = []any{hackathonID}
	} else {
		q = `SELECT id, hackathon_id, key, name, description, min_judges_per_team,
				scoring_version, scoring_locked, sort_order, created_at
			FROM tracks WHERE hackathon_id = $1 AND key = $2`
		args = []any{hackathonID, strings.ToLower(strings.TrimSpace(key))}
	}
	err := pool.QueryRow(ctx, q, args...).Scan(
		&t.ID, &t.HackathonID, &t.Key, &t.Name, &t.Description, &t.MinJudgesPerTeam,
		&t.ScoringVersion, &t.ScoringLocked, &t.SortOrder, &t.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			if strings.TrimSpace(key) == "" {
				return uuid.Nil, nil, httpx.NewStatusError(http.StatusNotFound, "not_found", "hackathon has no tracks", err)
			}
			return uuid.Nil, nil, httpx.NewStatusError(http.StatusNotFound, "not_found", "unknown track key", err)
		}
		return uuid.Nil, nil, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to resolve track", err)
	}
	return t.ID, &t, nil
}

func SlugifyKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '_' || r == '-':
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	return out
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	hackathonID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid hackathon id")
		return
	}
	rows, err := h.pool.Query(r.Context(), `
		SELECT tr.id, tr.hackathon_id, tr.key, tr.name, tr.description, tr.min_judges_per_team,
			tr.scoring_version, tr.scoring_locked, tr.sort_order, tr.created_at,
			(SELECT count(*) FROM teams t WHERE t.track_id = tr.id)
		FROM tracks tr WHERE tr.hackathon_id = $1 ORDER BY tr.sort_order, tr.created_at`, hackathonID)
	if err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to list tracks", err))
		return
	}
	defer rows.Close()
	out := []Track{}
	for rows.Next() {
		var t Track
		var cnt int64
		if err := rows.Scan(&t.ID, &t.HackathonID, &t.Key, &t.Name, &t.Description,
			&t.MinJudgesPerTeam, &t.ScoringVersion, &t.ScoringLocked, &t.SortOrder, &t.CreatedAt, &cnt); err != nil {
			httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to scan track", err))
			return
		}
		t.TeamCount = int(cnt)
		out = append(out, t)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"tracks": out})
}

type CreateRequest struct {
	Key              string `json:"key"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	MinJudgesPerTeam *int   `json:"min_judges_per_team"`
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	hackathonID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid hackathon id")
		return
	}
	var req CreateRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "track name is required")
		return
	}
	key := SlugifyKey(req.Key)
	if key == "" {
		key = SlugifyKey(req.Name)
	}
	if !ValidKey(key) {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "track key must be 1-64 chars: lowercase letters, digits, hyphens, underscores")
		return
	}
	minJudges := 3
	if req.MinJudgesPerTeam != nil {
		if *req.MinJudgesPerTeam < 1 {
			httpx.WriteError(w, http.StatusBadRequest, "bad_request", "min_judges_per_team must be at least 1")
			return
		}
		minJudges = *req.MinJudgesPerTeam
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

	var t Track
	err = h.pool.QueryRow(r.Context(), `
		INSERT INTO tracks (hackathon_id, key, name, description, min_judges_per_team, sort_order)
		SELECT $1, $2, $3, $4, $5, COALESCE(max(sort_order), -1) + 1 FROM tracks WHERE hackathon_id = $1
		RETURNING id, hackathon_id, key, name, description, min_judges_per_team,
			scoring_version, scoring_locked, sort_order, created_at`,
		hackathonID, key, strings.TrimSpace(req.Name), req.Description, minJudges,
	).Scan(&t.ID, &t.HackathonID, &t.Key, &t.Name, &t.Description,
		&t.MinJudgesPerTeam, &t.ScoringVersion, &t.ScoringLocked, &t.SortOrder, &t.CreatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			httpx.WriteError(w, http.StatusConflict, "conflict", "track key already exists")
			return
		}
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to create track", err))
		return
	}
	audit.Record(r.Context(), h.pool, audit.Event{
		HackathonID: &hackathonID,
		ActorID:     &user.ID,
		ActorRole:   "organizer",
		Action:      "track.created",
		EntityType:  "track",
		EntityID:    t.ID.String(),
		Payload:     map[string]any{"key": t.Key, "min_judges": t.MinJudgesPerTeam},
	})
	httpx.WriteJSON(w, http.StatusCreated, t)
}

type UpdateRequest struct {
	Name             *string `json:"name"`
	MinJudgesPerTeam *int    `json:"min_judges_per_team"`
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	hackathonID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid hackathon id")
		return
	}
	trackID, err := uuid.Parse(chi.URLParam(r, "trackId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid track id")
		return
	}
	var req UpdateRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if req.Name == nil && req.MinJudgesPerTeam == nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "nothing to update")
		return
	}

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to start tx", err))
		return
	}
	defer tx.Rollback(r.Context())

	var locked bool
	var hkStatus string
	if err := tx.QueryRow(r.Context(), `
		SELECT tr.scoring_locked, h.status FROM tracks tr
		JOIN hackathons h ON h.id = tr.hackathon_id
		WHERE tr.id = $1 AND tr.hackathon_id = $2`, trackID, hackathonID,
	).Scan(&locked, &hkStatus); err != nil {
		if err == pgx.ErrNoRows {
			httpx.WriteError(w, http.StatusNotFound, "not_found", "track not found in this hackathon")
			return
		}
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to load track", err))
		return
	}
	if hkStatus == "finalized" {
		httpx.WriteError(w, http.StatusConflict, "conflict", "hackathon is finalized")
		return
	}
	if locked {
		httpx.WriteError(w, http.StatusConflict, "scoring_locked", "track scoring is locked")
		return
	}

	versionBump := false
	if req.Name != nil {
		if strings.TrimSpace(*req.Name) == "" {
			httpx.WriteError(w, http.StatusBadRequest, "bad_request", "track name is required")
			return
		}
		if _, err := tx.Exec(r.Context(), `UPDATE tracks SET name = $2 WHERE id = $1`, trackID, strings.TrimSpace(*req.Name)); err != nil {
			httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to rename track", err))
			return
		}
	}
	if req.MinJudgesPerTeam != nil {
		if *req.MinJudgesPerTeam < 1 {
			httpx.WriteError(w, http.StatusBadRequest, "bad_request", "min_judges_per_team must be at least 1")
			return
		}
		if _, err := tx.Exec(r.Context(),
			`UPDATE tracks SET min_judges_per_team = $2, scoring_version = scoring_version + 1 WHERE id = $1`,
			trackID, *req.MinJudgesPerTeam); err != nil {
			httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to update track", err))
			return
		}
		versionBump = true
	}
	if err := tx.Commit(r.Context()); err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to commit", err))
		return
	}
	payload := map[string]any{}
	if req.Name != nil {
		payload["name"] = strings.TrimSpace(*req.Name)
	}
	if req.MinJudgesPerTeam != nil {
		payload["min_judges"] = *req.MinJudgesPerTeam
		payload["version_bump"] = versionBump
	}
	audit.Record(r.Context(), h.pool, audit.Event{
		HackathonID: &hackathonID,
		ActorID:     &user.ID,
		ActorRole:   "organizer",
		Action:      "track.updated",
		EntityType:  "track",
		EntityID:    trackID.String(),
		Payload:     payload,
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
