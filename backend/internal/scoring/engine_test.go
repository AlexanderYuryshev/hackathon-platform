package scoring

import (
	"testing"
	"time"
)

func fp(v float64) *float64 { return &v }

func TestComputeTeamWeightsAffectScore(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	input := []MetricInput{
		{
			Key: "tech", Name: "Technical execution", Type: "manual", Weight: 30, Required: true,
			ScaleMin: 0, ScaleMax: 10, JudgeScores: []float64{8.4, 8.4, 8.6, 8.4},
		},
		{
			Key: "latency", Name: "API latency", Type: "automated", Weight: 15, Required: true,
			Direction: DirectionLower,
			Normalization: Normalization{Kind: KindRange, Min: 50, Max: 500},
			RawValue: fp(124), CapturedAt: &now,
		},
		{
			Key: "uptime", Name: "Uptime", Type: "automated", Weight: 5, Required: true,
			Normalization: Normalization{Kind: KindBinary},
			RawValue: fp(1), CapturedAt: &now,
		},
	}
	r := ComputeTeam(input, now, 3)

	if r.Status != "complete" || !r.Eligible {
		t.Fatalf("expected complete, got %+v", r.Status)
	}
	// tech: judges [8.4 8.4 8.6 8.4] -> median 8.4, MAD 0 -> 8.6 excluded -> mean 8.4 -> norm 84
	// latency: (500-124)/450*100 = 83.56
	// uptime: 100
	want := (84*30 + 83.5555*15 + 100*5) / 50
	if !approx(r.Score, want, 0.05) {
		t.Errorf("score = %v, want %v", r.Score, want)
	}

	var tech *BreakdownItem
	for i := range r.Breakdown {
		if r.Breakdown[i].Key == "tech" {
			tech = &r.Breakdown[i]
		}
	}
	if tech == nil {
		t.Fatal("tech breakdown missing")
	}
	if !approx(tech.Normalized, 84, 0.01) || !approx(tech.Contribution, 25.2, 0.01) {
		t.Errorf("tech breakdown = %+v", tech)
	}
	if tech.JudgeCount != 4 || tech.ExcludedCount != 1 || tech.AggregationMethod != "trimmed_mean" {
		t.Errorf("tech agg fields = %+v", tech)
	}
}

func TestComputeTeamProvisionalOnMissingRequired(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	input := []MetricInput{
		{Key: "latency", Name: "API latency", Type: "automated", Weight: 15, Required: true,
			Normalization: Normalization{Kind: KindRange, Min: 0, Max: 500}},
		{Key: "tech", Name: "Technical execution", Type: "manual", Weight: 30, Required: true,
			ScaleMin: 0, ScaleMax: 10, JudgeScores: []float64{8.0}},
	}
	r := ComputeTeam(input, now, 3)
	if r.Status != "provisional" || r.Eligible {
		t.Errorf("expected provisional, got %+v", r)
	}
	if !approx(r.Completeness, 0, 0.001) {
		t.Errorf("completeness = %v, want 0", r.Completeness)
	}
}

func TestComputeTeamCompletenessPartial(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	fresh := now
	input := []MetricInput{
		{Key: "a", Name: "A", Type: "automated", Weight: 10, Required: true,
			Normalization: Normalization{Kind: KindBinary}, RawValue: fp(1), CapturedAt: &fresh},
		{Key: "b", Name: "B", Type: "automated", Weight: 10, Required: true,
			Normalization: Normalization{Kind: KindBinary}},
		{Key: "c", Name: "C", Type: "automated", Weight: 10, Required: false,
			Normalization: Normalization{Kind: KindBinary}},
	}
	r := ComputeTeam(input, now, 1)
	if !approx(r.Completeness, 50, 0.001) {
		t.Errorf("completeness = %v, want 50", r.Completeness)
	}
	if r.Status != "provisional" {
		t.Errorf("status = %v, want provisional", r.Status)
	}
	// denominator counts all three metrics (missing values contribute 0):
	// (100*10 + 0*10 + 0*10) / 30 = 33.33 — TASK §8, no renormalization
	if !approx(r.Score, 33.33, 0.01) {
		t.Errorf("score = %v, want 33.33", r.Score)
	}
}

func TestComputeTeamStaleness(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	old := now.Add(-2 * time.Hour)
	input := []MetricInput{
		{Key: "lat", Name: "lat", Type: "automated", Weight: 10, Required: true,
			Normalization: Normalization{Kind: KindRange, Min: 0, Max: 100},
			RawValue: fp(50), CapturedAt: &old, Staleness: 30 * time.Minute},
	}
	r := ComputeTeam(input, now, 1)
	if r.Breakdown[0].Status != StatusStale {
		t.Errorf("status = %v, want stale", r.Breakdown[0].Status)
	}
	if r.Status != "complete" {
		t.Errorf("stale value still counts as complete, got %v", r.Status)
	}
}

func TestComputeTeamJudgeCoverageBelowMin(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	input := []MetricInput{
		{Key: "tech", Name: "tech", Type: "manual", Weight: 10, Required: true,
			ScaleMin: 0, ScaleMax: 10, JudgeScores: []float64{7, 8}},
	}
	r := ComputeTeam(input, now, 3)
	if r.Status != "provisional" || r.Breakdown[0].Status != StatusProvisional {
		t.Errorf("expected provisional due to judge coverage, got %+v", r)
	}
	if !approx(r.Completeness, 0, 0.001) {
		t.Errorf("completeness = %v, want 0", r.Completeness)
	}
}

func TestComputeTeamDeterministic(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	input := []MetricInput{
		{Key: "x", Name: "x", Type: "manual", Weight: 10, Required: true,
			ScaleMin: 0, ScaleMax: 10, JudgeScores: []float64{5, 9, 3, 9, 5}},
	}
	a := ComputeTeam(input, now, 2)
	b := ComputeTeam(input, now, 2)
	if a.Score != b.Score || len(a.Breakdown) != len(b.Breakdown) {
		t.Error("engine is not deterministic")
	}
}
