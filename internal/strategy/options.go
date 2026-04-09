package strategy

import (
	"context"

	"tradingbot/pkg/types"
)

type OptionsGamma struct {
	id  string
	ins []types.Instrument
}

func NewOptionsGamma(id string, ins []types.Instrument, params map[string]any) *OptionsGamma {
	_ = params
	return &OptionsGamma{id: id, ins: ins}
}

func (o *OptionsGamma) ID() string   { return o.id }
func (o *OptionsGamma) Type() string { return "options_gamma" }

func (o *OptionsGamma) OnBar(ctx context.Context, b Bar) ([]types.Signal, error) {
	_ = ctx
	var out []types.Signal
	for _, in := range o.ins {
		if in.Symbol != b.Instrument.Symbol {
			continue
		}
		in.Kind = types.MarketOption
		out = append(out, types.Signal{
			StrategyID: o.id,
			Instrument: in,
			Direction:  0,
			Confidence: 0,
			Reason:     "options_gamma_requires_deribit_adapter",
			Generated:  b.Time(),
		})
	}
	return out, nil
}

type DeltaNeutral struct {
	id  string
	ins []types.Instrument
}

func NewDeltaNeutral(id string, ins []types.Instrument, params map[string]any) *DeltaNeutral {
	_ = params
	return &DeltaNeutral{id: id, ins: ins}
}

func (d *DeltaNeutral) ID() string   { return d.id }
func (d *DeltaNeutral) Type() string { return "delta_neutral" }

func (d *DeltaNeutral) OnBar(ctx context.Context, b Bar) ([]types.Signal, error) {
	_ = ctx
	var out []types.Signal
	for _, in := range d.ins {
		if in.Symbol != b.Instrument.Symbol {
			continue
		}
		out = append(out, types.Signal{
			StrategyID: d.id,
			Instrument: in,
			Direction:  0,
			Confidence: 0,
			Reason:     "delta_neutral_requires_options_chain",
			Generated:  b.Time(),
		})
	}
	return out, nil
}
