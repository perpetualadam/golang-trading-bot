package exchange

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"tradingbot/pkg/types"
)

// FinnhubQuote is read-only (requires API token in Key).
type FinnhubQuote struct {
	Key  string
	HTTP HTTPDoer
}

func NewFinnhub(token string) *FinnhubQuote {
	return &FinnhubQuote{Key: token, HTTP: defaultClient()}
}

func (f *FinnhubQuote) Name() types.Venue { return "FINNHUB" }

func (f *FinnhubQuote) Health(ctx context.Context) error {
	if f.Key == "" {
		return ErrAuthRequired
	}
	u := "https://finnhub.io/api/v1/quote?symbol=AAPL&token=" + url.QueryEscape(f.Key)
	var v struct {
		C float64 `json:"c"`
	}
	return jsonGet(ctx, f.HTTP, u, nil, &v)
}

func (f *FinnhubQuote) FetchBookTop(ctx context.Context, ins types.Instrument) (types.BookTop, error) {
	if f.Key == "" {
		return types.BookTop{}, ErrAuthRequired
	}
	u := fmt.Sprintf("https://finnhub.io/api/v1/quote?symbol=%s&token=%s",
		url.QueryEscape(ins.Symbol), url.QueryEscape(f.Key))
	var q struct {
		C float64 `json:"c"` // current
		H float64 `json:"h"`
		L float64 `json:"l"`
	}
	if err := jsonGet(ctx, f.HTTP, u, nil, &q); err != nil {
		return types.BookTop{}, err
	}
	if q.C == 0 {
		return types.BookTop{}, fmt.Errorf("finnhub: no quote")
	}
	sp := q.C * 0.00015
	return types.BookTop{
		Instrument: ins,
		BidPrice:   q.C - sp,
		AskPrice:   q.C + sp,
		BidSize:    1,
		AskSize:    1,
		Timestamp:  time.Now().UTC(),
	}, nil
}

func (f *FinnhubQuote) PlaceOrder(ctx context.Context, o types.OrderIntent) (types.Order, error) {
	return types.Order{}, ErrReadOnly
}

func (f *FinnhubQuote) CancelOrder(ctx context.Context, exchangeOrderID string, ins types.Instrument) error {
	return ErrReadOnly
}

func (f *FinnhubQuote) CancelAll(ctx context.Context) error { return ErrReadOnly }

func (f *FinnhubQuote) FetchBalances(ctx context.Context) ([]types.Balance, error) {
	return nil, ErrReadOnly
}

func (f *FinnhubQuote) FetchPositions(ctx context.Context) ([]types.Position, error) {
	return nil, ErrReadOnly
}
