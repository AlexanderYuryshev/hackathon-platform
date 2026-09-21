package audit

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hackaton-platform/backend/internal/httpx"
)

type Handler struct {
	pool *pgxpool.Pool
}

func NewHandler(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool}
}

type EventRow struct {
	ID          uuid.UUID       `json:"id"`
	Action      string          `json:"action"`
	ActorRole   string          `json:"actor_role"`
	ActorName   *string         `json:"actor_name"`
	EntityType  string          `json:"entity_type"`
	EntityID    string          `json:"entity_id"`
	Payload     map[string]any  `json:"payload"`
	Reason      *string         `json:"reason"`
	CreatedAt   time.Time       `json:"created_at"`
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	hackathonID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid hackathon id")
		return
	}
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	offset := 0
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	rows, err := h.pool.Query(r.Context(), `
		SELECT a.id, a.action, a.actor_role, u.name, a.entity_type, a.entity_id, a.payload, a.reason, a.created_at
		FROM audit_events a
		LEFT JOIN users u ON u.id = a.actor_id
		WHERE a.hackathon_id = $1
		ORDER BY a.created_at DESC
		LIMIT $2 OFFSET $3`, hackathonID, limit, offset)
	if err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to list audit events", err))
		return
	}
	defer rows.Close()

	events := []EventRow{}
	for rows.Next() {
		var e EventRow
		if err := rows.Scan(&e.ID, &e.Action, &e.ActorRole, &e.ActorName, &e.EntityType, &e.EntityID, &e.Payload, &e.Reason, &e.CreatedAt); err != nil {
			httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to scan audit event", err))
			return
		}
		events = append(events, e)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"events": events})
}
