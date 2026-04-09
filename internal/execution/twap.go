package execution

import (
	"context"
	"time"

	"tradingbot/internal/exchange"
	"tradingbot/pkg/types"
)

// TWAP slices parent quantity over duration.
type TWAP struct {
	Connector exchange.Connector
	SliceQty  float64
	Interval  time.Duration
}

// Run executes slices until parent qty done or ctx canceled.
func (t *TWAP) Run(ctx context.Context, parent types.OrderIntent) error {
	remaining := parent.Quantity
	for remaining > 0 && ctx.Err() == nil {
		q := t.SliceQty
		if q > remaining {
			q = remaining
		}
		slice := parent
		slice.Quantity = q
		slice.CreatedAt = time.Now().UTC()
		_, err := t.Connector.PlaceOrder(ctx, slice)
		if err != nil {
			return err
		}
		remaining -= q
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(t.Interval):
		}
	}
	return nil
}
