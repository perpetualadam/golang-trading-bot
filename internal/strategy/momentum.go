package strategy

import (
	"context"

	"tradingbot/pkg/types"
)

type Momentum struct {
	id   string
	ins  []types.Instrument
	fast int
	slow int
}

func NewMomentum(id string, ins []types.Instrument, params map[string]any) *Momentum {
	fast, slow := 5, 20
	if v, ok := intFromParams(params, "fast"); ok {
		fast = v
	}
	if v, ok := intFromParams(params, "slow"); ok {
		slow = v
	}
	return &Momentum{id: id, ins: ins, fast: fast, slow: slow}
}

func (m *Momentum) ID() string   { return m.id }
func (m *Momentum) Type() string { return "momentum" }

func (m *Momentum) OnBar(ctx context.Context, b Bar) ([]types.Signal, error) {
	_ = ctx
	// Placeholder: real impl maintains EMA state per instrument
	dir := 0.0
	if b.Close > b.Open {
		dir = 0.3
	} else if b.Close < b.Open {
		dir = -0.3
	}
	var out []types.Signal
	for _, in := range m.ins {
		if in.Symbol != b.Instrument.Symbol {
			continue
		}
		out = append(out, types.Signal{
			StrategyID: m.id,
			Instrument: in,
			Direction:  dir,
			Confidence: 0.4,
			Reason:     "momentum_stub",
			Generated:  b.Time(),
		})
	}
	return out, nil
}
