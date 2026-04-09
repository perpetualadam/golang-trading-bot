package execution

import (
	"time"

	"tradingbot/pkg/types"
)

// VWAPSlice holds cumulative volume for VWAP-style scheduling.
type VWAPSlice struct {
	Intervals int
	Weights   []float64 // optional per-interval volume weights; if nil, uniform
}

// Quantities splits parent qty across intervals using weights (or uniform).
func (v VWAPSlice) Quantities(total float64) []float64 {
	n := v.Intervals
	if n <= 0 {
		n = 1
	}
	out := make([]float64, n)
	if len(v.Weights) == n {
		sum := 0.0
		for _, w := range v.Weights {
			sum += w
		}
		if sum <= 0 {
			for i := range out {
				out[i] = total / float64(n)
			}
			return out
		}
		for i := range out {
			out[i] = total * v.Weights[i] / sum
		}
		return out
	}
	q := total / float64(n)
	for i := range out {
		out[i] = q
	}
	return out
}

// Schedule returns slice start times spread over duration.
func Schedule(start time.Time, duration time.Duration, intervals int) []time.Time {
	if intervals <= 0 {
		intervals = 1
	}
	step := duration / time.Duration(intervals)
	out := make([]time.Time, intervals)
	for i := range out {
		out[i] = start.Add(time.Duration(i) * step)
	}
	return out
}

// VWAPChildIntent builds child order intents from slices (price set by caller).
func VWAPChildIntent(parent types.OrderIntent, qty float64, t time.Time) types.OrderIntent {
	ch := parent
	ch.Quantity = qty
	ch.CreatedAt = t
	ch.ClientTag = "vwap_slice"
	return ch
}
