export interface Hackathon {
  id: string
  slug: string
  name: string
  description: string
  status: string
  starts_at: string | null
  ends_at: string | null
  judging_ends_at: string | null
  finalized_at: string | null
}

export interface Track {
  id: string
  hackathon_id: string
  key: string
  name: string
  description: string
  min_judges_per_team: number
  scoring_version: number
  scoring_locked: boolean
  sort_order: number
  team_count: number
  created_at: string
}

export interface LeaderRow {
  rank: number
  team_id: string
  team_name: string
  track: string
  score: number
  score_delta: number | null
  rank_delta: number | null
  completeness: number
  status: 'provisional' | 'complete'
  last_update: string
  sparkline: number[]
}

export interface LeaderboardMeta {
  run_id: string
  mode: string
  config_hash: string
  version: number
  generated_at: string
  finalized: boolean
}

export interface LeaderboardResponse {
  leaderboard: LeaderRow[]
  meta: LeaderboardMeta | null
}

export interface BreakdownItem {
  key: string
  name: string
  type: string
  raw: number | null
  normalized: number
  weight: number
  contribution: number
  status: 'fresh' | 'stale' | 'missing' | 'provisional'
  last_timestamp: string | null
  source: string
  judge_count: number
  excluded_count: number
  aggregation_method: string
}

export interface TeamDetailResponse {
  team: LeaderRow
  meta: LeaderboardMeta
  breakdown: BreakdownItem[]
  history: { score: number; rank: number; captured_at: string }[]
}

export interface Team {
  id: string
  hackathon_id: string
  name: string
  track_id: string
  track_key: string
  track_name: string
  member_count: number
}

export interface Submission {
  id: string
  team_id: string
  version: number
  title: string
  description: string
  track: string
  repo_url: string
  demo_url: string
  app_url: string
  extra_links: { label?: string; url?: string }[]
  updated_at: string
}

export interface MyAssignment {
  team_id: string
  team_name: string
  track_id: string
  track_key: string
  track_name: string
  status: 'assigned' | 'recused'
  recusal_reason: string | null
  scored_criteria: number
  total_criteria: number
  project_title: string
}

export interface SavedScores {
  scores: { criterion_key: string; value: number; created_at: string }[]
  comment: string
  assignment_status: 'assigned' | 'recused'
}

export interface Criterion {
  id: string
  key: string
  name: string
  description: string
  scale_min: number
  scale_max: number
  weight: number
  required: boolean
  public: boolean
}

export interface MetricDefinition {
  id: string
  key: string
  name: string
  description: string
  type: string
  weight: number
  required: boolean
  public: boolean
  direction: string
  normalization: Record<string, unknown>
  source: string
  staleness_seconds: number
}

export interface MetricsResponse {
  metrics: MetricDefinition[]
  criteria: Criterion[]
}

export interface ScoreRun {
  id: string
  version: number
  config_hash: string
  mode: string
  status: string
  error: string | null
  track_key: string
  started_at: string
  completed_at: string | null
  teams_scored: number
}

export interface AuditEvent {
  id: string
  action: string
  actor_role: string
  actor_name: string | null
  entity_type: string
  entity_id: string
  payload: Record<string, unknown>
  reason: string | null
  created_at: string
}

export interface Assignment {
  judge_id: string
  judge_name: string
  team_id: string
  team_name: string
  track_key: string
  track_name: string
  status: string
  recusal_reason: string | null
  teams_assigned: number
}

export interface IngestSource {
  id: string
  name: string
  kind: string
  last_seen_at: string | null
  created_at: string
}
