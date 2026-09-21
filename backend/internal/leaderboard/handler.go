package leaderboard

import (
	"errors"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hackaton-platform/backend/internal/httpx"
	"github.com/hackaton-platform/backend/internal/rbac"
	"github.com/hackaton-platform/backend/internal/tracks"
)

type Handler struct {
	pool  *pgxpool.Pool
	resolver *rbac.Resolver
}

func NewHandler(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool, resolver: rbac.NewResolver(pool)}
}

type LeaderRow struct {
	Rank         int       `json:"rank"`
	TeamID       uuid.UUID `json:"team_id"`
	TeamName     string    `json:"team_name"`
	Track        string    `json:"track"`
	Score        float64   `json:"score"`
	ScoreDelta   *float64  `json:"score_delta"`
	RankDelta    *int      `json:"rank_delta"`
	Completeness float64   `json:"completeness"`
	Status       string    `json:"status"`
	ComputedAt   time.Time `json:"last_update"`
	Sparkline    []float64 `json:"sparkline"`
}

type LeaderboardMeta struct {
	RunID       uuid.UUID `json:"run_id"`
	Mode        string    `json:"mode"`
	ConfigHash  string    `json:"config_hash"`
	Version     int       `json:"version"`
	GeneratedAt time.Time `json:"generated_at"`
	Finalized   bool      `json:"finalized"`
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	hackathonID, ok := hackathonParam(w, r)
	if !ok {
		return
	}
	if err := h.checkJudgeBlindness(w, r, hackathonID); err != nil {
		return
	}
	trackID, track, err := tracks.ResolveTrackID(r.Context(), h.pool, hackathonID, r.URL.Query().Get("track"))
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}

	runID, meta, err := h.currentRun(r, hackathonID, trackID)
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	if runID == nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"leaderboard": []LeaderRow{},
			"meta":        nil,
		})
		return
	}

	// Ranks are computed per track by the scoring run, so every row here
	// already belongs to the requested track's leaderboard.
	rows, err := h.pool.Query(r.Context(), `
		SELECT ts.rank, ts.team_id, t.name, $2, ts.score, ts.completeness, ts.status, ts.computed_at
		FROM team_scores ts
		JOIN teams t ON t.id = ts.team_id
		WHERE ts.score_run_id = $1
		ORDER BY ts.score DESC, t.name`, runID, track.Key)
	if err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to load leaderboard", err))
		return
	}
	defer rows.Close()

	leaders := []LeaderRow{}
	for rows.Next() {
		var l LeaderRow
		if err := rows.Scan(&l.Rank, &l.TeamID, &l.TeamName, &l.Track, &l.Score, &l.Completeness, &l.Status, &l.ComputedAt); err != nil {
			httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to scan leaderboard row", err))
			return
		}
		if meta.Mode == "final" && l.Rank == 0 {
			continue
		}
		leaders = append(leaders, l)
	}
	if err := rows.Err(); err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "leaderboard query failed", err))
		return
	}

	h.attachDeltas(r, hackathonID, runID, leaders)
	h.attachSparklines(r, hackathonID, leaders)

	if status := r.URL.Query().Get("status"); status == "provisional" || status == "complete" {
		filtered := leaders[:0]
		for _, l := range leaders {
			if l.Status == status {
				filtered = append(filtered, l)
			}
		}
		leaders = filtered
	}
	if q := r.URL.Query().Get("q"); q != "" {
		filtered := leaders[:0]
		for _, l := range leaders {
			if containsFold(l.TeamName, q) {
				filtered = append(filtered, l)
			}
		}
		leaders = filtered
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"leaderboard": leaders,
		"meta":        meta,
	})
}

func (h *Handler) TeamDetail(w http.ResponseWriter, r *http.Request) {
	hackathonID, ok := hackathonParam(w, r)
	if !ok {
		return
	}
	if err := h.checkJudgeBlindness(w, r, hackathonID); err != nil {
		return
	}
	teamID, err := uuid.Parse(chi.URLParam(r, "teamId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid team id")
		return
	}
	// The team's own track decides which leaderboard it belongs to.
	var teamTrackID uuid.UUID
	var teamTrackKey string
	if err := h.pool.QueryRow(r.Context(), `
		SELECT t.track_id, tr.key FROM teams t JOIN tracks tr ON tr.id = t.track_id
		WHERE t.id = $1 AND t.hackathon_id = $2`, teamID, hackathonID,
	).Scan(&teamTrackID, &teamTrackKey); err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusNotFound, "not_found", "team not found", err))
		return
	}

	runID, meta, err := h.currentRun(r, hackathonID, teamTrackID)
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	if runID == nil {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "no scores available yet")
		return
	}

	var l LeaderRow
	var breakdown []map[string]any
	err = h.pool.QueryRow(r.Context(), `
		SELECT ts.rank, ts.team_id, t.name, $3, ts.score, ts.completeness, ts.status, ts.computed_at, ts.breakdown
		FROM team_scores ts
		JOIN teams t ON t.id = ts.team_id
		WHERE ts.score_run_id = $1 AND ts.team_id = $2`, runID, teamID, teamTrackKey,
	).Scan(&l.Rank, &l.TeamID, &l.TeamName, &l.Track, &l.Score, &l.Completeness, &l.Status, &l.ComputedAt, &breakdown)
	if err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusNotFound, "not_found", "team not scored yet", err))
		return
	}

	history := []map[string]any{}
	hrows, err := h.pool.Query(r.Context(), `
		SELECT score, rank, captured_at FROM score_snapshots
		WHERE hackathon_id = $1 AND team_id = $2 ORDER BY captured_at ASC LIMIT 500`, hackathonID, teamID)
	if err == nil {
		defer hrows.Close()
		for hrows.Next() {
			var score float64
			var rank int
			var captured time.Time
			if hrows.Scan(&score, &rank, &captured) == nil {
				history = append(history, map[string]any{
					"score": score, "rank": rank, "captured_at": captured,
				})
			}
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"team":      l,
		"meta":      meta,
		"breakdown": breakdown,
		"history":   history,
	})
}

func (h *Handler) currentRun(r *http.Request, hackathonID, trackID uuid.UUID) (*uuid.UUID, *LeaderboardMeta, error) {
	var status string
	if err := h.pool.QueryRow(r.Context(),
		`SELECT status FROM hackathons WHERE id = $1`, hackathonID).Scan(&status); err != nil {
		return nil, nil, httpx.NewStatusError(http.StatusNotFound, "not_found", "hackathon not found", err)
	}
	mode := "live"
	if status == "finalized" {
		mode = "final"
	}
	var runID uuid.UUID
	var meta LeaderboardMeta
	err := h.pool.QueryRow(r.Context(), `
		SELECT id, config_hash, version, completed_at FROM score_runs
		WHERE track_id = $1 AND mode = $2 AND status = 'completed'
		ORDER BY started_at DESC LIMIT 1`, trackID, mode,
	).Scan(&runID, &meta.ConfigHash, &meta.Version, &meta.GeneratedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, nil
		}
		return nil, nil, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to load run", err)
	}
	meta.RunID = runID
	meta.Mode = mode
	meta.Finalized = status == "finalized"
	return &runID, &meta, nil
}

func (h *Handler) attachDeltas(r *http.Request, hackathonID uuid.UUID, runID *uuid.UUID, leaders []LeaderRow) {
	ids := make([]uuid.UUID, 0, len(leaders))
	for _, l := range leaders {
		ids = append(ids, l.TeamID)
	}
	if len(ids) == 0 {
		return
	}
	rows, err := h.pool.Query(r.Context(), `
		SELECT DISTINCT ON (team_id) team_id, score, rank
		FROM score_snapshots
		WHERE hackathon_id = $1 AND score_run_id <> $2
		ORDER BY team_id, captured_at DESC`, hackathonID, *runID)
	if err != nil {
		return
	}
	defer rows.Close()
	prevScore := map[uuid.UUID]float64{}
	prevRank := map[uuid.UUID]int{}
	for rows.Next() {
		var id uuid.UUID
		var s float64
		var rk int
		if rows.Scan(&id, &s, &rk) == nil {
			prevScore[id] = s
			prevRank[id] = rk
		}
	}
	for i := range leaders {
		if s, ok := prevScore[leaders[i].TeamID]; ok {
			d := round2(leaders[i].Score - s)
			leaders[i].ScoreDelta = &d
		}
		if rk, ok := prevRank[leaders[i].TeamID]; ok {
			d := rk - leaders[i].Rank
			leaders[i].RankDelta = &d
		}
	}
}

func (h *Handler) attachSparklines(r *http.Request, hackathonID uuid.UUID, leaders []LeaderRow) {
	rows, err := h.pool.Query(r.Context(), `
		SELECT team_id, score FROM (
			SELECT team_id, score, row_number() OVER (PARTITION BY team_id ORDER BY captured_at DESC) AS rn
			FROM score_snapshots WHERE hackathon_id = $1
		) sub WHERE rn <= 20 ORDER BY team_id, rn DESC`, hackathonID)
	if err != nil {
		return
	}
	defer rows.Close()
	sparks := map[uuid.UUID][]float64{}
	for rows.Next() {
		var id uuid.UUID
		var s float64
		if rows.Scan(&id, &s) == nil {
			sparks[id] = append(sparks[id], s)
		}
	}
	for i := range leaders {
		if sp, ok := sparks[leaders[i].TeamID]; ok {
			leaders[i].Sparkline = sp
		} else {
			leaders[i].Sparkline = []float64{}
		}
	}
}

func (h *Handler) CheckJudgeBlindness(w http.ResponseWriter, r *http.Request, hackathonID uuid.UUID) error {
	return h.checkJudgeBlindness(w, r, hackathonID)
}

func (h *Handler) checkJudgeBlindness(w http.ResponseWriter, r *http.Request, hackathonID uuid.UUID) error {
	var status string
	if err := h.pool.QueryRow(r.Context(),
		`SELECT status FROM hackathons WHERE id = $1`, hackathonID).Scan(&status); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "hackathon not found")
		return errJudgeBlind
	}
	role, _, err := h.resolver.RoleFromContext(r.Context(), hackathonID)
	if err != nil {
		httpx.WriteErr(w, err)
		return err
	}
	if role == rbac.RoleJudge && status != "finalized" {
		httpx.WriteError(w, http.StatusForbidden, "judge_blind", "judges cannot view the leaderboard while judging is in progress")
		return errJudgeBlind
	}
	return nil
}

var errJudgeBlind = &staticError{}

type staticError struct{}

func (*staticError) Error() string { return "judge blind" }

func containsFold(s, q string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(q))
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func hackathonParam(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid hackathon id")
		return uuid.Nil, false
	}
	return id, true
}
