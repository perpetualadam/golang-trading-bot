// Package exchange defines multi-venue adapters. Plug in native REST/WebSocket clients per venue.
package exchange

import (
	"context"
	"errors"

	"tradingbot/pkg/types"
)

var ErrNotImplemented = errors.New("exchange adapter not implemented")

// Connector is the minimal surface strategies and execution need.
type Connector interface {
	Name() types.Venue
	PlaceOrder(ctx context.Context, o types.OrderIntent) (types.Order, error)
	CancelOrder(ctx context.Context, exchangeOrderID string, ins types.Instrument) error
	CancelAll(ctx context.Context) error
	FetchBalances(ctx context.Context) ([]types.Balance, error)
	FetchPositions(ctx context.Context) ([]types.Position, error)
	FetchBookTop(ctx context.Context, ins types.Instrument) (types.BookTop, error)
	Health(ctx context.Context) error
}

// Registry holds enabled connectors keyed by venue name.
type Registry struct {
	byName map[string]Connector
}

func NewRegistry() *Registry {
	return &Registry{byName: make(map[string]Connector)}
}

func (r *Registry) Register(c Connector) {
	r.byName[string(c.Name())] = c
}

func (r *Registry) Get(name string) (Connector, bool) {
	c, ok := r.byName[name]
	return c, ok
}

func (r *Registry) All() []Connector {
	out := make([]Connector, 0, len(r.byName))
	for _, c := range r.byName {
		out = append(out, c)
	}
	return out
}
