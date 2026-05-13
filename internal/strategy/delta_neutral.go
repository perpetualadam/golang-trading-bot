package strategy

import (
	"context"
	"math"

	"tradingbot/pkg/types"
)

// DeltaNeutral runs a rolling beta between two price series (OLS: leg1 ~ beta*leg2),
// then mean-reverts the level spread residual: leg1 - beta*leg2.
// Requires universe size >= 2. First symbol is the dependent leg in regression.
type DeltaNeutral struct {
	id   string
	leg1 types.Instrument
	leg2 types.Instrument

	betaWindow int
	lookback   int
	zEntry     float64

	lastPx map[string]float64
	pairs  [][2]float64 // simultaneous (p1,p2) snaps
	maxBuf int

	spreadHist []float64
}

// NewDeltaNeutral params:
//   - beta_window: points for rolling OLS beta (default 80)
//   - lookback: spread history for z-score (default 60)
//   - z_entry: |z| threshold (default 1.75)
func NewDeltaNeutral(id string, ins []types.Instrument, params map[string]any) *DeltaNeutral {
	bw := 80
	if v, ok := intFromParams(params, "beta_window"); ok && v >= 5 {
		bw = v
	}
	lb := 60
	if v, ok := intFromParams(params, "lookback"); ok && v >= 5 {
		lb = v
	}
	z := floatFromParams(params, "z_entry", 1.75)
	if z <= 0 {
		z = 1.75
	}
	maxPairs := bw + lb + 120
	d := &DeltaNeutral{
		id:         id,
		betaWindow: bw,
		lookback:   lb,
		zEntry:     z,
		lastPx:     make(map[string]float64),
		maxBuf:     maxPairs,
		spreadHist: make([]float64, 0, lb+20),
	}
	if len(ins) >= 2 {
		d.leg1, d.leg2 = ins[0], ins[1]
	}
	return d
}

func pushCapPair(pairs *[][2]float64, p1, p2 float64, max int) {
	*pairs = append(*pairs, [2]float64{p1, p2})
	if len(*pairs) > max {
		*pairs = (*pairs)[len(*pairs)-max:]
	}
}

func (d *DeltaNeutral) ID() string   { return d.id }
func (d *DeltaNeutral) Type() string { return "delta_neutral" }

func (d *DeltaNeutral) OnBar(ctx context.Context, b Bar) ([]types.Signal, error) {
	_ = ctx
	if d.leg1.Symbol == "" || d.leg2.Symbol == "" {
		return nil, nil
	}
	if b.Close <= 0 || math.IsNaN(b.Close) {
		return nil, nil
	}
	k := instKey(b.Instrument)
	d.lastPx[k] = b.Close

	k1, k2 := instKey(d.leg1), instKey(d.leg2)
	var p1, p2 float64
	var have bool
	switch k {
	case k1:
		p1 = b.Close
		p2 = d.lastPx[k2]
		have = p2 > 0
	case k2:
		p2 = b.Close
		p1 = d.lastPx[k1]
		have = p1 > 0
	default:
		return nil, nil
	}
	if !have {
		return nil, nil
	}

	pushCapPair(&d.pairs, p1, p2, d.maxBuf)
	if len(d.pairs) < d.betaWindow {
		return nil, nil
	}

	win := d.pairs[len(d.pairs)-d.betaWindow:]
	x := make([]float64, len(win))
	y := make([]float64, len(win))
	for i := range win {
		x[i] = win[i][0]
		y[i] = win[i][1]
	}
	beta, ok := olsSlopeXY(x, y)
	if !ok || math.Abs(beta) < 1e-9 {
		return nil, nil
	}

	res := p1 - beta*p2
	pushCapFloat(&d.spreadHist, res, d.lookback+80)
	if len(d.spreadHist) < d.lookback {
		return nil, nil
	}
	hist := d.spreadHist[len(d.spreadHist)-d.lookback:]
	cur := hist[len(hist)-1]
	prev := hist[:len(hist)-1]
	mu, sigma, ok := meanStd(prev)
	if !ok {
		return nil, nil
	}
	z := (cur - mu) / sigma
	if math.Abs(z) < d.zEntry {
		return nil, nil
	}

	dir := -math.Copysign(1.0, z)
	conf := 0.4 + 0.2*math.Min(1.0, (math.Abs(z)-d.zEntry)/(d.zEntry+0.5))
	if conf > 0.82 {
		conf = 0.82
	}

	return []types.Signal{
		{
			StrategyID: d.id,
			Instrument: d.leg1,
			Direction:  dir,
			Confidence: conf,
			Reason:     "delta_neutral_beta_spread_revert",
			Generated:  b.Time(),
		},
		{
			StrategyID: d.id,
			Instrument: d.leg2,
			Direction:  -dir * math.Copysign(1.0, beta),
			Confidence: conf,
			Reason:     "delta_neutral_beta_spread_revert",
			Generated:  b.Time(),
		},
	}, nil
}
