package strategy

import (
	"context"

	"tradingbot/pkg/types"
)

type StatArb struct {
	id  string
	ins []types.Instrument
}

func NewStatArb(id string, ins []types.Instrument, params map[string]any) *StatArb {
	_ = params
	return &StatArb{id: id, ins: ins}
}

func (s *StatArb) ID() string   { return s.id }
func (s *StatArb) Type() string { return "stat_arb" }

func (s *StatArb) OnBar(ctx context.Context, b Bar) ([]types.Signal, error) {
	_ = ctx
	var out []types.Signal
	for _, in := range s.ins {
		if in.Symbol != b.Instrument.Symbol {
			continue
		}
		out = append(out, types.Signal{
			StrategyID: s.id,
			Instrument: in,
			Direction:  0.05,
			Confidence: 0.2,
			Reason:     "stat_arb_pair_stub",
			Generated:  b.Time(),
		})
	}
	return out, nil
}
