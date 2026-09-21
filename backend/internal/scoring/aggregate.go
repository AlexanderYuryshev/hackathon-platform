package scoring

import (
	"sort"
)

type AggregateResult struct {
	Score         float64 `json:"score"`
	JudgeCount    int     `json:"judge_count"`
	ExcludedCount int     `json:"excluded_count"`
	Method        string  `json:"method"`
	Provisional   bool    `json:"provisional"`
}

func median(values []float64) float64 {
	n := len(values)
	if n == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func mad(values []float64, m float64) float64 {
	devs := make([]float64, len(values))
	for i, v := range values {
		devs[i] = abs(v - m)
	}
	return median(devs)
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// AggregateJudgeScores implements the outlier-resistant aggregation policy:
//   - 1 score: provisional
//   - 2-3 scores: median
//   - 4+ scores: MAD-based outlier rejection, then mean of the rest;
//     if fewer than 2 scores remain, median of the originals.
func AggregateJudgeScores(scores []float64) AggregateResult {
	n := len(scores)
	switch {
	case n == 0:
		return AggregateResult{Method: "none"}
	case n == 1:
		return AggregateResult{Score: scores[0], JudgeCount: 1, Method: "single", Provisional: true}
	case n <= 3:
		return AggregateResult{Score: median(scores), JudgeCount: n, Method: "median"}
	}

	m := median(scores)
	threshold := 3 * 1.4826 * mad(scores, m)
	kept := make([]float64, 0, n)
	excluded := 0
	for _, s := range scores {
		if abs(s-m) > threshold {
			excluded++
			continue
		}
		kept = append(kept, s)
	}
	if len(kept) < 2 {
		return AggregateResult{Score: m, JudgeCount: n, ExcludedCount: excluded, Method: "median_fallback"}
	}
	return AggregateResult{Score: mean(kept), JudgeCount: n, ExcludedCount: excluded, Method: "trimmed_mean"}
}
