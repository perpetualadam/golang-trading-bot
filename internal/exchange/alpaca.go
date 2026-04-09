package exchange

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"tradingbot/pkg/types"
)

// Alpaca supports US equities and crypto via REST v2.
type Alpaca struct {
	BaseURL string
	Key     string
	Secret  string
	HTTP    HTTPDoer
}

func NewAlpaca(paper bool, key, secret string) *Alpaca {
	base := "https://api.alpaca.markets"
	if paper {
		base = "https://paper-api.alpaca.markets"
	}
	return &Alpaca{BaseURL: base, Key: key, Secret: secret, HTTP: defaultClient()}
}

func (a *Alpaca) Name() types.Venue { return "ALPACA" }

func (a *Alpaca) headers() map[string]string {
	return map[string]string{
		"APCA-API-KEY-ID":     a.Key,
		"APCA-API-SECRET-KEY": a.Secret,
	}
}

func (a *Alpaca) Health(ctx context.Context) error {
	if a.Key == "" {
		return ErrAuthRequired
	}
	u := a.BaseURL + "/v2/account"
	return jsonGet(ctx, a.HTTP, u, a.headers(), &json.RawMessage{})
}

func (a *Alpaca) FetchBookTop(ctx context.Context, ins types.Instrument) (types.BookTop, error) {
	// Latest trade / quote for crypto or stock
	sym := ins.Symbol
	u := fmt.Sprintf("%s/v2/stocks/%s/trades/latest?feed=iex", a.BaseURL, sym)
	var wrap struct {
		Trade struct {
			P float64 `json:"p"`
		} `json:"trade"`
	}
	if err := jsonGet(ctx, a.HTTP, u, a.headers(), &wrap); err != nil {
		// try crypto
		u2 := fmt.Sprintf("%s/v1beta3/crypto/us/latest/trades?symbols=%s", a.BaseURL, sym)
		var cw struct {
			Trades map[string]struct {
				P float64 `json:"p"`
			} `json:"trades"`
		}
		if err2 := jsonGet(ctx, a.HTTP, u2, a.headers(), &cw); err2 != nil {
			return types.BookTop{}, err
		}
		for _, t := range cw.Trades {
			px := t.P
			return types.BookTop{Instrument: ins, BidPrice: px * 0.9999, AskPrice: px * 1.0001, BidSize: 1, AskSize: 1, Timestamp: time.Now().UTC()}, nil
		}
		return types.BookTop{}, fmt.Errorf("alpaca: no trade")
	}
	px := wrap.Trade.P
	return types.BookTop{Instrument: ins, BidPrice: px * 0.9999, AskPrice: px * 1.0001, BidSize: 1, AskSize: 1, Timestamp: time.Now().UTC()}, nil
}

func (a *Alpaca) FetchBalances(ctx context.Context) ([]types.Balance, error) {
	if a.Key == "" {
		return nil, ErrAuthRequired
	}
	u := a.BaseURL + "/v2/account"
	var acc struct {
		Cash   string `json:"cash"`
		Equity string `json:"equity"`
	}
	if err := jsonGet(ctx, a.HTTP, u, a.headers(), &acc); err != nil {
		return nil, err
	}
	return []types.Balance{{
		Venue: a.Name(), Asset: "USD", Free: mustParseFloat(acc.Cash), USDValue: mustParseFloat(acc.Equity),
	}}, nil
}

func (a *Alpaca) FetchPositions(ctx context.Context) ([]types.Position, error) {
	u := a.BaseURL + "/v2/positions"
	var list []struct {
		Symbol       string  `json:"symbol"`
		Qty          string  `json:"qty"`
		AvgEntry     string  `json:"avg_entry_price"`
		UnrealizedPL string  `json:"unrealized_pl"`
	}
	if err := jsonGet(ctx, a.HTTP, u, a.headers(), &list); err != nil {
		return nil, err
	}
	var out []types.Position
	for _, p := range list {
		out = append(out, types.Position{
			Instrument: types.Instrument{Venue: a.Name(), Symbol: p.Symbol, Class: types.AssetEquity},
			Qty:        mustParseFloat(p.Qty),
			AvgEntry:   mustParseFloat(p.AvgEntry),
			Unrealized: mustParseFloat(p.UnrealizedPL),
			UpdatedAt:  time.Now().UTC(),
		})
	}
	return out, nil
}

func (a *Alpaca) PlaceOrder(ctx context.Context, o types.OrderIntent) (types.Order, error) {
	if a.Key == "" {
		return types.Order{}, ErrAuthRequired
	}
	body := map[string]any{
		"symbol":      o.Instrument.Symbol,
		"qty":         o.Quantity,
		"side":        o.Side,
		"type":        "market",
		"time_in_force": "day",
	}
	if o.Type == types.OrderLimit {
		body["type"] = "limit"
		body["limit_price"] = o.LimitPrice
		body["time_in_force"] = "gtc"
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.BaseURL+"/v2/orders", bytes.NewReader(b))
	if err != nil {
		return types.Order{}, err
	}
	for k, v := range a.headers() {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return types.Order{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return types.Order{}, fmt.Errorf("alpaca %d: %s", resp.StatusCode, truncate(string(raw), 400))
	}
	var ord struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &ord)
	now := time.Now().UTC()
	return types.Order{ID: o.ID, ExchangeID: ord.ID, Intent: o, State: types.OrderOpen, SubmittedAt: now, UpdatedAt: now}, nil
}

func (a *Alpaca) CancelOrder(ctx context.Context, exchangeOrderID string, ins types.Instrument) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, a.BaseURL+"/v2/orders/"+exchangeOrderID, nil)
	if err != nil {
		return err
	}
	for k, v := range a.headers() {
		req.Header.Set(k, v)
	}
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("alpaca cancel %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

func (a *Alpaca) CancelAll(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, a.BaseURL+"/v2/orders", nil)
	if err != nil {
		return err
	}
	for k, v := range a.headers() {
		req.Header.Set(k, v)
	}
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}
