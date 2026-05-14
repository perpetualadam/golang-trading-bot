package strategy

import (
	"context"
	"time"

	"tradingbot/pkg/types"
)

// FlattenSessionEnd emits Signal{Flatten: true} once per calendar day per instrument during the last
// N minutes of the last trading hour before session_utc_end (same convention as ema_cross_atr: end exclusive).
// List this strategy before ema_cross_atr in config if you want flatten to run before entries on the same tick.
// Only simple sessions with session_utc_start < session_utc_end are supported (no overnight window).
type FlattenSessionEnd struct {
	id              string
	ins             []types.Instrument
	sessionEnabled  bool
	sessionStart    int
	sessionEnd      int
	minutesBeforeEnd int
	lastEmitted     map[string]string // instKey -> yyyy-mm-dd
}

// NewFlattenSessionEnd params:
//   - session_enabled (default true)
//   - session_utc_start, session_utc_end: same as ema (end exclusive); default 8, 17
//   - flatten_minutes_before_end: emit Flatten when minute >= 60 - N in hour (sessionEnd-1); default 5
func NewFlattenSessionEnd(id string, ins []types.Instrument, params map[string]any) *FlattenSessionEnd {
	sStart := 8
	if v, ok := intFromParams(params, "session_utc_start"); ok {
		sStart = clampHour(v)
	}
	sEnd := 17
	if v, ok := intFromParams(params, "session_utc_end"); ok {
		sEnd = clampHour(v)
	}
	sEn := boolFromParams(params, "session_enabled", true)
	mb := 5
	if v, ok := intFromParams(params, "flatten_minutes_before_end"); ok && v > 0 {
		mb = v
	}
	if mb > 59 {
		mb = 59
	}
	return &FlattenSessionEnd{
		id:               id,
		ins:              ins,
		sessionEnabled:   sEn,
		sessionStart:     sStart,
		sessionEnd:       sEnd,
		minutesBeforeEnd: mb,
		lastEmitted:      make(map[string]string),
	}
}

func (f *FlattenSessionEnd) ID() string   { return f.id }
func (f *FlattenSessionEnd) Type() string { return "flatten_session_end" }

func (f *FlattenSessionEnd) OnBar(ctx context.Context, b Bar) ([]types.Signal, error) {
	_ = ctx
	if !f.sessionEnabled {
		return nil, nil
	}
	if f.sessionStart >= f.sessionEnd {
		return nil, nil
	}
	var target *types.Instrument
	for i := range f.ins {
		in := &f.ins[i]
		if in.Venue == b.Instrument.Venue && in.Symbol == b.Instrument.Symbol {
			target = in
			break
		}
	}
	if target == nil {
		return nil, nil
	}
	t := time.Unix(b.Timestamp, 0).UTC()
	if !inSessionUTC(t, f.sessionStart, f.sessionEnd) {
		return nil, nil
	}
	if !f.inFlattenWindow(t) {
		return nil, nil
	}
	key := instKey(*target)
	day := t.Format("2006-01-02")
	if f.lastEmitted[key] == day {
		return nil, nil
	}
	f.lastEmitted[key] = day
	return []types.Signal{{
		StrategyID: f.id,
		Instrument: *target,
		Flatten:    true,
		Reason:     "flatten_session_end",
		Generated:  t,
	}}, nil
}

func (f *FlattenSessionEnd) inFlattenWindow(t time.Time) bool {
	lastTradingHour := f.sessionEnd - 1
	if lastTradingHour < 0 {
		return false
	}
	if t.Hour() != lastTradingHour {
		return false
	}
	if t.Minute() < 60-f.minutesBeforeEnd {
		return false
	}
	return true
}
