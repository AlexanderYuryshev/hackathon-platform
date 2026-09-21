package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hackaton-platform/backend/internal/httpx"
)

type User struct {
	ID    uuid.UUID `json:"id"`
	Email string    `json:"email"`
	Name  string    `json:"name"`
}

type Session struct {
	Token     string
	CSRFToken string
	UserID    uuid.UUID
	ExpiresAt time.Time
	User      User
}

var ErrInvalidCredentials = errors.New("invalid credentials")

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) UserByEmail(ctx context.Context, email string) (User, string, error) {
	var u User
	var passwordHash string
	err := s.pool.QueryRow(ctx,
		`SELECT id, email, name, password_hash FROM users WHERE email = $1`, email,
	).Scan(&u.ID, &u.Email, &u.Name, &passwordHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return u, "", httpx.NewStatusError(http.StatusUnauthorized, "invalid_credentials", "invalid email or password", nil)
		}
		return u, "", httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to load user", err)
	}
	return u, passwordHash, nil
}

func (s *Store) UserByID(ctx context.Context, id uuid.UUID) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx,
		`SELECT id, email, name FROM users WHERE id = $1`, id,
	).Scan(&u.ID, &u.Email, &u.Name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return u, httpx.NewStatusError(http.StatusUnauthorized, "unauthorized", "user not found", nil)
		}
		return u, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to load user", err)
	}
	return u, nil
}

func (s *Store) CreateSession(ctx context.Context, userID uuid.UUID, ttl time.Duration) (*Session, error) {
	raw, csrf, err := newTokens()
	if err != nil {
		return nil, httpx.NewStatusError(http.StatusInternalServerError, "token_error", "failed to generate token", err)
	}
	sess := &Session{
		Token:     raw,
		CSRFToken: csrf,
		UserID:    userID,
		ExpiresAt: time.Now().Add(ttl),
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO sessions (user_id, token_hash, csrf_token, expires_at) VALUES ($1, $2, $3, $4)`,
		userID, hashToken(raw), csrf, sess.ExpiresAt,
	)
	if err != nil {
		return nil, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to create session", err)
	}
	return sess, nil
}

func (s *Store) SessionByToken(ctx context.Context, rawToken string) (*Session, error) {
	sess := &Session{Token: rawToken}
	err := s.pool.QueryRow(ctx, `
		SELECT s.csrf_token, s.expires_at, u.id, u.email, u.name
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now()`,
		hashToken(rawToken),
	).Scan(&sess.CSRFToken, &sess.ExpiresAt, &sess.User.ID, &sess.User.Email, &sess.User.Name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to load session", err)
	}
	return sess, nil
}

func (s *Store) DeleteSession(ctx context.Context, rawToken string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, hashToken(rawToken))
	if err != nil {
		return httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to delete session", err)
	}
	return nil
}

func newTokens() (raw, csrf string, err error) {
	rb := make([]byte, 32)
	cb := make([]byte, 32)
	if _, err = rand.Read(rb); err != nil {
		return "", "", err
	}
	if _, err = rand.Read(cb); err != nil {
		return "", "", err
	}
	return hex.EncodeToString(rb), hex.EncodeToString(cb), nil
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
