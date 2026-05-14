package exchange

import (
	"encoding/json"
	"testing"

	"tradingbot/pkg/types"
)

func TestApplyOANDABrackets_jsonShape(t *testing.T) {
	order := map[string]any{
		"type": "MARKET", "instrument": "EUR_USD", "units": "1000", "timeInForce": "FOK",
	}
	applyOANDABrackets(order, types.OrderIntent{
		StopLossPrice:   1.07234,
		TakeProfitPrice: 150.123,
	})
	b, err := json.Marshal(order)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	sl, ok := m["stopLossOnFill"].(map[string]any)
	if !ok {
		t.Fatalf("stopLossOnFill missing: %s", string(b))
	}
	if sl["price"] != "1.07234" {
		t.Fatalf("EUR-ish price: %#v", sl["price"])
	}
	tp, ok := m["takeProfitOnFill"].(map[string]any)
	if !ok {
		t.Fatal("takeProfitOnFill missing")
	}
	if tp["price"] != "150.123" {
		t.Fatalf("JPY-ish price: %#v", tp["price"])
	}
}

func TestFormatOANDAPrice(t *testing.T) {
	if g, w := formatOANDAPrice(1.1), "1.10000"; g != w {
		t.Fatalf("got %q want %q", g, w)
	}
	if g, w := formatOANDAPrice(155.2), "155.200"; g != w {
		t.Fatalf("got %q want %q", g, w)
	}
}
