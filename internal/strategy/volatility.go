package strategy

import (
	"context"
	"math"

	"tradingbot/pkg/types"
)

type Volatility struct {
	id     string
	ins    []types.Instrument
	target float64
}

func NewVolatility(id string, ins []types.Instrument, params map[string]any) *Volatility {
	t := floatFromParams(params, "target_ann_vol", 0.2)
	return &Volatility{id: id, ins: ins, target: t}
}

func (v *Volatility) ID() string   { return v.id }
func (v *Volatility) Type() string { return "volatility" }

func (v *Volatility) OnBar(ctx context.Context, b Bar) ([]types.Signal, error) {
	_ = ctx
	tr := 0.0
	if b.Open > 0 {
		tr = math.Abs(b.Close-b.Open) / b.Open
	}
	dir := 0.0
	if tr < v.target/252 {
		dir = 0.1 // vol arb stub: lean long vol proxy
	} else {
		dir = -0.1
	}
	var out []types.Signal
	for _, in := range v.ins {
		if in.Symbol != b.Instrument.Symbol {
			continue
		}
		out = append(out, types.Signal{
			StrategyID: v.id,
			Instrument: in,
			Direction:  dir,
			Confidence: 0.3,
			Reason:     "volatility_stub",
			Generated:  b.Time(),
		})
	}
	return out, nil
}
