package strategy

import (
	"context"
	"testing"
	"time"

	"tradingbot/pkg/types"
)

func TestFlattenSessionEnd_emitsOncePerDay(t *testing.T) {
	ctx := context.Background()
	ins, _ := ParseSymbol("OANDA:EUR_USD")
	f := NewFlattenSessionEnd("f", []types.Instrument{ins}, map[string]any{
		"session_utc_start":            8,
		"session_utc_end":              17,
		"flatten_minutes_before_end":   5,
	})
	// Last trading hour 16; trigger at 16:57 UTC
	ts := time.Date(2025, 6, 2, 16, 57, 0, 0, time.UTC).Unix()
	sig, err := f.OnBar(ctx, Bar{Instrument: ins, Timestamp: ts, Close: 1.1})
	if err != nil {
		t.Fatal(err)
	}
	if len(sig) != 1 || !sig[0].Flatten {
		t.Fatalf("got %+v", sig)
	}
	sig2, err := f.OnBar(ctx, Bar{Instrument: ins, Timestamp: ts + 30, Close: 1.1})
	if err != nil {
		t.Fatal(err)
	}
	if len(sig2) != 0 {
		t.Fatalf("second same day: %+v", sig2)
	}
	nextDay := time.Date(2025, 6, 3, 16, 57, 0, 0, time.UTC).Unix()
	sig3, err := f.OnBar(ctx, Bar{Instrument: ins, Timestamp: nextDay, Close: 1.1})
	if err != nil {
		t.Fatal(err)
	}
	if len(sig3) != 1 {
		t.Fatalf("next day: %+v", sig3)
	}
}

func TestFlattenSessionEnd_outsideWindow(t *testing.T) {
	ctx := context.Background()
	ins, _ := ParseSymbol("OANDA:EUR_USD")
	f := NewFlattenSessionEnd("f", []types.Instrument{ins}, map[string]any{
		"session_utc_end":            17,
		"flatten_minutes_before_end": 5,
	})
	ts := time.Date(2025, 6, 2, 10, 0, 0, 0, time.UTC).Unix()
	sig, err := f.OnBar(ctx, Bar{Instrument: ins, Timestamp: ts, Close: 1.1})
	if err != nil {
		t.Fatal(err)
	}
	if len(sig) != 0 {
		t.Fatalf("got %+v", sig)
	}
}
