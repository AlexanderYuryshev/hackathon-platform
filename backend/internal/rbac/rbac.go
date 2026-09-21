package rbac

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hackaton-platform/backend/internal/auth"
	"github.com/hackaton-platform/backend/internal/httpx"
)
type Role string

const (
	RoleOrganizer   Role = "organizer"
	RoleJudge       Role = "judge"
	RoleTeamMember  Role = "team_member"
	RoleAuthPublic  Role = "public"
	RoleAnonymous   Role = "anonymous"
)

type Resolver struct {
	pool *pgxpool.Pool
}

func NewResolver(pool *pgxpool.Pool) *Resolver {
	return &Resolver{pool: pool}
}

func (rs *Resolver) Resolve(ctx context.Context, userID *uuid.UUID, hackathonID uuid.UUID) (Role, error) {
	if userID == nil {
		return RoleAnonymous, nil
	}
	var isOrganizer bool
	err := rs.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM hackathons WHERE id = $1 AND organizer_id = $2)`,
		hackathonID, *userID,
	).Scan(&isOrganizer)
	if err != nil {
		return RoleAnonymous, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to resolve role", err)
	}
	if isOrganizer {
		return RoleOrganizer, nil
	}

	var isJudge bool
	err = rs.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM judge_assignments WHERE hackathon_id = $1 AND judge_id = $2)`,
		hackathonID, *userID,
	).Scan(&isJudge)
	if err != nil {
		return RoleAnonymous, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to resolve role", err)
	}
	if isJudge {
		return RoleJudge, nil
	}

	var isMember bool
	err = rs.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM team_members WHERE hackathon_id = $1 AND user_id = $2)`,
		hackathonID, *userID,
	).Scan(&isMember)
	if err != nil {
		return RoleAnonymous, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to resolve role", err)
	}
	if isMember {
		return RoleTeamMember, nil
	}
	return RoleAuthPublic, nil
}

func (rs *Resolver) RoleFromContext(ctx context.Context, hackathonID uuid.UUID) (Role, *auth.User, error) {
	user := auth.UserFrom(ctx)
	var userID *uuid.UUID
	if user != nil {
		id := user.ID
		userID = &id
	}
	role, err := rs.Resolve(ctx, userID, hackathonID)
	return role, user, err
}

func roleAtLeast(role, min Role) bool {
	rank := map[Role]int{
		RoleAnonymous:   0,
		RoleAuthPublic:  0,
		RoleTeamMember:  1,
		RoleJudge:       2,
		RoleOrganizer:   3,
	}
	return rank[role] >= rank[min]
}

func (rs *Resolver) Require(min Role, param string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := chi.URLParam(r, param)
			hackathonID, err := uuid.Parse(raw)
			if err != nil {
				httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid hackathon id")
				return
			}
			role, _, err := rs.RoleFromContext(r.Context(), hackathonID)
			if err != nil {
				httpx.WriteErr(w, err)
				return
			}
			if !roleAtLeast(role, min) {
				httpx.WriteError(w, http.StatusForbidden, "forbidden", "insufficient permissions")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
