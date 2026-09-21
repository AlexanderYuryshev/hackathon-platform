package scoring

import (
	"math"
	"time"
)

type MetricInput struct {
	Key           string
	Name          string
	Type          string // automated | manual
	Weight        float64
	Required      bool
	Public        bool
	Direction     string
	Normalization Normalization
	Source        string
	Staleness     time.Duration

	RawValue   *float64
	CapturedAt *time.Time

	JudgeScores []float64
	ScaleMin    float64
	ScaleMax    float64
}

type BreakdownItem struct {
	Key                string    `json:"key"`
	Name               string    `json:"name"`
	Type               string    `json:"type"`
	Raw                *float64  `json:"raw"`
	Normalized         float64   `json:"normalized"`
	Weight             float64   `json:"weight"`
	Contribution       float64   `json:"contribution"`
	Status             string    `json:"status"` // fresh | stale | missing | provisional
	LastTimestamp      *time.Time `json:"last_timestamp"`
	Source             string    `json:"source"`
	JudgeCount         int       `json:"judge_count"`
	ExcludedCount      int       `json:"excluded_count"`
	AggregationMethod  string    `json:"aggregation_method"`
}

type TeamResult struct {
	Score        float64        `json:"score"`
	Status       string         `json:"status"` // provisional | complete
	Completeness float64        `json:"completeness"`
	Breakdown    []BreakdownItem `json:"breakdown"`
	Eligible     bool           `json:"eligible"` // eligible for final ranking
}

const (
	StatusFresh       = "fresh"
	StatusStale       = "stale"
	StatusMissing     = "missing"
	StatusProvisional = "provisional"
)

// ComputeTeam evaluates a team. minJudges is the minimum number of judge scores
// per manual criterion for the result to be considered complete.
func ComputeTeam(input []MetricInput, now time.Time, minJudges int) TeamResult {
	if minJudges < 1 {
		minJudges = 1
	}
	result := TeamResult{Status: "complete", Breakdown: make([]BreakdownItem, 0, len(input))}

	weightedSum := 0.0
	weightTotal := 0.0
	requiredTotal := 0
	requiredComplete := 0

	for _, m := range input {
		item := BreakdownItem{
			Key:    m.Key,
			Name:   m.Name,
			Type:   m.Type,
			Weight: m.Weight,
			Source: m.Source,
		}

		if m.Required {
			requiredTotal++
		}

		var normalized float64
		switch m.Type {
		case "manual":
			agg := AggregateJudgeScores(m.JudgeScores)
			item.JudgeCount = agg.JudgeCount
			item.ExcludedCount = agg.ExcludedCount
			item.AggregationMethod = agg.Method
			if agg.JudgeCount > 0 {
				v := agg.Score
				item.Raw = &v
				norm := m.Normalization
				norm.Kind = KindManual
				if m.ScaleMax > m.ScaleMin {
					norm.Min, norm.Max = m.ScaleMin, m.ScaleMax
				}
				normalized, _ = Normalize(norm, DirectionHigher, agg.Score)
			}
			if agg.JudgeCount == 0 {
				item.Status = StatusMissing
			} else if agg.Provisional || agg.JudgeCount < minJudges {
				item.Status = StatusProvisional
			} else {
				item.Status = StatusFresh
			}
			item.LastTimestamp = nil
		default:
			if m.RawValue != nil {
				v := *m.RawValue
				item.Raw = &v
				var err error
				normalized, err = Normalize(m.Normalization, m.Direction, v)
				if err != nil {
					normalized = 0
				}
				item.LastTimestamp = m.CapturedAt
				item.Status = StatusFresh
				if m.Staleness > 0 && m.CapturedAt != nil && now.Sub(*m.CapturedAt) > m.Staleness {
					item.Status = StatusStale
				}
			} else {
				item.Status = StatusMissing
			}
		}

		hasValue := item.Raw != nil
		complete := hasValue && (m.Type != "manual" || (item.JudgeCount >= minJudges))
		if m.Required && complete {
			requiredComplete++
		}
		item.Normalized = round2(normalized)
		// TASK §8: FinalScore = Σ(normᵢ×wᵢ) / Σ(wᵢ) over the full metric set.
		// A missing metric contributes 0 but its weight always stays in the
		// denominator — no renormalization over present values, which would
		// inflate scores of teams with gaps and make runs incomparable.
		weightTotal += m.Weight
		item.Contribution = round2(normalized * m.Weight / 100)
		weightedSum += normalized * m.Weight
		if m.Required && !complete {
			result.Status = "provisional"
		}

		result.Breakdown = append(result.Breakdown, item)
	}

	if weightTotal > 0 {
		result.Score = round2(weightedSum / weightTotal)
	} else {
		result.Score = 0
		result.Status = "provisional"
	}
	if requiredTotal > 0 {
		result.Completeness = round2(float64(requiredComplete) / float64(requiredTotal) * 100)
	} else {
		result.Completeness = 100
	}
	result.Eligible = result.Status == "complete"
	return result
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
