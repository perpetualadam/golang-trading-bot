package exchange

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"tradingbot/pkg/types"
)

// PolygonAgg is read-only last trade / quote (Key = polygon API key).
type PolygonAgg struct {
	Key  string
	HTTP HTTPDoer
}

func NewPolygon(key string) *PolygonAgg {
	return &PolygonAgg{Key: key, HTTP: defaultClient()}
}

func (p *PolygonAgg) Name() types.Venue { return "POLYGON" }

func (p *PolygonAgg) Health(ctx context.Context) error {
	if p.Key == "" {
		return ErrAuthRequired
	}
	u := "https://api.polygon.io/v1/marketstatus/now?apiKey=" + url.QueryEscape(p.Key)
	return jsonGet(ctx, p.HTTP, u, nil, &json.RawMessage{})
}

func (p *PolygonAgg) FetchBookTop(ctx context.Context, ins types.Instrument) (types.BookTop, error) {
	if p.Key == "" {
		return types.BookTop{}, ErrAuthRequired
	}
	// Stocks: /v2/last/trade/{ticker}
	u := fmt.Sprintf("https://api.polygon.io/v2/last/trade/%s?apiKey=%s", url.PathEscape(ins.Symbol), url.QueryEscape(p.Key))
	var wrap struct {
		Results struct {
			P float64 `json:"p"`
		} `json:"results"`
	}
	if err := jsonGet(ctx, p.HTTP, u, nil, &wrap); err != nil {
		return types.BookTop{}, err
	}
	px := wrap.Results.P
	if px == 0 {
		return types.BookTop{}, fmt.Errorf("polygon: no trade")
	}
	sp := px * 0.0001
	return types.BookTop{
		Instrument: ins,
		BidPrice:   px - sp,
		AskPrice:   px + sp,
		BidSize:    1,
		AskSize:    1,
		Timestamp:  time.Now().UTC(),
	}, nil
}

func (p *PolygonAgg) PlaceOrder(ctx context.Context, o types.OrderIntent) (types.Order, error) {
	return types.Order{}, ErrReadOnly
}

func (p *PolygonAgg) CancelOrder(ctx context.Context, exchangeOrderID string, ins types.Instrument) error {
	return ErrReadOnly
}

func (p *PolygonAgg) CancelAll(ctx context.Context) error { return ErrReadOnly }

func (p *PolygonAgg) FetchBalances(ctx context.Context) ([]types.Balance, error) {
	return nil, ErrReadOnly
}

func (p *PolygonAgg) FetchPositions(ctx context.Context) ([]types.Position, error) {
	return nil, ErrReadOnly
}
