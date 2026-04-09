package ml

import "math"

// DriftDetector compares recent mean to baseline using z-diff (concept drift hook).
type DriftDetector struct {
	Threshold float64
}

func (d DriftDetector) IsDrift(recent, baseline []float64) bool {
	th := d.Threshold
	if th <= 0 {
		th = 3
	}
	if len(recent) < 4 || len(baseline) < 4 {
		return false
	}
	mr := mean(recent)
	mb := mean(baseline)
	sr := std(recent)
	sb := std(baseline)
	den := math.Sqrt(sr*sr/float64(len(recent)) + sb*sb/float64(len(baseline)))
	if den < 1e-12 {
		return false
	}
	z := math.Abs(mr-mb) / den
	return z > th
}

func mean(x []float64) float64 {
	s := 0.0
	for _, v := range x {
		s += v
	}
	return s / float64(len(x))
}

func std(x []float64) float64 {
	m := mean(x)
	s := 0.0
	for _, v := range x {
		d := v - m
		s += d * d
	}
	return math.Sqrt(s / float64(len(x)))
}
