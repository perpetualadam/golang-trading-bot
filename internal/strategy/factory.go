package strategy

import (
	"fmt"

	"tradingbot/internal/config"
	"tradingbot/pkg/types"
)

// BuildFromConfig returns enabled strategies from YAML entries.
func BuildFromConfig(cfgs []config.StrategyCfg) ([]Strategy, error) {
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
			out = append(out, NewMLMeta(c.ID, ins, c.Params))
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

// ParseSymbol format "VENUE:PAIR" e.g. "BINANCE:BTCUSDT" or bare "BTCUSDT".
func ParseSymbol(s string) (types.Instrument, error) {
	var ins types.Instrument
	ins.Kind = types.MarketSpot
	ins.Class = types.AssetCrypto
	ins.TickSize = 0.01
	ins.LotSize = 0.0001
	rest := s
	if i := indexByte(s, ':'); i >= 0 {
		ins.Venue = types.Venue(s[:i])
		rest = s[i+1:]
	} else {
		ins.Venue = "PAPER"
	}
	ins.Symbol = rest
	switch {
	case len(rest) >= 6 && rest[len(rest)-4:] == "USDT":
		ins.Base = rest[:len(rest)-4]
		ins.Quote = "USDT"
	case len(rest) >= 6 && rest[len(rest)-3:] == "USD":
		ins.Base = rest[:len(rest)-3]
		ins.Quote = "USD"
	default:
		ins.Base = rest
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
