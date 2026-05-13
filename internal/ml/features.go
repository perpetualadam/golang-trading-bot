package ml

import (
	"math"

	"gonum.org/v1/gonum/stat"
)

// Returns computes simple log returns from closes.
func Returns(closes []float64) []float64 {
	if len(closes) < 2 {
		return nil
	}
	out := make([]float64, 0, len(closes)-1)
	for i := 1; i < len(closes); i++ {
		if closes[i-1] <= 0 || closes[i] <= 0 {
			continue
		}
		out = append(out, math.Log(closes[i]/closes[i-1]))
	}
	return out
}

// ZScoreLast vs window mean/std (population).
func ZScoreLast(window []float64) float64 {
	if len(window) < 2 {
		return 0
	}
	x := window[len(window)-1]
	mu := stat.Mean(window, nil)
	sd := stat.StdDev(window, nil)
	if sd < 1e-12 {
		return 0
	}
	return (x - mu) / sd
}

// EWMAVol annualized from log returns, half-life in bars.
func EWMAVol(logRet []float64, halfLife int) float64 {
	if len(logRet) == 0 || halfLife <= 0 {
		return 0
	}
	lambda := math.Exp(math.Log(0.5) / float64(halfLife))
	var v float64
	for i, r := range logRet {
		w := math.Pow(lambda, float64(len(logRet)-1-i))
		v += w * r * r
	}
	// normalize weights roughly
	sumw := (1 - math.Pow(lambda, float64(len(logRet)))) / (1 - lambda)
	if sumw <= 0 {
		return 0
	}
	perBarVar := v / sumw
	// annualize ~ 252 bars
	return math.Sqrt(perBarVar * 252)
}

// BarFeaturesFromCloses summarizes price history for ML models.
func BarFeaturesFromCloses(closes []float64) (zClose, ewmaVol float64) {
	if len(closes) == 0 {
		return 0, 0
	}
	z := ZScoreLast(closes)
	lr := Returns(closes)
	vol := EWMAVol(lr, 32)
	return z, vol
}
