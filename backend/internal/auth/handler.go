package auth

import (
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hackaton-platform/backend/internal/audit"
	"github.com/hackaton-platform/backend/internal/httpx"
)

const sessionTTL = 7 * 24 * time.Hour

const loginAttemptsPerMinute = 10

// dummyHash is compared against for unknown emails so that response timing does
// not reveal whether an account exists.
const dummyHash = "$2a$10$szKvID5FYwgvd38WfMPsTu34s7u5VQTUGZXnJju5vl1Xr484NlU.e"

type Handler struct {
	store   *Store
	pool    *pgxpool.Pool
	secure  bool
	limiter *attemptLimiter
}

func NewHandler(store *Store, secure bool) *Handler {
	return &Handler{
		store:   store,
		pool:    store.pool,
		secure:  secure,
		limiter: newAttemptLimiter(time.Minute, loginAttemptsPerMinute),
	}
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type meResponse struct {
	User      User   `json:"user"`
	CSRFToken string `json:"csrf_token"`
}

type myRolesResponse struct {
	// Роли текущего пользователя по хакатонам. Публичные/анонимные роли
	// не возвращаются: отсутствие ключа = нет особой роли. При пересечении
	// побеждает старшая роль (organizer > judge > team_member, как в rbac).
	Roles map[string]string `json:"roles"`
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if req.Email == "" || req.Password == "" {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "email and password are required")
		return
	}

	now := time.Now().UTC()
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if !h.limiter.allow("ip:"+host, now) || !h.limiter.allow("email:"+req.Email, now) {
		httpx.WriteError(w, http.StatusTooManyRequests, "rate_limited", "too many login attempts, try again later")
		return
	}

	user, passwordHash, err := h.store.UserByEmail(r.Context(), req.Email)
	if err != nil {
		var se *httpx.StatusError
		if errors.As(err, &se) && se.Code == http.StatusUnauthorized {
			CheckPassword(dummyHash, req.Password)
			httpx.WriteError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
			return
		}
		httpx.WriteErr(w, err)
		return
	}
	if !CheckPassword(passwordHash, req.Password) {
		audit.Record(r.Context(), h.pool, audit.Event{
			ActorID:   &user.ID,
			ActorRole: "public",
			Action:    "auth.login_failed",
			EntityType: "user",
			EntityID:  user.ID.String(),
		})
		httpx.WriteError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	}

	sess, err := h.store.CreateSession(r.Context(), user.ID, sessionTTL)
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	setSessionCookie(w, sess.Token, h.secure, sess.ExpiresAt)
	audit.Record(r.Context(), h.pool, audit.Event{
		ActorID:    &user.ID,
		ActorRole:  "public",
		Action:     "auth.login",
		EntityType: "user",
		EntityID:   user.ID.String(),
	})
	httpx.WriteJSON(w, http.StatusOK, meResponse{User: user, CSRFToken: sess.CSRFToken})
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	sess := SessionFrom(r.Context())
	if sess != nil {
		if err := h.store.DeleteSession(r.Context(), sess.Token); err != nil {
			httpx.WriteErr(w, err)
			return
		}
		user := UserFrom(r.Context())
		if user != nil {
			audit.Record(r.Context(), h.pool, audit.Event{
				ActorID:    &user.ID,
				ActorRole:  "public",
				Action:     "auth.logout",
				EntityType: "user",
				EntityID:   user.ID.String(),
			})
		}
	}
	clearSessionCookie(w, h.secure)
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	sess := SessionFrom(r.Context())
	user := UserFrom(r.Context())
	if user == nil || sess == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, meResponse{User: *user, CSRFToken: sess.CSRFToken})
}

func (h *Handler) MyRoles(w http.ResponseWriter, r *http.Request) {
	user := UserFrom(r.Context())
	if user == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	rows, err := h.pool.Query(r.Context(), `
		SELECT h.id::text, 'organizer' FROM hackathons h WHERE h.organizer_id = $1
		UNION
		SELECT DISTINCT ja.hackathon_id::text, 'judge' FROM judge_assignments ja WHERE ja.judge_id = $1
		UNION
		SELECT DISTINCT tm.hackathon_id::text, 'team_member' FROM team_members tm WHERE tm.user_id = $1
	`, user.ID)
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	defer rows.Close()
	roles := map[string]string{}
	rank := map[string]int{"team_member": 1, "judge": 2, "organizer": 3}
	for rows.Next() {
		var hackathonID, role string
		if err := rows.Scan(&hackathonID, &role); err != nil {
			httpx.WriteErr(w, err)
			return
		}
		if cur, ok := roles[hackathonID]; !ok || rank[role] > rank[cur] {
			roles[hackathonID] = role
		}
	}
	if err := rows.Err(); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, myRolesResponse{Roles: roles})
}
