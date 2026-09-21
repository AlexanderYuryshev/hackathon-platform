package submissions

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
)

func hackathonParam(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid hackathon id")
		return uuid.Nil, false
	}
	return id, true
}

type Handler struct {
	pool *pgxpool.Pool
}

func NewHandler(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool}
}

type Submission struct {
	ID          uuid.UUID       `json:"id"`
	TeamID      uuid.UUID       `json:"team_id"`
	Version     int             `json:"version"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Track       string          `json:"track"`
	RepoURL     string          `json:"repo_url"`
	DemoURL     string          `json:"demo_url"`
	AppURL      string          `json:"app_url"`
	ExtraLinks  []map[string]any `json:"extra_links"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type PutRequest struct {
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Track       string          `json:"track"`
	RepoURL     string          `json:"repo_url"`
	DemoURL     string          `json:"demo_url"`
	AppURL      string          `json:"app_url"`
	ExtraLinks  []map[string]any `json:"extra_links"`
	Reason      string          `json:"reason"`
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
	var s Submission
	err = h.pool.QueryRow(r.Context(), `
		SELECT sv.submission_id, su.team_id, sv.version, sv.title, sv.description, sv.track, sv.repo_url, sv.demo_url, sv.app_url, sv.extra_links, sv.created_at
		FROM submission_versions sv
		JOIN submissions su ON su.id = sv.submission_id
		WHERE su.team_id = $1 AND su.hackathon_id = $2 AND sv.version = su.current_version`, teamID, hackathonID,
	).Scan(&s.ID, &s.TeamID, &s.Version, &s.Title, &s.Description, &s.Track, &s.RepoURL, &s.DemoURL, &s.AppURL, &s.ExtraLinks, &s.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			httpx.WriteError(w, http.StatusNotFound, "not_found", "submission not found")
			return
		}
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to load submission", err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, s)
}

func (h *Handler) Put(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	hackathonID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid hackathon id")
		return
	}
	teamID, err := uuid.Parse(chi.URLParam(r, "teamId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid team id")
		return
	}
	var req PutRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "title is required")
		return
	}
	if req.ExtraLinks == nil {
		req.ExtraLinks = []map[string]any{}
	}

	var endsAt *time.Time
	var status string
	var organizerID uuid.UUID
	if err := h.pool.QueryRow(r.Context(),
		`SELECT ends_at, status, organizer_id FROM hackathons WHERE id = $1`, hackathonID,
	).Scan(&endsAt, &status, &organizerID); err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusNotFound, "not_found", "hackathon not found", err))
		return
	}

	var memberTeamID *uuid.UUID
	if err := h.pool.QueryRow(r.Context(),
		`SELECT team_id FROM team_members WHERE hackathon_id = $1 AND user_id = $2`, hackathonID, user.ID,
	).Scan(&memberTeamID); err != nil && err != pgx.ErrNoRows {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to check membership", err))
		return
	}

	isOrganizer := organizerID == user.ID
	isTeamMember := memberTeamID != nil && *memberTeamID == teamID
	if !isOrganizer && !isTeamMember {
		httpx.WriteError(w, http.StatusForbidden, "forbidden", "only team members or the organizer can update this submission")
		return
	}

	afterDeadline := endsAt != nil && time.Now().UTC().After(*endsAt)
	if afterDeadline || status == "judging" || status == "finalized" {
		if !isOrganizer {
			httpx.WriteError(w, http.StatusConflict, "deadline_passed", "submission deadline has passed")
			return
		}
		if strings.TrimSpace(req.Reason) == "" {
			httpx.WriteError(w, http.StatusBadRequest, "bad_request", "reason is required for administrative changes after the deadline")
			return
		}
	}

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to start tx", err))
		return
	}
	defer tx.Rollback(r.Context())

	var submissionID uuid.UUID
	var currentVersion int
	err = tx.QueryRow(r.Context(), `
		INSERT INTO submissions (hackathon_id, team_id) VALUES ($1, $2)
		ON CONFLICT (hackathon_id, team_id) DO UPDATE SET updated_at = now()
		RETURNING id, current_version`, hackathonID, teamID,
	).Scan(&submissionID, &currentVersion)
	if err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to upsert submission", err))
		return
	}
	newVersion := currentVersion + 1
	if _, err := tx.Exec(r.Context(), `
		INSERT INTO submission_versions (submission_id, version, title, description, track, repo_url, demo_url, app_url, extra_links, edited_by, reason)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NULLIF($11, ''))`,
		submissionID, newVersion, req.Title, req.Description, req.Track, req.RepoURL, req.DemoURL, req.AppURL, req.ExtraLinks, user.ID, req.Reason,
	); err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to create submission version", err))
		return
	}
	if _, err := tx.Exec(r.Context(),
		`UPDATE submissions SET current_version = $2, updated_at = now() WHERE id = $1`, submissionID, newVersion,
	); err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to bump version", err))
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to commit", err))
		return
	}

	action := "submission.updated"
	if afterDeadline {
		action = "submission.updated_after_deadline"
	}
	audit.Record(r.Context(), h.pool, audit.Event{
		HackathonID: &hackathonID,
		ActorID:     &user.ID,
		ActorRole:   "team_member",
		Action:      action,
		EntityType:  "submission",
		EntityID:    submissionID.String(),
		Reason:      req.Reason,
		Payload:     map[string]any{"version": newVersion, "team_id": teamID},
	})
	httpx.WriteJSON(w, http.StatusOK, Submission{
		ID: submissionID, TeamID: teamID, Version: newVersion, Title: req.Title,
		Description: req.Description, Track: req.Track, RepoURL: req.RepoURL,
		DemoURL: req.DemoURL, AppURL: req.AppURL, ExtraLinks: req.ExtraLinks,
		UpdatedAt: time.Now().UTC(),
	})
}
