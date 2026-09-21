package scoring

import (
	"math"
	"testing"
)

func approx(a, b, tol float64) bool {
	return math.Abs(a-b) <= tol
}

func TestNormalizeRange(t *testing.T) {
	n := Normalization{Kind: KindRange, Min: 0, Max: 200}
	cases := []struct {
		value float64
		dir   string
		want  float64
	}{
		{0, DirectionHigher, 0},
		{100, DirectionHigher, 50},
		{200, DirectionHigher, 100},
		{300, DirectionHigher, 100},
		{-10, DirectionHigher, 0},
		{0, DirectionLower, 100},
		{100, DirectionLower, 50},
		{200, DirectionLower, 0},
	}
	for _, c := range cases {
		got, err := Normalize(n, c.dir, c.value)
		if err != nil {
			t.Fatalf("Normalize(%v, %s, %v) error: %v", n, c.dir, c.value, err)
		}
		if !approx(got, c.want, 0.001) {
			t.Errorf("Normalize(range, %s, %v) = %v, want %v", c.dir, c.value, got, c.want)
		}
	}
}

func TestNormalizeTarget(t *testing.T) {
	n := Normalization{Kind: KindTarget, Target: 100, Lower: 50, Upper: 300}
	cases := []struct {
		value float64
		want  float64
	}{
		{100, 100},
		{75, 50},
		{50, 0},
		{49, 0},
		{200, 50},
		{300, 0},
		{400, 0},
	}
	for _, c := range cases {
		got, err := Normalize(n, DirectionHigher, c.value)
		if err != nil {
			t.Fatalf("Normalize(target, %v) error: %v", c.value, err)
		}
		if !approx(got, c.want, 0.001) {
			t.Errorf("Normalize(target, %v) = %v, want %v", c.value, got, c.want)
		}
	}
}

func TestNormalizeBinary(t *testing.T) {
	n := Normalization{Kind: KindBinary}
	if got, _ := Normalize(n, DirectionHigher, 1); got != 100 {
		t.Errorf("binary higher pass = %v, want 100", got)
	}
	if got, _ := Normalize(n, DirectionHigher, 0); got != 0 {
		t.Errorf("binary higher fail = %v, want 0", got)
	}
	if got, _ := Normalize(n, DirectionLower, 0); got != 100 {
		t.Errorf("binary lower zero = %v, want 100", got)
	}
	if got, _ := Normalize(n, DirectionLower, 5); got != 0 {
		t.Errorf("binary lower nonzero = %v, want 0", got)
	}
}

func TestNormalizeManual(t *testing.T) {
	n := Normalization{Kind: KindManual, Min: 0, Max: 10}
	if got, _ := Normalize(n, DirectionHigher, 8.4); !approx(got, 84, 0.001) {
		t.Errorf("manual 8.4/10 = %v, want 84", got)
	}
	if got, _ := Normalize(n, DirectionHigher, 10); got != 100 {
		t.Errorf("manual 10/10 = %v, want 100", got)
	}
}

func TestNormalizeInvalidConfig(t *testing.T) {
	if _, err := Normalize(Normalization{Kind: KindRange, Min: 100, Max: 10}, DirectionHigher, 5); err == nil {
		t.Error("expected error for min >= max")
	}
	if _, err := Normalize(Normalization{Kind: KindTarget, Target: 100, Lower: 150, Upper: 300}, DirectionHigher, 5); err == nil {
		t.Error("expected error for lower >= target")
	}
	if _, err := Normalize(Normalization{Kind: "bogus"}, DirectionHigher, 5); err == nil {
		t.Error("expected error for unknown kind")
	}
	if _, err := Normalize(Normalization{Kind: KindRange, Min: 0, Max: 10}, DirectionHigher, math.NaN()); err == nil {
		t.Error("expected error for NaN")
	}
}
