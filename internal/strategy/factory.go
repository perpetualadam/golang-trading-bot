package strategy

import (
	"fmt"
	"strings"

	"tradingbot/internal/config"
	"tradingbot/internal/ml"
	"tradingbot/pkg/types"
)

// BuildFromConfig returns enabled strategies from YAML entries.
func BuildFromConfig(cfgs []config.StrategyCfg, deps StrategyDeps) ([]Strategy, error) {
	if deps.Infer == nil {
		deps.Infer = ml.NewNoopONNX()
	}
	var out []Strategy
	for _, c := range cfgs {
		if !c.Enabled {
			continue
		}
		ins, err := universeInstruments(c.Universe)
		if err != nil {
			return nil, err
		}
		switch c.Type {
		case "momentum":
			out = append(out, NewMomentum(c.ID, ins, c.Params))
		case "mean_reversion":
			out = append(out, NewMeanReversion(c.ID, ins, c.Params))
		case "breakout":
			out = append(out, NewBreakout(c.ID, ins, c.Params))
		case "volatility":
			out = append(out, NewVolatility(c.ID, ins, c.Params))
		case "stat_arb":
			out = append(out, NewStatArb(c.ID, ins, c.Params))
		case "options_gamma":
			out = append(out, NewOptionsGamma(c.ID, ins, c.Params))
		case "delta_neutral":
			out = append(out, NewDeltaNeutral(c.ID, ins, c.Params))
		case "ml_meta":
			out = append(out, NewMLMeta(c.ID, ins, c.Params, deps.Infer, deps.ML))
		case "ema_cross_atr":
			out = append(out, NewEMACrossATR(c.ID, ins, c.Params))
		default:
			return nil, fmt.Errorf("unknown strategy type %q", c.Type)
		}
	}
	return out, nil
}

func universeInstruments(symbols []string) ([]types.Instrument, error) {
	out := make([]types.Instrument, 0, len(symbols))
	for _, s := range symbols {
		ins, err := ParseSymbol(s)
		if err != nil {
			return nil, err
		}
		out = append(out, ins)
	}
	return out, nil
}

// ParseSymbol format "VENUE:PAIR" e.g. "BINANCE:BTCUSDT", "OANDA:EUR_USD", or bare "BTCUSDT" (PAPER).
func ParseSymbol(s string) (types.Instrument, error) {
	var ins types.Instrument
	ins.Kind = types.MarketSpot
	ins.Class = types.AssetCrypto
	ins.TickSize = 0.01
	ins.LotSize = 0.0001
	rest := strings.TrimSpace(s)
	if rest == "" {
		return ins, fmt.Errorf("empty symbol")
	}
	if i := indexByte(rest, ':'); i >= 0 {
		ins.Venue = types.Venue(strings.ToUpper(rest[:i]))
		rest = rest[i+1:]
	} else {
		ins.Venue = "PAPER"
	}
	ins.Symbol = strings.TrimSpace(rest)
	if ins.Symbol == "" {
		return ins, fmt.Errorf("empty instrument in %q", s)
	}
	switch {
	case ins.Venue == "OANDA" || (len(ins.Symbol) == 7 && ins.Symbol[3] == '_'):
		ins.Class = types.AssetForex
		ins.TickSize = 1e-5
		ins.LotSize = 1
		parts := strings.Split(ins.Symbol, "_")
		if len(parts) == 2 && len(parts[0]) >= 3 && len(parts[1]) >= 3 {
			ins.Base = parts[0]
			ins.Quote = parts[1]
		} else {
			ins.Base = ins.Symbol
			ins.Quote = "USD"
		}
	case len(ins.Symbol) >= 6 && ins.Symbol[len(ins.Symbol)-4:] == "USDT":
		ins.Base = ins.Symbol[:len(ins.Symbol)-4]
		ins.Quote = "USDT"
	case len(ins.Symbol) >= 6 && ins.Symbol[len(ins.Symbol)-3:] == "USD":
		ins.Base = ins.Symbol[:len(ins.Symbol)-3]
		ins.Quote = "USD"
	default:
		ins.Base = ins.Symbol
		ins.Quote = "USD"
	}
	return ins, nil
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
