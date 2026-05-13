package strategy

import (
	"context"
	"math"

	"tradingbot/pkg/types"
)

// StatArb is two-leg mean reversion on a log spread: log(p1) - hedge*log(p2).
// Requires universe size >= 2 (first two symbols are the pair). If only one symbol, OnBar returns nil.
type StatArb struct {
	id    string
	leg1  types.Instrument
	leg2  types.Instrument
	hedge float64

	lookback int
	zEntry   float64

	lastPx  map[string]float64
	spreads []float64
	maxBuf  int
}

// NewStatArb params:
//   - hedge: log-spread hedge on leg2 (default 1)
//   - lookback: bars for spread z-score (default 60)
//   - z_entry: minimum |z| to trade (default 1.75)
func NewStatArb(id string, ins []types.Instrument, params map[string]any) *StatArb {
	h := floatFromParams(params, "hedge", 1.0)
	if math.Abs(h) < 1e-9 {
		h = 1.0
	}
	lb := 60
	if v, ok := intFromParams(params, "lookback"); ok && v >= 5 {
		lb = v
	}
	z := floatFromParams(params, "z_entry", 1.75)
	if z <= 0 {
		z = 1.75
	}
	st := &StatArb{
		id:       id,
		hedge:    h,
		lookback: lb,
		zEntry:   z,
		lastPx:   make(map[string]float64),
		maxBuf:   lb + 80,
	}
	if len(ins) >= 2 {
		st.leg1, st.leg2 = ins[0], ins[1]
	}
	return st
}

func (s *StatArb) ID() string   { return s.id }
func (s *StatArb) Type() string { return "stat_arb" }

func (s *StatArb) OnBar(ctx context.Context, b Bar) ([]types.Signal, error) {
	_ = ctx
	if s.leg1.Symbol == "" || s.leg2.Symbol == "" {
		return nil, nil
	}

	k := instKey(b.Instrument)
	if b.Close <= 0 || math.IsNaN(b.Close) {
		return nil, nil
	}
	s.lastPx[k] = b.Close

	k1, k2 := instKey(s.leg1), instKey(s.leg2)
	var p1, p2 float64
	var have bool
	switch k {
	case k1:
		p1 = b.Close
		p2 = s.lastPx[k2]
		have = p2 > 0
	case k2:
		p2 = b.Close
		p1 = s.lastPx[k1]
		have = p1 > 0
	default:
		return nil, nil
	}
	if !have {
		return nil, nil
	}

	sp := math.Log(p1) - s.hedge*math.Log(p2)
	pushCapFloat(&s.spreads, sp, s.maxBuf)
	if len(s.spreads) < s.lookback {
		return nil, nil
	}
	hist := s.spreads[len(s.spreads)-s.lookback:]
	if len(hist) < 3 {
		return nil, nil
	}
	cur := hist[len(hist)-1]
	prev := hist[:len(hist)-1]
	mu, sigma, ok := meanStd(prev)
	if !ok {
		return nil, nil
	}
	z := (cur - mu) / sigma
	if math.Abs(z) < s.zEntry {
		return nil, nil
	}

	// Mean reversion: high spread -> short leg1 / long leg2.
	dir := -math.Copysign(1.0, z)
	conf := 0.42 + 0.18*math.Min(1.0, (math.Abs(z)-s.zEntry)/(s.zEntry+0.5))
	if conf > 0.82 {
		conf = 0.82
	}

	return []types.Signal{
		{
			StrategyID: s.id,
			Instrument: s.leg1,
			Direction:  dir,
			Confidence: conf,
			Reason:     "stat_arb_spread_mean_revert",
			Generated:  b.Time(),
		},
		{
			StrategyID: s.id,
			Instrument: s.leg2,
			Direction:  -dir,
			Confidence: conf,
			Reason:     "stat_arb_spread_mean_revert",
			Generated:  b.Time(),
		},
	}, nil
}
