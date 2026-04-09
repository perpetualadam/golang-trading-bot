package strategy

import (
	"context"

	"tradingbot/pkg/types"
)

type Breakout struct {
	id   string
	ins  []types.Instrument
	nbar int
}

func NewBreakout(id string, ins []types.Instrument, params map[string]any) *Breakout {
	n := 10
	if v, ok := intFromParams(params, "nbar"); ok {
		n = v
	}
	return &Breakout{id: id, ins: ins, nbar: n}
}

func (b *Breakout) ID() string   { return b.id }
func (b *Breakout) Type() string { return "breakout" }

func (b *Breakout) OnBar(ctx context.Context, bar Bar) ([]types.Signal, error) {
	_ = ctx
	_ = b.nbar
	dir := 0.0
	if bar.Close >= bar.High*0.999 {
		dir = 0.5
	} else if bar.Close <= bar.Low*1.001 {
		dir = -0.5
	}
	var out []types.Signal
	for _, in := range b.ins {
		if in.Symbol != bar.Instrument.Symbol {
			continue
		}
		out = append(out, types.Signal{
			StrategyID: b.id,
			Instrument: in,
			Direction:  dir,
			Confidence: 0.45,
			Reason:     "breakout_stub",
			Generated:  bar.Time(),
		})
	}
	return out, nil
}
