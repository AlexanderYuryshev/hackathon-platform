package scoring

import "testing"

func TestAssignRanksLive(t *testing.T) {
	results := []TeamResult{
		{Score: 90, Eligible: true},
		{Score: 85, Eligible: true},
		{Score: 85, Eligible: true},
		{Score: 70, Eligible: true},
	}
	ranks := assignRanks(results, "live")
	want := []int{1, 2, 2, 4}
	for i := range want {
		if ranks[i] != want[i] {
			t.Fatalf("live ranks = %v, want %v", ranks, want)
		}
	}
}

func TestAssignRanksFinalSkipsIneligible(t *testing.T) {
	results := []TeamResult{
		{Score: 95, Eligible: false},
		{Score: 90, Eligible: true},
		{Score: 85, Eligible: false},
		{Score: 80, Eligible: true},
		{Score: 80, Eligible: true},
	}
	ranks := assignRanks(results, "final")
	want := []int{0, 1, 0, 2, 2}
	for i := range want {
		if ranks[i] != want[i] {
			t.Fatalf("final ranks = %v, want %v (ineligible must not consume rank positions)", ranks, want)
		}
	}
}

func TestAssignRanksZeroScores(t *testing.T) {
	results := []TeamResult{
		{Score: 0, Eligible: true},
		{Score: 0, Eligible: true},
	}
	ranks := assignRanks(results, "live")
	if ranks[0] != 1 || ranks[1] != 1 {
		t.Fatalf("zero-score tie ranks = %v, want [1 1]", ranks)
	}
}
