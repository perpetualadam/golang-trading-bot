package execution

import (
	"context"
	"errors"
	"testing"
	"time"

	"tradingbot/internal/exchange"
	"tradingbot/pkg/types"
)

type stubConn struct {
	name     types.Venue
	delay    time.Duration
	placeErr error
	cancels  int
}

func (s *stubConn) Name() types.Venue { return s.name }

func (s *stubConn) PlaceOrder(ctx context.Context, o types.OrderIntent) (types.Order, error) {
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return types.Order{}, ctx.Err()
		}
	}
	if s.placeErr != nil {
		return types.Order{}, s.placeErr
	}
	return types.Order{ID: "local", ExchangeID: "ex-1", FilledQty: 1, AvgPrice: 100}, nil
}

func (s *stubConn) CancelOrder(ctx context.Context, exchangeOrderID string, ins types.Instrument) error {
	s.cancels++
	return nil
}

func (s *stubConn) CancelAll(ctx context.Context) error { return nil }

func (s *stubConn) FetchBalances(ctx context.Context) ([]types.Balance, error) { return nil, nil }

func (s *stubConn) FetchPositions(ctx context.Context) ([]types.Position, error) { return nil, nil }

func (s *stubConn) FetchBookTop(ctx context.Context, ins types.Instrument) (types.BookTop, error) {
	return types.BookTop{}, nil
}

func (s *stubConn) Health(ctx context.Context) error { return nil }

func TestPlaceParallel_dedupesVenues(t *testing.T) {
	reg := exchange.NewRegistry()
	reg.Register(&stubConn{name: "BINANCE"})
	r := NewRouter(reg, RouterConfig{MaxParallel: 4, DefaultRPS: 1000, HardCancelMs: 0})
	intent := types.OrderIntent{Instrument: types.Instrument{Venue: "BINANCE", Symbol: "BTCUSDT"}}
	res := r.PlaceParallel(context.Background(), []types.Venue{"BINANCE", "BINANCE", "BINANCE"}, intent)
	if len(res) != 1 {
		t.Fatalf("want 1 result, got %d", len(res))
	}
	if res[0].Err != nil {
		t.Fatal(res[0].Err)
	}
}

func TestPlaceOne_slowRTT_triggersCancelWhenConfigured(t *testing.T) {
	reg := exchange.NewRegistry()
	st := &stubConn{name: "OANDA", delay: 120 * time.Millisecond}
	reg.Register(st)
	r := NewRouter(reg, RouterConfig{
		MaxParallel:  1,
		DefaultRPS:   1000,
		HardCancelMs: 50,
	})
	intent := types.OrderIntent{Instrument: types.Instrument{Venue: "OANDA", Symbol: "EUR_USD"}}
	res := r.placeOne(context.Background(), "OANDA", intent)
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if st.cancels != 1 {
		t.Fatalf("want 1 cancel after slow RTT, got %d", st.cancels)
	}
}

func TestPlaceOne_failedPlace_doesNotCancel(t *testing.T) {
	reg := exchange.NewRegistry()
	st := &stubConn{name: "OANDA", placeErr: errors.New("reject"), delay: 120 * time.Millisecond}
	reg.Register(st)
	r := NewRouter(reg, RouterConfig{MaxParallel: 1, DefaultRPS: 1000, HardCancelMs: 50})
	intent := types.OrderIntent{Instrument: types.Instrument{Venue: "OANDA", Symbol: "EUR_USD"}}
	res := r.placeOne(context.Background(), "OANDA", intent)
	if res.Err == nil {
		t.Fatal("expected error")
	}
	if st.cancels != 0 {
		t.Fatalf("cancel should not run on place error, got %d", st.cancels)
	}
}

func TestPlaceFallback_stopsAfterFirstSuccess(t *testing.T) {
	reg := exchange.NewRegistry()
	reg.Register(&stubConn{name: "BINANCE", placeErr: errors.New("down")})
	reg.Register(&stubConn{name: "OKX"})
	r := NewRouter(reg, RouterConfig{DefaultRPS: 1000, HardCancelMs: 0})
	intent := types.OrderIntent{Instrument: types.Instrument{Venue: "OKX", Symbol: "ETHUSDT"}}
	res, ok := r.PlaceFallback(context.Background(), []types.Venue{"BINANCE", "OKX"}, intent)
	if !ok || res.Err != nil || res.Venue != "OKX" {
		t.Fatalf("got ok=%v res=%+v", ok, res)
	}
}

func TestPlace_usesIntentVenue(t *testing.T) {
	reg := exchange.NewRegistry()
	reg.Register(&stubConn{name: "OANDA"})
	r := NewRouter(reg, RouterConfig{DefaultRPS: 1000})
	intent := types.OrderIntent{Instrument: types.Instrument{Venue: "OANDA", Symbol: "EUR_USD"}}
	out := r.Place(context.Background(), intent)
	if len(out) != 1 || out[0].Err != nil {
		t.Fatalf("%+v", out)
	}
}
