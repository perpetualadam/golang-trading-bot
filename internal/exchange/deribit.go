package exchange

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"tradingbot/pkg/types"
)

// DeribitJSONRPC uses v2 JSON-RPC with OAuth-style client_credentials token cache.
type DeribitJSONRPC struct {
	BaseURL string
	Key     string // client_id
	Secret  string // client_secret
	HTTP    HTTPDoer

	mu       sync.Mutex
	token    string
	expiry   time.Time
}

func NewDeribit(testnet bool, key, secret string) *DeribitJSONRPC {
	base := "https://www.deribit.com"
	if testnet {
		base = "https://test.deribit.com"
	}
	return &DeribitJSONRPC{BaseURL: base + "/api/v2", Key: key, Secret: secret, HTTP: defaultClient()}
}

func (d *DeribitJSONRPC) Name() types.Venue { return "DERIBIT" }

func (d *DeribitJSONRPC) Health(ctx context.Context) error {
	body := `{"jsonrpc":"2.0","id":1,"method":"public/test","params":{}}`
	return jsonPost(ctx, d.HTTP, d.BaseURL, map[string]string{"Content-Type": "application/json"}, body, &json.RawMessage{})
}

func (d *DeribitJSONRPC) ensureToken(ctx context.Context) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.token != "" && time.Now().Before(d.expiry.Add(-30*time.Second)) {
		return d.token, nil
	}
	if d.Key == "" || d.Secret == "" {
		return "", ErrAuthRequired
	}
	params := map[string]any{
		"grant_type":    "client_credentials",
		"client_id":     d.Key,
		"client_secret": d.Secret,
	}
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "public/auth", "params": params})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.BaseURL, bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var wrap struct {
		Result struct {
			AccessToken string `json:"access_token"`
			ExpiresIn   int    `json:"expires_in"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return "", err
	}
	if wrap.Error != nil {
		return "", fmt.Errorf("deribit auth: %s", wrap.Error.Message)
	}
	d.token = wrap.Result.AccessToken
	d.expiry = time.Now().Add(time.Duration(wrap.Result.ExpiresIn) * time.Second)
	return d.token, nil
}

func (d *DeribitJSONRPC) privateCall(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	tok, err := d.ensureToken(ctx)
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 2, "method": method, "params": params})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.BaseURL, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "bearer "+tok)
	resp, err := d.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var wrap struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return nil, err
	}
	if wrap.Error != nil {
		return nil, fmt.Errorf("deribit %s: %s", method, wrap.Error.Message)
	}
	return wrap.Result, nil
}

func (d *DeribitJSONRPC) FetchBookTop(ctx context.Context, ins types.Instrument) (types.BookTop, error) {
	params := map[string]any{"instrument_name": ins.Symbol, "depth": 1}
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 3, "method": "public/get_order_book", "params": params})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.BaseURL, bytes.NewReader(b))
	if err != nil {
		return types.BookTop{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.HTTP.Do(req)
	if err != nil {
		return types.BookTop{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var wrap struct {
		Result struct {
			Bids [][]float64 `json:"bids"`
			Asks [][]float64 `json:"asks"`
			Ts   int64       `json:"timestamp"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return types.BookTop{}, err
	}
	bt := types.BookTop{Instrument: ins, Timestamp: time.Now().UTC()}
	if len(wrap.Result.Bids) > 0 {
		bt.BidPrice = wrap.Result.Bids[0][0]
		bt.BidSize = wrap.Result.Bids[0][1]
	}
	if len(wrap.Result.Asks) > 0 {
		bt.AskPrice = wrap.Result.Asks[0][0]
		bt.AskSize = wrap.Result.Asks[0][1]
	}
	return bt, nil
}

func (d *DeribitJSONRPC) FetchBalances(ctx context.Context) ([]types.Balance, error) {
	raw, err := d.privateCall(ctx, "private/get_account_summary", map[string]any{"currency": "BTC"})
	if err != nil {
		return nil, err
	}
	var sum struct {
		Currency string  `json:"currency"`
		Equity   float64 `json:"equity"`
	}
	_ = json.Unmarshal(raw, &sum)
	if sum.Equity == 0 {
		return nil, nil
	}
	return []types.Balance{{Venue: d.Name(), Asset: sum.Currency, Free: sum.Equity}}, nil
}

func (d *DeribitJSONRPC) FetchPositions(ctx context.Context) ([]types.Position, error) {
	raw, err := d.privateCall(ctx, "private/get_positions", map[string]any{"currency": "any"})
	if err != nil {
		return nil, err
	}
	var list []struct {
		Instrument string  `json:"instrument_name"`
		Size       float64 `json:"size"`
		AvgPrice   float64 `json:"average_price"`
		Pnl        float64 `json:"floating_profit_loss"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	var out []types.Position
	for _, p := range list {
		if p.Size == 0 {
			continue
		}
		out = append(out, types.Position{
			Instrument: types.Instrument{Venue: d.Name(), Symbol: p.Instrument},
			Qty:        p.Size,
			AvgEntry:   p.AvgPrice,
			Unrealized: p.Pnl,
			UpdatedAt:  time.Now().UTC(),
		})
	}
	return out, nil
}

func (d *DeribitJSONRPC) PlaceOrder(ctx context.Context, o types.OrderIntent) (types.Order, error) {
	side := "buy"
	if o.Side == types.SideSell {
		side = "sell"
	}
	typ := "market"
	if o.Type == types.OrderLimit {
		typ = "limit"
	}
	params := map[string]any{
		"instrument_name": o.Instrument.Symbol,
		"amount":          o.Quantity,
		"type":            typ,
		"label":           o.StrategyID,
	}
	if typ == "limit" {
		params["price"] = o.LimitPrice
	}
	raw, err := d.privateCall(ctx, "private/"+side, params)
	if err != nil {
		return types.Order{}, err
	}
	var ord struct {
		Order struct {
			OrderID string  `json:"order_id"`
			Filled  float64 `json:"filled_amount"`
			Avg     float64 `json:"average_price"`
		} `json:"order"`
	}
	_ = json.Unmarshal(raw, &ord)
	now := time.Now().UTC()
	return types.Order{
		ID:          o.ID,
		ExchangeID:  ord.Order.OrderID,
		Intent:      o,
		State:       types.OrderOpen,
		FilledQty:   ord.Order.Filled,
		AvgPrice:    ord.Order.Avg,
		SubmittedAt: now,
		UpdatedAt:   now,
	}, nil
}

func (d *DeribitJSONRPC) CancelOrder(ctx context.Context, exchangeOrderID string, ins types.Instrument) error {
	_, err := d.privateCall(ctx, "private/cancel", map[string]any{"order_id": exchangeOrderID})
	return err
}

func (d *DeribitJSONRPC) CancelAll(ctx context.Context) error {
	_, err := d.privateCall(ctx, "private/cancel_all", map[string]any{})
	return err
}
