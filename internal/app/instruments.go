package app

import (
	"fmt"

	"tradingbot/internal/config"
	"tradingbot/internal/strategy"
	"tradingbot/pkg/types"
)

func resolveLoopInstruments(cfg *config.Root) ([]types.Instrument, error) {
	if len(cfg.Runtime.Instruments) > 0 {
		out := make([]types.Instrument, 0, len(cfg.Runtime.Instruments))
		for _, s := range cfg.Runtime.Instruments {
			ins, err := strategy.ParseSymbol(s)
			if err != nil {
				return nil, fmt.Errorf("runtime.instruments %q: %w", s, err)
			}
			out = append(out, ins)
		}
		return out, nil
	}
	seen := make(map[string]struct{})
	var out []types.Instrument
	for _, st := range cfg.Strategies {
		if !st.Enabled {
			continue
		}
		for _, u := range st.Universe {
			ins, err := strategy.ParseSymbol(u)
			if err != nil {
				return nil, fmt.Errorf("strategy %s universe %q: %w", st.ID, u, err)
			}
			key := string(ins.Venue) + "|" + ins.Symbol
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, ins)
		}
	}
	if len(out) == 0 {
		ins, _ := strategy.ParseSymbol("BTCUSDT")
		return []types.Instrument{ins}, nil
	}
	return out, nil
}
