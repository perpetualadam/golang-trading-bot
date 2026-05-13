package strategy

import (
	"tradingbot/internal/config"
	"tradingbot/internal/ml"
)

// StrategyDeps carries optional runtime dependencies for strategies that need
// inference or global ML settings (e.g. ml_meta).
type StrategyDeps struct {
	Infer ml.ONNXRunner
	ML    config.MLConfig
}
