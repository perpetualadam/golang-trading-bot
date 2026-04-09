package strategy

import (
	"math"
	"sync"

	"gonum.org/v1/gonum/stat"
)

// CorrelationMonitor tracks rolling correlation of strategy returns; reduces multiplier when elevated.
type CorrelationMonitor struct {
	mu       sync.Mutex
	window   int
	series   map[string][]float64 // strategyID -> returns
	threshold float64             // e.g. 0.7
}

func NewCorrelationMonitor(window int, threshold float64) *CorrelationMonitor {
	if window < 8 {
		window = 8
	}
	return &CorrelationMonitor{
		window:    window,
		series:    make(map[string][]float64),
		threshold: threshold,
	}
}

// Push adds a per-strategy return for last bar.
func (c *CorrelationMonitor) Push(strategyID string, ret float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.series[strategyID]
	s = append(s, ret)
	if len(s) > c.window {
		s = s[len(s)-c.window:]
	}
	c.series[strategyID] = s
}

// ExposureMultiplier returns 1..0 scale-down when average pairwise corr high.
func (c *CorrelationMonitor) ExposureMultiplier() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	ids := make([]string, 0, len(c.series))
	for id, xs := range c.series {
		if len(xs) >= c.window/2 {
			ids = append(ids, id)
		}
	}
	if len(ids) < 2 {
		return 1
	}
	sum := 0.0
	cnt := 0
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			a := c.series[ids[i]]
			b := c.series[ids[j]]
			n := min(len(a), len(b))
			if n < 4 {
				continue
			}
			r := stat.Correlation(a[len(a)-n:], b[len(b)-n:], nil)
			sum += math.Abs(r)
			cnt++
		}
	}
	if cnt == 0 {
		return 1
	}
	avg := sum / float64(cnt)
	if avg < c.threshold {
		return 1
	}
	// linear decay above threshold
	excess := (avg - c.threshold) / (1 - c.threshold)
	m := 1 - 0.5*excess
	if m < 0.25 {
		m = 0.25
	}
	return m
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
