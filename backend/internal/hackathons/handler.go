package hackathons

import (
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
	"github.com/hackaton-platform/backend/internal/tracks"
)

type Handler struct {
	pool *pgxpool.Pool
}

func NewHandler(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool}
}

type Hackathon struct {
	ID            uuid.UUID  `json:"id"`
	Slug          string     `json:"slug"`
	Name          string     `json:"name"`
	Description   string     `json:"description"`
	Status        string     `json:"status"`
	StartsAt      *time.Time `json:"starts_at"`
	EndsAt        *time.Time `json:"ends_at"`
	JudgingEndsAt *time.Time `json:"judging_ends_at"`
	FinalizedAt   *time.Time `json:"finalized_at"`
	CreatedAt     time.Time  `json:"created_at"`
}

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,62}[a-z0-9]$`)

type createRequest struct {
	Slug             string     `json:"slug"`
	Name             string     `json:"name"`
	Description      string     `json:"description"`
	StartsAt         *time.Time `json:"starts_at"`
	EndsAt           *time.Time `json:"ends_at"`
	JudgingEndsAt    *time.Time `json:"judging_ends_at"`
	MinJudgesPerTeam int        `json:"min_judges_per_team"`
	Tracks           []struct {
		Key              string `json:"key"`
		Name             string `json:"name"`
		MinJudgesPerTeam *int   `json:"min_judges_per_team"`
	} `json:"tracks"`
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	if user == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	var req createRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	req.Slug = strings.ToLower(strings.TrimSpace(req.Slug))
	if !slugRe.MatchString(req.Slug) {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "slug must be 4-64 chars: lowercase letters, digits, hyphens")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "name is required")
		return
	}
	if req.MinJudgesPerTeam < 1 {
		req.MinJudgesPerTeam = 3
	}

	// Tracks can be defined up front; otherwise a single default track is
	// created (single-track hackathons need no extra setup).
	type trackSpec struct {
		key       string
		name      string
		minJudges int
	}
	var specs []trackSpec
	if len(req.Tracks) == 0 {
		specs = []trackSpec{{key: "general", name: "Общий зачёт", minJudges: req.MinJudgesPerTeam}}
	} else {
		seen := map[string]bool{}
		for _, t := range req.Tracks {
			name := strings.TrimSpace(t.Name)
			if name == "" {
				httpx.WriteError(w, http.StatusBadRequest, "bad_request", "track name is required")
				return
			}
			key := tracks.SlugifyKey(t.Key)
			if key == "" {
				key = tracks.SlugifyKey(name)
			}
			if !tracks.ValidKey(key) {
				httpx.WriteError(w, http.StatusBadRequest, "bad_request", "track key must be 1-64 chars: lowercase letters, digits, hyphens, underscores")
				return
			}
			if seen[key] {
				httpx.WriteError(w, http.StatusBadRequest, "bad_request", "duplicate track key: "+key)
				return
			}
			seen[key] = true
			minJudges := 3
			if t.MinJudgesPerTeam != nil {
				if *t.MinJudgesPerTeam < 1 {
					httpx.WriteError(w, http.StatusBadRequest, "bad_request", "min_judges_per_team must be at least 1")
					return
				}
				minJudges = *t.MinJudgesPerTeam
			}
			specs = append(specs, trackSpec{key: key, name: name, minJudges: minJudges})
		}
	}

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to start tx", err))
		return
	}
	defer tx.Rollback(r.Context())

	var hk Hackathon
	err = tx.QueryRow(r.Context(), `
		INSERT INTO hackathons (slug, name, description, organizer_id, starts_at, ends_at, judging_ends_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, slug, name, description, status, starts_at, ends_at, judging_ends_at, finalized_at, created_at`,
		req.Slug, req.Name, req.Description, user.ID, req.StartsAt, req.EndsAt, req.JudgingEndsAt,
	).Scan(&hk.ID, &hk.Slug, &hk.Name, &hk.Description, &hk.Status, &hk.StartsAt, &hk.EndsAt, &hk.JudgingEndsAt,
		&hk.FinalizedAt, &hk.CreatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			httpx.WriteError(w, http.StatusConflict, "conflict", "slug already exists")
			return
		}
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to create hackathon", err))
		return
	}
	// Every hackathon starts with at least one track.
	for i, s := range specs {
		if _, err := tx.Exec(r.Context(), `
			INSERT INTO tracks (hackathon_id, key, name, min_judges_per_team, sort_order)
			VALUES ($1, $2, $3, $4, $5)`,
			hk.ID, s.key, s.name, s.minJudges, i); err != nil {
			if strings.Contains(err.Error(), "duplicate key") {
				httpx.WriteError(w, http.StatusConflict, "conflict", "track key already exists: "+s.key)
				return
			}
			httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to create track", err))
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to commit", err))
		return
	}

	audit.Record(r.Context(), h.pool, audit.Event{
		HackathonID: &hk.ID,
		ActorID:     &user.ID,
		ActorRole:   "organizer",
		Action:      "hackathon.created",
		EntityType:  "hackathon",
		EntityID:    hk.ID.String(),
	})
	httpx.WriteJSON(w, http.StatusCreated, hk)
}

// List returns hackathons for the index page. Drafts are visible only to their
// organizer.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	const cols = `id, slug, name, description, status, starts_at, ends_at, judging_ends_at, finalized_at, created_at`
	var rows pgx.Rows
	var err error
	if user := auth.UserFrom(r.Context()); user != nil {
		rows, err = h.pool.Query(r.Context(),
			`SELECT `+cols+` FROM hackathons WHERE status <> 'draft' OR organizer_id = $1 ORDER BY created_at DESC LIMIT 200`, user.ID)
	} else {
		rows, err = h.pool.Query(r.Context(),
			`SELECT `+cols+` FROM hackathons WHERE status <> 'draft' ORDER BY created_at DESC LIMIT 200`)
	}
	if err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to list hackathons", err))
		return
	}
	defer rows.Close()
	out := []Hackathon{}
	for rows.Next() {
		var hk Hackathon
		if err := rows.Scan(&hk.ID, &hk.Slug, &hk.Name, &hk.Description, &hk.Status, &hk.StartsAt, &hk.EndsAt, &hk.JudgingEndsAt,
			&hk.FinalizedAt, &hk.CreatedAt); err != nil {
			httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to scan hackathon", err))
			return
		}
		out = append(out, hk)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"hackathons": out})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	raw := chi.URLParam(r, "id")
	id, err := uuid.Parse(raw)
	if err != nil {
		h.getBySlug(w, r, raw)
		return
	}
	var hk Hackathon
	err = h.pool.QueryRow(r.Context(), `
		SELECT id, slug, name, description, status, starts_at, ends_at, judging_ends_at, finalized_at, created_at
		FROM hackathons WHERE id = $1`, id,
	).Scan(&hk.ID, &hk.Slug, &hk.Name, &hk.Description, &hk.Status, &hk.StartsAt, &hk.EndsAt, &hk.JudgingEndsAt,
		&hk.FinalizedAt, &hk.CreatedAt)
	if err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusNotFound, "not_found", "hackathon not found", err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, hk)
}

func (h *Handler) getBySlug(w http.ResponseWriter, r *http.Request, slug string) {
	var hk Hackathon
	err := h.pool.QueryRow(r.Context(), `
		SELECT id, slug, name, description, status, starts_at, ends_at, judging_ends_at, finalized_at, created_at
		FROM hackathons WHERE slug = $1`, slug,
	).Scan(&hk.ID, &hk.Slug, &hk.Name, &hk.Description, &hk.Status, &hk.StartsAt, &hk.EndsAt, &hk.JudgingEndsAt,
		&hk.FinalizedAt, &hk.CreatedAt)
	if err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusNotFound, "not_found", "hackathon not found", err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, hk)
}

var allowedTransitions = map[string][]string{
	"draft":     {"running"},
	"running":   {"judging"},
	"judging":   {"running"},
}

func (h *Handler) SetStatus(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	hackathonID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid hackathon id")
		return
	}
	var req struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	var current string
	if err := h.pool.QueryRow(r.Context(),
		`SELECT status FROM hackathons WHERE id = $1`, hackathonID).Scan(&current); err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusNotFound, "not_found", "hackathon not found", err))
		return
	}
	allowed := false
	for _, s := range allowedTransitions[current] {
		if s == req.Status {
			allowed = true
		}
	}
	if !allowed {
		httpx.WriteError(w, http.StatusConflict, "conflict", "transition from "+current+" to "+req.Status+" is not allowed")
		return
	}

	// Optimistic guard: the transition is only applied if the status read above
	// is still current, so concurrent status changes cannot interleave.
	tag, err := h.pool.Exec(r.Context(),
		`UPDATE hackathons SET status = $2 WHERE id = $1 AND status = $3`, hackathonID, req.Status, current)
	if err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to update status", err))
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.WriteError(w, http.StatusConflict, "conflict", "hackathon status was changed concurrently, retry")
		return
	}
	audit.Record(r.Context(), h.pool, audit.Event{
		HackathonID: &hackathonID,
		ActorID:     &user.ID,
		ActorRole:   "organizer",
		Action:      "hackathon.status_changed",
		EntityType:  "hackathon",
		EntityID:    hackathonID.String(),
		Reason:      req.Reason,
		Payload:     map[string]string{"from": current, "to": req.Status},
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": req.Status})
}
