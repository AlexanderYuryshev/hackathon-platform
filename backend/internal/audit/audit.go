package audit

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

type Event struct {
	HackathonID *uuid.UUID
	ActorID     *uuid.UUID
	ActorRole   string
	Action      string
	EntityType  string
	EntityID    string
	Reason      string
	Payload     any
}

type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func Record(ctx context.Context, db Execer, e Event) error {
	var payload []byte
	if e.Payload != nil {
		b, err := json.Marshal(e.Payload)
		if err != nil {
			return err
		}
		payload = b
	}
	if payload == nil {
		payload = []byte("{}")
	}
	var hackathonID, actorID any
	if e.HackathonID != nil {
		hackathonID = *e.HackathonID
	}
	if e.ActorID != nil {
		actorID = *e.ActorID
	}
	_, err := db.Exec(ctx, `
		INSERT INTO audit_events (hackathon_id, actor_id, actor_role, action, entity_type, entity_id, payload, reason)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''))`,
		hackathonID, actorID, e.ActorRole, e.Action, e.EntityType, e.EntityID, payload, e.Reason,
	)
	if err != nil {
		slog.Error("audit record failed", "action", e.Action, "err", err)
	}
	return err
}
