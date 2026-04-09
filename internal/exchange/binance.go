package exchange

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"tradingbot/pkg/types"
)

// BinanceSpot implements spot REST (futures: add separate adapter using fapi base URL).
type BinanceSpot struct {
	BaseURL string
	Key     string
	Secret  string
	HTTP    HTTPDoer
}

func NewBinanceSpot(testnet bool, key, secret string) *BinanceSpot {
	base := "https://api.binance.com"
	if testnet {
		base = "https://testnet.binance.vision"
	}
	return &BinanceSpot{BaseURL: base, Key: key, Secret: secret, HTTP: defaultClient()}
}

func (b *BinanceSpot) Name() types.Venue { return "BINANCE" }

func (b *BinanceSpot) Health(ctx context.Context) error {
	u := b.BaseURL + "/api/v3/ping"
	return jsonGet(ctx, b.HTTP, u, nil, &json.RawMessage{})
}

func (b *BinanceSpot) signedRaw(ctx context.Context, method, path string, params url.Values) ([]byte, error) {
	if b.Key == "" || b.Secret == "" {
		return nil, ErrAuthRequired
	}
	if params == nil {
		params = url.Values{}
	}
	params.Set("timestamp", strconv.FormatInt(time.Now().UnixMilli(), 10))
	params.Set("recvWindow", "5000")
	qs := params.Encode()
	sig := hmacSHA256Hex(b.Secret, qs)
	payload := qs + "&signature=" + sig
	reqURL := b.BaseURL + path
	var rdr io.Reader
	if method == http.MethodGet || method == http.MethodDelete {
		reqURL += "?" + payload
	} else {
		rdr = strings.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, reqURL, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-MBX-APIKEY", b.Key)
	if method != http.MethodGet && method != http.MethodDelete {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := b.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("binance %d: %s", resp.StatusCode, truncate(string(raw), 400))
	}
	return raw, nil
}

func (b *BinanceSpot) FetchBookTop(ctx context.Context, ins types.Instrument) (types.BookTop, error) {
	u := fmt.Sprintf("%s/api/v3/ticker/bookTicker?symbol=%s", b.BaseURL, url.QueryEscape(ins.Symbol))
	var v struct {
		Symbol   string `json:"symbol"`
		BidPrice string `json:"bidPrice"`
		BidQty   string `json:"bidQty"`
		AskPrice string `json:"askPrice"`
		AskQty   string `json:"askQty"`
	}
	if err := jsonGet(ctx, b.HTTP, u, nil, &v); err != nil {
		return types.BookTop{}, err
	}
	return types.BookTop{
		Instrument: ins,
		BidPrice:   mustParseFloat(v.BidPrice),
		BidSize:    mustParseFloat(v.BidQty),
		AskPrice:   mustParseFloat(v.AskPrice),
		AskSize:    mustParseFloat(v.AskQty),
		Timestamp:  time.Now().UTC(),
	}, nil
}

func (b *BinanceSpot) FetchBalances(ctx context.Context) ([]types.Balance, error) {
	raw, err := b.signedRaw(ctx, http.MethodGet, "/api/v3/account", nil)
	if err != nil {
		return nil, err
	}
	var acc struct {
		Balances []struct {
			Asset  string `json:"asset"`
			Free   string `json:"free"`
			Locked string `json:"locked"`
		} `json:"balances"`
	}
	if err := json.Unmarshal(raw, &acc); err != nil {
		return nil, err
	}
	out := make([]types.Balance, 0, len(acc.Balances))
	for _, x := range acc.Balances {
		f := mustParseFloat(x.Free)
		l := mustParseFloat(x.Locked)
		if f+l == 0 {
			continue
		}
		out = append(out, types.Balance{Venue: b.Name(), Asset: x.Asset, Free: f, Locked: l})
	}
	return out, nil
}

func (b *BinanceSpot) FetchPositions(ctx context.Context) ([]types.Position, error) {
	// Spot: no separate positions endpoint in this adapter.
	return nil, nil
}

func (b *BinanceSpot) PlaceOrder(ctx context.Context, o types.OrderIntent) (types.Order, error) {
	if b.Key == "" {
		return types.Order{}, ErrAuthRequired
	}
	p := url.Values{}
	p.Set("symbol", o.Instrument.Symbol)
	p.Set("side", strings.ToUpper(string(o.Side)))
	if o.Type == types.OrderMarket {
		p.Set("type", "MARKET")
		p.Set("quantity", fmtQty(o.Quantity))
	} else {
		p.Set("type", "LIMIT")
		p.Set("timeInForce", "GTC")
		p.Set("quantity", fmtQty(o.Quantity))
		p.Set("price", fmtQty(o.LimitPrice))
	}
	if o.PostOnly {
		p.Set("type", "LIMIT_MAKER")
	}
	raw, err := b.signedRaw(ctx, http.MethodPost, "/api/v3/order", p)
	if err != nil {
		return types.Order{}, err
	}
	var resp struct {
		OrderID       int64  `json:"orderId"`
		ExecutedQty   string `json:"executedQty"`
		Status        string `json:"status"`
		AvgPrice      string `json:"avgPrice"`
		CummulativeQ  string `json:"cummulativeQuoteQty"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return types.Order{}, err
	}
	now := time.Now().UTC()
	st := types.OrderOpen
	if resp.Status == "FILLED" {
		st = types.OrderFilled
	}
	return types.Order{
		ID:          o.ID,
		ExchangeID:  strconv.FormatInt(resp.OrderID, 10),
		Intent:      o,
		State:       st,
		FilledQty:   mustParseFloat(resp.ExecutedQty),
		AvgPrice:    mustParseFloat(resp.AvgPrice),
		SubmittedAt: now,
		UpdatedAt:   now,
	}, nil
}

func (b *BinanceSpot) CancelOrder(ctx context.Context, exchangeOrderID string, ins types.Instrument) error {
	p := url.Values{}
	p.Set("symbol", ins.Symbol)
	p.Set("orderId", exchangeOrderID)
	_, err := b.signedRaw(ctx, http.MethodDelete, "/api/v3/order", p)
	return err
}

func (b *BinanceSpot) CancelAll(ctx context.Context) error {
	// Without symbol, Binance cancels all — omitting symbol cancels entire account spot (dangerous).
	// Caller should pass symbol via config in production; here we no-op to avoid accidental mass cancel.
	return nil
}
