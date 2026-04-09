package strategy

import (
	"context"

	"tradingbot/pkg/types"
)

type MeanReversion struct {
	id    string
	ins   []types.Instrument
	look  int
}

func NewMeanReversion(id string, ins []types.Instrument, params map[string]any) *MeanReversion {
	look := 20
	if v, ok := intFromParams(params, "lookback"); ok {
		look = v
	}
	return &MeanReversion{id: id, ins: ins, look: look}
}

func (m *MeanReversion) ID() string   { return m.id }
func (m *MeanReversion) Type() string { return "mean_reversion" }

func (m *MeanReversion) OnBar(ctx context.Context, b Bar) ([]types.Signal, error) {
	_ = ctx
	_ = m.look
	dir := 0.0
	if b.Low > 0 && b.Close < (b.High+b.Low)/2 {
		dir = 0.25
	} else {
		dir = -0.25
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
			Confidence: 0.35,
			Reason:     "mean_reversion_stub",
			Generated:  b.Time(),
		})
	}
	return out, nil
}
