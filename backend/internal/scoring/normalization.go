package scoring

import (
	"fmt"
	"math"
)

type Normalization struct {
	Kind   string  `json:"kind"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
	Target float64 `json:"target"`
	Lower  float64 `json:"lower"`
	Upper  float64 `json:"upper"`
	// ValidMin/ValidMax are optional plausibility bounds for raw ingested
	// values (e.g. latency ∈ [0, 60000]). Events outside them are stored with
	// status='invalid' and never become the current value, so a broken
	// collector cannot destroy the last correct measurement (TASK §6).
	ValidMin *float64 `json:"valid_min,omitempty"`
	ValidMax *float64 `json:"valid_max,omitempty"`
}

const (
	KindRange  = "range"
	KindTarget = "target"
	KindBinary = "binary"
	KindManual = "manual"

	DirectionHigher = "higher_is_better"
	DirectionLower  = "lower_is_better"
)

func (n Normalization) Validate() error {
	switch n.Kind {
	case KindRange:
		if n.Max <= n.Min {
			return fmt.Errorf("range normalization requires min < max")
		}
	case KindTarget:
		if n.Lower >= n.Target || n.Upper <= n.Target {
			return fmt.Errorf("target normalization requires lower < target < upper")
		}
	case KindBinary, KindManual:
	default:
		return fmt.Errorf("unknown normalization kind %q", n.Kind)
	}
	return nil
}

func clamp100(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func Normalize(n Normalization, direction string, value float64) (float64, error) {
	if err := n.Validate(); err != nil {
		return 0, err
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("value must be finite")
	}

	var norm float64
	switch n.Kind {
	case KindRange:
		if n.Max == n.Min {
			return 0, fmt.Errorf("range normalization requires min < max")
		}
		norm = (value - n.Min) / (n.Max - n.Min) * 100
		if direction == DirectionLower {
			norm = (n.Max - value) / (n.Max - n.Min) * 100
		}
	case KindTarget:
		switch {
		case value == n.Target:
			norm = 100
		case value > n.Target && value < n.Upper:
			norm = (n.Upper - value) / (n.Upper - n.Target) * 100
		case value < n.Target && value > n.Lower:
			norm = (value - n.Lower) / (n.Target - n.Lower) * 100
		default:
			norm = 0
		}
	case KindBinary:
		pass := value > 0
		if direction == DirectionLower {
			pass = value == 0
		}
		if pass {
			norm = 100
		}
	case KindManual:
		if n.Max <= n.Min {
			return 0, fmt.Errorf("manual normalization requires min < max")
		}
		norm = (value - n.Min) / (n.Max - n.Min) * 100
	}
	return clamp100(norm), nil
}
