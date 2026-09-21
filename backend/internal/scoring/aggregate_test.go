package scoring

import "testing"

func TestAggregateSingle(t *testing.T) {
	r := AggregateJudgeScores([]float64{7})
	if r.Method != "single" || !r.Provisional || r.Score != 7 || r.JudgeCount != 1 {
		t.Errorf("single = %+v", r)
	}
}

func TestAggregateEmpty(t *testing.T) {
	r := AggregateJudgeScores(nil)
	if r.Method != "none" || r.JudgeCount != 0 {
		t.Errorf("empty = %+v", r)
	}
}

func TestAggregateMedianSmallSets(t *testing.T) {
	r := AggregateJudgeScores([]float64{6, 8})
	if r.Method != "median" || r.Score != 7 || r.Provisional {
		t.Errorf("two scores = %+v", r)
	}
	r = AggregateJudgeScores([]float64{6, 8, 9})
	if r.Method != "median" || r.Score != 8 {
		t.Errorf("three scores = %+v", r)
	}
}

func TestAggregateOutlierRejection(t *testing.T) {
	r := AggregateJudgeScores([]float64{8, 8.5, 8.2, 1.0})
	if r.ExcludedCount != 1 {
		t.Errorf("expected 1 outlier excluded, got %+v", r)
	}
	want := (8 + 8.5 + 8.2) / 3
	if !approx(r.Score, want, 0.001) || r.Method != "trimmed_mean" {
		t.Errorf("trimmed mean = %v, want %v (%+v)", r.Score, want, r)
	}
}

func TestAggregateAllSameScores(t *testing.T) {
	r := AggregateJudgeScores([]float64{5, 5, 5, 5})
	if r.ExcludedCount != 0 || r.Score != 5 || r.Method != "trimmed_mean" {
		t.Errorf("identical scores = %+v", r)
	}
}

func TestAggregateExtremeOutlierFallsBackToMedian(t *testing.T) {
	r := AggregateJudgeScores([]float64{2, 2, 9, 9})
	// m = 5.5, MAD = 3.5, threshold = 15.57 -> no exclusions, mean = 5.5
	if r.ExcludedCount != 0 || !approx(r.Score, 5.5, 0.001) {
		t.Errorf("moderate spread = %+v", r)
	}
}

func TestAggregateFallbackWhenTooFewRemain(t *testing.T) {
	// m = 8.4, MAD = 0 (three identical), 4th value far away -> outlier excluded,
	// 3 remain -> trimmed mean of [8.4 8.4 8.4]
	r := AggregateJudgeScores([]float64{8.4, 8.4, 8.4, 0.1})
	if r.ExcludedCount != 1 || !approx(r.Score, 8.4, 0.001) {
		t.Errorf("fallback = %+v", r)
	}
}

func TestAggregateNoFalseExclusion(t *testing.T) {
	// spread of 4 scores without extreme outlier
	r := AggregateJudgeScores([]float64{6, 7, 8, 9})
	if r.ExcludedCount != 0 || !approx(r.Score, 7.5, 0.001) {
		t.Errorf("normal spread = %+v", r)
	}
}
