package teams

import (
	"net/http"
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

type Team struct {
	ID          uuid.UUID `json:"id"`
	HackathonID uuid.UUID `json:"hackathon_id"`
	Name        string    `json:"name"`
	TrackID     uuid.UUID `json:"track_id"`
	TrackKey    string    `json:"track_key"`
	TrackName   string    `json:"track_name"`
	MemberCount int       `json:"member_count"`
	CreatedAt   time.Time `json:"created_at"`
}

type CreateRequest struct {
	Name  string `json:"name"`
	Track string `json:"track"`
}

const teamCols = `t.id, t.hackathon_id, t.name, t.track_id, tr.key, tr.name, t.created_at,
	(SELECT count(*) FROM team_members m WHERE m.team_id = t.id)`

func scanTeam(rows pgx.Row, t *Team) error {
	return rows.Scan(&t.ID, &t.HackathonID, &t.Name, &t.TrackID, &t.TrackKey, &t.TrackName, &t.CreatedAt, &t.MemberCount)
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	hackathonID, ok := hackathonParam(w, r)
	if !ok {
		return
	}
	trackKey := strings.TrimSpace(r.URL.Query().Get("track"))
	rows, err := h.pool.Query(r.Context(), `
		SELECT `+teamCols+`
		FROM teams t JOIN tracks tr ON tr.id = t.track_id
		WHERE t.hackathon_id = $1 AND ($2 = '' OR tr.key = $2)
		ORDER BY t.created_at`, hackathonID, trackKey)
	if err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to list teams", err))
		return
	}
	defer rows.Close()

	teams := []Team{}
	for rows.Next() {
		var t Team
		if err := scanTeam(rows, &t); err != nil {
			httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to scan team", err))
			return
		}
		teams = append(teams, t)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"teams": teams})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	hackathonID, ok := hackathonParam(w, r)
	if !ok {
		return
	}
	teamID, err := uuid.Parse(chi.URLParam(r, "teamId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid team id")
		return
	}
	var t Team
	err = h.pool.QueryRow(r.Context(), `
		SELECT `+teamCols+`
		FROM teams t JOIN tracks tr ON tr.id = t.track_id
		WHERE t.id = $1 AND t.hackathon_id = $2`, teamID, hackathonID,
	).Scan(&t.ID, &t.HackathonID, &t.Name, &t.TrackID, &t.TrackKey, &t.TrackName, &t.CreatedAt, &t.MemberCount)
	if err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusNotFound, "not_found", "team not found", err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, t)
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	hackathonID, ok := hackathonParam(w, r)
	if !ok {
		return
	}
	var req CreateRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "team name is required")
		return
	}

	var status string
	if err := h.pool.QueryRow(r.Context(),
		`SELECT status FROM hackathons WHERE id = $1`, hackathonID).Scan(&status); err != nil {
		if err == pgx.ErrNoRows {
			httpx.WriteError(w, http.StatusNotFound, "not_found", "hackathon not found")
			return
		}
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to load hackathon", err))
		return
	}
	if status == "finalized" {
		httpx.WriteError(w, http.StatusConflict, "conflict", "hackathon is finalized")
		return
	}

	// `track` carries a track key (legacy free-text values resolve the same way
	// when they match a key); empty means the default track.
	trackID, track, err := tracks.ResolveTrackID(r.Context(), h.pool, hackathonID, req.Track)
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to start tx", err))
		return
	}
	defer tx.Rollback(r.Context())

	var teamID uuid.UUID
	err = tx.QueryRow(r.Context(), `
		INSERT INTO teams (hackathon_id, track_id, name, created_by) VALUES ($1, $2, $3, $4) RETURNING id`,
		hackathonID, trackID, req.Name, user.ID).Scan(&teamID)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			httpx.WriteError(w, http.StatusConflict, "conflict", "team name already taken in this track")
			return
		}
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to create team", err))
		return
	}
	if _, err := tx.Exec(r.Context(), `
		INSERT INTO team_members (hackathon_id, team_id, user_id, is_captain) VALUES ($1, $2, $3, true)`,
		hackathonID, teamID, user.ID); err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			httpx.WriteError(w, http.StatusConflict, "conflict", "you are already a member of a team in this hackathon")
			return
		}
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to add team member", err))
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to commit", err))
		return
	}

	audit.Record(r.Context(), h.pool, audit.Event{
		HackathonID: &hackathonID,
		ActorID:     &user.ID,
		ActorRole:   "team_member",
		Action:      "team.created",
		EntityType:  "team",
		EntityID:    teamID.String(),
	})
	httpx.WriteJSON(w, http.StatusCreated, Team{
		ID: teamID, HackathonID: hackathonID, Name: req.Name,
		TrackID: trackID, TrackKey: track.Key, TrackName: track.Name, MemberCount: 1,
	})
}

// MyTeam returns the current user's team in the hackathon (404 when they have
// not joined one yet) — powers the team area UI.
func (h *Handler) MyTeam(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	hackathonID, ok := hackathonParam(w, r)
	if !ok {
		return
	}
	var t Team
	err := h.pool.QueryRow(r.Context(), `
		SELECT `+teamCols+`
		FROM teams t
		JOIN tracks tr ON tr.id = t.track_id
		JOIN team_members tm ON tm.team_id = t.id
		WHERE tm.hackathon_id = $1 AND tm.user_id = $2`, hackathonID, user.ID,
	).Scan(&t.ID, &t.HackathonID, &t.Name, &t.TrackID, &t.TrackKey, &t.TrackName, &t.CreatedAt, &t.MemberCount)
	if err != nil {
		if err == pgx.ErrNoRows {
			httpx.WriteError(w, http.StatusNotFound, "no_team", "you are not a member of any team in this hackathon")
			return
		}
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to load team", err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, t)
}

func hackathonParam(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid hackathon id")
		return uuid.Nil, false
	}
	return id, true
}
