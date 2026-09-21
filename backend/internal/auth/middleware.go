package auth

import (
	"context"
	"net/http"
	"time"

	"github.com/hackaton-platform/backend/internal/httpx"
)

type ctxKey int

const (
	sessionKey ctxKey = iota
	userKey
)

func WithSession(ctx context.Context, sess *Session) context.Context {
	return context.WithValue(ctx, sessionKey, sess)
}

func SessionFrom(ctx context.Context) *Session {
	sess, _ := ctx.Value(sessionKey).(*Session)
	return sess
}

func UserFrom(ctx context.Context) *User {
	u, _ := ctx.Value(userKey).(*User)
	return u
}

const CookieName = "hs_session"

func setSessionCookie(w http.ResponseWriter, token string, secure bool, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  expires,
	})
}

func clearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func sessionCookie(r *http.Request) string {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

func (s *Store) Middleware(secure bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := sessionCookie(r)
			if token == "" {
				next.ServeHTTP(w, r)
				return
			}
			sess, err := s.SessionByToken(r.Context(), token)
			if err != nil {
				httpx.WriteErr(w, err)
				return
			}
			if sess == nil {
				next.ServeHTTP(w, r)
				return
			}
			user := sess.User
			ctx := WithSession(r.Context(), sess)
			ctx = context.WithValue(ctx, userKey, &user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if UserFrom(r.Context()) == nil {
			httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func RequireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		sess := SessionFrom(r.Context())
		if sess == nil {
			httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		if r.Header.Get("X-CSRF-Token") != sess.CSRFToken {
			httpx.WriteError(w, http.StatusForbidden, "csrf_failed", "missing or invalid CSRF token")
			return
		}
		next.ServeHTTP(w, r)
	})
}
