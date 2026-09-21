package judging

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hackaton-platform/backend/internal/audit"
	"github.com/hackaton-platform/backend/internal/auth"
	"github.com/hackaton-platform/backend/internal/httpx"
	"github.com/hackaton-platform/backend/internal/scoring"
	"github.com/hackaton-platform/backend/internal/tracks"
)

type Handler struct {
	pool *pgxpool.Pool
}

func NewHandler(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool}
}

type AssignmentRow struct {
	JudgeID       uuid.UUID `json:"judge_id"`
	JudgeName     string    `json:"judge_name"`
	TeamID        uuid.UUID `json:"team_id"`
	TeamName      string    `json:"team_name"`
	TrackKey      string    `json:"track_key"`
	TrackName     string    `json:"track_name"`
	Status        string    `json:"status"`
	RecusalReason *string   `json:"recusal_reason"`
	TeamsAssigned int       `json:"teams_assigned"`
}

func (h *Handler) ListAssignments(w http.ResponseWriter, r *http.Request) {
	hackathonID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid hackathon id")
		return
	}
	rows, err := h.pool.Query(r.Context(), `
		SELECT ja.judge_id, u.name, ja.team_id, t.name, tr.key, tr.name, ja.status, ja.recusal_reason,
			(SELECT count(*) FROM judge_assignments x WHERE x.judge_id = ja.judge_id AND x.hackathon_id = ja.hackathon_id AND x.status = 'assigned')
		FROM judge_assignments ja
		JOIN users u ON u.id = ja.judge_id
		JOIN teams t ON t.id = ja.team_id
		JOIN tracks tr ON tr.id = t.track_id
		WHERE ja.hackathon_id = $1
		ORDER BY tr.sort_order, u.name, t.name`, hackathonID)
	if err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to list assignments", err))
		return
	}
	defer rows.Close()
	out := []AssignmentRow{}
	for rows.Next() {
		var a AssignmentRow
		var cnt int64
		if err := rows.Scan(&a.JudgeID, &a.JudgeName, &a.TeamID, &a.TeamName, &a.TrackKey, &a.TrackName, &a.Status, &a.RecusalReason, &cnt); err != nil {
			httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to scan assignment", err))
			return
		}
		a.TeamsAssigned = int(cnt)
		out = append(out, a)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"assignments": out})
}

type MyAssignment struct {
	TeamID       uuid.UUID `json:"team_id"`
	TeamName     string    `json:"team_name"`
	TrackID      uuid.UUID `json:"track_id"`
	TrackKey     string    `json:"track_key"`
	TrackName    string    `json:"track_name"`
	Status       string    `json:"status"`
	RecusalReason *string  `json:"recusal_reason"`
	ScoredCriteria int     `json:"scored_criteria"`
	TotalCriteria int      `json:"total_criteria"`
	ProjectTitle string    `json:"project_title"`
}

func (h *Handler) MyAssignments(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	hackathonID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid hackathon id")
		return
	}
	rows, err := h.pool.Query(r.Context(), `
		SELECT ja.team_id, t.name, t.track_id, tr.key, tr.name, ja.status, ja.recusal_reason,
			(SELECT count(*) FROM rubric_criteria c WHERE c.track_id = t.track_id) AS total,
			(SELECT count(DISTINCT js.criterion_id) FROM judge_scores js
			 WHERE js.hackathon_id = ja.hackathon_id AND js.team_id = ja.team_id AND js.judge_id = ja.judge_id) AS scored,
			COALESCE((SELECT sv.title FROM submission_versions sv
			 JOIN submissions su ON su.id = sv.submission_id
			 WHERE su.team_id = ja.team_id AND sv.version = su.current_version), '')
		FROM judge_assignments ja
		JOIN teams t ON t.id = ja.team_id
		JOIN tracks tr ON tr.id = t.track_id
		WHERE ja.hackathon_id = $1 AND ja.judge_id = $2
		ORDER BY tr.sort_order, t.name`, hackathonID, user.ID)
	if err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to load assignments", err))
		return
	}
	defer rows.Close()
	out := []MyAssignment{}
	for rows.Next() {
		var a MyAssignment
		var total, scored int64
		if err := rows.Scan(&a.TeamID, &a.TeamName, &a.TrackID, &a.TrackKey, &a.TrackName, &a.Status, &a.RecusalReason, &total, &scored, &a.ProjectTitle); err != nil {
			httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to scan assignment", err))
			return
		}
		a.TotalCriteria = int(total)
		a.ScoredCriteria = int(scored)
		out = append(out, a)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"assignments": out})
}

type ScoreRequest struct {
	Scores []struct {
		CriterionKey string  `json:"criterion_key"`
		Value        float64 `json:"value"`
	} `json:"scores"`
	Comment string `json:"comment"`
}

// GetMyScores returns the judge's own latest saved score per criterion for a
// team, so the form can be initialized from stored values instead of defaults.
func (h *Handler) GetMyScores(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	hackathonID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid hackathon id")
		return
	}
	teamID, err := uuid.Parse(chi.URLParam(r, "teamId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid team id")
		return
	}

	var assignmentStatus string
	if err := h.pool.QueryRow(r.Context(), `
		SELECT status FROM judge_assignments
		WHERE hackathon_id = $1 AND judge_id = $2 AND team_id = $3`,
		hackathonID, user.ID, teamID).Scan(&assignmentStatus); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "you are not assigned to this team")
		return
	}

	rows, err := h.pool.Query(r.Context(), `
		SELECT DISTINCT ON (js.criterion_id) rc.key, js.raw_value, js.comment, js.created_at
		FROM judge_scores js
		JOIN rubric_criteria rc ON rc.id = js.criterion_id
		JOIN judge_assignments ja
			ON ja.hackathon_id = js.hackathon_id
			AND ja.judge_id = js.judge_id
			AND ja.team_id = js.team_id
		WHERE js.hackathon_id = $1 AND js.judge_id = $2 AND js.team_id = $3
			AND js.created_at >= ja.scores_valid_from
		ORDER BY js.criterion_id, js.created_at DESC, js.id DESC`,
		hackathonID, user.ID, teamID)
	if err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to load scores", err))
		return
	}
	defer rows.Close()

	type savedScore struct {
		CriterionKey string  `json:"criterion_key"`
		Value        float64 `json:"value"`
		CreatedAt    string  `json:"created_at"`
	}
	scores := []savedScore{}
	comment := ""
	for rows.Next() {
		var s savedScore
		var cmt string
		var createdAt time.Time
		if err := rows.Scan(&s.CriterionKey, &s.Value, &cmt, &createdAt); err != nil {
			httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to scan score", err))
			return
		}
		s.CreatedAt = createdAt.UTC().Format(time.RFC3339)
		scores = append(scores, s)
		if cmt != "" {
			comment = cmt
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"scores":            scores,
		"comment":           comment,
		"assignment_status": assignmentStatus,
	})
}

func (h *Handler) PutScores(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	hackathonID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid hackathon id")
		return
	}
	teamID, err := uuid.Parse(chi.URLParam(r, "teamId"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid team id")
		return
	}
	var req ScoreRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if len(req.Scores) == 0 {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "scores are required")
		return
	}
	// Deduplicate criterion keys (last value wins): duplicate rows within one
	// transaction share the same created_at, which would make the effective
	// score depend on physical row order.
	deduped := make(map[string]float64, len(req.Scores))
	for _, s := range req.Scores {
		deduped[s.CriterionKey] = s.Value
	}

	var status string
	var judgingEndsAt *time.Time
	if err := h.pool.QueryRow(r.Context(),
		`SELECT status, judging_ends_at FROM hackathons WHERE id = $1`, hackathonID,
	).Scan(&status, &judgingEndsAt); err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusNotFound, "not_found", "hackathon not found", err))
		return
	}
	if status == "finalized" || status == "draft" {
		httpx.WriteError(w, http.StatusConflict, "conflict", "judging is not open for this hackathon")
		return
	}
	if judgingEndsAt != nil && time.Now().UTC().After(*judgingEndsAt) {
		httpx.WriteError(w, http.StatusConflict, "judging_closed", "judging period has ended")
		return
	}

	var assigned bool
	if err := h.pool.QueryRow(r.Context(), `
		SELECT EXISTS(SELECT 1 FROM judge_assignments
			WHERE hackathon_id = $1 AND judge_id = $2 AND team_id = $3 AND status = 'assigned')`,
		hackathonID, user.ID, teamID).Scan(&assigned); err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to check assignment", err))
		return
	}
	if !assigned {
		httpx.WriteError(w, http.StatusForbidden, "forbidden", "you are not assigned to this team")
		return
	}

	criteria := map[string]struct {
		ID       uuid.UUID
		TrackID  uuid.UUID
		Min, Max float64
	}{}
	crows, err := h.pool.Query(r.Context(), `
		SELECT rc.id, rc.key, rc.scale_min, rc.scale_max, rc.track_id
		FROM rubric_criteria rc
		JOIN tracks tr ON tr.id = rc.track_id
		WHERE tr.hackathon_id = $1`, hackathonID)
	if err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to load criteria", err))
		return
	}
	defer crows.Close()
	for crows.Next() {
		var key string
		var id, trackID uuid.UUID
		var minV, maxV float64
		if err := crows.Scan(&id, &key, &minV, &maxV, &trackID); err != nil {
			continue
		}
		criteria[key] = struct {
			ID       uuid.UUID
			TrackID  uuid.UUID
			Min, Max float64
		}{id, trackID, minV, maxV}
	}

	// The team's track decides which rubric applies: criteria from other
	// tracks of the same hackathon must not leak into this team's score.
	var teamTrackID uuid.UUID
	if err := h.pool.QueryRow(r.Context(),
		`SELECT track_id FROM teams WHERE id = $1 AND hackathon_id = $2`, teamID, hackathonID,
	).Scan(&teamTrackID); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "team does not belong to this hackathon")
		return
	}

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to start tx", err))
		return
	}
	defer tx.Rollback(r.Context())

	for key, value := range deduped {
		c, ok := criteria[key]
		if !ok || c.TrackID != teamTrackID {
			httpx.WriteError(w, http.StatusBadRequest, "bad_request", "unknown criterion key for this team's track: "+key)
			return
		}
		if value < c.Min || value > c.Max {
			httpx.WriteError(w, http.StatusBadRequest, "bad_request", "value out of scale for criterion "+key)
			return
		}
		if _, err := tx.Exec(r.Context(), `
			INSERT INTO judge_scores (hackathon_id, judge_id, team_id, criterion_id, raw_value, comment)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			hackathonID, user.ID, teamID, c.ID, value, req.Comment); err != nil {
			httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to store score", err))
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to commit", err))
		return
	}

	audit.Record(r.Context(), h.pool, audit.Event{
		HackathonID: &hackathonID,
		ActorID:     &user.ID,
		ActorRole:   "judge",
		Action:      "judging.scores_submitted",
		EntityType:  "team",
		EntityID:    teamID.String(),
		Payload:     map[string]any{"criteria": len(req.Scores)},
	})
	scoring.NotifyDirty(r.Context(), h.pool, teamTrackID)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"stored": len(req.Scores)})
}

func (h *Handler) Recusal(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	hackathonID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid hackathon id")
		return
	}
	var req struct {
		TeamID string `json:"team_id"`
		Reason string `json:"reason"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	teamID, err := uuid.Parse(req.TeamID)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid team id")
		return
	}
	if req.Reason == "" {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "recusal reason is required")
		return
	}

	tag, err := h.pool.Exec(r.Context(), `
		UPDATE judge_assignments SET status = 'recused', recusal_reason = $4, recused_at = now()
		WHERE hackathon_id = $1 AND judge_id = $2 AND team_id = $3 AND status = 'assigned'`,
		hackathonID, user.ID, teamID, req.Reason)
	if err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to record recusal", err))
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "active assignment not found")
		return
	}
	audit.Record(r.Context(), h.pool, audit.Event{
		HackathonID: &hackathonID,
		ActorID:     &user.ID,
		ActorRole:   "judge",
		Action:      "judging.recusal",
		EntityType:  "team",
		EntityID:    teamID.String(),
		Reason:      req.Reason,
	})
	var recusalTrackID uuid.UUID
	if err := h.pool.QueryRow(r.Context(),
		`SELECT track_id FROM teams WHERE id = $1 AND hackathon_id = $2`, teamID, hackathonID,
	).Scan(&recusalTrackID); err == nil {
		scoring.NotifyDirty(r.Context(), h.pool, recusalTrackID)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) AutoAssign(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	hackathonID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid hackathon id")
		return
	}

	var onlyTrackID uuid.UUID
	if key := strings.TrimSpace(r.URL.Query().Get("track")); key != "" {
		var err error
		onlyTrackID, _, err = tracks.ResolveTrackID(r.Context(), h.pool, hackathonID, key)
		if err != nil {
			httpx.WriteErr(w, err)
			return
		}
	}

	// Serialize concurrent auto-assign/assign calls and make the whole
	// read-decide-insert cycle atomic, so min_judges cannot be exceeded.
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to start tx", err))
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := tx.Exec(r.Context(),
		`SELECT pg_advisory_xact_lock(hashtext($1))`, "auto_assign:"+hackathonID.String()); err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to lock", err))
		return
	}

	// Coverage targets are per track: each track has its own min_judges.
	type trackTarget struct {
		ID        uuid.UUID
		MinJudges int
		Teams     []uuid.UUID
	}
	targets := []trackTarget{}
	trows, err := tx.Query(r.Context(), `
		SELECT id, min_judges_per_team FROM tracks
		WHERE hackathon_id = $1 AND ($2::uuid IS NULL OR id = $2)
		ORDER BY sort_order, created_at`, hackathonID, uuidToNull(onlyTrackID))
	if err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to load tracks", err))
		return
	}
	defer trows.Close()
	for trows.Next() {
		var tgt trackTarget
		if err := trows.Scan(&tgt.ID, &tgt.MinJudges); err != nil {
			httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to scan track", err))
			return
		}
		tgt.Teams, err = h.loadTeams(r, tx, tgt.ID)
		if err != nil {
			httpx.WriteErr(w, err)
			return
		}
		targets = append(targets, tgt)
	}
	if err := trows.Err(); err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to load tracks", err))
		return
	}
	if len(targets) == 0 {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusNotFound, "not_found", "hackathon not found", nil))
		return
	}
	allJudges, err := h.loadJudges(r, tx, hackathonID)
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	if len(allJudges) == 0 {
		httpx.WriteError(w, http.StatusConflict, "conflict", "no judges available; assign judges first")
		return
	}

	anyAssigned := map[uuid.UUID]map[uuid.UUID]bool{}
	activePerTeam := map[uuid.UUID]int{}
	load := map[uuid.UUID]int{}
	rows, err := tx.Query(r.Context(), `
		SELECT judge_id, team_id, status FROM judge_assignments WHERE hackathon_id = $1`, hackathonID)
	if err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to load assignments", err))
		return
	}
	defer rows.Close()
	for rows.Next() {
		var j, t uuid.UUID
		var st string
		if err := rows.Scan(&j, &t, &st); err == nil {
			if anyAssigned[j] == nil {
				anyAssigned[j] = map[uuid.UUID]bool{}
			}
			anyAssigned[j][t] = true
			if st == "assigned" {
				activePerTeam[t]++
				load[j]++
			}
		}
	}

	created := 0
	createdPerTrack := map[string]int{}
	for _, tgt := range targets {
		for _, teamID := range tgt.Teams {
			need := tgt.MinJudges - activePerTeam[teamID]
			for need > 0 {
			candidates := make([]uuid.UUID, len(allJudges))
			copy(candidates, allJudges)
			sort.Slice(candidates, func(i, k int) bool {
				if load[candidates[i]] != load[candidates[k]] {
					return load[candidates[i]] < load[candidates[k]]
				}
				return candidates[i].String() < candidates[k].String()
			})
			var picked uuid.UUID
			for _, c := range candidates {
				if !anyAssigned[c][teamID] {
					picked = c
					break
				}
			}
			if picked == uuid.Nil {
				break
			}
			if _, err := tx.Exec(r.Context(), `
				INSERT INTO judge_assignments (hackathon_id, judge_id, team_id) VALUES ($1, $2, $3)
				ON CONFLICT (hackathon_id, judge_id, team_id) DO NOTHING`,
				hackathonID, picked, teamID); err != nil {
				httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to assign judge", err))
				return
			}
			load[picked]++
			if anyAssigned[picked] == nil {
				anyAssigned[picked] = map[uuid.UUID]bool{}
			}
			anyAssigned[picked][teamID] = true
			activePerTeam[teamID]++
			created++
			createdPerTrack[tgt.ID.String()]++
			need--
		}
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to commit", err))
		return
	}

	audit.Record(r.Context(), h.pool, audit.Event{
		HackathonID: &hackathonID,
		ActorID:     &user.ID,
		ActorRole:   "organizer",
		Action:      "judging.auto_assigned",
		EntityType:  "hackathon",
		EntityID:    hackathonID.String(),
		Payload:     map[string]any{"created": created, "per_track": createdPerTrack},
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"created": created})
}

func (h *Handler) AssignJudge(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	hackathonID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid hackathon id")
		return
	}
	var req struct {
		JudgeID    string `json:"judge_id"`
		JudgeEmail string `json:"judge_email"`
		TeamID     string `json:"team_id"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	teamID, err := uuid.Parse(req.TeamID)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid team id")
		return
	}
	var judgeID uuid.UUID
	switch {
	case req.JudgeID != "":
		judgeID, err = uuid.Parse(req.JudgeID)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "bad_request", "invalid judge id")
			return
		}
		var exists bool
		if err := h.pool.QueryRow(r.Context(),
			`SELECT EXISTS(SELECT 1 FROM users WHERE id = $1)`, judgeID).Scan(&exists); err != nil || !exists {
			httpx.WriteError(w, http.StatusBadRequest, "bad_request", "judge not found")
			return
		}
	case req.JudgeEmail != "":
		if err := h.pool.QueryRow(r.Context(),
			`SELECT id FROM users WHERE email = $1`, req.JudgeEmail).Scan(&judgeID); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "bad_request", "judge not found by email")
			return
		}
	default:
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "judge_id or judge_email is required")
		return
	}
	var teamOK bool
	var hkStatus string
	if err := h.pool.QueryRow(r.Context(), `
		SELECT true, h.status FROM teams t JOIN hackathons h ON h.id = t.hackathon_id
		WHERE t.id = $1 AND t.hackathon_id = $2`, teamID, hackathonID,
	).Scan(&teamOK, &hkStatus); err != nil || !teamOK {
		httpx.WriteError(w, http.StatusBadRequest, "bad_request", "team does not belong to this hackathon")
		return
	}
	if hkStatus == "finalized" {
		httpx.WriteError(w, http.StatusConflict, "conflict", "hackathon is finalized")
		return
	}
	// Conflict of interest: a participant of any team in this hackathon cannot
	// judge it (they would be able to score their own competition).
	var isParticipant bool
	if err := h.pool.QueryRow(r.Context(), `
		SELECT EXISTS(SELECT 1 FROM team_members WHERE hackathon_id = $1 AND user_id = $2)`,
		hackathonID, judgeID).Scan(&isParticipant); err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to check conflict of interest", err))
		return
	}
	if isParticipant {
		httpx.WriteError(w, http.StatusConflict, "conflict_of_interest", "judge is a member of a team in this hackathon")
		return
	}
	// Re-assigning a recused judge opens a new validity window: their scores
	// submitted before the recusal stay excluded from the aggregate.
	if _, err := h.pool.Exec(r.Context(), `
		INSERT INTO judge_assignments (hackathon_id, judge_id, team_id) VALUES ($1, $2, $3)
		ON CONFLICT (hackathon_id, judge_id, team_id) DO UPDATE SET
			status = 'assigned',
			recusal_reason = NULL,
			recused_at = NULL,
			scores_valid_from = CASE WHEN judge_assignments.status = 'recused' THEN now()
				ELSE judge_assignments.scores_valid_from END`,
		hackathonID, judgeID, teamID); err != nil {
		httpx.WriteErr(w, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to assign judge", err))
		return
	}
	audit.Record(r.Context(), h.pool, audit.Event{
		HackathonID: &hackathonID,
		ActorID:     &user.ID,
		ActorRole:   "organizer",
		Action:      "judging.assigned",
		EntityType:  "team",
		EntityID:    teamID.String(),
		Payload:     map[string]string{"judge_id": judgeID.String()},
	})
	httpx.WriteJSON(w, http.StatusCreated, map[string]bool{"ok": true})
}

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func uuidToNull(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

func (h *Handler) loadTeams(r *http.Request, q querier, trackID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := q.Query(r.Context(),
		`SELECT id FROM teams WHERE track_id = $1 ORDER BY created_at`, trackID)
	if err != nil {
		return nil, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to load teams", err)
	}
	defer rows.Close()
	teams := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err == nil {
			teams = append(teams, id)
		}
	}
	return teams, nil
}

func (h *Handler) loadJudges(r *http.Request, q querier, hackathonID uuid.UUID) ([]uuid.UUID, error) {
	// Exclude hackathon participants: they have a conflict of interest and
	// cannot judge their own event.
	rows, err := q.Query(r.Context(), `
		SELECT DISTINCT ja.judge_id FROM judge_assignments ja
		WHERE ja.hackathon_id = $1 AND ja.status IN ('assigned', 'recused')
			AND NOT EXISTS (
				SELECT 1 FROM team_members tm
				WHERE tm.hackathon_id = ja.hackathon_id AND tm.user_id = ja.judge_id
			)`, hackathonID)
	if err != nil {
		return nil, httpx.NewStatusError(http.StatusInternalServerError, "db_error", "failed to load judges", err)
	}
	defer rows.Close()
	judges := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err == nil {
			judges = append(judges, id)
		}
	}
	return judges, nil
}
