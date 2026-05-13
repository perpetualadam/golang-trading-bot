package strategy

import (
	"context"
	"math"
	"sort"

	"tradingbot/pkg/types"
)

// OptionsGamma proxies long/short **realized volatility** positioning for spot/futures: when true range
// is in the high quantile of recent history, trade in the bar direction (“long gamma” vs quiet = fade).
// For instruments with Kind == MarketOption, returns nil until option greeks are plumbed into feeds.
type OptionsGamma struct {
	id     string
	ins    []types.Instrument
	window int
	highQ  float64 // e.g. 0.85 — spike threshold quantile
	lowQ   float64 // e.g. 0.15 — crush threshold
	trBuf  map[string][]float64
	maxBuf int
}

// NewOptionsGamma params:
//   - window: lookback for TR/close sample (default 60)
//   - high_quantile: trade with bar when TR rank >= this (default 0.82)
//   - low_quantile: fade bar when TR rank <= this (default 0.18)
func NewOptionsGamma(id string, ins []types.Instrument, params map[string]any) *OptionsGamma {
	w := 60
	if v, ok := intFromParams(params, "window"); ok && v >= 10 {
		w = v
	}
	hq := floatFromParams(params, "high_quantile", 0.82)
	lq := floatFromParams(params, "low_quantile", 0.18)
	if hq <= lq {
		hq, lq = 0.82, 0.18
	}
	return &OptionsGamma{
		id:     id,
		ins:    ins,
		window: w,
		highQ:  hq,
		lowQ:   lq,
		trBuf:  make(map[string][]float64),
		maxBuf: w + 30,
	}
}

func (o *OptionsGamma) ID() string   { return o.id }
func (o *OptionsGamma) Type() string { return "options_gamma" }

func (o *OptionsGamma) OnBar(ctx context.Context, b Bar) ([]types.Signal, error) {
	_ = ctx

	var target *types.Instrument
	for i := range o.ins {
		in := &o.ins[i]
		if in.Venue == b.Instrument.Venue && in.Symbol == b.Instrument.Symbol {
			target = in
			break
		}
	}
	if target == nil || b.Close <= 0 || math.IsNaN(b.Close) {
		return nil, nil
	}
	if target.Kind == types.MarketOption {
		return nil, nil
	}

	tr := b.High - b.Low
	if tr < 0 {
		tr = -tr
	}
	ratio := tr / b.Close

	key := instKey(*target)
	buf := o.trBuf[key]
	pushCapFloat(&buf, ratio, o.maxBuf)
	o.trBuf[key] = buf
	if len(buf) < o.window {
		return nil, nil
	}
	sample := buf[len(buf)-o.window:]
	cp := make([]float64, len(sample))
	copy(cp, sample)
	sort.Float64s(cp)
	rank := quantileRank(cp, ratio)
	if rank < 0 {
		return nil, nil
	}

	barDir := 0.0
	if b.Close > b.Open {
		barDir = 1
	} else if b.Close < b.Open {
		barDir = -1
	} else {
		return nil, nil
	}

	var dir float64
	var reason string
	switch {
	case rank >= o.highQ:
		dir = barDir
		reason = "options_gamma_high_realized_vol"
	case rank <= o.lowQ:
		dir = -barDir
		reason = "options_gamma_low_realized_vol_fade"
	default:
		return nil, nil
	}

	conf := 0.35 + 0.45*math.Min(1.0, math.Abs(rank-0.5)*2)
	if conf > 0.88 {
		conf = 0.88
	}

	return []types.Signal{{
		StrategyID: o.id,
		Instrument: *target,
		Direction:  dir,
		Confidence: conf,
		Reason:     reason,
		Generated:  b.Time(),
	}}, nil
}

// quantileRank returns empirical CDF (fraction of sample <= x) for sorted ascending sample.
func quantileRank(sortedSample []float64, x float64) float64 {
	n := len(sortedSample)
	if n == 0 {
		return -1
	}
	i := sort.Search(n, func(j int) bool { return sortedSample[j] > x })
	return float64(i) / float64(n)
}
