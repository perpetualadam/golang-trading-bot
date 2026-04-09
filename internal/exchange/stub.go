package exchange

import (
	"context"

	"tradingbot/pkg/types"
)

// StubConnector returns ErrNotImplemented for all methods; use as a template for REST/WebSocket adapters.
type StubConnector struct {
	VenueName types.Venue
}

func (s *StubConnector) Name() types.Venue { return s.VenueName }

func (s *StubConnector) Health(ctx context.Context) error { return ErrNotImplemented }

func (s *StubConnector) PlaceOrder(ctx context.Context, o types.OrderIntent) (types.Order, error) {
	return types.Order{}, ErrNotImplemented
}

func (s *StubConnector) CancelOrder(ctx context.Context, exchangeOrderID string, ins types.Instrument) error {
	return ErrNotImplemented
}

func (s *StubConnector) CancelAll(ctx context.Context) error { return ErrNotImplemented }

func (s *StubConnector) FetchBalances(ctx context.Context) ([]types.Balance, error) {
	return nil, ErrNotImplemented
}

func (s *StubConnector) FetchPositions(ctx context.Context) ([]types.Position, error) {
	return nil, ErrNotImplemented
}

func (s *StubConnector) FetchBookTop(ctx context.Context, ins types.Instrument) (types.BookTop, error) {
	return types.BookTop{}, ErrNotImplemented
}
