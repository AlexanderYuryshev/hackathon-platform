package server

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hackaton-platform/backend/internal/audit"
	"github.com/hackaton-platform/backend/internal/auth"
	"github.com/hackaton-platform/backend/internal/config"
	"github.com/hackaton-platform/backend/internal/hackathons"
	"github.com/hackaton-platform/backend/internal/httpx"
	"github.com/hackaton-platform/backend/internal/ingest"
	"github.com/hackaton-platform/backend/internal/judging"
	"github.com/hackaton-platform/backend/internal/leaderboard"
	"github.com/hackaton-platform/backend/internal/metrics"
	"github.com/hackaton-platform/backend/internal/rbac"
	"github.com/hackaton-platform/backend/internal/scoring"
	"github.com/hackaton-platform/backend/internal/submissions"
	"github.com/hackaton-platform/backend/internal/teams"
	"github.com/hackaton-platform/backend/internal/tracks"
)

type Server struct {
	cfg   config.Config
	pool  *pgxpool.Pool
	authH *auth.Handler
	authS *auth.Store
	rbac  *rbac.Resolver
	hackH *hackathons.Handler
	teamH *teams.Handler
	subH  *submissions.Handler
	metrH *metrics.Handler
	audH  *audit.Handler
	ingH  *ingest.Handler
	scorH *scoring.Handler
	leadH *leaderboard.Handler
	hub   *leaderboard.Hub
	judH  *judging.Handler
	trackH *tracks.Handler
}

func New(cfg config.Config, pool *pgxpool.Pool) *Server {
	authStore := auth.NewStore(pool)
	srv := &Server{
		cfg:   cfg,
		pool:  pool,
		authS: authStore,
		authH: auth.NewHandler(authStore, cfg.CookieSecure),
		rbac:  rbac.NewResolver(pool),
		hackH: hackathons.NewHandler(pool),
		teamH: teams.NewHandler(pool),
		subH:  submissions.NewHandler(pool),
		metrH: metrics.NewHandler(pool),
		audH:  audit.NewHandler(pool),
		ingH:  ingest.NewHandler(pool),
		scorH: scoring.NewHandler(pool),
		leadH: leaderboard.NewHandler(pool),
		hub:   leaderboard.NewHub(pool),
		judH:  judging.NewHandler(pool),
		trackH: tracks.NewHandler(pool),
	}
	srv.hub.SetBlindnessCheck(srv.leadH.CheckJudgeBlindness)
	return srv
}

func (s *Server) StartBackground(ctx context.Context) {
	s.hub.Start(ctx)
}

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Logger)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"http://localhost:3000", "http://localhost:5173", "http://localhost"},
		AllowedMethods:   []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions},
		AllowedHeaders:   []string{"Accept", "Content-Type", "X-CSRF-Token"},
		AllowCredentials: true,
		MaxAge:           300,
	}))
	r.Use(s.authS.Middleware(s.cfg.CookieSecure))

	r.Route("/api", func(r chi.Router) {
		r.Get("/healthz", s.health)

		r.Route("/auth", func(r chi.Router) {
			r.Post("/login", s.authH.Login)
			r.Group(func(r chi.Router) {
				r.Use(auth.RequireAuth)
				r.Use(auth.RequireCSRF)
				r.Post("/logout", s.authH.Logout)
			})
		})
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireAuth)
			r.Use(auth.RequireCSRF)
			r.Get("/me", s.authH.Me)
			r.Get("/my-roles", s.authH.MyRoles)
			r.Post("/hackathons", s.hackH.Create)
		})

		r.Post("/v1/ingest/metrics", s.ingH.Ingest)

		r.Get("/hackathons", s.hackH.List)

		r.Route("/hackathons/{id}", func(r chi.Router) {
			r.Get("/", s.hackH.Get)
			r.Get("/teams", s.teamH.List)
			r.Get("/teams/{teamId}", s.teamH.Get)
			r.Get("/tracks", s.trackH.List)
			r.Get("/submissions/{teamId}", s.subH.Get)
			r.Get("/metrics", s.metrH.List)
			r.Get("/leaderboard", s.leadH.List)
			r.Get("/leaderboard/{teamId}", s.leadH.TeamDetail)
			r.Get("/leaderboard/events", s.hub.Events)

		r.Group(func(r chi.Router) {
			r.Use(auth.RequireAuth)
			r.Use(auth.RequireCSRF)
			r.Post("/teams", s.teamH.Create)
			r.Put("/submissions/{teamId}", s.subH.Put)
			r.Get("/my-team", s.teamH.MyTeam)
		})

			r.Group(func(r chi.Router) {
				r.Use(auth.RequireAuth)
				r.Use(auth.RequireCSRF)
			r.Use(s.rbac.Require(rbac.RoleJudge, "id"))
			r.Put("/judging/scores/{teamId}", s.judH.PutScores)
			r.Get("/judging/scores/{teamId}", s.judH.GetMyScores)
			r.Post("/judging/recusal", s.judH.Recusal)
			r.Get("/judging/my-assignments", s.judH.MyAssignments)
			})

			r.Group(func(r chi.Router) {
				r.Use(auth.RequireAuth)
				r.Use(auth.RequireCSRF)
				r.Use(s.rbac.Require(rbac.RoleOrganizer, "id"))
				r.Post("/metrics", s.metrH.Create)
				r.Post("/rubric/criteria", s.metrH.CreateCriterion)
				r.Post("/tracks", s.trackH.Create)
				r.Patch("/tracks/{trackId}", s.trackH.Update)
				r.Get("/audit", s.audH.List)
				r.Post("/ingest-sources", s.ingH.CreateSource)
				r.Get("/ingest-sources", s.ingH.ListSources)
				r.Post("/scoring/recalculate", s.scorH.Recalculate)
				r.Post("/finalize", s.scorH.Finalize)
				r.Get("/score-runs", s.scorH.ListRuns)
				r.Post("/scoring/lock", s.scorH.SetScoringLock)
				r.Post("/status", s.hackH.SetStatus)
				r.Get("/judging/assignments", s.judH.ListAssignments)
				r.Post("/judging/auto-assign", s.judH.AutoAssign)
				r.Post("/judging/assign", s.judH.AssignJudge)
			})
		})
	})

	return r
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.pool.Ping(r.Context()); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "db_unavailable", "database is not reachable")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
