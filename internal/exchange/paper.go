package exchange

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"math"
	"sync"
	"time"

	"tradingbot/pkg/types"
)

// PaperConnector simulates order fills and balances; market data comes from public Binance spot book (no keys).
type PaperConnector struct {
	venueName types.Venue
	mu        sync.RWMutex
	balances  map[string]types.Balance
	positions map[string]types.Position
	orders    map[string]types.Order
}

func NewPaperConnector(venue types.Venue, initialUSD float64) *PaperConnector {
	return &PaperConnector{
		venueName: venue,
		balances: map[string]types.Balance{
			"USD": {Venue: venue, Asset: "USD", Free: initialUSD, Locked: 0, USDValue: initialUSD},
		},
		positions: make(map[string]types.Position),
		orders:    make(map[string]types.Order),
	}
}

func (p *PaperConnector) Name() types.Venue { return p.venueName }

func (p *PaperConnector) Health(ctx context.Context) error { return ctx.Err() }

func (p *PaperConnector) FetchBookTop(ctx context.Context, ins types.Instrument) (types.BookTop, error) {
	bt, err := BinancePublicBookTop(ctx, ins)
	if err != nil {
		return types.BookTop{}, err
	}
	return bt, ctx.Err()
}

func (p *PaperConnector) FetchBalances(ctx context.Context) ([]types.Balance, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]types.Balance, 0, len(p.balances))
	for _, b := range p.balances {
		out = append(out, b)
	}
	return out, ctx.Err()
}

func (p *PaperConnector) FetchPositions(ctx context.Context) ([]types.Position, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]types.Position, 0, len(p.positions))
	for _, pos := range p.positions {
		out = append(out, pos)
	}
	return out, ctx.Err()
}

func (p *PaperConnector) PlaceOrder(ctx context.Context, o types.OrderIntent) (types.Order, error) {
	book, err := BinancePublicBookTop(ctx, o.Instrument)
	if err != nil {
		return types.Order{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	id := randomID()
	mid := (book.BidPrice + book.AskPrice) / 2
	price := mid
	if o.Type == types.OrderLimit && o.LimitPrice > 0 {
		price = o.LimitPrice
	}
	notional := price * o.Quantity
	now := time.Now().UTC()
	ord := types.Order{
		ID:          id,
		ExchangeID:  "paper-" + id,
		Intent:      o,
		State:       types.OrderFilled,
		FilledQty:   o.Quantity,
		AvgPrice:    price,
		FeesPaid:    notional * 0.0004, // approximate taker fee; tune vs venue
		SubmittedAt: now,
		UpdatedAt:   now,
	}
	p.orders[id] = ord

	key := o.Instrument.Symbol
	pos, ok := p.positions[key]
	if !ok {
		pos = types.Position{Instrument: o.Instrument, UpdatedAt: now}
	}
	sign := 1.0
	if o.Side == types.SideSell {
		sign = -1.0
	}
	pos.Qty += sign * o.Quantity
	if pos.Qty != 0 {
		pos.AvgEntry = price // simplified
	}
	pos.UpdatedAt = now
	p.positions[key] = pos

	return ord, ctx.Err()
}

func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (p *PaperConnector) CancelOrder(ctx context.Context, exchangeOrderID string, ins types.Instrument) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, o := range p.orders {
		if o.ExchangeID == exchangeOrderID {
			o.State = types.OrderCanceled
			o.UpdatedAt = time.Now().UTC()
			p.orders[k] = o
			return ctx.Err()
		}
	}
	return ctx.Err()
}

// FlattenAll closes all open paper positions with market-style orders.
func (p *PaperConnector) FlattenAll(ctx context.Context) error {
	pos, err := p.FetchPositions(ctx)
	if err != nil {
		return err
	}
	for _, x := range pos {
		if math.Abs(x.Qty) < 1e-12 {
			continue
		}
		side := types.SideSell
		qty := x.Qty
		if x.Qty < 0 {
			side = types.SideBuy
			qty = -x.Qty
		}
		_, err := p.PlaceOrder(ctx, types.OrderIntent{
			StrategyID:     "nuke",
			Instrument:     x.Instrument,
			Side:           side,
			Type:           types.OrderMarket,
			Quantity:       qty,
			ReduceOnly:     true,
			MaxSlippageBps: 100,
			CreatedAt:      time.Now().UTC(),
		})
		if err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (p *PaperConnector) CancelAll(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now().UTC()
	for k, o := range p.orders {
		if o.State == types.OrderOpen || o.State == types.OrderPartial || o.State == types.OrderPending {
			o.State = types.OrderCanceled
			o.UpdatedAt = now
			p.orders[k] = o
		}
	}
	return ctx.Err()
}
