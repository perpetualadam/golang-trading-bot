package risk

import (
	"testing"
	"time"

	"tradingbot/internal/config"
	"tradingbot/internal/portfolio"
	"tradingbot/pkg/types"
)

func TestMaxPositionQtyFromStop(t *testing.T) {
	e := NewEngine(&config.RiskConfig{MaxRiskPerTradePct: 1.0, DailyDrawdownHardPct: 99}, portfolio.NewState(100_000))
	q := e.MaxPositionQtyFromStop(100.0, 99.0, 100_000)
	if want := 1000.0; q != want {
		t.Fatalf("MaxPositionQtyFromStop got %v want %v", q, want)
	}
}

func TestPreTrade_OANDA_requiresExitsAndRiskAtStop(t *testing.T) {
	cfg := &config.RiskConfig{
		MaxRiskPerTradePct:    1.0,
		MaxSlippageBpsDefault: 100,
		RequireBrokerStops:    true,
		DailyDrawdownHardPct:  99,
	}
	e := NewEngine(cfg, portfolio.NewState(100_000))
	book := types.BookTop{BidPrice: 99.9, AskPrice: 100.1, BidSize: 1e6, AskSize: 1e6}

	intent := types.OrderIntent{
		Instrument: types.Instrument{Venue: "OANDA", Symbol: "EUR_USD"},
		Side:       types.SideBuy,
		Type:       types.OrderMarket,
		Quantity:   500,
	}
	if err := e.PreTrade(intent, book, 100_000); err != ErrMissingExitPlan {
		t.Fatalf("missing exits: got %v want ErrMissingExitPlan", err)
	}

	intent.StopLossPrice = 99.0
	intent.TakeProfitPrice = 101.0
	if err := e.PreTrade(intent, book, 100_000); err != nil {
		t.Fatal(err)
	}

	// risk at stop = 500 * (100.1 - 99) = 550; budget = 1000 → too big
	intent.Quantity = 1000
	if err := e.PreTrade(intent, book, 100_000); err != ErrExposureLimit {
		t.Fatalf("oversized qty: got %v want ErrExposureLimit", err)
	}
}

func TestPreTrade_optionalNotionalCap(t *testing.T) {
	cfg := &config.RiskConfig{
		MaxRiskPerTradePct:       5.0,
		MaxNotionalPerTradePct:   1.0,
		MaxSlippageBpsDefault:    100,
		RequireBrokerStops:       false,
		DailyDrawdownHardPct:     99,
	}
	e := NewEngine(cfg, portfolio.NewState(100_000))
	book := types.BookTop{BidPrice: 10.0, AskPrice: 10.0, BidSize: 1e6, AskSize: 1e6}
	// 500 * 10 = 5000 notional = 5% equity — allowed by risk%, but notional cap is 1%
	intent := types.OrderIntent{
		Instrument: types.Instrument{Venue: "BINANCE", Symbol: "BTCUSDT"},
		Side:       types.SideBuy,
		Type:       types.OrderMarket,
		Quantity:   500,
		CreatedAt:  time.Now().UTC(),
	}
	if err := e.PreTrade(intent, book, 100_000); err != ErrExposureLimit {
		t.Fatalf("got %v want ErrExposureLimit", err)
	}
}

func TestPreTrade_reduceOnlyNoEquity(t *testing.T) {
	cfg := &config.RiskConfig{MaxSlippageBpsDefault: 100, DailyDrawdownHardPct: 99}
	e := NewEngine(cfg, portfolio.NewState(0))
	book := types.BookTop{BidPrice: 1.0, AskPrice: 1.01, BidSize: 1e6, AskSize: 1e6}
	intent := types.OrderIntent{
		Instrument: types.Instrument{Symbol: "X"},
		Side:       types.SideSell,
		Type:       types.OrderMarket,
		Quantity:   1,
		ReduceOnly: true,
	}
	if err := e.PreTrade(intent, book, 0); err != nil {
		t.Fatal(err)
	}
}
