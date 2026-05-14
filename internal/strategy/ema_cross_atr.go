package strategy

import (
	"context"
	"math"
	"strings"
	"time"

	"tradingbot/pkg/types"
)

// EMACrossATR implements dual EMA cross with a minimum ATR/close filter and
// optional UTC session window (e.g. London 08:00–17:00).
type EMACrossATR struct {
	id   string
	ins  []types.Instrument
	fast int
	slow int
	atrP int

	fastAlpha float64
	slowAlpha float64

	minATRRatio float64
	maxATRRatio float64 // 0 = disabled

	sessionEnabled bool
	sessionStart   int // UTC hour [0,23]
	sessionEnd     int // exclusive UTC hour

	stopATRMult  float64 // stop distance in ATR multiples (hard max loss)
	takeProfitRR float64 // take-profit distance as multiple of stop distance

	state map[string]*emaAtrBarState
}

type emaAtrBarState struct {
	seen int

	hasPrevClose bool
	prevClose    float64

	fastEMA float64
	slowEMA float64

	atrInitCount int
	sumTR        float64
	atr          float64
}

func instKey(in types.Instrument) string {
	return string(in.Venue) + "|" + strings.TrimSpace(in.Symbol)
}

// NewEMACrossATR builds the strategy. Params:
//   - fast, slow: EMA periods (default 12, 26)
//   - atr_period: Wilder ATR period (default 14)
//   - min_atr_ratio: require ATR/Close >= this (default 0.0001)
//   - max_atr_ratio: if > 0, skip when ATR/Close exceeds (default 0 = off)
//   - session_enabled: if true, only trade inside UTC window (default true)
//   - session_utc_start, session_utc_end: hour [0,23], end exclusive (default 8, 17)
//   - stop_atr_mult: stop distance = this * ATR from entry reference (default 1.5)
//   - take_profit_rr: take-profit distance = this * stop distance (default 2)
//
// Cross-style reversals (flatten when a new signal opposes the current position): set either
//   risk.exit_on_opposite_signal: true, or params.exit_on_opposite_signal: true (merged into risk at load).
//
// Flatten-only without a companion: use strategy type flatten_session_end (see NewFlattenSessionEnd), or any
// strategy that returns types.Signal{ Flatten: true }.
//
// Session filter applies only when emitting a signal after a cross: EMA/ATR still
// advance on every bar so the series stays consistent when the venue streams 24h data.
func NewEMACrossATR(id string, ins []types.Instrument, params map[string]any) *EMACrossATR {
	fast, slow := 12, 26
	if v, ok := intFromParams(params, "fast"); ok && v > 0 {
		fast = v
	}
	if v, ok := intFromParams(params, "slow"); ok && v > 0 {
		slow = v
	}
	atrP := 14
	if v, ok := intFromParams(params, "atr_period"); ok && v > 0 {
		atrP = v
	}
	if slow < fast {
		fast, slow = slow, fast
	}

	minR := floatFromParams(params, "min_atr_ratio", 0.0001)
	maxR := floatFromParams(params, "max_atr_ratio", 0)

	sStart := 8
	if v, ok := intFromParams(params, "session_utc_start"); ok {
		sStart = clampHour(v)
	}
	sEnd := 17
	if v, ok := intFromParams(params, "session_utc_end"); ok {
		sEnd = clampHour(v)
	}
	sEn := boolFromParams(params, "session_enabled", true)

	stopM := floatFromParams(params, "stop_atr_mult", 1.5)
	tpRR := floatFromParams(params, "take_profit_rr", 2)
	if stopM <= 0 {
		stopM = 1.5
	}
	if tpRR <= 0 {
		tpRR = 2
	}

	return &EMACrossATR{
		id:             id,
		ins:            ins,
		fast:           fast,
		slow:           slow,
		atrP:           atrP,
		fastAlpha:      2.0 / (float64(fast) + 1.0),
		slowAlpha:      2.0 / (float64(slow) + 1.0),
		minATRRatio:    minR,
		maxATRRatio:    maxR,
		sessionEnabled: sEn,
		sessionStart:   sStart,
		sessionEnd:     sEnd,
		stopATRMult:    stopM,
		takeProfitRR:   tpRR,
		state:          make(map[string]*emaAtrBarState),
	}
}

func clampHour(h int) int {
	if h < 0 {
		return 0
	}
	if h > 23 {
		return 23
	}
	return h
}

func (e *EMACrossATR) ID() string   { return e.id }
func (e *EMACrossATR) Type() string { return "ema_cross_atr" }

func (e *EMACrossATR) OnBar(ctx context.Context, b Bar) ([]types.Signal, error) {
	_ = ctx

	var target *types.Instrument
	for i := range e.ins {
		in := &e.ins[i]
		if in.Venue == b.Instrument.Venue && in.Symbol == b.Instrument.Symbol {
			target = in
			break
		}
	}
	if target == nil {
		return nil, nil
	}

	t := b.Time()

	if b.Close <= 0 || math.IsNaN(b.Close) {
		return nil, nil
	}

	key := instKey(*target)
	st, ok := e.state[key]
	if !ok {
		st = &emaAtrBarState{}
		e.state[key] = st
	}

	// True range
	var tr float64
	if !st.hasPrevClose {
		tr = b.High - b.Low
	} else {
		hl := b.High - b.Low
		hc := math.Abs(b.High - st.prevClose)
		lc := math.Abs(b.Low - st.prevClose)
		tr = math.Max(hl, math.Max(hc, lc))
	}

	// Wilder ATR
	if st.atrInitCount < e.atrP {
		st.sumTR += tr
		st.atrInitCount++
		if st.atrInitCount == e.atrP {
			st.atr = st.sumTR / float64(e.atrP)
		}
	} else {
		st.atr = (st.atr*float64(e.atrP-1) + tr) / float64(e.atrP)
	}

	oldFast, oldSlow := st.fastEMA, st.slowEMA
	if st.seen == 0 {
		st.fastEMA = b.Close
		st.slowEMA = b.Close
	} else {
		st.fastEMA += e.fastAlpha * (b.Close - st.fastEMA)
		st.slowEMA += e.slowAlpha * (b.Close - st.slowEMA)
	}
	st.seen++

	st.prevClose = b.Close
	st.hasPrevClose = true

	warmup := e.slow
	if e.atrP > warmup {
		warmup = e.atrP
	}
	if st.seen <= warmup || st.atrInitCount < e.atrP {
		return nil, nil
	}

	ratio := st.atr / b.Close
	if ratio < e.minATRRatio {
		return nil, nil
	}
	if e.maxATRRatio > 0 && ratio > e.maxATRRatio {
		return nil, nil
	}

	var dir float64
	var reason string
	if oldFast <= oldSlow && st.fastEMA > st.slowEMA {
		dir = 1.0
		reason = "ema_cross_atr_bull"
	} else if oldFast >= oldSlow && st.fastEMA < st.slowEMA {
		dir = -1.0
		reason = "ema_cross_atr_bear"
	}
	if dir == 0 {
		return nil, nil
	}

	if e.sessionEnabled && !inSessionUTC(t, e.sessionStart, e.sessionEnd) {
		return nil, nil
	}

	entry := b.Close
	riskDist := e.stopATRMult * st.atr
	var stopPx, tpPx float64
	if dir > 0 {
		stopPx = entry - riskDist
		tpPx = entry + e.takeProfitRR*riskDist
	} else {
		stopPx = entry + riskDist
		tpPx = entry - e.takeProfitRR*riskDist
	}

	return []types.Signal{{
		StrategyID:          e.id,
		Instrument:          *target,
		Direction:           dir,
		Confidence:          0.72,
		Reason:              reason,
		Generated:           t,
		EntryReferencePrice: entry,
		StopLossPrice:       stopPx,
		TakeProfitPrice:     tpPx,
	}}, nil
}

func inSessionUTC(t time.Time, startH, endH int) bool {
	h := t.UTC().Hour()
	if startH < endH {
		return h >= startH && h < endH
	}
	// overnight window e.g. 22–6
	return h >= startH || h < endH
}

// WarmupBars returns the minimum bar count before the strategy may emit a signal.
func (e *EMACrossATR) WarmupBars() int {
	w := e.slow
	if e.atrP > w {
		w = e.atrP
	}
	return w + 1
}
