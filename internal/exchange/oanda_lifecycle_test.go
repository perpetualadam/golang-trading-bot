package exchange

import (
	"encoding/json"
	"strings"
	"testing"

	"tradingbot/pkg/types"
)

func TestOANDASanitizeClientID(t *testing.T) {
	if g, w := oandaSanitizeClientID("a:b|c"), "a_b_c"; g != w {
		t.Fatalf("got %q want %q", g, w)
	}
	if g := oandaSanitizeClientID(strings.Repeat("x", 60)); len(g) != 50 {
		t.Fatalf("len %d", len(g))
	}
}

func TestParseOANDAOrderCreateResponse_fillWithTrade(t *testing.T) {
	raw := `{
  "orderCreateTransaction": {"id": "oc1", "units": "1000"},
  "orderFillTransaction": {
    "orderID": "order-1",
    "units": "1000",
    "price": "1.10000",
    "tradeOpenedID": "8844",
    "commission": "0",
    "guaranteedExecutionFee": "0"
  }
}`
	intent := types.OrderIntent{ID: "strat-OANDA-EUR_USD-123"}
	ord, err := parseOANDAOrderCreateResponse([]byte(raw), intent)
	if err != nil {
		t.Fatal(err)
	}
	if ord.State != types.OrderFilled {
		t.Fatalf("state %s", ord.State)
	}
	if ord.ExchangeID != "order-1" || ord.ExchangeTradeID != "8844" {
		t.Fatalf("ids %+v", ord)
	}
	if ord.FilledQty != 1000 || ord.AvgPrice != 1.1 {
		t.Fatalf("fill %+v", ord)
	}
}

func TestParseOANDAOrderCreateResponse_partial(t *testing.T) {
	raw := `{
  "orderCreateTransaction": {"id": "oc1", "units": "1000"},
  "orderFillTransaction": {
    "orderID": "order-2",
    "units": "400",
    "price": "1.20000",
    "tradeOpenedID": "99",
    "commission": "0",
    "guaranteedExecutionFee": "0"
  }
}`
	intent := types.OrderIntent{ID: "x"}
	ord, err := parseOANDAOrderCreateResponse([]byte(raw), intent)
	if err != nil {
		t.Fatal(err)
	}
	if ord.State != types.OrderPartial {
		t.Fatalf("state %s", ord.State)
	}
}

func TestParseOANDAOrderCreateResponse_reject(t *testing.T) {
	raw := `{"orderRejectTransaction": {"rejectReason": "MARGIN_CHECK"}}`
	_, err := parseOANDAOrderCreateResponse([]byte(raw), types.OrderIntent{ID: "a"})
	if err == nil || !strings.Contains(err.Error(), "MARGIN_CHECK") {
		t.Fatalf("got %v", err)
	}
}

func TestApplyOANDAClientExtensions_shape(t *testing.T) {
	inner := map[string]any{"type": "MARKET", "units": "10", "instrument": "EUR_USD", "timeInForce": "FOK"}
	applyOANDAClientExtensions(inner, types.OrderIntent{
		ID: "fx:SIGNAL", StrategyID: "ema", ClientTag: "reason_tag",
	})
	b, err := json.Marshal(inner)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"id"`) || !strings.Contains(string(b), `reason_tag`) {
		t.Fatalf("%s", string(b))
	}
}
