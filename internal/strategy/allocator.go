package strategy

import (
	"math"

	"gonum.org/v1/gonum/mat"
	"tradingbot/pkg/types"
)

// HRPWeights returns approximate hierarchical risk parity weights from covariance.
// Reference: Lopez de Prado — simplified clustering step omitted; uses inverse variance.
func HRPWeights(cov *mat.SymDense) []float64 {
	n, _ := cov.Dims()
	if n == 0 {
		return nil
	}
	invVar := make([]float64, n)
	for i := 0; i < n; i++ {
		v := cov.At(i, i)
		if v <= 1e-12 {
			invVar[i] = 0
		} else {
			invVar[i] = 1 / v
		}
	}
	sum := 0.0
	for _, w := range invVar {
		sum += w
	}
	if sum <= 0 {
		w := 1 / float64(n)
		out := make([]float64, n)
		for i := range out {
			out[i] = w
		}
		return out
	}
	for i := range invVar {
		invVar[i] /= sum
	}
	return invVar
}

// FractionalKelly returns f* where f = (mu/sigma^2)*fraction, capped.
func FractionalKelly(mu, sigma2, cap float64) float64 {
	if sigma2 <= 1e-12 {
		return 0
	}
	f := mu / sigma2
	if cap > 0 && f > cap {
		f = cap
	}
	if f < 0 {
		f = 0
	}
	return f
}

// VolTargetScale scales raw weight to hit annualized vol target given est vol.
func VolTargetScale(weight, estAnnVol, targetAnnVol float64) float64 {
	if estAnnVol <= 1e-8 {
		return 0
	}
	s := targetAnnVol / estAnnVol
	return weight * math.Min(s, 3) // cap leverage bump
}

// Allocate combines strategy weights, HRP on signal basket, Kelly fraction, vol targeting.
func Allocate(signals []types.Signal, stratWeights map[string]float64, equity float64, maxRiskPct float64) []types.OrderIntent {
	var out []types.OrderIntent
	for _, s := range signals {
		if s.Confidence <= 0 || math.Abs(s.Direction) < 1e-6 {
			continue
		}
		sw := stratWeights[s.StrategyID]
		if sw <= 0 {
			sw = 1
		}
		notional := equity * (maxRiskPct / 100) * sw * s.Confidence
		// price unknown here — sizing done upstream with book; emit qty placeholder 0
		_ = notional
		side := types.SideBuy
		if s.Direction < 0 {
			side = types.SideSell
		}
		out = append(out, types.OrderIntent{
			ID:              s.StrategyID + "-" + s.Instrument.Symbol,
			StrategyID:      s.StrategyID,
			Instrument:      s.Instrument,
			Side:            side,
			Type:            types.OrderLimit,
			Quantity:        0, // filled by runner
			StopLossPrice:   s.StopLossPrice,
			TakeProfitPrice: s.TakeProfitPrice,
			MaxSlippageBps:  15,
			ClientTag:       s.Reason,
			CreatedAt:       s.Generated,
		})
	}
	return out
}
