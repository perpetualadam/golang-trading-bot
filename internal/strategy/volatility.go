package strategy

import (
	"context"
	"math"

	"tradingbot/pkg/types"
)

// Volatility trades only when short-horizon realized volatility expands vs a slow baseline,
// in the direction of an intermediate price trend (fast vs slow EMA on close).
type Volatility struct {
	id     string
	ins    []types.Instrument
	fastN  int
	slowN  int
	trFast float64 // alpha for EWMA of (TR/close)^2 proxy
	trSlow float64

	expansionRatio float64
	minTRRatio     float64 // ignore when TR/close below this noise floor

	state map[string]*volState
}

type volState struct {
	seen int

	trFastVar float64 // EWMA of squared TR ratio
	trSlowVar float64

	fastEMA float64
	slowEMA float64

	fastAlpha float64
	slowAlpha float64
}

func alphaFromHalfLife(h int) float64 {
	if h < 1 {
		h = 1
	}
	return 1.0 - math.Exp(-math.Ln2/float64(h))
}

// NewVolatility builds the strategy. Params:
//   - fast, slow: EMA periods on close for trend filter (default 8, 21)
//   - tr_half_life_fast, tr_half_life_slow: EWMA half-lives for TR/close volatility (default 5, 40)
//   - expansion_ratio: require fast vol > ratio * slow vol (default 1.15)
//   - min_tr_ratio: minimum TR/close to care about (default 1e-5)
func NewVolatility(id string, ins []types.Instrument, params map[string]any) *Volatility {
	fast, slow := 8, 21
	if v, ok := intFromParams(params, "fast"); ok && v > 0 {
		fast = v
	}
	if v, ok := intFromParams(params, "slow"); ok && v > 0 {
		slow = v
	}
	if slow < fast {
		fast, slow = slow, fast
	}
	hf := 5
	if v, ok := intFromParams(params, "tr_half_life_fast"); ok && v > 0 {
		hf = v
	}
	hs := 40
	if v, ok := intFromParams(params, "tr_half_life_slow"); ok && v > 0 {
		hs = v
	}
	exp := floatFromParams(params, "expansion_ratio", 1.15)
	minTR := floatFromParams(params, "min_tr_ratio", 1e-5)

	return &Volatility{
		id:             id,
		ins:            ins,
		fastN:          fast,
		slowN:          slow,
		trFast:         alphaFromHalfLife(hf),
		trSlow:         alphaFromHalfLife(hs),
		expansionRatio: exp,
		minTRRatio:     minTR,
		state:          make(map[string]*volState),
	}
}

func (v *Volatility) ID() string   { return v.id }
func (v *Volatility) Type() string { return "volatility" }

func (v *Volatility) OnBar(ctx context.Context, b Bar) ([]types.Signal, error) {
	_ = ctx

	var target *types.Instrument
	for i := range v.ins {
		in := &v.ins[i]
		if in.Venue == b.Instrument.Venue && in.Symbol == b.Instrument.Symbol {
			target = in
			break
		}
	}
	if target == nil || b.Close <= 0 || math.IsNaN(b.Close) {
		return nil, nil
	}

	key := instKey(*target)
	st, ok := v.state[key]
	if !ok {
		st = &volState{
			fastAlpha: 2.0 / (float64(v.fastN) + 1.0),
			slowAlpha: 2.0 / (float64(v.slowN) + 1.0),
		}
		v.state[key] = st
	}

	tr := b.High - b.Low
	if tr < 0 {
		tr = -tr
	}
	trRatio := tr / b.Close
	if trRatio < v.minTRRatio {
		// still update EMA state lightly using floor to avoid stalling
		trRatio = v.minTRRatio
	}

	if st.seen == 0 {
		st.trFastVar = trRatio * trRatio
		st.trSlowVar = trRatio * trRatio
		st.fastEMA = b.Close
		st.slowEMA = b.Close
	} else {
		st.trFastVar += v.trFast * (trRatio*trRatio - st.trFastVar)
		st.trSlowVar += v.trSlow * (trRatio*trRatio - st.trSlowVar)
		st.fastEMA += st.fastAlpha * (b.Close - st.fastEMA)
		st.slowEMA += st.slowAlpha * (b.Close - st.slowEMA)
	}
	st.seen++

	warmup := v.slowN
	if warmup < 15 {
		warmup = 15
	}
	if st.seen <= warmup || st.trSlowVar <= 0 {
		return nil, nil
	}

	volFast := math.Sqrt(st.trFastVar)
	volSlow := math.Sqrt(st.trSlowVar)
	if volFast <= v.expansionRatio*volSlow {
		return nil, nil
	}

	var dir float64
	if st.fastEMA > st.slowEMA {
		dir = 1.0
	} else if st.fastEMA < st.slowEMA {
		dir = -1.0
	} else {
		return nil, nil
	}

	conf := 0.45 + 0.25*math.Min(1.0, (volFast/(volSlow*v.expansionRatio))-1.0)
	if conf > 0.85 {
		conf = 0.85
	}

	return []types.Signal{{
		StrategyID: v.id,
		Instrument: *target,
		Direction:  dir,
		Confidence: conf,
		Reason:     "volatility_expansion_with_trend",
		Generated:  b.Time(),
	}}, nil
}
