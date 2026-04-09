package exchange

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"tradingbot/pkg/types"
)

// BybitV5 unified account REST.
type BybitV5 struct {
	BaseURL string
	Key     string
	Secret  string
	HTTP    HTTPDoer
}

func NewBybitV5(testnet bool, key, secret string) *BybitV5 {
	base := "https://api.bybit.com"
	if testnet {
		base = "https://api-testnet.bybit.com"
	}
	return &BybitV5{BaseURL: base, Key: key, Secret: secret, HTTP: defaultClient()}
}

func (x *BybitV5) Name() types.Venue { return "BYBIT" }

func (x *BybitV5) Health(ctx context.Context) error {
	u := x.BaseURL + "/v5/market/time"
	return jsonGet(ctx, x.HTTP, u, nil, &json.RawMessage{})
}

func (x *BybitV5) signGet(ctx context.Context, path, query string) ([]byte, error) {
	if x.Key == "" || x.Secret == "" {
		return nil, ErrAuthRequired
	}
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	recv := "5000"
	payload := ts + x.Key + recv + query
	sig := hmacSHA256Hex(x.Secret, payload)
	u := x.BaseURL + path
	if query != "" {
		u += "?" + query
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-BAPI-API-KEY", x.Key)
	req.Header.Set("X-BAPI-SIGN", sig)
	req.Header.Set("X-BAPI-TIMESTAMP", ts)
	req.Header.Set("X-BAPI-RECV-WINDOW", recv)
	resp, err := x.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("bybit %d: %s", resp.StatusCode, truncate(string(raw), 400))
	}
	return raw, nil
}

func (x *BybitV5) signPostJSON(ctx context.Context, path, body string) ([]byte, error) {
	if x.Key == "" || x.Secret == "" {
		return nil, ErrAuthRequired
	}
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	recv := "5000"
	payload := ts + x.Key + recv + body
	sig := hmacSHA256Hex(x.Secret, payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, x.BaseURL+path, bytes.NewReader([]byte(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-BAPI-API-KEY", x.Key)
	req.Header.Set("X-BAPI-SIGN", sig)
	req.Header.Set("X-BAPI-TIMESTAMP", ts)
	req.Header.Set("X-BAPI-RECV-WINDOW", recv)
	resp, err := x.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("bybit %d: %s", resp.StatusCode, truncate(string(raw), 400))
	}
	return raw, nil
}

func (x *BybitV5) FetchBookTop(ctx context.Context, ins types.Instrument) (types.BookTop, error) {
	u := fmt.Sprintf("%s/v5/market/tickers?category=spot&symbol=%s", x.BaseURL, ins.Symbol)
	var wrap struct {
		Result struct {
			List []struct {
				Bid1Price string `json:"bid1Price"`
				Bid1Size  string `json:"bid1Size"`
				Ask1Price string `json:"ask1Price"`
				Ask1Size  string `json:"ask1Size"`
			} `json:"list"`
		} `json:"result"`
	}
	if err := jsonGet(ctx, x.HTTP, u, nil, &wrap); err != nil {
		return types.BookTop{}, err
	}
	if len(wrap.Result.List) == 0 {
		return types.BookTop{}, fmt.Errorf("bybit: empty ticker")
	}
	t := wrap.Result.List[0]
	return types.BookTop{
		Instrument: ins,
		BidPrice:   mustParseFloat(t.Bid1Price),
		BidSize:    mustParseFloat(t.Bid1Size),
		AskPrice:   mustParseFloat(t.Ask1Price),
		AskSize:    mustParseFloat(t.Ask1Size),
		Timestamp:  time.Now().UTC(),
	}, nil
}

func (x *BybitV5) FetchBalances(ctx context.Context) ([]types.Balance, error) {
	q := "accountType=UNIFIED"
	raw, err := x.signGet(ctx, "/v5/account/wallet-balance", q)
	if err != nil {
		return nil, err
	}
	var wrap struct {
		Result struct {
			List []struct {
				Coin []struct {
					Coin  string `json:"coin"`
					WalletBalance string `json:"walletBalance"`
					Locked string `json:"locked"`
				} `json:"coin"`
			} `json:"list"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return nil, err
	}
	var out []types.Balance
	for _, lst := range wrap.Result.List {
		for _, c := range lst.Coin {
			f := mustParseFloat(c.WalletBalance)
			l := mustParseFloat(c.Locked)
			if f+l == 0 {
				continue
			}
			out = append(out, types.Balance{Venue: x.Name(), Asset: c.Coin, Free: f - l, Locked: l})
		}
	}
	return out, nil
}

func (x *BybitV5) FetchPositions(ctx context.Context) ([]types.Position, error) {
	q := "category=linear&settleCoin=USDT"
	raw, err := x.signGet(ctx, "/v5/position/list", q)
	if err != nil {
		return nil, nil
	}
	var wrap struct {
		Result struct {
			List []struct {
				Symbol       string `json:"symbol"`
				Size         string `json:"size"`
				AvgPrice     string `json:"avgPrice"`
				UnrealisedPnl string `json:"unrealisedPnl"`
			} `json:"list"`
		} `json:"result"`
	}
	_ = json.Unmarshal(raw, &wrap)
	var out []types.Position
	for _, p := range wrap.Result.List {
		sz := mustParseFloat(p.Size)
		if sz == 0 {
			continue
		}
		out = append(out, types.Position{
			Instrument: types.Instrument{Venue: x.Name(), Symbol: p.Symbol},
			Qty:        sz,
			AvgEntry:   mustParseFloat(p.AvgPrice),
			Unrealized: mustParseFloat(p.UnrealisedPnl),
			UpdatedAt:  time.Now().UTC(),
		})
	}
	return out, nil
}

func (x *BybitV5) PlaceOrder(ctx context.Context, o types.OrderIntent) (types.Order, error) {
	side := "Buy"
	if o.Side == types.SideSell {
		side = "Sell"
	}
	ot := "Market"
	if o.Type == types.OrderLimit {
		ot = "Limit"
	}
	body := map[string]string{
		"category":    "spot",
		"symbol":      o.Instrument.Symbol,
		"side":        side,
		"orderType":   ot,
		"qty":         fmtQty(o.Quantity),
		"timeInForce": "GTC",
	}
	if ot == "Limit" {
		body["price"] = fmtQty(o.LimitPrice)
	}
	if o.PostOnly {
		body["orderType"] = "Limit"
		body["timeInForce"] = "PostOnly"
	}
	b, _ := json.Marshal(body)
	raw, err := x.signPostJSON(ctx, "/v5/order/create", string(b))
	if err != nil {
		return types.Order{}, err
	}
	var wrap struct {
		Result struct {
			OrderID string `json:"orderId"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return types.Order{}, err
	}
	now := time.Now().UTC()
	return types.Order{
		ID:          o.ID,
		ExchangeID:  wrap.Result.OrderID,
		Intent:      o,
		State:       types.OrderOpen,
		SubmittedAt: now,
		UpdatedAt:   now,
	}, nil
}

func (x *BybitV5) CancelOrder(ctx context.Context, exchangeOrderID string, ins types.Instrument) error {
	body := fmt.Sprintf(`{"category":"spot","symbol":"%s","orderId":"%s"}`, ins.Symbol, exchangeOrderID)
	_, err := x.signPostJSON(ctx, "/v5/order/cancel", body)
	return err
}

func (x *BybitV5) CancelAll(ctx context.Context) error {
	return nil
}
