package strategy

import (
	"testing"

	"tradingbot/internal/config"
	"tradingbot/pkg/types"
)

func TestParseSymbol_oandaForex(t *testing.T) {
	ins, err := ParseSymbol("OANDA:EUR_USD")
	if err != nil {
		t.Fatal(err)
	}
	if ins.Venue != "OANDA" || ins.Symbol != "EUR_USD" {
		t.Fatalf("got venue=%q symbol=%q", ins.Venue, ins.Symbol)
	}
	if ins.Class != types.AssetForex || ins.Base != "EUR" || ins.Quote != "USD" {
		t.Fatalf("class/base/quote: %+v", ins)
	}
}

func TestParseSymbol_paperCrypto(t *testing.T) {
	ins, err := ParseSymbol("BTCUSDT")
	if err != nil {
		t.Fatal(err)
	}
	if ins.Venue != "PAPER" || ins.Symbol != "BTCUSDT" || ins.Class != types.AssetCrypto {
		t.Fatalf("%+v", ins)
	}
}

func TestParseSymbol_venueCase(t *testing.T) {
	ins, err := ParseSymbol("oanda:EUR_USD")
	if err != nil {
		t.Fatal(err)
	}
	if ins.Venue != "OANDA" {
		t.Fatalf("want uppercase venue, got %q", ins.Venue)
	}
}

func TestBuildFromConfig_flattenSessionEnd(t *testing.T) {
	strategies, err := BuildFromConfig([]config.StrategyCfg{
		{
			ID: "fl", Type: "flatten_session_end", Enabled: true,
			Params:   map[string]any{"session_utc_end": 17},
			Universe: []string{"OANDA:EUR_USD"},
		},
	}, StrategyDeps{})
	if err != nil {
		t.Fatal(err)
	}
	if len(strategies) != 1 || strategies[0].Type() != "flatten_session_end" {
		t.Fatalf("%+v", strategies)
	}
}
