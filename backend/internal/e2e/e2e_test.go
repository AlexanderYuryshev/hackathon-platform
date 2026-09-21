package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hackaton-platform/backend/internal/config"
	"github.com/hackaton-platform/backend/internal/db"
	"github.com/hackaton-platform/backend/internal/server"
	"github.com/hackaton-platform/backend/migrations"
)

func testDatabaseURL(t *testing.T) string {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		base = config.Load().DatabaseURL
	}

	ctx := t.Context()
	admin, err := pgxpool.New(ctx, base)
	if err != nil {
		t.Skipf("cannot parse database url: %v", err)
	}
	defer admin.Close()
	if _, err := admin.Exec(ctx, `SELECT 1`); err != nil {
		t.Skipf("database not reachable, skipping E2E: %v", err)
	}
	_, err = admin.Exec(ctx, `CREATE DATABASE hackathon_e2e`)
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("create test db: %v", err)
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse db url: %v", err)
	}
	u.Path = "/hackathon_e2e"
	return u.String()
}

type client struct {
	t      *testing.T
	base   string
	http   *http.Client
	csrf   string
	cookie *cookiejar.Jar
}

func newClient(t *testing.T, base string) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: t, base: base, http: &http.Client{Jar: jar, Timeout: 15 * time.Second}, cookie: jar}
}

func (c *client) do(method, path string, body any, headers map[string]string) (int, map[string]any) {
	c.t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.t.Fatalf("marshal: %v", err)
		}
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		c.t.Fatalf("request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if c.csrf != "" && method != "GET" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("do %s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	out := map[string]any{}
	if res.StatusCode != 204 {
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			c.t.Fatalf("decode %s %s: %v", method, path, err)
		}
	}
	return res.StatusCode, out
}

func (c *client) login(email string) {
	c.t.Helper()
	status, out := c.do("POST", "/api/auth/login", map[string]string{
		"email":    email,
		"password": "e2epass123",
	}, nil)
	if status != 200 {
		c.t.Fatalf("login %s: %d %v", email, status, out)
	}
	c.csrf = out["csrf_token"].(string)
}

func TestE2EFlow(t *testing.T) {
	dbURL := testDatabaseURL(t)
	ctx := t.Context()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	defer pool.Close()

	if _, err := pool.Exec(ctx, `
		DROP TABLE IF EXISTS schema_migrations, audit_events, score_snapshots, team_scores, score_runs,
			judge_scores, judge_assignments, rubric_criteria, metric_values, metric_definitions,
			submission_versions, submissions, team_members, teams, tracks, hackathons, sessions, users,
			ingest_sources CASCADE`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if err := db.Migrate(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	srv := server.New(config.Config{}, pool)
	ts := &http.Server{Addr: ":0", Handler: srv.Handler(), ReadHeaderTimeout: 5 * time.Second}
	listener := newLocalListener(t)
	go listener.serve(ts)
	base := "http://" + listener.addr
	t.Cleanup(func() { ts.Close() })

	organizer := newClient(t, base)
	teamUser := newClient(t, base)
	judge := newClient(t, base)

	seedUsers(t, pool)

	organizer.login("e2e-org@t.io")
	teamUser.login("e2e-team@t.io")
	judge.login("e2e-judge@t.io")

	status, hk := organizer.do("POST", "/api/hackathons", map[string]any{
		"slug": "e2e-hack", "name": "E2E Hack", "min_judges_per_team": 1,
		"ends_at": time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
	}, nil)
	if status != 201 {
		t.Fatalf("create hackathon: %d %v", status, hk)
	}
	hackathonID := hk["id"].(string)

	status, _ = organizer.do("POST", "/api/hackathons/"+hackathonID+"/metrics", map[string]any{
		"key": "api_latency", "name": "API latency", "type": "automated", "weight": 30,
		"direction": "lower_is_better", "normalization": map[string]any{"kind": "range", "min": 50, "max": 500},
	}, nil)
	if status != 201 {
		t.Fatalf("create metric: %d", status)
	}

	status, _ = organizer.do("POST", "/api/hackathons/"+hackathonID+"/rubric/criteria", map[string]any{
		"key": "tech", "name": "Technical", "weight": 70,
	}, nil)
	if status != 201 {
		t.Fatalf("create criterion: %d", status)
	}

	status, src := organizer.do("POST", "/api/hackathons/"+hackathonID+"/ingest-sources", map[string]string{"name": "ci"}, nil)
	if status != 201 {
		t.Fatalf("create source: %d", status)
	}
	ingestToken := src["token"].(string)

	status, out := organizer.do("POST", "/api/hackathons/"+hackathonID+"/status", map[string]string{"status": "running"}, nil)
	if status != 200 {
		t.Fatalf("start hackathon: %d %v", status, out)
	}

	status, team := teamUser.do("POST", "/api/hackathons/"+hackathonID+"/teams", map[string]string{"name": "E2E Team"}, nil)
	if status != 201 {
		t.Fatalf("create team: %d %v", status, team)
	}
	teamID := team["id"].(string)

	status, _ = teamUser.do("PUT", "/api/hackathons/"+hackathonID+"/submissions/"+teamID, map[string]any{
		"title": "E2E Project", "description": "demo", "track": "general",
	}, nil)
	if status != 200 {
		t.Fatalf("put submission: %d", status)
	}

	// first recalc makes the team appear on the leaderboard (provisional, no data yet)
	status, run := organizer.do("POST", "/api/hackathons/"+hackathonID+"/scoring/recalculate", nil, nil)
	if status != 200 || run["status"] != "completed" {
		t.Fatalf("initial recalc: %d %v", status, run)
	}
	status, board := organizer.do("GET", "/api/hackathons/"+hackathonID+"/leaderboard", nil, nil)
	if status != 200 {
		t.Fatalf("leaderboard: %d", status)
	}
	if len(board["leaderboard"].([]any)) != 1 {
		t.Fatalf("expected 1 team row, got %v", board["leaderboard"])
	}

	// assign judge, then judge must be blind to the leaderboard while judging is open
	judgeID := seedJudgeID(t, pool)
	status, _ = organizer.do("POST", "/api/hackathons/"+hackathonID+"/judging/assign", map[string]string{
		"judge_id": judgeID, "team_id": teamID,
	}, nil)
	if status != 201 {
		t.Fatalf("assign judge: %d", status)
	}
	status, out = judge.do("GET", "/api/hackathons/"+hackathonID+"/leaderboard", nil, nil)
	if status != 403 || out["error"] != "judge_blind" {
		t.Fatalf("judge should be blind, got %d %v", status, out)
	}

	t.Run("my_roles", func(t *testing.T) {
		roleOf := func(c *client) string {
			t.Helper()
			status, out := c.do("GET", "/api/my-roles", nil, nil)
			if status != 200 {
				t.Fatalf("my-roles: %d %v", status, out)
			}
			roles, ok := out["roles"].(map[string]any)
			if !ok {
				t.Fatalf("my-roles shape: %v", out)
			}
			r, _ := roles[hackathonID].(string)
			return r
		}
		if got := roleOf(organizer); got != "organizer" {
			t.Fatalf("organizer role = %q", got)
		}
		if got := roleOf(judge); got != "judge" {
			t.Fatalf("judge role = %q", got)
		}
		if got := roleOf(teamUser); got != "team_member" {
			t.Fatalf("team member role = %q", got)
		}
		anon := newClient(t, base)
		if status, _ := anon.do("GET", "/api/my-roles", nil, nil); status != 401 {
			t.Fatalf("anonymous my-roles = %d, want 401", status)
		}
	})

	t.Run("ingest_metric_updates_score", func(t *testing.T) {
		ingest := newClient(t, base)
		status, out := ingest.do("POST", "/api/v1/ingest/metrics", map[string]any{
			"hackathon_id": hackathonID, "team_id": teamID, "metric_key": "api_latency",
			"value": 124.3, "external_event_id": "e2e-evt-1",
			"captured_at": time.Now().UTC().Format(time.RFC3339),
		}, map[string]string{"Authorization": "Bearer " + ingestToken})
		if status != 201 {
			t.Fatalf("ingest: %d %v", status, out)
		}
		// duplicate is idempotent
		status, dup := ingest.do("POST", "/api/v1/ingest/metrics", map[string]any{
			"hackathon_id": hackathonID, "team_id": teamID, "metric_key": "api_latency",
			"value": 999, "external_event_id": "e2e-evt-1",
		}, map[string]string{"Authorization": "Bearer " + ingestToken})
		if status != 200 || dup["duplicate"] != true {
			t.Fatalf("duplicate ingest: %d %v", status, dup)
		}

		status, run := organizer.do("POST", "/api/hackathons/"+hackathonID+"/scoring/recalculate", nil, nil)
		if status != 200 || run["status"] != "completed" {
			t.Fatalf("recalc: %d %v", status, run)
		}

		status, board := organizer.do("GET", "/api/hackathons/"+hackathonID+"/leaderboard", nil, nil)
		if status != 200 {
			t.Fatalf("leaderboard: %d", status)
		}
		row := board["leaderboard"].([]any)[0].(map[string]any)
		if row["status"] != "provisional" {
			t.Fatalf("team without judge scores must be provisional, got %v", row["status"])
		}
		if row["completeness"].(float64) != 50 {
			t.Fatalf("completeness = %v, want 50 (metric ok, no judges)", row["completeness"])
		}
	})

	t.Run("judge_score_changes_result", func(t *testing.T) {
		status, out := judge.do("PUT", "/api/hackathons/"+hackathonID+"/judging/scores/"+teamID, map[string]any{
			"scores": []map[string]any{{"criterion_key": "tech", "value": 9.0}},
		}, nil)
		if status != 200 {
			t.Fatalf("judge scores: %d %v", status, out)
		}
		// edit: judge changes mind -> append new value
		status, _ = judge.do("PUT", "/api/hackathons/"+hackathonID+"/judging/scores/"+teamID, map[string]any{
			"scores": []map[string]any{{"criterion_key": "tech", "value": 8.0}},
		}, nil)
		if status != 200 {
			t.Fatalf("judge rescore: %d", status)
		}

		organizer.do("POST", "/api/hackathons/"+hackathonID+"/scoring/recalculate", nil, nil)

		status, detail := organizer.do("GET", "/api/hackathons/"+hackathonID+"/leaderboard/"+teamID, nil, nil)
		if status != 200 {
			t.Fatalf("team detail: %d", status)
		}
		breakdown := detail["breakdown"].([]any)
		if len(breakdown) != 2 {
			t.Fatalf("breakdown items = %d, want 2", len(breakdown))
		}
		for _, item := range breakdown {
			b := item.(map[string]any)
			switch b["key"] {
			case "api_latency":
				if b["raw"].(float64) != 124.3 {
					t.Errorf("latency raw = %v, want 124.3 (duplicate must not overwrite)", b["raw"])
				}
				if b["normalized"].(float64) != 83.49 {
					t.Errorf("latency normalized = %v, want 83.49", b["normalized"])
				}
			case "tech":
				// single judge -> provisional aggregation uses the LATEST score (8.0)
				if b["raw"].(float64) != 8.0 {
					t.Errorf("tech raw = %v, want 8.0 (latest judge edit)", b["raw"])
				}
				if b["judge_count"].(float64) != 1 {
					t.Errorf("judge_count = %v, want 1", b["judge_count"])
				}
			}
		}
		row := detail["team"].(map[string]any)
		if row["status"] != "complete" {
			t.Errorf("min_judges=1 -> team should be complete, got %v", row["status"])
		}
	})

	t.Run("judging_integrity", func(t *testing.T) {
		// Conflict of interest: a team member cannot be assigned as judge.
		teamUserID := "99999999-9999-4999-8999-000000000002"
		status, out := organizer.do("POST", "/api/hackathons/"+hackathonID+"/judging/assign", map[string]string{
			"judge_id": teamUserID, "team_id": teamID,
		}, nil)
		if status != 409 || out["error"] != "conflict_of_interest" {
			t.Fatalf("assigning a team member as judge must 409, got %d %v", status, out)
		}

		// Judge can reload their own saved scores (used to initialize the form).
		status, out = judge.do("GET", "/api/hackathons/"+hackathonID+"/judging/scores/"+teamID, nil, nil)
		if status != 200 {
			t.Fatalf("get my scores: %d %v", status, out)
		}
		scores := out["scores"].([]any)
		if len(scores) != 1 || scores[0].(map[string]any)["criterion_key"] != "tech" {
			t.Fatalf("saved scores = %v, want latest tech score", scores)
		}
		if scores[0].(map[string]any)["value"].(float64) != 8.0 {
			t.Fatalf("saved value = %v, want 8.0", scores[0].(map[string]any)["value"])
		}

		// Duplicate criterion keys in one PUT: last value wins deterministically.
		status, _ = judge.do("PUT", "/api/hackathons/"+hackathonID+"/judging/scores/"+teamID, map[string]any{
			"scores": []map[string]any{
				{"criterion_key": "tech", "value": 2.0},
				{"criterion_key": "tech", "value": 7.0},
			},
		}, nil)
		if status != 200 {
			t.Fatalf("dup keys put: %d", status)
		}

		// Recusal excludes the judge's scores from the aggregate.
		status, _ = judge.do("POST", "/api/hackathons/"+hackathonID+"/judging/recusal", map[string]string{
			"team_id": teamID, "reason": "conflict of interest",
		}, nil)
		if status != 200 {
			t.Fatalf("recusal: %d", status)
		}
		organizer.do("POST", "/api/hackathons/"+hackathonID+"/scoring/recalculate", nil, nil)
		status, detail := organizer.do("GET", "/api/hackathons/"+hackathonID+"/leaderboard/"+teamID, nil, nil)
		if status != 200 {
			t.Fatalf("team detail: %d", status)
		}
		for _, item := range detail["breakdown"].([]any) {
			if b := item.(map[string]any); b["key"] == "tech" {
				if b["raw"] != nil {
					t.Fatalf("recused judge scores must be excluded, got raw=%v", b["raw"])
				}
			}
		}

		// Re-assigning a recused judge must NOT bring their old scores back.
		status, _ = organizer.do("POST", "/api/hackathons/"+hackathonID+"/judging/assign", map[string]string{
			"judge_id": judgeID, "team_id": teamID,
		}, nil)
		if status != 201 {
			t.Fatalf("re-assign judge: %d", status)
		}
		organizer.do("POST", "/api/hackathons/"+hackathonID+"/scoring/recalculate", nil, nil)
		status, detail = organizer.do("GET", "/api/hackathons/"+hackathonID+"/leaderboard/"+teamID, nil, nil)
		if status != 200 {
			t.Fatalf("team detail after re-assign: %d", status)
		}
		for _, item := range detail["breakdown"].([]any) {
			if b := item.(map[string]any); b["key"] == "tech" {
				if b["raw"] != nil {
					t.Fatalf("pre-recusal scores must stay excluded after re-assign, got raw=%v", b["raw"])
				}
			}
		}

		// A fresh score after re-assignment does count.
		status, _ = judge.do("PUT", "/api/hackathons/"+hackathonID+"/judging/scores/"+teamID, map[string]any{
			"scores": []map[string]any{{"criterion_key": "tech", "value": 6.0}},
		}, nil)
		if status != 200 {
			t.Fatalf("score after re-assign: %d", status)
		}
		organizer.do("POST", "/api/hackathons/"+hackathonID+"/scoring/recalculate", nil, nil)
		status, detail = organizer.do("GET", "/api/hackathons/"+hackathonID+"/leaderboard/"+teamID, nil, nil)
		if status != 200 {
			t.Fatalf("team detail after new score: %d", status)
		}
		for _, item := range detail["breakdown"].([]any) {
			if b := item.(map[string]any); b["key"] == "tech" {
				if b["raw"].(float64) != 6.0 {
					t.Fatalf("fresh score after re-assign must count, got raw=%v", b["raw"])
				}
			}
		}
	})

	t.Run("tracks_crud", func(t *testing.T) {
		// Creation with an explicit track list.
		status, hk2 := organizer.do("POST", "/api/hackathons", map[string]any{
			"slug": "e2e-multi", "name": "E2E Multi",
			"tracks": []map[string]any{
				{"key": "a", "name": "A", "min_judges_per_team": 2},
				{"name": "B без ключа"},
			},
		}, nil)
		if status != 201 {
			t.Fatalf("create hackathon with tracks: %d %v", status, hk2)
		}
		hk2ID := hk2["id"].(string)
		status, tr := organizer.do("GET", "/api/hackathons/"+hk2ID+"/tracks", nil, nil)
		if status != 200 {
			t.Fatalf("list tracks: %d", status)
		}
		list := tr["tracks"].([]any)
		if len(list) != 2 {
			t.Fatalf("tracks = %v, want 2", list)
		}
		if list[0].(map[string]any)["key"] != "a" || list[1].(map[string]any)["key"] != "b" {
			t.Fatalf("track keys = %v, want [a b]", list)
		}
		if list[0].(map[string]any)["min_judges_per_team"].(float64) != 2 {
			t.Fatalf("track min_judges = %v, want 2", list[0])
		}
		// Duplicate keys inside one request → 400.
		status, out := organizer.do("POST", "/api/hackathons", map[string]any{
			"slug": "e2e-dup", "name": "E2E Dup",
			"tracks": []map[string]any{{"key": "x", "name": "X"}, {"key": "x", "name": "X2"}},
		}, nil)
		if status != 400 {
			t.Fatalf("duplicate track keys must 400, got %d %v", status, out)
		}
		// Duplicate key via tracks API → 409.
		status, out = organizer.do("POST", "/api/hackathons/"+hackathonID+"/tracks", map[string]string{
			"key": "tmp", "name": "Tmp",
		}, nil)
		if status != 201 {
			t.Fatalf("create tmp track: %d %v", status, out)
		}
		status, out = organizer.do("POST", "/api/hackathons/"+hackathonID+"/tracks", map[string]string{
			"key": "tmp", "name": "Tmp dup",
		}, nil)
		if status != 409 {
			t.Fatalf("duplicate track key must 409, got %d %v", status, out)
		}
		// Unknown track key on team creation → 404.
		status, out = teamUser.do("POST", "/api/hackathons/"+hackathonID+"/teams", map[string]string{
			"name": "Nope Team", "track": "nope",
		}, nil)
		if status != 404 {
			t.Fatalf("unknown track must 404, got %d %v", status, out)
		}
	})

	t.Run("tracks_isolation", func(t *testing.T) {
		// Second track with its own scoring config.
		status, tr := organizer.do("POST", "/api/hackathons/"+hackathonID+"/tracks", map[string]any{
			"key": "ml", "name": "ML", "min_judges_per_team": 1,
		}, nil)
		if status != 201 {
			t.Fatalf("create track: %d %v", status, tr)
		}
		status, _ = organizer.do("POST", "/api/hackathons/"+hackathonID+"/metrics", map[string]any{
			"key": "ml_latency", "name": "ML latency", "type": "automated", "weight": 100,
			"direction": "lower_is_better", "normalization": map[string]any{"kind": "range", "min": 0, "max": 1000},
			"track": "ml",
		}, nil)
		if status != 201 {
			t.Fatalf("create ml metric: %d", status)
		}
		// Team in the ml track (organizer acts as captain).
		status, mlTeam := organizer.do("POST", "/api/hackathons/"+hackathonID+"/teams", map[string]string{
			"name": "ML Team", "track": "ml",
		}, nil)
		if status != 201 {
			t.Fatalf("create ml team: %d %v", status, mlTeam)
		}
		if mlTeam["track_key"] != "ml" {
			t.Fatalf("team track = %v, want ml", mlTeam["track_key"])
		}
		mlTeamID := mlTeam["id"].(string)

		ingest := newClient(t, base)
		authz := map[string]string{"Authorization": "Bearer " + ingestToken}
		status, _ = ingest.do("POST", "/api/v1/ingest/metrics", map[string]any{
			"hackathon_id": hackathonID, "team_id": mlTeamID, "metric_key": "ml_latency",
			"value": 100.0, "external_event_id": "e2e-ml-1",
			"captured_at": time.Now().UTC().Format(time.RFC3339),
		}, authz)
		if status != 201 {
			t.Fatalf("ml ingest: %d", status)
		}
		// The general track's metric key must NOT resolve inside the ml track.
		status, out := ingest.do("POST", "/api/v1/ingest/metrics", map[string]any{
			"hackathon_id": hackathonID, "team_id": mlTeamID, "metric_key": "api_latency",
			"value": 100.0, "external_event_id": "e2e-ml-2",
		}, authz)
		if status != 404 {
			t.Fatalf("cross-track metric key must 404, got %d %v", status, out)
		}

		// Recalculate ONLY the ml track.
		status, run := organizer.do("POST", "/api/hackathons/"+hackathonID+"/scoring/recalculate?track=ml", nil, nil)
		if status != 200 || run["status"] != "completed" {
			t.Fatalf("ml recalc: %d %v", status, run)
		}
		if run["track_id"] == nil {
			t.Fatalf("run summary must carry track_id, got %v", run)
		}

		status, mlBoard := organizer.do("GET", "/api/hackathons/"+hackathonID+"/leaderboard?track=ml", nil, nil)
		if status != 200 {
			t.Fatalf("ml leaderboard: %d", status)
		}
		mlRows := mlBoard["leaderboard"].([]any)
		if len(mlRows) != 1 {
			t.Fatalf("ml board rows = %d, want 1", len(mlRows))
		}
		if mlRows[0].(map[string]any)["score"].(float64) != 90 {
			t.Fatalf("ml score = %v, want 90", mlRows[0].(map[string]any)["score"])
		}

		// The general track board is untouched: still 1 team.
		status, genBoard := organizer.do("GET", "/api/hackathons/"+hackathonID+"/leaderboard", nil, nil)
		if status != 200 {
			t.Fatalf("general leaderboard: %d", status)
		}
		if len(genBoard["leaderboard"].([]any)) != 1 {
			t.Fatalf("general board rows = %v, want 1", genBoard["leaderboard"])
		}

		// Independent configs prove per-track scoring.
		mlHash := mlBoard["meta"].(map[string]any)["config_hash"]
		genHash := genBoard["meta"].(map[string]any)["config_hash"]
		if mlHash == genHash {
			t.Fatalf("config hashes must differ across tracks")
		}

		// Unknown track key → 404.
		status, _ = organizer.do("GET", "/api/hackathons/"+hackathonID+"/leaderboard?track=nope", nil, nil)
		if status != 404 {
			t.Fatalf("unknown track must 404, got %d", status)
		}
	})

	t.Run("scoring_lock_blocks_recalc", func(t *testing.T) {
		status, out := organizer.do("POST", "/api/hackathons/"+hackathonID+"/scoring/lock", map[string]any{
			"locked": true, "reason": "pause during investigation",
		}, nil)
		if status != 200 {
			t.Fatalf("lock scoring: %d %v", status, out)
		}
		status, out = organizer.do("POST", "/api/hackathons/"+hackathonID+"/scoring/recalculate", nil, nil)
		if status != 409 || out["error"] != "scoring_locked" {
			t.Fatalf("recalc while locked must 409 scoring_locked, got %d %v", status, out)
		}
		status, out = organizer.do("POST", "/api/hackathons/"+hackathonID+"/scoring/lock", map[string]any{
			"locked": false, "reason": "resume",
		}, nil)
		if status != 200 {
			t.Fatalf("unlock scoring: %d %v", status, out)
		}
		status, _ = organizer.do("POST", "/api/hackathons/"+hackathonID+"/scoring/recalculate", nil, nil)
		if status != 200 {
			t.Fatalf("recalc after unlock: %d", status)
		}
	})

	t.Run("finalize_is_immutable", func(t *testing.T) {
		status, tr := organizer.do("GET", "/api/hackathons/"+hackathonID+"/tracks", nil, nil)
		if status != 200 {
			t.Fatalf("list tracks: %d", status)
		}
		wantRuns := len(tr["tracks"].([]any))
		status, fin := organizer.do("POST", "/api/hackathons/"+hackathonID+"/finalize", nil, nil)
		runs, ok := fin["runs"].([]any)
		if status != 200 || !ok || len(runs) != wantRuns {
			t.Fatalf("finalize must produce one run per track (%d), got %d %v", wantRuns, status, fin)
		}
		for _, r := range runs {
			if r.(map[string]any)["status"] != "completed" {
				t.Fatalf("final run not completed: %v", r)
			}
		}
		status, out := organizer.do("POST", "/api/hackathons/"+hackathonID+"/scoring/recalculate", nil, nil)
		if status != 409 || out["error"] != "conflict" {
			t.Fatalf("recalc after finalize must fail, got %d %v", status, out)
		}
		status, _ = organizer.do("POST", "/api/hackathons/"+hackathonID+"/metrics", map[string]any{
			"key": "another", "name": "x", "type": "automated", "normalization": map[string]any{"kind": "binary"},
		}, nil)
		if status != 409 {
			t.Fatalf("metric creation after finalize must be blocked, got %d", status)
		}

		status, board := organizer.do("GET", "/api/hackathons/"+hackathonID+"/leaderboard", nil, nil)
		if status != 200 {
			t.Fatalf("final leaderboard: %d", status)
		}
		meta := board["meta"].(map[string]any)
		if meta["mode"] != "final" {
			t.Fatalf("leaderboard mode = %v, want final", meta["mode"])
		}
		// judge blindness is lifted after finalization
		status, _ = judge.do("GET", "/api/hackathons/"+hackathonID+"/leaderboard", nil, nil)
		if status != 200 {
			t.Fatalf("judge should see final leaderboard, got %d", status)
		}
	})

	t.Run("audit_trail_present", func(t *testing.T) {
		status, out := organizer.do("GET", "/api/hackathons/"+hackathonID+"/audit?limit=50", nil, nil)
		if status != 200 {
			t.Fatalf("audit: %d", status)
		}
		actions := map[string]bool{}
		for _, e := range out["events"].([]any) {
			actions[e.(map[string]any)["action"].(string)] = true
		}
		for _, want := range []string{
			"hackathon.created", "metric.created", "rubric.criterion_created",
			"team.created", "submission.updated", "judging.scores_submitted",
			"scoring.recalculate", "hackathon.finalized",
		} {
			if !actions[want] {
				t.Errorf("audit missing action %q (have %v)", want, keys(actions))
			}
		}
	})
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func seedUsers(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := t.Context()
	hash, err := bcryptHash("e2epass123")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	for i, email := range []string{"e2e-org@t.io", "e2e-team@t.io", "e2e-judge@t.io"} {
		_, err := pool.Exec(ctx, `
			INSERT INTO users (id, email, password_hash, name) VALUES ($1, $2, $3, $4)
			ON CONFLICT (email) DO UPDATE SET password_hash = EXCLUDED.password_hash`,
			fmt.Sprintf("99999999-9999-4999-8999-%012d", i+1), email, hash, "E2E "+email,
		)
		if err != nil {
			t.Fatalf("seed user %s: %v", email, err)
		}
	}
}

func seedJudgeID(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(t.Context(), `SELECT id FROM users WHERE email = 'e2e-judge@t.io'`).Scan(&id); err != nil {
		t.Fatalf("judge id: %v", err)
	}
	return id
}
