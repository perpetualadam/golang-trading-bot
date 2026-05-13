package strategy

import (
	"context"
	"math"

	"tradingbot/internal/config"
	"tradingbot/internal/ml"
	"tradingbot/pkg/types"
)

// MLMeta feeds a short feature vector into ml.ONNXRunner.Predict (or a remote HTTP infer service).
// First model output is interpreted as a directional score in [-1,1]; magnitude maps to confidence.
// If inference returns empty (noop model / error), no signals are emitted.
// Optional drift guard uses global ML config drift_z_threshold against feature norm vs EWMA.
type MLMeta struct {
	id    string
	ins   []types.Instrument
	infer ml.ONNXRunner

	driftZ float64
	ewmaHL int

	featLen int
	buf     map[string][]float64

	ewmaNorm float64
	ewmaVar  float64
	seen     bool
}

// NewMLMeta params:
//   - feature_len: lookback closes for raw log-return features (default 12, min 4)
func NewMLMeta(id string, ins []types.Instrument, params map[string]any, infer ml.ONNXRunner, mlCfg config.MLConfig) *MLMeta {
	if infer == nil {
		infer = ml.NewNoopONNX()
	}
	fl := 12
	if v, ok := intFromParams(params, "feature_len"); ok && v >= 4 {
		fl = v
	}
	hl := mlCfg.EWMAHalfLifeBars
	if hl < 1 {
		hl = 32
	}
	dz := mlCfg.DriftZThreshold
	return &MLMeta{
		id:      id,
		ins:     ins,
		infer:   infer,
		driftZ:  dz,
		ewmaHL:  hl,
		featLen: fl,
		buf:     make(map[string][]float64),
	}
}

func (m *MLMeta) ID() string   { return m.id }
func (m *MLMeta) Type() string { return "ml_meta" }

func (m *MLMeta) OnBar(ctx context.Context, b Bar) ([]types.Signal, error) {
	_ = ctx

	var target *types.Instrument
	for i := range m.ins {
		in := &m.ins[i]
		if in.Venue == b.Instrument.Venue && in.Symbol == b.Instrument.Symbol {
			target = in
			break
		}
	}
	if target == nil || b.Close <= 0 || math.IsNaN(b.Close) {
		return nil, nil
	}

	key := instKey(*target)
	buf := m.buf[key]
	pushCapFloat(&buf, b.Close, m.featLen+2)
	m.buf[key] = buf
	if len(buf) < m.featLen {
		return nil, nil
	}
	closes := buf
	feats := make([]float32, 0, m.featLen-1)
	var norm float64
	for i := len(closes) - m.featLen + 1; i < len(closes); i++ {
		prev := closes[i-1]
		if prev <= 0 {
			return nil, nil
		}
		r := (closes[i] - prev) / prev
		if math.IsNaN(r) || math.IsInf(r, 0) {
			return nil, nil
		}
		feats = append(feats, float32(r))
		norm += r * r
	}
	norm = math.Sqrt(norm)

	alpha := 1.0 - math.Exp(-math.Ln2/float64(m.ewmaHL))
	if !m.seen {
		m.ewmaNorm = norm
		m.ewmaVar = 1e-8
		m.seen = true
	} else {
		d := norm - m.ewmaNorm
		m.ewmaNorm += alpha * d
		m.ewmaVar = (1-alpha)*m.ewmaVar + alpha*d*d
	}
	if m.driftZ > 0 && m.ewmaVar > 1e-18 {
		sig := math.Sqrt(m.ewmaVar)
		if sig > 0 && math.Abs(norm-m.ewmaNorm)/sig > m.driftZ {
			return nil, nil
		}
	}

	out, err := m.infer.Predict(feats)
	if err != nil || len(out) == 0 {
		return nil, nil
	}
	score := float64(out[0])
	if math.IsNaN(score) || math.IsInf(score, 0) {
		return nil, nil
	}
	if score > 1 {
		score = 1
	}
	if score < -1 {
		score = -1
	}
	if math.Abs(score) < 1e-6 {
		return nil, nil
	}
	dir := math.Copysign(1.0, score)
	conf := math.Abs(score)
	if conf > 1 {
		conf = 1
	}
	if conf < 0.05 {
		return nil, nil
	}

	return []types.Signal{{
		StrategyID: m.id,
		Instrument: *target,
		Direction:  dir,
		Confidence: conf,
		Reason:     "ml_meta_infer_score",
		Generated:  b.Time(),
	}}, nil
}
