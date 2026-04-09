package exchange

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"tradingbot/pkg/types"
)

// TwelveDataQuote is read-only (API key in Key).
type TwelveDataQuote struct {
	Key  string
	HTTP HTTPDoer
}

func NewTwelveData(token string) *TwelveDataQuote {
	return &TwelveDataQuote{Key: token, HTTP: defaultClient()}
}

func (t *TwelveDataQuote) Name() types.Venue { return "TWELVEDATA" }

func (t *TwelveDataQuote) Health(ctx context.Context) error {
	if t.Key == "" {
		return ErrAuthRequired
	}
	u := "https://api.twelvedata.com/time_series?symbol=AAPL&interval=1day&outputsize=1&apikey=" + url.QueryEscape(t.Key)
	return jsonGet(ctx, t.HTTP, u, nil, &json.RawMessage{})
}

func (t *TwelveDataQuote) FetchBookTop(ctx context.Context, ins types.Instrument) (types.BookTop, error) {
	if t.Key == "" {
		return types.BookTop{}, ErrAuthRequired
	}
	u := fmt.Sprintf("https://api.twelvedata.com/quote?symbol=%s&apikey=%s",
		url.QueryEscape(ins.Symbol), url.QueryEscape(t.Key))
	var q struct {
		Close string `json:"close"`
	}
	if err := jsonGet(ctx, t.HTTP, u, nil, &q); err != nil {
		return types.BookTop{}, err
	}
	px := mustParseFloat(q.Close)
	if px == 0 {
		return types.BookTop{}, fmt.Errorf("twelvedata: no close")
	}
	sp := px * 0.00015
	return types.BookTop{
		Instrument: ins,
		BidPrice:   px - sp,
		AskPrice:   px + sp,
		BidSize:    1,
		AskSize:    1,
		Timestamp:  time.Now().UTC(),
	}, nil
}

func (t *TwelveDataQuote) PlaceOrder(ctx context.Context, o types.OrderIntent) (types.Order, error) {
	return types.Order{}, ErrReadOnly
}

func (t *TwelveDataQuote) CancelOrder(ctx context.Context, exchangeOrderID string, ins types.Instrument) error {
	return ErrReadOnly
}

func (t *TwelveDataQuote) CancelAll(ctx context.Context) error { return ErrReadOnly }

func (t *TwelveDataQuote) FetchBalances(ctx context.Context) ([]types.Balance, error) {
	return nil, ErrReadOnly
}

func (t *TwelveDataQuote) FetchPositions(ctx context.Context) ([]types.Position, error) {
	return nil, ErrReadOnly
}
