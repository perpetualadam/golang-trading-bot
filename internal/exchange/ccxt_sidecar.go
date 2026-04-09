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

// CcxtSidecar proxies to a local HTTP service that wraps CCXT (Python/Node).
// Expected JSON contract (implement in your sidecar):
//
//	POST {base}/book   body: {"symbol":"BTC/USDT","venue":"binance"}  -> {"bid":1,"ask":2,"bidSize":..,"askSize":..}
//	POST {base}/order  body: order intent fields                      -> {"id":"..."}
//	POST {base}/cancel body: {"id":"...","symbol":"..."}
//	POST {base}/cancel_all body: {}
//	GET  {base}/balances -> [{"asset":"USDT","free":1,"locked":0}]
//	GET  {base}/positions -> [{"symbol":"...","qty":1,...}]
//
// BaseURL example: http://127.0.0.1:8099/ccxt
type CcxtSidecar struct {
	BaseURL string
	HTTP    HTTPDoer
}

func NewCcxtSidecar(baseURL string) *CcxtSidecar {
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8099/ccxt"
	}
	return &CcxtSidecar{BaseURL: baseURL, HTTP: defaultClient()}
}

func (c *CcxtSidecar) Name() types.Venue { return "CCXT" }

func (c *CcxtSidecar) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSidecar, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return ErrSidecar
	}
	return nil
}

func (c *CcxtSidecar) postJSON(ctx context.Context, path string, body any, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSidecar, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: %s", ErrSidecar, truncate(string(raw), 300))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func (c *CcxtSidecar) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSidecar, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: %s", ErrSidecar, truncate(string(raw), 300))
	}
	return json.Unmarshal(raw, out)
}

func (c *CcxtSidecar) FetchBookTop(ctx context.Context, ins types.Instrument) (types.BookTop, error) {
	var v struct {
		Bid     float64 `json:"bid"`
		Ask     float64 `json:"ask"`
		BidSize float64 `json:"bidSize"`
		AskSize float64 `json:"askSize"`
	}
	err := c.postJSON(ctx, "/book", map[string]string{"symbol": ins.Symbol, "venue": string(ins.Venue)}, &v)
	if err != nil {
		return types.BookTop{}, err
	}
	return types.BookTop{
		Instrument: ins,
		BidPrice:   v.Bid,
		AskPrice:   v.Ask,
		BidSize:    v.BidSize,
		AskSize:    v.AskSize,
		Timestamp:  time.Now().UTC(),
	}, nil
}

func (c *CcxtSidecar) FetchBalances(ctx context.Context) ([]types.Balance, error) {
	var list []struct {
		Asset  string  `json:"asset"`
		Free   float64 `json:"free"`
		Locked float64 `json:"locked"`
	}
	if err := c.getJSON(ctx, "/balances", &list); err != nil {
		return nil, err
	}
	out := make([]types.Balance, 0, len(list))
	for _, x := range list {
		out = append(out, types.Balance{Venue: c.Name(), Asset: x.Asset, Free: x.Free, Locked: x.Locked})
	}
	return out, nil
}

func (c *CcxtSidecar) FetchPositions(ctx context.Context) ([]types.Position, error) {
	var list []struct {
		Symbol string  `json:"symbol"`
		Qty    float64 `json:"qty"`
		Avg    float64 `json:"avgEntry"`
		Upnl   float64 `json:"unrealized"`
	}
	if err := c.getJSON(ctx, "/positions", &list); err != nil {
		return nil, err
	}
	var out []types.Position
	for _, p := range list {
		out = append(out, types.Position{
			Instrument: types.Instrument{Venue: c.Name(), Symbol: p.Symbol},
			Qty:        p.Qty,
			AvgEntry:   p.Avg,
			Unrealized: p.Upnl,
			UpdatedAt:  time.Now().UTC(),
		})
	}
	return out, nil
}

func (c *CcxtSidecar) PlaceOrder(ctx context.Context, o types.OrderIntent) (types.Order, error) {
	var res struct {
		ID string `json:"id"`
	}
	body := map[string]any{
		"symbol":   o.Instrument.Symbol,
		"side":     o.Side,
		"type":     o.Type,
		"qty":      o.Quantity,
		"price":    o.LimitPrice,
		"postOnly": o.PostOnly,
	}
	if err := c.postJSON(ctx, "/order", body, &res); err != nil {
		return types.Order{}, err
	}
	now := time.Now().UTC()
	return types.Order{ID: o.ID, ExchangeID: res.ID, Intent: o, State: types.OrderOpen, SubmittedAt: now, UpdatedAt: now}, nil
}

func (c *CcxtSidecar) CancelOrder(ctx context.Context, exchangeOrderID string, ins types.Instrument) error {
	return c.postJSON(ctx, "/cancel", map[string]string{"id": exchangeOrderID, "symbol": ins.Symbol}, nil)
}

func (c *CcxtSidecar) CancelAll(ctx context.Context) error {
	return c.postJSON(ctx, "/cancel_all", map[string]any{}, nil)
}
