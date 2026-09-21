package metrics

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hackaton-platform/backend/internal/audit"
	"github.com/hackaton-platform/backend/internal/auth"
	"github.com/hackaton-platform/backend/internal/httpx"
	"github.com/hackaton-platform/backend/internal/tracks"
)

var keyRe = regexp.MustCompile(`^[a-zA-Z0-9_]{2,64}$`)

type Handler struct {
	pool *pgxpool.Pool
}

func NewHandler(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool}
}

type MetricDefinition struct {
	ID               uuid.UUID       `json:"id"`
	Key              string          `json:"key"`
	Name             string          `json:"name"`
	Description      string          `json:"description"`
	Type             string          `json:"type"`
	Weight           float64         `json:"weight"`
	Required         bool            `json:"required"`
	Public           bool            `json:"public"`
	Direction        string          `json:"direction"`
	Normalization    json.RawMessage `json:"normalization"`
	Source           string          `json:"source"`
	StalenessSeconds int             `json:"staleness_seconds"`
}

type RubricCriterion struct {
	ID          uuid.UUID `json:"id"`
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	ScaleMin    float64   `json:"scale_min"`
	ScaleMax    float64   `json:"scale_max"`
	Weight      float64   `json:"weight"`
	Required    bool      `json:"required"`
	Public      bool      `json:"public"`
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	hackathonID, ok := hackathonParam(w, r)
	if !ok {
		return
	}
	trackID, _, err := tracks.ResolveTrackID(r.Context(), h.pool, hackathonID, r.URL.Query().Get("track"))
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	isOrganizer := h.isOrganizer(r, hackathonID)
	canSeePrivate := isOrganizer || h.isJudge(r, hackathonID)

	rows, err := h.pool.Query(r.Context(), `
		SELECT id, key, name, description, type, weight, required, public, direction, normalization, source, staleness_seconds
		FROM metric_definitions WHERE track_id = $1 ORDER BY sort_order, key`, trackID)
	if err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to list metrics", err))
		return
	}
	defer rows.Close()

	metrics := []MetricDefinition{}
	for rows.Next() {
		var m MetricDefinition
		if err := rows.Scan(&m.ID, &m.Key, &m.Name, &m.Description, &m.Type, &m.Weight, &m.Required, &m.Public, &m.Direction, &m.Normalization, &m.Source, &m.StalenessSeconds); err != nil {
			httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to scan metric", err))
			return
		}
		if !m.Public && !isOrganizer {
			metrics = append(metrics, MetricDefinition{ID: m.ID, Key: m.Key, Name: m.Name, Public: false, Type: m.Type})
			continue
		}
		metrics = append(metrics, m)
	}

	criteria := []RubricCriterion{}
	crows, err := h.pool.Query(r.Context(), `
		SELECT id, key, name, description, scale_min, scale_max, weight, required, public
		FROM rubric_criteria WHERE track_id = $1 ORDER BY sort_order, key`, trackID)
	if err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to list rubric", err))
		return
	}
	defer crows.Close()
	for crows.Next() {
		var c RubricCriterion
		if err := crows.Scan(&c.ID, &c.Key, &c.Name, &c.Description, &c.ScaleMin, &c.ScaleMax, &c.Weight, &c.Required, &c.Public); err != nil {
			httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to scan criterion", err))
			return
		}
		if !c.Public && !canSeePrivate {
			criteria = append(criteria, RubricCriterion{ID: c.ID, Key: c.Key, Name: c.Name, Public: false})
			continue
		}
		criteria = append(criteria, c)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"metrics": metrics, "criteria": criteria})
}

type CreateMetricRequest struct {
	Key              string          `json:"key"`
	Name             string          `json:"name"`
	Description      string          `json:"description"`
	Type             string          `json:"type"`
	Weight           float64         `json:"weight"`
	Required         *bool           `json:"required"`
	Public           *bool           `json:"public"`
	Direction        string          `json:"direction"`
	Normalization    json.RawMessage `json:"normalization"`
	Source           string          `json:"source"`
	StalenessSeconds int             `json:"staleness_seconds"`
	Track            string          `json:"track"`
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	hackathonID, ok := hackathonParam(w, r)
	if !ok {
		return
	}
	var req CreateMetricRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	trackID, _, err := tracks.ResolveTrackID(r.Context(), h.pool, hackathonID, req.Track)
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	if err := h.checkMutable(r, hackathonID, trackID); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	req.Key = strings.ToLower(strings.TrimSpace(req.Key))
	if !keyRe.MatchString(req.Key) {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "key must be 2-64 chars: letters, digits, underscores")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "name is required")
		return
	}
	if req.Type != "automated" {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "type must be automated; manual metrics are managed as rubric criteria")
		return
	}
	if req.Weight < 0 {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "weight must be >= 0")
		return
	}
	if req.Direction == "" {
		req.Direction = "higher_is_better"
	}
	if req.Direction != "higher_is_better" && req.Direction != "lower_is_better" {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid direction")
		return
	}
	if req.StalenessSeconds <= 0 {
		req.StalenessSeconds = 1800
	}
	required := true
	if req.Required != nil {
		required = *req.Required
	}
	isPublic := true
	if req.Public != nil {
		isPublic = *req.Public
	}

	kind, verr := validateNormalization(req.Normalization, req.Direction)
	if verr != "" {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", verr)
		return
	}

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to start tx", err))
		return
	}
	defer tx.Rollback(r.Context())

	var id uuid.UUID
	err = tx.QueryRow(r.Context(), `
		INSERT INTO metric_definitions (hackathon_id, track_id, key, name, description, type, weight, required, public, direction, normalization, source, staleness_seconds)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13) RETURNING id`,
		hackathonID, trackID, req.Key, req.Name, req.Description, req.Type, req.Weight, required, isPublic, req.Direction, []byte(req.Normalization), req.Source, req.StalenessSeconds,
	).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			httpx.WriteError(w, http.StatusConflict, "conflict", "metric key already exists")
			return
		}
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to create metric", err))
		return
	}
	if err := bumpScoringVersion(r.Context(), tx, hackathonID, trackID, user.ID, "metric.created", id.String(), map[string]any{"key": req.Key, "kind": kind, "weight": req.Weight}); err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to bump scoring version", err))
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to commit", err))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, MetricDefinition{
		ID: id, Key: req.Key, Name: req.Name, Description: req.Description, Type: req.Type,
		Weight: req.Weight, Required: required, Public: isPublic, Direction: req.Direction,
		Normalization: req.Normalization, Source: req.Source, StalenessSeconds: req.StalenessSeconds,
	})
}

type CreateCriterionRequest struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	ScaleMin    *float64 `json:"scale_min"`
	ScaleMax    *float64 `json:"scale_max"`
	Weight      float64  `json:"weight"`
	Required    *bool    `json:"required"`
	Public      *bool    `json:"public"`
	Track       string   `json:"track"`
}

func (h *Handler) CreateCriterion(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	hackathonID, ok := hackathonParam(w, r)
	if !ok {
		return
	}
	var req CreateCriterionRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	trackID, _, err := tracks.ResolveTrackID(r.Context(), h.pool, hackathonID, req.Track)
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	if err := h.checkMutable(r, hackathonID, trackID); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	req.Key = strings.ToLower(strings.TrimSpace(req.Key))
	if !keyRe.MatchString(req.Key) {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "key must be 2-64 chars: letters, digits, underscores")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "name is required")
		return
	}
	scaleMin, scaleMax := 0.0, 10.0
	if req.ScaleMin != nil || req.ScaleMax != nil {
		if req.ScaleMin == nil || req.ScaleMax == nil || *req.ScaleMax <= *req.ScaleMin {
			httpx.WriteError(w, http.StatusBadRequest, "bad_request", "scale_max must be greater than scale_min")
			return
		}
		scaleMin, scaleMax = *req.ScaleMin, *req.ScaleMax
	}
	if req.Weight < 0 {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "weight must be >= 0")
		return
	}
	required := true
	if req.Required != nil {
		required = *req.Required
	}
	isPublic := true
	if req.Public != nil {
		isPublic = *req.Public
	}

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to start tx", err))
		return
	}
	defer tx.Rollback(r.Context())

	var id uuid.UUID
	err = tx.QueryRow(r.Context(), `
		INSERT INTO rubric_criteria (hackathon_id, track_id, key, name, description, scale_min, scale_max, weight, required, public)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id`,
		hackathonID, trackID, req.Key, req.Name, req.Description, scaleMin, scaleMax, req.Weight, required, isPublic,
	).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			httpx.WriteError(w, http.StatusConflict, "conflict", "criterion key already exists")
			return
		}
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to create criterion", err))
		return
	}
	if err := bumpScoringVersion(r.Context(), tx, hackathonID, trackID, user.ID, "rubric.criterion_created", id.String(), map[string]any{"key": req.Key, "weight": req.Weight}); err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to bump scoring version", err))
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to commit", err))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, RubricCriterion{
		ID: id, Key: req.Key, Name: req.Name, Description: req.Description,
		ScaleMin: scaleMin, ScaleMax: scaleMax, Weight: req.Weight, Required: required, Public: isPublic,
	})
}

func validateNormalization(raw json.RawMessage, direction string) (string, string) {
	if len(raw) == 0 {
		return "", "normalization is required"
	}
	var n struct {
		Kind     string   `json:"kind"`
		Min      *float64 `json:"min"`
		Max      *float64 `json:"max"`
		Target   *float64 `json:"target"`
		Lower    *float64 `json:"lower"`
		Upper    *float64 `json:"upper"`
		ValidMin *float64 `json:"valid_min"`
		ValidMax *float64 `json:"valid_max"`
	}
	if err := json.Unmarshal(raw, &n); err != nil {
		return "", "normalization must be a valid object"
	}
	switch n.Kind {
	case "range":
		if n.Min == nil || n.Max == nil || *n.Max <= *n.Min {
			return "", "range normalization requires min < max"
		}
	case "target":
		if n.Target == nil || n.Lower == nil || n.Upper == nil || *n.Lower >= *n.Target || *n.Upper <= *n.Target {
			return "", "target normalization requires lower < target < upper"
		}
	case "binary":
	case "manual":
	default:
		return "", "normalization kind must be range, target, binary or manual"
	}
	if n.ValidMin != nil && n.ValidMax != nil && *n.ValidMax <= *n.ValidMin {
		return "", "valid_max must be greater than valid_min"
	}
	return n.Kind, ""
}

func (h *Handler) checkMutable(r *http.Request, hackathonID, trackID uuid.UUID) error {
	var locked bool
	var status string
	if err := h.pool.QueryRow(r.Context(), `
		SELECT tr.scoring_locked, h.status FROM tracks tr
		JOIN hackathons h ON h.id = tr.hackathon_id
		WHERE tr.id = $1 AND tr.hackathon_id = $2`, trackID, hackathonID).Scan(&locked, &status); err != nil {
		return httpx.NewStatusError(http.StatusNotFound, "not_found", "track not found", err)
	}
	if status == "finalized" {
		return httpx.NewStatusError(http.StatusConflict, "conflict", "hackathon is finalized", nil)
	}
	if locked {
		return httpx.NewStatusError(http.StatusConflict, "scoring_locked", "track scoring is locked", nil)
	}
	return nil
}

func bumpScoringVersion(ctx context.Context, tx pgx.Tx, hackathonID, trackID, actorID uuid.UUID, action, entityID string, payload any) error {
	if _, err := tx.Exec(ctx,
		`UPDATE tracks SET scoring_version = scoring_version + 1 WHERE id = $1`, trackID); err != nil {
		return err
	}
	return audit.Record(ctx, tx, audit.Event{
		HackathonID: &hackathonID,
		ActorID:     &actorID,
		ActorRole:   "organizer",
		Action:      action,
		EntityType:  "metric_definition",
		EntityID:    entityID,
		Payload:     payload,
	})
}

func (h *Handler) isOrganizer(r *http.Request, hackathonID uuid.UUID) bool {
	user := auth.UserFrom(r.Context())
	if user == nil {
		return false
	}
	var organizerID uuid.UUID
	err := h.pool.QueryRow(r.Context(),
		`SELECT organizer_id FROM hackathons WHERE id = $1`, hackathonID).Scan(&organizerID)
	return err == nil && organizerID == user.ID
}

func (h *Handler) isJudge(r *http.Request, hackathonID uuid.UUID) bool {
	user := auth.UserFrom(r.Context())
	if user == nil {
		return false
	}
	var isJudge bool
	err := h.pool.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM judge_assignments WHERE hackathon_id = $1 AND judge_id = $2)`,
		hackathonID, user.ID).Scan(&isJudge)
	return err == nil && isJudge
}

func hackathonParam(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid hackathon id")
		return uuid.Nil, false
	}
	return id, true
}
