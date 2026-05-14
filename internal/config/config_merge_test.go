package config

import (
	"testing"

	"tradingbot/pkg/types"
)

func TestMergeStrategyRiskFlags_emaExitOnOpposite(t *testing.T) {
	c := &Root{
		Risk: RiskConfig{ExitOnOppositeSignal: false},
		Strategies: []StrategyCfg{
			{ID: "e", Type: "ema_cross_atr", Enabled: true, Params: map[string]any{"exit_on_opposite_signal": true}},
			{ID: "m", Type: "momentum", Enabled: true},
		},
	}
	mergeStrategyRiskFlags(c)
	if !c.Risk.ExitOnOppositeSignal {
		t.Fatal("expected ExitOnOppositeSignal true from ema param")
	}
}

func TestMergeStrategyRiskFlags_preservesRiskTrue(t *testing.T) {
	c := &Root{
		Risk: RiskConfig{ExitOnOppositeSignal: true},
		Strategies: []StrategyCfg{
			{ID: "e", Type: "ema_cross_atr", Enabled: true, Params: map[string]any{}},
		},
	}
	mergeStrategyRiskFlags(c)
	if !c.Risk.ExitOnOppositeSignal {
		t.Fatal("expected stay true")
	}
}

func TestMergeStrategyRiskFlags_disabledStratIgnored(t *testing.T) {
	c := &Root{
		Risk: RiskConfig{ExitOnOppositeSignal: false},
		Strategies: []StrategyCfg{
			{ID: "e", Type: "ema_cross_atr", Enabled: false, Params: map[string]any{"exit_on_opposite_signal": true}},
		},
	}
	mergeStrategyRiskFlags(c)
	if c.Risk.ExitOnOppositeSignal {
		t.Fatal("disabled strategy should not merge")
	}
}

func TestParamBoolOR(t *testing.T) {
	if !paramBoolOR(map[string]any{"exit_on_opposite": "1"}, "exit_on_opposite_signal", "exit_on_opposite") {
		t.Fatal("alias exit_on_opposite")
	}
	if paramBoolOR(nil, "a") {
		t.Fatal("nil map")
	}
	if !paramBoolOR(map[string]any{"exit_on_opposite_signal": true}, "exit_on_opposite_signal") {
		t.Fatal("bool true")
	}
}

func TestMode_root(t *testing.T) {
	c := &Root{Runtime: RuntimeConfig{Mode: "paper"}}
	if c.Mode() != types.ModePaper {
		t.Fatal(c.Mode())
	}
}
