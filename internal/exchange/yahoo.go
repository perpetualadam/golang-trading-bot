package exchange

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"tradingbot/pkg/types"
)

// YahooChart uses the public chart API (delayed quotes). Read-only: no orders.
type YahooChart struct {
	HTTP HTTPDoer
}

func NewYahooChart() *YahooChart {
	return &YahooChart{HTTP: defaultClient()}
}

func (y *YahooChart) Name() types.Venue { return "YAHOO" }

func (y *YahooChart) Health(ctx context.Context) error {
	u := "https://query1.finance.yahoo.com/v8/finance/chart/AAPL?interval=1d&range=1d"
	return jsonGet(ctx, y.HTTP, u, nil, &json.RawMessage{})
}

func yahooSymbol(ins types.Instrument) string {
	if ins.Symbol != "" && (ins.Class == types.AssetEquity || ins.Class == types.AssetForex) {
		return ins.Symbol
	}
	// crypto heuristic
	if ins.Base != "" && ins.Quote != "" {
		return ins.Base + "-" + ins.Quote
	}
	s := ins.Symbol
	if s == "BTCUSDT" || s == "BTCUSD" {
		return "BTC-USD"
	}
	if s == "ETHUSDT" || s == "ETHUSD" {
		return "ETH-USD"
	}
	return s
}

func (y *YahooChart) FetchBookTop(ctx context.Context, ins types.Instrument) (types.BookTop, error) {
	sym := yahooSymbol(ins)
	u := "https://query1.finance.yahoo.com/v8/finance/chart/" + url.PathEscape(sym) + "?interval=1m&range=1d"
	var wrap struct {
		Chart struct {
			Result []struct {
				Meta struct {
					RegMarketPrice float64 `json:"regularMarketPrice"`
					PrevClose      float64 `json:"previousClose"`
				} `json:"meta"`
			} `json:"result"`
		} `json:"chart"`
	}
	if err := jsonGet(ctx, y.HTTP, u, map[string]string{"User-Agent": "Mozilla/5.0"}, &wrap); err != nil {
		return types.BookTop{}, err
	}
	if len(wrap.Chart.Result) == 0 {
		return types.BookTop{}, fmt.Errorf("yahoo: empty chart")
	}
	px := wrap.Chart.Result[0].Meta.RegMarketPrice
	if px == 0 {
		px = wrap.Chart.Result[0].Meta.PrevClose
	}
	if px == 0 {
		return types.BookTop{}, fmt.Errorf("yahoo: no price")
	}
	spread := px * 0.0002
	return types.BookTop{
		Instrument: ins,
		BidPrice:   px - spread/2,
		AskPrice:   px + spread/2,
		BidSize:    1,
		AskSize:    1,
		Timestamp:  time.Now().UTC(),
	}, nil
}

func (y *YahooChart) PlaceOrder(ctx context.Context, o types.OrderIntent) (types.Order, error) {
	return types.Order{}, ErrReadOnly
}

func (y *YahooChart) CancelOrder(ctx context.Context, exchangeOrderID string, ins types.Instrument) error {
	return ErrReadOnly
}

func (y *YahooChart) CancelAll(ctx context.Context) error { return ErrReadOnly }

func (y *YahooChart) FetchBalances(ctx context.Context) ([]types.Balance, error) {
	return nil, ErrReadOnly
}

func (y *YahooChart) FetchPositions(ctx context.Context) ([]types.Position, error) {
	return nil, ErrReadOnly
}
