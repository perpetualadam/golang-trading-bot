package ml

// RegimeLabel is coarse market state from simple stats.
type RegimeLabel int

const (
	RegimeUnknown RegimeLabel = iota
	RegimeLowVol
	RegimeHighVol
	RegimeTrendUp
	RegimeTrendDown
)

// DetectRegime uses EWMA vol vs median and momentum sign.
func DetectRegime(closes []float64, halfLife int) RegimeLabel {
	if len(closes) < 8 {
		return RegimeUnknown
	}
	lr := Returns(closes)
	v := EWMAVol(lr, halfLife)
	// crude median vol proxy
	mid := len(lr) / 2
	v1 := EWMAVol(lr[:mid], halfLife)
	v2 := EWMAVol(lr[mid:], halfLife)
	if v > v1*1.5 && v > v2*1.5 {
		return RegimeHighVol
	}
	if v < v1*0.7 && v < v2*0.7 {
		return RegimeLowVol
	}
	if len(closes) >= 2 && closes[len(closes)-1] > closes[0]*1.02 {
		return RegimeTrendUp
	}
	if len(closes) >= 2 && closes[len(closes)-1] < closes[0]*0.98 {
		return RegimeTrendDown
	}
	return RegimeUnknown
}
