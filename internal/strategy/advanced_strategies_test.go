package strategy

import (
	"context"
	"testing"
	"time"

	"tradingbot/internal/config"
	"tradingbot/pkg/types"
)

func TestStatArb_meanReversionPair(t *testing.T) {
	ctx := context.Background()
	eu, _ := ParseSymbol("OANDA:EUR_USD")
	gb, _ := ParseSymbol("OANDA:GBP_USD")
	s := NewStatArb("t", []types.Instrument{eu, gb}, map[string]any{
		"lookback": 20,
		"z_entry":  0.5,
		"hedge":    1.0,
	})
	base := time.Unix(1700000000, 0).UTC()
	// Pump correlated series then spike leg1 to force positive z on log-spread.
	for i := 0; i < 40; i++ {
		p := 1.10 + float64(i)*0.0005
		_, _ = s.OnBar(ctx, Bar{
			Instrument: eu, Timestamp: base.Add(time.Duration(i) * time.Minute).Unix(),
			Open: p, High: p + 0.002, Low: p - 0.002, Close: p, Volume: 1,
		})
		_, _ = s.OnBar(ctx, Bar{
			Instrument: gb, Timestamp: base.Add(time.Duration(i) * time.Minute).Unix(),
			Open: p * 0.88, High: p*0.88 + 0.002, Low: p*0.88 - 0.002, Close: p * 0.88, Volume: 1,
		})
	}
	spike := 1.22
	sig, err := s.OnBar(ctx, Bar{
		Instrument: eu, Timestamp: base.Add(41 * time.Minute).Unix(),
		Open: spike, High: spike + 0.01, Low: spike - 0.01, Close: spike, Volume: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sig) < 2 {
		t.Fatalf("expected 2 leg signals, got %d", len(sig))
	}
	if sig[0].Instrument.Symbol != eu.Symbol || sig[1].Instrument.Symbol != gb.Symbol {
		t.Fatalf("unexpected instruments: %+v %+v", sig[0].Instrument, sig[1].Instrument)
	}
	if sig[0].Direction*sig[1].Direction >= 0 {
		t.Fatalf("legs should have opposing directions, got %v %v", sig[0].Direction, sig[1].Direction)
	}
}

type fixedInfer struct {
	out []float32
}

func (f fixedInfer) Predict(features []float32) ([]float32, error) {
	_ = features
	return f.out, nil
}

func TestMLMeta_usesInferScore(t *testing.T) {
	ctx := context.Background()
	ins, _ := ParseSymbol("OANDA:EUR_USD")
	m := NewMLMeta("m", []types.Instrument{ins}, map[string]any{"feature_len": 6},
		fixedInfer{out: []float32{0.75}},
		config.MLConfig{DriftZThreshold: 0},
	)
	base := time.Unix(1700000000, 0).UTC()
	p := 1.20
	for i := 0; i < 8; i++ {
		p += 0.0003 * float64(i)
		sig, err := m.OnBar(ctx, Bar{
			Instrument: ins, Timestamp: base.Add(time.Duration(i) * time.Minute).Unix(),
			Open: p, High: p + 0.001, Low: p - 0.001, Close: p, Volume: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		if i < 5 {
			if len(sig) != 0 {
				t.Fatalf("warmup: want no signal at i=%d", i)
			}
			continue
		}
		if len(sig) != 1 {
			t.Fatalf("want 1 signal at i=%d got %d", i, len(sig))
		}
		if sig[0].Direction != 1 || sig[0].Confidence < 0.7 {
			t.Fatalf("got %+v", sig[0])
		}
	}
}

func TestOptionsGamma_volSpike(t *testing.T) {
	ctx := context.Background()
	ins, _ := ParseSymbol("OANDA:EUR_USD")
	o := NewOptionsGamma("g", []types.Instrument{ins}, map[string]any{
		"window":          15,
		"high_quantile":   0.7,
		"low_quantile":    0.3,
	})
	base := time.Unix(1700000000, 0).UTC()
	p := 1.20
	for i := 0; i < 14; i++ {
		_, _ = o.OnBar(ctx, Bar{
			Instrument: ins, Timestamp: base.Add(time.Duration(i) * time.Minute).Unix(),
			Open: p, High: p + 0.0001, Low: p - 0.0001, Close: p + 0.00005, Volume: 1,
		})
	}
	wide := 1.205
	sig, err := o.OnBar(ctx, Bar{
		Instrument: ins, Timestamp: base.Add(14 * time.Minute).Unix(),
		Open: wide, High: wide + 0.02, Low: wide - 0.02, Close: wide + 0.015, Volume: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sig) != 1 {
		t.Fatalf("expected signal on wide bar, got %d", len(sig))
	}
	if sig[0].Direction <= 0 {
		t.Fatalf("bullish wide bar should lean long, got %v", sig[0].Direction)
	}
}

func TestVolatility_expansionTrend(t *testing.T) {
	ctx := context.Background()
	ins, _ := ParseSymbol("OANDA:EUR_USD")
	v := NewVolatility("v", []types.Instrument{ins}, map[string]any{
		"fast":              3,
		"slow":              8,
		"tr_half_life_fast": 2,
		"tr_half_life_slow": 30,
		"expansion_ratio":   1.02,
		"min_tr_ratio":      1e-6,
	})
	base := time.Unix(1700000000, 0).UTC()
	p := 1.10
	for i := 0; i < 25; i++ {
		inc := 0.002
		if i > 18 {
			inc = 0.015
		}
		p += inc
		trW := 0.001
		if i > 18 {
			trW = 0.04
		}
		_, _ = v.OnBar(ctx, Bar{
			Instrument: ins, Timestamp: base.Add(time.Duration(i) * time.Minute).Unix(),
			Open: p - trW, High: p + trW, Low: p - trW, Close: p, Volume: 1,
		})
	}
	p += 0.02
	sig, err := v.OnBar(ctx, Bar{
		Instrument: ins, Timestamp: base.Add(26 * time.Minute).Unix(),
		Open: p - 0.02, High: p + 0.02, Low: p - 0.02, Close: p, Volume: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sig) != 1 {
		t.Fatalf("want vol expansion signal, got %v", sig)
	}
	if sig[0].Direction != 1 {
		t.Fatalf("uptrend + expansion should be long, got %v", sig[0].Direction)
	}
}
