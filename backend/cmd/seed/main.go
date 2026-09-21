package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hackaton-platform/backend/internal/auth"
	"github.com/hackaton-platform/backend/internal/config"
	"github.com/hackaton-platform/backend/internal/db"
	"github.com/hackaton-platform/backend/internal/scoring"
	"github.com/hackaton-platform/backend/migrations"
)

const (
	password    = "demo1234"
	fixedToken  = "demo-collector-token"
	teamsCount  = 50
	judgesCount = 8
	historyHours = 6
)

var (
	hackathonID uuid.UUID
	organizerID uuid.UUID
	judgeIDs    []uuid.UUID
	teamIDs     []uuid.UUID
	teamTracks  = map[string]string{}
	rng         = rand.New(rand.NewSource(42))
	// Two demo tracks with deliberately different scoring configs to showcase
	// per-track leaderboards and scoring isolation.
	seedTracks = []struct {
		Key       string
		Name      string
		MinJudges int
		Weights   map[string]float64
	}{
		{"ai", "AI & ML", 3, map[string]float64{
			"api_latency": 15, "uptime": 10, "test_pass_rate": 10, "bundle_size": 5, "a11y_score": 10,
			"tech_execution": 30, "product_usefulness": 25, "originality": 20, "ux": 15, "demo": 10,
		}},
		{"web", "Web", 2, map[string]float64{
			"api_latency": 5, "uptime": 5, "test_pass_rate": 10, "bundle_size": 20, "a11y_score": 10,
			"tech_execution": 20, "product_usefulness": 20, "originality": 15, "ux": 25, "demo": 20,
		}},
	}
	trackIDs   = map[string]uuid.UUID{}
)

type metricSpec struct {
	Key  string
	Name string
	Dir  string
	Norm string
}

var automatedMetrics = []metricSpec{
	{"api_latency", "API p95 latency", "lower_is_better", `{"kind":"range","min":50,"max":500}`},
	{"uptime", "Uptime check", "higher_is_better", `{"kind":"binary"}`},
	{"test_pass_rate", "Test pass rate", "higher_is_better", `{"kind":"range","min":0,"max":100}`},
	{"bundle_size", "Bundle size KB", "lower_is_better", `{"kind":"target","target":300,"lower":100,"upper":1500}`},
	{"a11y_score", "Accessibility score", "higher_is_better", `{"kind":"range","min":0,"max":100}`},
}

var rubric = []struct {
	Key  string
	Name string
}{
	{"tech_execution", "Technical execution"},
	{"product_usefulness", "Product usefulness"},
	{"originality", "Originality"},
	{"ux", "UX"},
	{"demo", "Demo"},
}

func main() {
	cfg := config.Load()
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		fail("invalid database url: %v", err)
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool, migrations.FS); err != nil {
		fail("migration failed: %v", err)
	}

	if len(os.Args) > 1 && os.Args[1] == "reset" {
		triggers := []string{
			"trg_metric_values_append_only",
			"trg_judge_scores_append_only",
			"trg_audit_events_append_only",
			"trg_score_snapshots_append_only",
		}
		for _, t := range triggers {
			if _, err := pool.Exec(ctx, fmt.Sprintf(`ALTER TABLE %s DISABLE TRIGGER %s`, triggerTable(t), t)); err != nil {
				fail("disable trigger %s failed: %v", t, err)
			}
		}
		if _, err := pool.Exec(ctx, `DELETE FROM hackathons`); err != nil {
			fail("reset failed: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM users WHERE email LIKE '%@demo.io'`); err != nil {
			fail("reset users failed: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM audit_events`); err != nil {
			fail("reset audit failed: %v", err)
		}
		for _, t := range triggers {
			if _, err := pool.Exec(ctx, fmt.Sprintf(`ALTER TABLE %s ENABLE TRIGGER %s`, triggerTable(t), t)); err != nil {
				fail("enable trigger %s failed: %v", t, err)
			}
		}
		fmt.Println("existing demo data removed")
	}

	var exists bool
	err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM hackathons WHERE slug = 'demo-hack-2026')`).Scan(&exists)
	if err != nil {
		fail("check failed: %v", err)
	}
	if exists {
		fmt.Println("demo hackathon already seeded (run 'seed reset' to reseed)")
		os.Exit(0)
	}

	seedUsers(ctx, pool)
	seedHackathon(ctx, pool)
	seedCriteriaAndMetrics(ctx, pool)
	seedTeamsAndSubmissions(ctx, pool)
	seedMetricHistory(ctx, pool)
	seedJudging(ctx, pool)
	seedSnapshots(ctx, pool)
	seedIngestSource(ctx, pool)

	if _, err := scoring.NewRunner(pool).Run(ctx, hackathonID, trackIDs["ai"], "live"); err != nil {
		fail("initial scoring run failed: %v", err)
	}
	if _, err := scoring.NewRunner(pool).Run(ctx, hackathonID, trackIDs["web"], "live"); err != nil {
		fail("initial scoring run failed: %v", err)
	}

	fmt.Println("seed done:")
	fmt.Printf("  hackathon:  demo-hack-2026 (%s)\n", hackathonID)
	fmt.Printf("  teams:      %d\n", teamsCount)
	fmt.Printf("  judges:     %d\n", judgesCount)
	fmt.Printf("  logins:     organizer@demo.io / judge1..8@demo.io / member001..150@demo.io (password: %s)\n", password)
	fmt.Printf("  ingest token: %s\n", fixedToken)
}

func seedUsers(ctx context.Context, pool *pgxpool.Pool) {
	hash, _ := auth.HashPassword(password)
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO users (id, email, password_hash, name) VALUES ($1,$2,$3,$4)`,
		mustUUID("11111111-1111-4111-8111-000000000001"), "organizer@demo.io", hash, "Alex Organizer")
	for i := 1; i <= judgesCount; i++ {
		batch.Queue(`INSERT INTO users (id, email, password_hash, name) VALUES ($1,$2,$3,$4)`,
			mustUUID(fmt.Sprintf("22222222-2222-4222-8222-%012d", i)),
			fmt.Sprintf("judge%d@demo.io", i), hash, fmt.Sprintf("Judge %d", i))
	}
	for i := 1; i <= teamsCount*3; i++ {
		batch.Queue(`INSERT INTO users (id, email, password_hash, name) VALUES ($1,$2,$3,$4)`,
			mustUUID(fmt.Sprintf("33333333-3333-4333-8333-%012d", i)),
			fmt.Sprintf("member%03d@demo.io", i), hash, fmt.Sprintf("Member %03d", i))
	}
	if err := pool.SendBatch(ctx, batch).Close(); err != nil {
		fail("seed users failed: %v", err)
	}
	organizerID = mustUUID("11111111-1111-4111-8111-000000000001")
	for i := 1; i <= judgesCount; i++ {
		judgeIDs = append(judgeIDs, mustUUID(fmt.Sprintf("22222222-2222-4222-8222-%012d", i)))
	}
}

func seedHackathon(ctx context.Context, pool *pgxpool.Pool) {
	now := time.Now().UTC()
	hackathonID = mustUUID("44444444-4444-4444-8444-000000000001")
	_, err := pool.Exec(ctx, `
		INSERT INTO hackathons (id, slug, name, description, status, organizer_id, starts_at, ends_at, judging_ends_at)
		VALUES ($1,'demo-hack-2026','Demo Hackathon 2026','Seeded demo hackathon with live scoring','running',$2,$3,$4,$5)`,
		hackathonID, organizerID,
		now.Add(-time.Duration(historyHours)*time.Hour), now.Add(18*time.Hour), now.Add(26*time.Hour))
	if err != nil {
		fail("seed hackathon failed: %v", err)
	}
	for i, t := range seedTracks {
		id := mustUUID(fmt.Sprintf("44444444-4444-4444-9444-%012d", i+1))
		trackIDs[t.Key] = id
		if _, err := pool.Exec(ctx, `
			INSERT INTO tracks (id, hackathon_id, key, name, min_judges_per_team, sort_order)
			VALUES ($1,$2,$3,$4,$5,$6)`,
			id, hackathonID, t.Key, t.Name, t.MinJudges, i); err != nil {
			fail("seed track %s failed: %v", t.Key, err)
		}
	}
}

func seedCriteriaAndMetrics(ctx context.Context, pool *pgxpool.Pool) {
	batch := &pgx.Batch{}
	for _, t := range seedTracks {
		trackID := trackIDs[t.Key]
		for i, m := range automatedMetrics {
			batch.Queue(`INSERT INTO metric_definitions
				(hackathon_id, track_id, key, name, description, type, weight, required, public, direction, normalization, source, staleness_seconds, sort_order)
				VALUES ($1,$2,$3,$4,$5,'automated',$6,true,true,$7,$8::jsonb,'simulator',1800,$9)`,
				hackathonID, trackID, m.Key, m.Name, m.Name, t.Weights[m.Key], m.Dir, m.Norm, i)
		}
		for i, c := range rubric {
			batch.Queue(`INSERT INTO rubric_criteria
				(hackathon_id, track_id, key, name, description, scale_min, scale_max, weight, required, public, sort_order)
				VALUES ($1,$2,$3,$4,$5,0,10,$6,true,true,$7)`,
				hackathonID, trackID, c.Key, c.Name, c.Name, t.Weights[c.Key], i)
		}
	}
	if err := pool.SendBatch(ctx, batch).Close(); err != nil {
		fail("seed metrics failed: %v", err)
	}
}

func seedTeamsAndSubmissions(ctx context.Context, pool *pgxpool.Pool) {
	metricRows := []struct{ id uuid.UUID; key string }{}
	rows, err := pool.Query(ctx, `SELECT id, key FROM metric_definitions WHERE hackathon_id = $1`, hackathonID)
	if err != nil {
		fail("load metrics failed: %v", err)
	}
	for rows.Next() {
		var m struct{ id uuid.UUID; key string }
		if rows.Scan(&m.id, &m.key) == nil {
			metricRows = append(metricRows, m)
		}
	}
	rows.Close()

	batch := &pgx.Batch{}
	now := time.Now().UTC()

	for t := 1; t <= teamsCount; t++ {
		teamID := mustUUID(fmt.Sprintf("55555555-5555-4555-8555-%012d", t))
		teamIDs = append(teamIDs, teamID)
		trackKey := seedTracks[(t-1)%len(seedTracks)].Key
		teamTracks[teamID.String()] = trackKey
		batch.Queue(`INSERT INTO teams (id, hackathon_id, track_id, name, created_by) VALUES ($1,$2,$3,$4,$5)`,
			teamID, hackathonID, trackIDs[trackKey], teamName(t), mustUUID(fmt.Sprintf("33333333-3333-4333-8333-%012d", t*3-2)))

		members := 2 + t%2
		for m := 0; m < members; m++ {
			uid := mustUUID(fmt.Sprintf("33333333-3333-4333-8333-%012d", t*3-2+m))
			batch.Queue(`INSERT INTO team_members (hackathon_id, team_id, user_id, is_captain) VALUES ($1,$2,$3,$4)`,
				hackathonID, teamID, uid, m == 0)
		}

		batch.Queue(`INSERT INTO submissions (hackathon_id, team_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
			hackathonID, teamID)
		batch.Queue(`INSERT INTO submission_versions
			(submission_id, version, title, description, track, repo_url, demo_url, app_url, extra_links, edited_by)
			SELECT su.id, 1, $2, $3, $4, $5, $6, $7, '[]'::jsonb, $8 FROM submissions su WHERE su.team_id = $1`,
			teamID, projectTitle(t), fmt.Sprintf("Project by team %d", t), trackKey,
			fmt.Sprintf("https://github.com/demo/team-%02d", t),
			fmt.Sprintf("https://demo-%02d.example.com", t),
			fmt.Sprintf("https://app-%02d.example.com", t),
			mustUUID(fmt.Sprintf("33333333-3333-4333-8333-%012d", t*3-2)))
		batch.Queue(`UPDATE submissions SET current_version = 1, updated_at = $3 WHERE team_id = $2 AND hackathon_id = $1`,
			hackathonID, teamID, now.Add(-time.Duration(historyHours-1)*time.Hour))
	}
	if err := pool.SendBatch(ctx, batch).Close(); err != nil {
		fail("seed teams failed: %v", err)
	}
}

func seedMetricHistory(ctx context.Context, pool *pgxpool.Pool) {
	type defRow struct {
		ID      uuid.UUID
		Key     string
		TrackID uuid.UUID
	}
	defs := []defRow{}
	rows, err := pool.Query(ctx, `SELECT id, key, track_id FROM metric_definitions WHERE hackathon_id = $1`, hackathonID)
	if err != nil {
		fail("load defs failed: %v", err)
	}
	for rows.Next() {
		var d defRow
		if rows.Scan(&d.ID, &d.Key, &d.TrackID) == nil {
			defs = append(defs, d)
		}
	}
	rows.Close()

	teamTrackIDs := map[string]uuid.UUID{}
	trows, err := pool.Query(ctx, `SELECT id, track_id FROM teams WHERE hackathon_id = $1`, hackathonID)
	if err != nil {
		fail("load team tracks failed: %v", err)
	}
	for trows.Next() {
		var id, trackID uuid.UUID
		if trows.Scan(&id, &trackID) == nil {
			teamTrackIDs[id.String()] = trackID
		}
	}
	trows.Close()

	now := time.Now().UTC()
	points := 12
	batch := &pgx.Batch{}

	for ti, teamID := range teamIDs {
		teamTrack := teamTrackIDs[teamID.String()]
		for _, d := range defs {
			if d.TrackID != teamTrack {
				continue
			}
			missing := rng.Float64() < 0.04
			stale := rng.Float64() < 0.06
			if missing {
				continue
			}
			for p := 0; p < points; p++ {
				if stale && p == points-1 && rng.Float64() < 0.5 {
					continue
				}
				captured := now.Add(-time.Duration(historyHours*60/points*(points-p)) * time.Minute)
				if stale && p == points-1 {
					captured = now.Add(-4 * time.Hour)
				}
				progress := float64(p) / float64(points-1)
				value := metricValue(d.Key, teamQuality(ti), progress)
				batch.Queue(`INSERT INTO metric_values
					(hackathon_id, team_id, metric_definition_id, external_event_id, raw_value, normalized_value, metadata, source, captured_at)
					VALUES ($1,$2,$3,$4,$5,$6,'{}'::jsonb,'simulator',$7)`,
					hackathonID, teamID, d.ID,
					fmt.Sprintf("seed-%s-%s-%d", d.Key, teamID, p), value, normalizedValue(d.Key, value), captured)
			}
		}
	}
	if err := pool.SendBatch(ctx, batch).Close(); err != nil {
		fail("seed metric history failed: %v", err)
	}
}

func seedJudging(ctx context.Context, pool *pgxpool.Pool) {
	type critRow struct {
		ID      uuid.UUID
		Key     string
		TrackID uuid.UUID
	}
	criteria := []critRow{}
	rows, err := pool.Query(ctx, `SELECT id, key, track_id FROM rubric_criteria WHERE hackathon_id = $1`, hackathonID)
	if err != nil {
		fail("load criteria failed: %v", err)
	}
	for rows.Next() {
		var c critRow
		if rows.Scan(&c.ID, &c.Key, &c.TrackID) == nil {
			criteria = append(criteria, c)
		}
	}
	rows.Close()

	teamTrackIDs := map[string]uuid.UUID{}
	trows, err := pool.Query(ctx, `SELECT id, track_id FROM teams WHERE hackathon_id = $1`, hackathonID)
	if err != nil {
		fail("load team tracks failed: %v", err)
	}
	for trows.Next() {
		var id, trackID uuid.UUID
		if trows.Scan(&id, &trackID) == nil {
			teamTrackIDs[id.String()] = trackID
		}
	}
	trows.Close()

	now := time.Now().UTC()
	batch := &pgx.Batch{}

	for ti, teamID := range teamIDs {
		teamTrack := teamTrackIDs[teamID.String()]
		quality := teamQuality(ti)
		assigned := 0
		for j := 0; j < judgesCount && assigned < 3+(ti%2); j++ {
			judge := judgeIDs[(ti+j)%judgesCount]
			batch.Queue(`INSERT INTO judge_assignments (hackathon_id, judge_id, team_id) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`,
				hackathonID, judge, teamID)
			assigned++

			scored := rng.Float64() < 0.85
			if !scored {
				continue
			}
			for _, c := range criteria {
				if c.TrackID != teamTrack {
					continue
				}
				value := clamp(quality*10+rng.NormFloat64()*0.7, 1, 10)
				if isOutlierTeam(ti, judge) && c.Key == rubric[0].Key {
					value = 0.5
				}
				batch.Queue(`INSERT INTO judge_scores (hackathon_id, judge_id, team_id, criterion_id, raw_value, comment, created_at)
					VALUES ($1,$2,$3,$4,$5,'',$6)`,
					hackathonID, judge, teamID, c.ID, round1(value),
					now.Add(-time.Duration(historyHours-2)*time.Hour+time.Duration(ti)*time.Minute))
			}
		}
	}
	if err := pool.SendBatch(ctx, batch).Close(); err != nil {
		fail("seed judging failed: %v", err)
	}
}

func seedSnapshots(ctx context.Context, pool *pgxpool.Pool) {
	now := time.Now().UTC()
	points := 12
	batch := &pgx.Batch{}

	for i, t := range seedTracks {
		runID := mustUUID(fmt.Sprintf("66666666-6666-4666-8666-%012d", i+1))
		_, err := pool.Exec(ctx, `
			INSERT INTO score_runs (id, hackathon_id, track_id, version, config_hash, mode, status, started_at, completed_at)
			VALUES ($1,$2,$3,1,'seed','live','completed',$4,$4)`,
			runID, hackathonID, trackIDs[t.Key], now.Add(-time.Duration(historyHours)*time.Hour))
		if err != nil {
			fail("seed run failed: %v", err)
		}
		for ti, teamID := range teamIDs {
			if teamTracks[teamID.String()] != t.Key {
				continue
			}
			base := teamQuality(ti) * 45
			ceiling := teamQuality(ti)*55 + 35
			for p := 0; p < points; p++ {
				progress := float64(p) / float64(points-1)
				score := base + (ceiling-base)*progress + rng.NormFloat64()*1.5
				if score < 5 {
					score = 5
				}
				captured := now.Add(-time.Duration(historyHours*60/points*(points-p)) * time.Minute)
				batch.Queue(`INSERT INTO score_snapshots (hackathon_id, team_id, score_run_id, mode, score, rank, captured_at)
					VALUES ($1,$2,$3,'live',$4,0,$5)`,
					hackathonID, teamID, runID, round2(score), captured)
			}
		}
	}
	if err := pool.SendBatch(ctx, batch).Close(); err != nil {
		fail("seed snapshots failed: %v", err)
	}
}

func seedIngestSource(ctx context.Context, pool *pgxpool.Pool) {
	sum := sha256.Sum256([]byte(fixedToken))
	_, err := pool.Exec(ctx, `
		INSERT INTO ingest_sources (hackathon_id, name, kind, token_hash) VALUES ($1,'demo-simulator','simulator',$2)`,
		hackathonID, hex.EncodeToString(sum[:]))
	if err != nil {
		fail("seed ingest source failed: %v", err)
	}
}

func teamQuality(i int) float64 {
	return clamp(0.35+0.6*float64((i*7919)%100)/100.0, 0.2, 0.98)
}

func isOutlierTeam(ti int, judge uuid.UUID) bool {
	return ti%9 == 0 && judge == judgeIDs[1]
}

func metricValue(key string, quality, progress float64) float64 {
	improve := 1 - 0.4*(1-progress)
	switch key {
	case "api_latency":
		return round1(500 - quality*420*improve + rng.NormFloat64()*10)
	case "uptime":
		if quality > 0.4 || progress > 0.5 {
			return 1
		}
		return 0
	case "test_pass_rate":
		return round1(clamp(40+quality*58*improve+rng.NormFloat64()*3, 0, 100))
	case "bundle_size":
		return round1(1400 - quality*900*improve + rng.NormFloat64()*30)
	case "a11y_score":
		return round1(clamp(50+quality*48*improve+rng.NormFloat64()*3, 0, 100))
	}
	return 50
}

func normalizedValue(key string, v float64) *float64 {
	var n float64
	switch key {
	case "api_latency":
		n = clamp((500-v)/450*100, 0, 100)
	case "uptime":
		if v > 0 {
			n = 100
		}
	case "test_pass_rate":
		n = clamp(v, 0, 100)
	case "bundle_size":
		switch {
		case v == 300:
			n = 100
		case v > 300 && v < 1500:
			n = (1500 - v) / 1200 * 100
		case v < 300 && v > 100:
			n = (v - 100) / 200 * 100
		default:
			n = 0
		}
	case "a11y_score":
		n = clamp(v, 0, 100)
	default:
		return nil
	}
	return &n
}

func teamName(i int) string {
	prefixes := []string{"Neon", "Quantum", "Pixel", "Turbo", "Cosmic", "Fusion", "Nimbus", "Echo", "Delta", "Aurora"}
	suffixes := []string{"Foxes", "Builders", "Pioneers", "Crew", "Labs", "Works", "Squad", "Forge", "Collective", "Systems"}
	return fmt.Sprintf("%s %s %02d", prefixes[i%10], suffixes[(i/10+3)%10], i)
}

func projectTitle(i int) string {
	titles := []string{
		"AI Meeting Summarizer", "Carbon Footprint Tracker", "Smart Budget Splitter", " accessibility Auditor",
		"Realtime Whiteboard", "Dev Onboarding Buddy", "Local Food Marketplace", "Habit Streak Tracker",
		"Open Data Explorer", "Voice Notes Search",
	}
	return strings.TrimSpace(titles[i%10]) + fmt.Sprintf(" v%d", i/10+1)
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func round1(v float64) float64 {
	return math.Round(v*10) / 10
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func mustUUID(s string) uuid.UUID {
	id, err := uuid.Parse(s)
	if err != nil {
		fail("invalid uuid %s: %v", s, err)
	}
	return id
}

func triggerTable(trigger string) string {
	switch trigger {
	case "trg_metric_values_append_only":
		return "metric_values"
	case "trg_judge_scores_append_only":
		return "judge_scores"
	case "trg_audit_events_append_only":
		return "audit_events"
	case "trg_score_snapshots_append_only":
		return "score_snapshots"
	}
	return ""
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
