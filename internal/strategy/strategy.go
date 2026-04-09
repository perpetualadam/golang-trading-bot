package strategy

import (
	"context"
	"time"

	"tradingbot/pkg/types"
)

// Strategy produces signals from market/feature state.
type Strategy interface {
	ID() string
	Type() string
	OnBar(ctx context.Context, b Bar) ([]types.Signal, error)
}

// Bar is OHLCV + optional features.
type Bar struct {
	Instrument types.Instrument
	Timestamp  int64
	Open       float64
	High       float64
	Low        float64
	Close      float64
	Volume     float64
}

// Time returns bar time as UTC.
func (b Bar) Time() time.Time {
	return time.Unix(b.Timestamp, 0).UTC()
}
