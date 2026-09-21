CREATE TABLE users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email text NOT NULL UNIQUE,
    password_hash text NOT NULL,
    name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,
    csrf_token text NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_sessions_user ON sessions(user_id);

CREATE TABLE hackathons (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug text NOT NULL UNIQUE,
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft', 'running', 'judging', 'finalized')),
    organizer_id uuid NOT NULL REFERENCES users(id),
    starts_at timestamptz,
    ends_at timestamptz,
    judging_ends_at timestamptz,
    finalized_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Tracks: tasks/nominations inside a hackathon. Each track has its own
-- scoring configuration (metrics, rubric, versions, locks) and its own
-- leaderboards; a team always belongs to exactly one track.
CREATE TABLE tracks (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    hackathon_id uuid NOT NULL REFERENCES hackathons(id) ON DELETE CASCADE,
    key text NOT NULL,
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    min_judges_per_team int NOT NULL DEFAULT 3,
    scoring_version int NOT NULL DEFAULT 1,
    scoring_locked bool NOT NULL DEFAULT false,
    sort_order int NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (hackathon_id, key)
);
CREATE INDEX idx_tracks_hackathon ON tracks(hackathon_id, sort_order);

CREATE TABLE teams (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    hackathon_id uuid NOT NULL REFERENCES hackathons(id) ON DELETE CASCADE,
    track_id uuid NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
    name text NOT NULL,
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (track_id, name)
);
CREATE INDEX idx_teams_hackathon ON teams(hackathon_id);
CREATE INDEX idx_teams_track ON teams(track_id);

CREATE TABLE team_members (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    hackathon_id uuid NOT NULL REFERENCES hackathons(id) ON DELETE CASCADE,
    team_id uuid NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    is_captain bool NOT NULL DEFAULT false,
    joined_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (team_id, user_id),
    UNIQUE (hackathon_id, user_id)
);
CREATE INDEX idx_team_members_team ON team_members(team_id);
CREATE INDEX idx_team_members_user ON team_members(user_id);

CREATE TABLE submissions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    hackathon_id uuid NOT NULL REFERENCES hackathons(id) ON DELETE CASCADE,
    team_id uuid NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    current_version int NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (hackathon_id, team_id)
);

CREATE TABLE submission_versions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    submission_id uuid NOT NULL REFERENCES submissions(id) ON DELETE CASCADE,
    version int NOT NULL,
    title text NOT NULL DEFAULT '',
    description text NOT NULL DEFAULT '',
    track text NOT NULL DEFAULT '',
    repo_url text NOT NULL DEFAULT '',
    demo_url text NOT NULL DEFAULT '',
    app_url text NOT NULL DEFAULT '',
    extra_links jsonb NOT NULL DEFAULT '[]',
    edited_by uuid NOT NULL REFERENCES users(id),
    reason text,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (submission_id, version)
);
CREATE INDEX idx_submission_versions_submission ON submission_versions(submission_id, version DESC);

CREATE TABLE metric_definitions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    hackathon_id uuid NOT NULL REFERENCES hackathons(id) ON DELETE CASCADE,
    track_id uuid NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
    key text NOT NULL,
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    type text NOT NULL CHECK (type IN ('automated', 'manual')),
    weight double precision NOT NULL DEFAULT 1 CHECK (weight >= 0),
    required bool NOT NULL DEFAULT true,
    public bool NOT NULL DEFAULT true,
    direction text NOT NULL DEFAULT 'higher_is_better'
        CHECK (direction IN ('higher_is_better', 'lower_is_better')),
    normalization jsonb NOT NULL DEFAULT '{}',
    source text NOT NULL DEFAULT '',
    staleness_seconds int NOT NULL DEFAULT 1800,
    sort_order int NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (track_id, key)
);
CREATE INDEX idx_metric_definitions_hackathon ON metric_definitions(hackathon_id);
CREATE INDEX idx_metric_definitions_track ON metric_definitions(track_id);

CREATE TABLE metric_values (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    hackathon_id uuid NOT NULL REFERENCES hackathons(id) ON DELETE CASCADE,
    team_id uuid NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    metric_definition_id uuid NOT NULL REFERENCES metric_definitions(id) ON DELETE CASCADE,
    external_event_id text NOT NULL,
    raw_value double precision NOT NULL,
    normalized_value double precision,
    metadata jsonb NOT NULL DEFAULT '{}',
    source text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'valid' CHECK (status IN ('valid', 'invalid')),
    captured_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (metric_definition_id, team_id, external_event_id)
);
CREATE INDEX idx_metric_values_current ON metric_values(team_id, metric_definition_id, captured_at DESC)
    WHERE status = 'valid';
CREATE INDEX idx_metric_values_hackathon ON metric_values(hackathon_id);

CREATE TABLE rubric_criteria (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    hackathon_id uuid NOT NULL REFERENCES hackathons(id) ON DELETE CASCADE,
    track_id uuid NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
    key text NOT NULL,
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    scale_min double precision NOT NULL DEFAULT 0,
    scale_max double precision NOT NULL DEFAULT 10,
    weight double precision NOT NULL DEFAULT 1 CHECK (weight >= 0),
    required bool NOT NULL DEFAULT true,
    public bool NOT NULL DEFAULT true,
    sort_order int NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (track_id, key)
);
CREATE INDEX idx_rubric_criteria_hackathon ON rubric_criteria(hackathon_id);
CREATE INDEX idx_rubric_criteria_track ON rubric_criteria(track_id);

CREATE TABLE judge_assignments (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    hackathon_id uuid NOT NULL REFERENCES hackathons(id) ON DELETE CASCADE,
    judge_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    team_id uuid NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    status text NOT NULL DEFAULT 'assigned'
        CHECK (status IN ('assigned', 'recused')),
    recusal_reason text,
    recused_at timestamptz,
    -- Re-assigning a judge after a recusal opens a new validity window here,
    -- so pre-recusal scores never silently re-enter the aggregate.
    scores_valid_from timestamptz NOT NULL DEFAULT '-infinity',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (hackathon_id, judge_id, team_id)
);
CREATE INDEX idx_judge_assignments_judge ON judge_assignments(hackathon_id, judge_id);
CREATE INDEX idx_judge_assignments_team ON judge_assignments(hackathon_id, team_id)
    WHERE status = 'assigned';

CREATE TABLE judge_scores (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    hackathon_id uuid NOT NULL REFERENCES hackathons(id) ON DELETE CASCADE,
    judge_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    team_id uuid NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    criterion_id uuid NOT NULL REFERENCES rubric_criteria(id) ON DELETE CASCADE,
    raw_value double precision NOT NULL,
    comment text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_judge_scores_effective ON judge_scores(team_id, criterion_id, judge_id, created_at DESC);
CREATE INDEX idx_judge_scores_hackathon ON judge_scores(hackathon_id);

CREATE TABLE score_runs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    hackathon_id uuid NOT NULL REFERENCES hackathons(id) ON DELETE CASCADE,
    track_id uuid NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
    version int NOT NULL,
    config_hash text NOT NULL,
    mode text NOT NULL DEFAULT 'live' CHECK (mode IN ('live', 'final')),
    status text NOT NULL DEFAULT 'running'
        CHECK (status IN ('running', 'completed', 'error')),
    error text,
    started_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz
);
CREATE INDEX idx_score_runs_hackathon ON score_runs(hackathon_id, started_at DESC);
CREATE INDEX idx_score_runs_track ON score_runs(track_id, started_at DESC);

CREATE TABLE team_scores (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    hackathon_id uuid NOT NULL REFERENCES hackathons(id) ON DELETE CASCADE,
    team_id uuid NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    score_run_id uuid NOT NULL REFERENCES score_runs(id) ON DELETE CASCADE,
    score double precision NOT NULL,
    rank int NOT NULL,
    completeness double precision NOT NULL,
    status text NOT NULL CHECK (status IN ('provisional', 'complete')),
    breakdown jsonb NOT NULL DEFAULT '{}',
    computed_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (score_run_id, team_id)
);
CREATE INDEX idx_team_scores_hackathon_run ON team_scores(hackathon_id, score_run_id);

CREATE TABLE score_snapshots (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    hackathon_id uuid NOT NULL REFERENCES hackathons(id) ON DELETE CASCADE,
    team_id uuid NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    score_run_id uuid NOT NULL REFERENCES score_runs(id) ON DELETE CASCADE,
    mode text NOT NULL DEFAULT 'live' CHECK (mode IN ('live', 'final')),
    score double precision NOT NULL,
    rank int NOT NULL,
    captured_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_score_snapshots_team_history ON score_snapshots(hackathon_id, team_id, captured_at DESC);
CREATE INDEX idx_score_snapshots_hackathon_time ON score_snapshots(hackathon_id, captured_at DESC);

CREATE TABLE audit_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    hackathon_id uuid REFERENCES hackathons(id) ON DELETE CASCADE,
    actor_id uuid REFERENCES users(id) ON DELETE SET NULL,
    actor_role text NOT NULL DEFAULT 'system',
    action text NOT NULL,
    entity_type text NOT NULL DEFAULT '',
    entity_id text NOT NULL DEFAULT '',
    payload jsonb NOT NULL DEFAULT '{}',
    reason text,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_audit_events_hackathon ON audit_events(hackathon_id, created_at DESC);

CREATE TABLE ingest_sources (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    hackathon_id uuid NOT NULL REFERENCES hackathons(id) ON DELETE CASCADE,
    name text NOT NULL,
    kind text NOT NULL DEFAULT 'webhook',
    token_hash text NOT NULL UNIQUE,
    last_seen_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (hackathon_id, name)
);

CREATE OR REPLACE FUNCTION forbid_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'table % is append-only', TG_TABLE_NAME;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_metric_values_append_only
    BEFORE UPDATE OR DELETE ON metric_values
    FOR EACH ROW EXECUTE FUNCTION forbid_mutation();

CREATE TRIGGER trg_judge_scores_append_only
    BEFORE UPDATE OR DELETE ON judge_scores
    FOR EACH ROW EXECUTE FUNCTION forbid_mutation();

CREATE TRIGGER trg_audit_events_append_only
    BEFORE UPDATE OR DELETE ON audit_events
    FOR EACH ROW EXECUTE FUNCTION forbid_mutation();

CREATE TRIGGER trg_score_snapshots_append_only
    BEFORE UPDATE OR DELETE ON score_snapshots
    FOR EACH ROW EXECUTE FUNCTION forbid_mutation();

-- Computed results are append-only too: the final leaderboard is immutable
-- and no code path may rewrite history.
CREATE TRIGGER trg_team_scores_append_only
    BEFORE UPDATE OR DELETE ON team_scores
    FOR EACH ROW EXECUTE FUNCTION forbid_mutation();

-- score_runs may only transition running -> completed/error; identity fields
-- (track, version, config_hash, mode, ...) are frozen.
CREATE OR REPLACE FUNCTION score_runs_guard() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'score_runs rows cannot be deleted';
    END IF;
    IF NEW.id <> OLD.id
       OR NEW.hackathon_id <> OLD.hackathon_id
       OR NEW.track_id <> OLD.track_id
       OR NEW.version <> OLD.version
       OR NEW.config_hash <> OLD.config_hash
       OR NEW.mode <> OLD.mode
       OR NEW.started_at <> OLD.started_at THEN
        RAISE EXCEPTION 'score_runs are append-only: identity fields cannot change';
    END IF;
    IF OLD.status <> 'running' OR NEW.status NOT IN ('completed', 'error') THEN
        RAISE EXCEPTION 'score_runs status can only move running -> completed/error';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_score_runs_guard
    BEFORE UPDATE OR DELETE ON score_runs
    FOR EACH ROW EXECUTE FUNCTION score_runs_guard();

CREATE OR REPLACE FUNCTION touch_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_hackathons_updated_at BEFORE UPDATE ON hackathons
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
CREATE TRIGGER trg_teams_updated_at BEFORE UPDATE ON teams
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
CREATE TRIGGER trg_submissions_updated_at BEFORE UPDATE ON submissions
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
CREATE TRIGGER trg_metric_definitions_updated_at BEFORE UPDATE ON metric_definitions
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
CREATE TRIGGER trg_rubric_criteria_updated_at BEFORE UPDATE ON rubric_criteria
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
