package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hackaton-platform/backend/internal/audit"
	"github.com/hackaton-platform/backend/internal/auth"
	"github.com/hackaton-platform/backend/internal/httpx"
	"github.com/hackaton-platform/backend/internal/scoring"
)

type Handler struct {
	pool *pgxpool.Pool
}

func NewHandler(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool}
}

type MetricPayload struct {
	HackathonID     string         `json:"hackathon_id"`
	TeamID          string         `json:"team_id"`
	MetricKey       string         `json:"metric_key"`
	Value           *float64       `json:"value"`
	ExternalEventID string         `json:"external_event_id"`
	CapturedAt      string         `json:"captured_at"`
	Metadata        map[string]any `json:"metadata"`
}

func (h *Handler) Ingest(w http.ResponseWriter, r *http.Request) {
	token := bearerToken(r)
	if token == "" {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing ingest token")
		return
	}
	var sourceID uuid.UUID
	var sourceHackathon uuid.UUID
	var sourceName string
	var hkStatus string
	err := h.pool.QueryRow(r.Context(), `
		SELECT s.id, s.hackathon_id, s.name, h.status
		FROM ingest_sources s JOIN hackathons h ON h.id = s.hackathon_id
		WHERE s.token_hash = $1`,
		hashToken(token)).Scan(&sourceID, &sourceHackathon, &sourceName, &hkStatus)
	if err != nil {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid ingest token")
		return
	}

	var p MetricPayload
	if err := httpx.Decode(r, &p); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	hackathonID, err := uuid.Parse(p.HackathonID)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid hackathon_id")
		return
	}
	if hackathonID != sourceHackathon {
		httpx.WriteError(w, http.StatusForbidden, "forbidden", "token is not valid for this hackathon")
		return
	}
	if hkStatus == "finalized" {
		httpx.WriteError(w, http.StatusConflict, "conflict", "hackathon is finalized, metrics are no longer accepted")
		return
	}
	teamID, err := uuid.Parse(p.TeamID)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid team_id")
		return
	}
	if p.MetricKey == "" || p.ExternalEventID == "" || p.Value == nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "metric_key, value and external_event_id are required")
		return
	}
	if math.IsNaN(*p.Value) || math.IsInf(*p.Value, 0) {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "value must be a finite number")
		return
	}

	var defID uuid.UUID
	var direction, normJSON string
	var normalization scoring.Normalization
	var teamTrackID uuid.UUID
	err = h.pool.QueryRow(r.Context(),
		`SELECT track_id FROM teams WHERE id = $1 AND hackathon_id = $2`, teamID, hackathonID,
	).Scan(&teamTrackID)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "team does not belong to this hackathon")
		return
	}
	err = h.pool.QueryRow(r.Context(), `
		SELECT id, direction, normalization::text FROM metric_definitions
		WHERE track_id = $1 AND key = $2 AND type = 'automated'`,
		teamTrackID, p.MetricKey).Scan(&defID, &direction, &normJSON)
	if err != nil {
		if err == pgx.ErrNoRows {
			httpx.WriteError(w, http.StatusNotFound, "not_found", "unknown automated metric key")
			return
		}
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to load metric", err))
		return
	}
	if err := decodeNormalization(normJSON, &normalization); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "metric has invalid normalization config")
		return
	}
	normalized, nerr := scoring.Normalize(normalization, direction, *p.Value)

	capturedAt := time.Now().UTC()
	if p.CapturedAt != "" {
		t, err := time.Parse(time.RFC3339, p.CapturedAt)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "bad_request", "captured_at must be RFC3339")
			return
		}
		capturedAt = t.UTC()
	}
	// A future timestamp would win the "latest value" sort forever and always
	// look fresh; allow only a small clock-skew tolerance.
	if capturedAt.After(time.Now().UTC().Add(5 * time.Minute)) {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "captured_at is too far in the future")
		return
	}
	if p.Metadata == nil {
		p.Metadata = map[string]any{}
	}

	// Erroneous measurements are stored but flagged invalid: they never become
	// the current value and cannot destroy the last correct one (TASK §6).
	invalidReason := ""
	switch {
	case nerr != nil:
		invalidReason = nerr.Error()
	case normalization.Kind == scoring.KindBinary && *p.Value != 0 && *p.Value != 1:
		invalidReason = "binary metric accepts only 0 or 1"
	case normalization.ValidMin != nil && *p.Value < *normalization.ValidMin:
		invalidReason = "value below valid_min plausibility bound"
	case normalization.ValidMax != nil && *p.Value > *normalization.ValidMax:
		invalidReason = "value above valid_max plausibility bound"
	}
	status := "valid"
	if invalidReason != "" {
		status = "invalid"
	}

	var storedNormalized *float64
	if nerr == nil && invalidReason == "" {
		v := normalized
		storedNormalized = &v
	}

	err = h.pool.QueryRow(r.Context(), `
		INSERT INTO metric_values
			(hackathon_id, team_id, metric_definition_id, external_event_id, raw_value, normalized_value, metadata, source, captured_at, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (metric_definition_id, team_id, external_event_id) DO NOTHING
		RETURNING id`,
		hackathonID, teamID, defID, p.ExternalEventID, *p.Value, storedNormalized, p.Metadata, sourceName, capturedAt, status,
	).Scan(new(uuid.UUID))
	if err != nil {
		if err == pgx.ErrNoRows {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"stored":      false,
				"duplicate":   true,
				"status":      status,
				"captured_at": capturedAt,
			})
			return
		}
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to store metric", err))
		return
	}

	h.pool.Exec(r.Context(), `UPDATE ingest_sources SET last_seen_at = now() WHERE id = $1`, sourceID)

	resp := map[string]any{
		"stored":      true,
		"duplicate":   false,
		"status":      status,
		"normalized":  storedNormalized,
		"captured_at": capturedAt,
	}
	if invalidReason != "" {
		resp["reason"] = invalidReason
		// Invalid events have no scoring impact; no need to mark dirty.
		httpx.WriteJSON(w, http.StatusCreated, resp)
		return
	}
	scoring.NotifyDirty(r.Context(), h.pool, teamTrackID)
	httpx.WriteJSON(w, http.StatusCreated, resp)
}

type CreateSourceRequest struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

func (h *Handler) CreateSource(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	hackathonID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid hackathon id")
		return
	}
	var req CreateSourceRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if req.Name == "" {
		req.Name = "collector"
	}
	if req.Kind == "" {
		req.Kind = "webhook"
	}

	raw := make([]byte, 32)
	if _, err := readRandom(raw); err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "token_error", "failed to generate token", err))
		return
	}
	token := hex.EncodeToString(raw)

	var id uuid.UUID
	err = h.pool.QueryRow(r.Context(), `
		INSERT INTO ingest_sources (hackathon_id, name, kind, token_hash)
		VALUES ($1, $2, $3, $4) RETURNING id`,
		hackathonID, req.Name, req.Kind, hashToken(token)).Scan(&id)
	if err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to create source", err))
		return
	}
	audit.Record(r.Context(), h.pool, audit.Event{
		HackathonID: &hackathonID,
		ActorID:     &user.ID,
		ActorRole:   "organizer",
		Action:      "ingest.source_created",
		EntityType:  "ingest_source",
		EntityID:    id.String(),
		Payload:     map[string]any{"name": req.Name, "kind": req.Kind},
	})
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"id":    id,
		"name":  req.Name,
		"kind":  req.Kind,
		"token": token,
	})
}

func (h *Handler) ListSources(w http.ResponseWriter, r *http.Request) {
	hackathonID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid hackathon id")
		return
	}
	rows, err := h.pool.Query(r.Context(), `
		SELECT id, name, kind, last_seen_at, created_at FROM ingest_sources
		WHERE hackathon_id = $1 ORDER BY created_at`, hackathonID)
	if err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to list sources", err))
		return
	}
	defer rows.Close()
	type row struct {
		ID         uuid.UUID  `json:"id"`
		Name       string     `json:"name"`
		Kind       string     `json:"kind"`
		LastSeenAt *time.Time `json:"last_seen_at"`
		CreatedAt  time.Time  `json:"created_at"`
	}
	sources := []row{}
	for rows.Next() {
		var s row
		if err := rows.Scan(&s.ID, &s.Name, &s.Kind, &s.LastSeenAt, &s.CreatedAt); err != nil {
			httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to scan source", err))
			return
		}
		sources = append(sources, s)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"sources": sources})
}

func bearerToken(r *http.Request) string {
	authz := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(authz) > len(prefix) && authz[:len(prefix)] == prefix {
		return authz[len(prefix):]
	}
	return ""
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
