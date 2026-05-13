package strategy

import (
	"testing"

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
