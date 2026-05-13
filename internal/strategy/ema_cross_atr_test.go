package strategy

import (
	"context"
	"testing"
	"time"

	"tradingbot/internal/config"
	"tradingbot/pkg/types"
)

func TestInSessionUTC_simpleWindow(t *testing.T) {
	if !inSessionUTC(time.Date(2025, 5, 1, 10, 0, 0, 0, time.UTC), 8, 17) {
		t.Fatal("10:00 should be inside 8–17")
	}
	if inSessionUTC(time.Date(2025, 5, 1, 7, 0, 0, 0, time.UTC), 8, 17) {
		t.Fatal("07:00 should be outside 8–17")
	}
	if inSessionUTC(time.Date(2025, 5, 1, 17, 0, 0, 0, time.UTC), 8, 17) {
		t.Fatal("17:00 end-exclusive should be outside")
	}
}

func TestBuildFromConfig_emaCrossATR(t *testing.T) {
	strategies, err := BuildFromConfig([]config.StrategyCfg{
		{
			ID:       "fx_ema",
			Type:     "ema_cross_atr",
			Enabled:  true,
			Params:   map[string]any{"fast": 9, "slow": 21},
			Universe: []string{"OANDA:EUR_USD"},
		},
	}, StrategyDeps{})
	if err != nil {
		t.Fatal(err)
	}
	if len(strategies) != 1 {
		t.Fatalf("want 1 strategy, got %d", len(strategies))
	}
	if strategies[0].Type() != "ema_cross_atr" {
		t.Fatalf("type %q", strategies[0].Type())
	}
}

// Session gate applies only to emissions; a bull cross outside the window must not fire.
func TestEMACrossATR_sessionBlocksEmit(t *testing.T) {
	ctx := context.Background()
	ins, err := ParseSymbol("OANDA:EUR_USD")
	if err != nil {
		t.Fatal(err)
	}
	params := map[string]any{
		"fast":              3,
		"slow":              5,
		"atr_period":        3,
		"min_atr_ratio":     0.0,
		"session_enabled":   true,
		"session_utc_start": 8,
		"session_utc_end":   17,
	}
	base := time.Date(2025, 5, 1, 10, 0, 0, 0, time.UTC)

	run := func(crossHour int) int {
		s := NewEMACrossATR("t", []types.Instrument{ins}, params)
		var nSig int
		// Warm-down closes so slow EMA stays above fast, then impulse up for cross
		for i := 0; i < 12; i++ {
			c := 1.20 - float64(i)*0.02
			h, low := c+0.005, c-0.005
			ts := base.Add(time.Duration(i) * time.Minute)
			if i == 11 {
				ts = time.Date(2025, 5, 2, crossHour, 0, 0, 0, time.UTC)
				c, h, low = 1.35, 1.38, 1.28
			}
			sig, err := s.OnBar(ctx, Bar{
				Instrument: ins,
				Timestamp:  ts.Unix(),
				Open:       c, High: h, Low: low, Close: c, Volume: 0,
			})
			if err != nil {
				t.Fatal(err)
			}
			nSig += len(sig)
		}
		return nSig
	}

	outside := run(5)
	inside := run(10)
	if outside > 0 {
		t.Fatalf("expected no signals outside London UTC window, got %d", outside)
	}
	if inside < 1 {
		t.Fatalf("expected at least one signal inside session, got %d", inside)
	}
}

func TestInstKey_stable(t *testing.T) {
	a, _ := ParseSymbol("OANDA:EUR_USD")
	if instKey(a) != "OANDA|EUR_USD" {
		t.Fatalf("got %q", instKey(a))
	}
}
