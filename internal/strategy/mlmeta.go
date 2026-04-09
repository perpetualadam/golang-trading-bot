package strategy

import (
	"context"

	"tradingbot/pkg/types"
)

type MLMeta struct {
	id  string
	ins []types.Instrument
}

func NewMLMeta(id string, ins []types.Instrument, params map[string]any) *MLMeta {
	_ = params
	return &MLMeta{id: id, ins: ins}
}

func (m *MLMeta) ID() string   { return m.id }
func (m *MLMeta) Type() string { return "ml_meta" }

func (m *MLMeta) OnBar(ctx context.Context, b Bar) ([]types.Signal, error) {
	_ = ctx
	var out []types.Signal
	for _, in := range m.ins {
		if in.Symbol != b.Instrument.Symbol {
			continue
		}
		out = append(out, types.Signal{
			StrategyID: m.id,
			Instrument: in,
			Direction:  0,
			Confidence: 0,
			Reason:     "ml_meta_onnx_hook_idle",
			Generated:  b.Time(),
		})
	}
	return out, nil
}
