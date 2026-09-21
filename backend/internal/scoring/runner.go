package scoring

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Runner struct {
	pool *pgxpool.Pool
}

func NewRunner(pool *pgxpool.Pool) *Runner {
	return &Runner{pool: pool}
}

var ErrScoringLocked = errors.New("scoring is locked for this hackathon")
var ErrAlreadyFinalized = errors.New("hackathon is already finalized")

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type RunSummary struct {
	RunID         uuid.UUID  `json:"run_id"`
	HackathonID   uuid.UUID  `json:"hackathon_id"`
	TrackID       uuid.UUID  `json:"track_id"`
	Version       int        `json:"version"`
	ConfigHash    string     `json:"config_hash"`
	Mode          string     `json:"mode"`
	Status        string     `json:"status"`
	TeamsScored   int        `json:"teams_scored"`
	TeamsEligible int        `json:"teams_eligible"`
	StartedAt     time.Time  `json:"started_at"`
	CompletedAt   *time.Time `json:"completed_at"`
	Error         string     `json:"error,omitempty"`
}

// Run computes and stores scores for one track in a single transaction guarded
// by a per-track advisory lock, so concurrent runs (worker debounce, periodic
// recalc, manual API calls) serialize instead of racing. Final mode ignores
// the track scoring lock (finalization must always be possible); live mode
// respects it. Finalization of the hackathon itself (status flip) is done by
// the Finalize handler after all tracks complete.
func (r *Runner) Run(ctx context.Context, hackathonID, trackID uuid.UUID, mode string) (*RunSummary, error) {
	if mode != "live" && mode != "final" {
		return nil, fmt.Errorf("invalid mode %q", mode)
	}

	startedAt := time.Now().UTC()

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, hackathonID.String()+":"+trackID.String()); err != nil {
		return nil, fmt.Errorf("acquire scoring lock: %w", err)
	}

	var version int
	var minJudges int
	var status string
	var locked bool
	if err := tx.QueryRow(ctx, `
		SELECT tr.scoring_version, tr.min_judges_per_team, h.status, tr.scoring_locked
		FROM tracks tr JOIN hackathons h ON h.id = tr.hackathon_id
		WHERE tr.id = $1 AND tr.hackathon_id = $2`,
		trackID, hackathonID).Scan(&version, &minJudges, &status, &locked); err != nil {
		return nil, fmt.Errorf("load track: %w", err)
	}
	if status == "finalized" {
		return nil, ErrAlreadyFinalized
	}
	if locked && mode == "live" {
		return nil, ErrScoringLocked
	}

	defs, criteria, err := loadConfig(ctx, tx, trackID, mode)
	if err != nil {
		return nil, err
	}
	configHash, err := computeConfigHash(version, defs, criteria, minJudges)
	if err != nil {
		return nil, err
	}

	var runID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO score_runs (hackathon_id, track_id, version, config_hash, mode, status, started_at)
		VALUES ($1, $2, $3, $4, $5, 'running', $6) RETURNING id`,
		hackathonID, trackID, version, configHash, mode, startedAt).Scan(&runID)
	if err != nil {
		return nil, fmt.Errorf("create score_run: %w", err)
	}

	summary, err := computeAndStore(ctx, tx, hackathonID, trackID, runID, mode, defs, criteria, minJudges)
	if err != nil {
		r.recordFailedRun(ctx, hackathonID, trackID, runID, version, configHash, mode, startedAt, err)
		notifyScoreUpdatedStatus(ctx, r.pool, hackathonID, trackID, runID, mode, "error")
		return &RunSummary{
			RunID: runID, HackathonID: hackathonID, TrackID: trackID, Version: version, ConfigHash: configHash,
			Mode: mode, Status: "error", TeamsScored: 0, TeamsEligible: 0,
			StartedAt: startedAt, Error: err.Error(),
		}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	summary.RunID = runID
	summary.HackathonID = hackathonID
	summary.TrackID = trackID
	summary.Version = version
	summary.ConfigHash = configHash
	summary.Mode = mode
	summary.Status = "completed"
	summary.StartedAt = startedAt
	now := time.Now().UTC()
	summary.CompletedAt = &now

	notifyScoreUpdatedStatus(ctx, r.pool, hackathonID, trackID, runID, mode, "completed")
	return summary, nil
}

// recordFailedRun re-inserts the score_run row with status 'error' after the main
// transaction rolled back, so failed runs stay visible in the run history.
func (r *Runner) recordFailedRun(ctx context.Context, hackathonID, trackID, runID uuid.UUID, version int, configHash, mode string, startedAt time.Time, runErr error) {
	errCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, _ = r.pool.Exec(errCtx, `
		INSERT INTO score_runs (id, hackathon_id, track_id, version, config_hash, mode, status, error, started_at, completed_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'error', $7, $8, now())`,
		runID, hackathonID, trackID, version, configHash, mode, runErr.Error(), startedAt)
}

type metricDefRow struct {
	ID            uuid.UUID
	Key           string
	Name          string
	Weight        float64
	Required      bool
	Public        bool
	Direction     string
	Normalization Normalization
	Source        string
	Staleness     int
}

type criterionRow struct {
	ID       uuid.UUID
	Key      string
	Name     string
	Weight   float64
	Required bool
	Public   bool
	ScaleMin float64
	ScaleMax float64
}

func loadConfig(ctx context.Context, q querier, trackID uuid.UUID, mode string) ([]metricDefRow, []criterionRow, error) {
	publicFilter := ""
	if mode == "live" {
		publicFilter = " AND public = true"
	}
	drows, err := q.Query(ctx, `
		SELECT id, key, name, weight, required, public, direction, normalization, source, staleness_seconds
		FROM metric_definitions WHERE track_id = $1 AND type = 'automated'`+publicFilter+`
		ORDER BY sort_order, key`, trackID)
	if err != nil {
		return nil, nil, fmt.Errorf("load metric definitions: %w", err)
	}
	defer drows.Close()

	defs := []metricDefRow{}
	for drows.Next() {
		var d metricDefRow
		var normJSON []byte
		if err := drows.Scan(&d.ID, &d.Key, &d.Name, &d.Weight, &d.Required, &d.Public, &d.Direction, &normJSON, &d.Source, &d.Staleness); err != nil {
			return nil, nil, fmt.Errorf("scan metric definition: %w", err)
		}
		if err := json.Unmarshal(normJSON, &d.Normalization); err != nil {
			d.Normalization = Normalization{Kind: KindBinary}
		}
		defs = append(defs, d)
	}
	if err := drows.Err(); err != nil {
		return nil, nil, err
	}

	cFilter := ""
	if mode == "live" {
		cFilter = " AND public = true"
	}
	crows, err := q.Query(ctx, `
		SELECT id, key, name, weight, required, public, scale_min, scale_max
		FROM rubric_criteria WHERE track_id = $1`+cFilter+`
		ORDER BY sort_order, key`, trackID)
	if err != nil {
		return nil, nil, fmt.Errorf("load rubric criteria: %w", err)
	}
	defer crows.Close()

	criteria := []criterionRow{}
	for crows.Next() {
		var c criterionRow
		if err := crows.Scan(&c.ID, &c.Key, &c.Name, &c.Weight, &c.Required, &c.Public, &c.ScaleMin, &c.ScaleMax); err != nil {
			return nil, nil, fmt.Errorf("scan rubric criterion: %w", err)
		}
		criteria = append(criteria, c)
	}
	if err := crows.Err(); err != nil {
		return nil, nil, err
	}
	return defs, criteria, nil
}

func computeConfigHash(version int, defs []metricDefRow, criteria []criterionRow, minJudges int) (string, error) {
	type cfgMetric struct {
		Key           string        `json:"key"`
		Weight        float64       `json:"weight"`
		Required      bool          `json:"required"`
		Public        bool          `json:"public"`
		Direction     string        `json:"direction"`
		Normalization Normalization `json:"normalization"`
	}
	type cfgCriterion struct {
		Key      string  `json:"key"`
		Weight   float64 `json:"weight"`
		Required bool    `json:"required"`
		Public   bool    `json:"public"`
		ScaleMin float64 `json:"scale_min"`
		ScaleMax float64 `json:"scale_max"`
	}
	cfg := struct {
		Version   int            `json:"version"`
		MinJudges int            `json:"min_judges"`
		Metrics   []cfgMetric    `json:"metrics"`
		Criteria  []cfgCriterion `json:"criteria"`
	}{Version: version, MinJudges: minJudges}
	for _, d := range defs {
		cfg.Metrics = append(cfg.Metrics, cfgMetric{d.Key, d.Weight, d.Required, d.Public, d.Direction, d.Normalization})
	}
	for _, c := range criteria {
		cfg.Criteria = append(cfg.Criteria, cfgCriterion{c.Key, c.Weight, c.Required, c.Public, c.ScaleMin, c.ScaleMax})
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

type teamRow struct {
	ID   uuid.UUID
	Name string
}

func computeAndStore(ctx context.Context, q querier, hackathonID, trackID, runID uuid.UUID, mode string,
	defs []metricDefRow, criteria []criterionRow, minJudges int) (*RunSummary, error) {

	teams, err := loadTeams(ctx, q, trackID)
	if err != nil {
		return nil, err
	}

	latestValues, err := loadLatestMetricValues(ctx, q, hackathonID)
	if err != nil {
		return nil, err
	}
	judgeScores, err := loadEffectiveJudgeScores(ctx, q, hackathonID)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	type teamOutcome struct {
		team   teamRow
		result TeamResult
	}
	outcomes := make([]teamOutcome, 0, len(teams))

	for _, t := range teams {
		input := buildTeamInput(defs, criteria, latestValues[t.ID], judgeScores[t.ID])
		result := ComputeTeam(input, now, minJudges)
		outcomes = append(outcomes, teamOutcome{team: t, result: result})
	}

	sort.SliceStable(outcomes, func(i, j int) bool {
		return outcomes[i].result.Score > outcomes[j].result.Score
	})

	results := make([]TeamResult, len(outcomes))
	for i := range outcomes {
		results[i] = outcomes[i].result
	}
	ranks := assignRanks(results, mode)

	eligible := 0
	for i, o := range outcomes {
		breakdownJSON, err := json.Marshal(o.result.Breakdown)
		if err != nil {
			return nil, fmt.Errorf("marshal breakdown: %w", err)
		}
		if _, err := q.Exec(ctx, `
			INSERT INTO team_scores (hackathon_id, team_id, score_run_id, score, rank, completeness, status, breakdown, computed_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			hackathonID, o.team.ID, runID, o.result.Score, ranks[i], o.result.Completeness, o.result.Status, breakdownJSON, now,
		); err != nil {
			return nil, fmt.Errorf("store team score: %w", err)
		}
		if _, err := q.Exec(ctx, `
			INSERT INTO score_snapshots (hackathon_id, team_id, score_run_id, mode, score, rank, captured_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			hackathonID, o.team.ID, runID, mode, o.result.Score, ranks[i], now,
		); err != nil {
			return nil, fmt.Errorf("store snapshot: %w", err)
		}
		if o.result.Eligible {
			eligible++
		}
	}

	if _, err := q.Exec(ctx, `
		UPDATE score_runs SET status = 'completed', completed_at = now() WHERE id = $1`, runID); err != nil {
		return nil, fmt.Errorf("complete run: %w", err)
	}

	return &RunSummary{TeamsScored: len(outcomes), TeamsEligible: eligible}, nil
}

func loadTeams(ctx context.Context, q querier, trackID uuid.UUID) ([]teamRow, error) {
	rows, err := q.Query(ctx,
		`SELECT id, name FROM teams WHERE track_id = $1 ORDER BY name`, trackID)
	if err != nil {
		return nil, fmt.Errorf("load teams: %w", err)
	}
	defer rows.Close()
	teams := []teamRow{}
	for rows.Next() {
		var t teamRow
		if err := rows.Scan(&t.ID, &t.Name); err != nil {
			return nil, err
		}
		teams = append(teams, t)
	}
	return teams, rows.Err()
}

type metricValueRow struct {
	TeamID     uuid.UUID
	DefID      uuid.UUID
	RawValue   float64
	CapturedAt time.Time
	Source     string
}

func loadLatestMetricValues(ctx context.Context, q querier, hackathonID uuid.UUID) (map[uuid.UUID]map[uuid.UUID]metricValueRow, error) {
	rows, err := q.Query(ctx, `
		SELECT DISTINCT ON (team_id, metric_definition_id)
			team_id, metric_definition_id, raw_value, captured_at, source
		FROM metric_values
		WHERE hackathon_id = $1 AND status = 'valid'
		ORDER BY team_id, metric_definition_id, captured_at DESC, received_at DESC`, hackathonID)
	if err != nil {
		return nil, fmt.Errorf("load metric values: %w", err)
	}
	defer rows.Close()

	result := map[uuid.UUID]map[uuid.UUID]metricValueRow{}
	for rows.Next() {
		var v metricValueRow
		if err := rows.Scan(&v.TeamID, &v.DefID, &v.RawValue, &v.CapturedAt, &v.Source); err != nil {
			return nil, err
		}
		if result[v.TeamID] == nil {
			result[v.TeamID] = map[uuid.UUID]metricValueRow{}
		}
		result[v.TeamID][v.DefID] = v
	}
	return result, rows.Err()
}

type judgeScoreRow struct {
	TeamID      uuid.UUID
	CriterionID uuid.UUID
	RawValue    float64
	CreatedAt   time.Time
}

func loadEffectiveJudgeScores(ctx context.Context, q querier, hackathonID uuid.UUID) (map[uuid.UUID]map[uuid.UUID][]float64, error) {
	rows, err := q.Query(ctx, `
		SELECT DISTINCT ON (js.team_id, js.criterion_id, js.judge_id)
			js.team_id, js.criterion_id, js.raw_value, js.created_at
		FROM judge_scores js
		JOIN judge_assignments ja
			ON ja.hackathon_id = js.hackathon_id
			AND ja.judge_id = js.judge_id
			AND ja.team_id = js.team_id
			AND ja.status = 'assigned'
			AND js.created_at >= ja.scores_valid_from
		WHERE js.hackathon_id = $1
		ORDER BY js.team_id, js.criterion_id, js.judge_id, js.created_at DESC, js.id DESC`, hackathonID)
	if err != nil {
		return nil, fmt.Errorf("load judge scores: %w", err)
	}
	defer rows.Close()

	result := map[uuid.UUID]map[uuid.UUID][]float64{}
	for rows.Next() {
		var s judgeScoreRow
		if err := rows.Scan(&s.TeamID, &s.CriterionID, &s.RawValue, &s.CreatedAt); err != nil {
			return nil, err
		}
		if result[s.TeamID] == nil {
			result[s.TeamID] = map[uuid.UUID][]float64{}
		}
		result[s.TeamID][s.CriterionID] = append(result[s.TeamID][s.CriterionID], s.RawValue)
	}
	return result, rows.Err()
}

func buildTeamInput(defs []metricDefRow, criteria []criterionRow,
	values map[uuid.UUID]metricValueRow, scores map[uuid.UUID][]float64) []MetricInput {

	input := make([]MetricInput, 0, len(defs)+len(criteria))
	for _, d := range defs {
		mi := MetricInput{
			Key: d.Key, Name: d.Name, Type: "automated",
			Weight: d.Weight, Required: d.Required, Public: d.Public,
			Direction: d.Direction, Normalization: d.Normalization,
			Source: d.Source, Staleness: time.Duration(d.Staleness) * time.Second,
		}
		if v, ok := values[d.ID]; ok {
			raw := v.RawValue
			captured := v.CapturedAt
			mi.RawValue = &raw
			mi.CapturedAt = &captured
			mi.Source = v.Source
		}
		input = append(input, mi)
	}
	for _, c := range criteria {
		input = append(input, MetricInput{
			Key: c.Key, Name: c.Name, Type: "manual",
			Weight: c.Weight, Required: c.Required, Public: c.Public,
			ScaleMin: c.ScaleMin, ScaleMax: c.ScaleMax,
			JudgeScores: scores[c.ID],
		})
	}
	return input
}

func notifyScoreUpdatedStatus(ctx context.Context, pool *pgxpool.Pool, hackathonID, trackID, runID uuid.UUID, mode, status string) {
	notifyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	payload, _ := json.Marshal(map[string]string{
		"hackathon_id": hackathonID.String(),
		"track_id":     trackID.String(),
		"run_id":       runID.String(),
		"mode":         mode,
		"status":       status,
	})
	pool.Exec(notifyCtx, `SELECT pg_notify('score_updated', $1)`, string(payload))
}

// assignRanks produces competition ranking (ties share a rank, next rank skips)
// over results sorted by score descending. In final mode ineligible teams get
// rank 0 and do not consume rank positions.
func assignRanks(results []TeamResult, mode string) []int {
	ranks := make([]int, len(results))
	currentRank := 0
	counted := 0
	lastScore := -1.0
	for i, res := range results {
		if mode == "final" && !res.Eligible {
			ranks[i] = 0
			continue
		}
		if res.Score != lastScore {
			currentRank = counted + 1
			lastScore = res.Score
		}
		ranks[i] = currentRank
		counted++
	}
	return ranks
}
