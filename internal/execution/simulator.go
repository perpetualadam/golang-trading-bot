package execution

import (
	"math"
	"math/rand"

	"tradingbot/pkg/types"
)

// FillSim applies fee and slippage for backtests (deterministic if rng seeded).
type FillSim struct {
	FeeBps      float64
	SlippageBps float64
}

// Apply returns executed price after side-dependent slippage and fee cash drain.
func (f *FillSim) Apply(mid float64, side types.Side, qty float64, rng *rand.Rand) (price float64, fee float64) {
	slip := f.SlippageBps / 10000
	if rng != nil {
		slip *= 1 + (rng.Float64()-0.5)*0.5
	}
	price = mid
	if side == types.SideBuy {
		price = mid * (1 + slip)
	} else {
		price = mid * (1 - slip)
	}
	notional := math.Abs(qty * price)
	fee = notional * (f.FeeBps / 10000)
	return price, fee
}
