package exchange

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
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

// CoinbaseExchange uses Coinbase Exchange REST signing (CB-ACCESS-* headers).
type CoinbaseExchange struct {
	BaseURL    string
	Key        string
	SecretB64  string
	Passphrase string
	HTTP       HTTPDoer
}

func NewCoinbaseExchange(key, secretB64, passphrase string) *CoinbaseExchange {
	return &CoinbaseExchange{
		BaseURL:    "https://api.exchange.coinbase.com",
		Key:        key,
		SecretB64:  secretB64,
		Passphrase: passphrase,
		HTTP:       defaultClient(),
	}
}

func (c *CoinbaseExchange) Name() types.Venue { return "COINBASE" }

func (c *CoinbaseExchange) Health(ctx context.Context) error {
	u := c.BaseURL + "/time"
	return jsonGet(ctx, c.HTTP, u, nil, &json.RawMessage{})
}

func (c *CoinbaseExchange) sign(ctx context.Context, method, requestPath, body string) ([]byte, error) {
	if c.Key == "" || c.SecretB64 == "" {
		return nil, ErrAuthRequired
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	pre := ts + strings.ToUpper(method) + requestPath + body
	sig := coinbaseHMACB64(c.SecretB64, pre)
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+requestPath, bytes.NewReader([]byte(body)))
	if err != nil {
		return nil, err
	}
	if body == "" {
		req, err = http.NewRequestWithContext(ctx, method, c.BaseURL+requestPath, nil)
		if err != nil {
			return nil, err
		}
	}
	req.Header.Set("CB-ACCESS-KEY", c.Key)
	req.Header.Set("CB-ACCESS-SIGN", sig)
	req.Header.Set("CB-ACCESS-TIMESTAMP", ts)
	req.Header.Set("CB-ACCESS-PASSPHRASE", c.Passphrase)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("coinbase %d: %s", resp.StatusCode, truncate(string(raw), 400))
	}
	return raw, nil
}

func coinbaseHMACB64(secretB64, message string) string {
	key, err := base64.StdEncoding.DecodeString(secretB64)
	if err != nil {
		key = []byte(secretB64)
	}
	m := hmac.New(sha256.New, key)
	_, _ = m.Write([]byte(message))
	return base64.StdEncoding.EncodeToString(m.Sum(nil))
}

func (c *CoinbaseExchange) FetchBookTop(ctx context.Context, ins types.Instrument) (types.BookTop, error) {
	pid := coinbaseProductID(ins)
	u := fmt.Sprintf("%s/products/%s/book?level=1", c.BaseURL, url.PathEscape(pid))
	var v struct {
		Bids [][]string `json:"bids"`
		Asks [][]string `json:"asks"`
	}
	if err := jsonGet(ctx, c.HTTP, u, nil, &v); err != nil {
		return types.BookTop{}, err
	}
	bt := types.BookTop{Instrument: ins, Timestamp: time.Now().UTC()}
	if len(v.Bids) > 0 {
		bt.BidPrice = mustParseFloat(v.Bids[0][0])
		bt.BidSize = mustParseFloat(v.Bids[0][1])
	}
	if len(v.Asks) > 0 {
		bt.AskPrice = mustParseFloat(v.Asks[0][0])
		bt.AskSize = mustParseFloat(v.Asks[0][1])
	}
	return bt, nil
}

func coinbaseProductID(ins types.Instrument) string {
	if strings.Contains(ins.Symbol, "-") {
		return ins.Symbol
	}
	if ins.Base != "" && ins.Quote != "" {
		return ins.Base + "-" + ins.Quote
	}
	return ins.Symbol
}

func (c *CoinbaseExchange) FetchBalances(ctx context.Context) ([]types.Balance, error) {
	raw, err := c.sign(ctx, http.MethodGet, "/accounts", "")
	if err != nil {
		return nil, err
	}
	var accs []struct {
		Currency string `json:"currency"`
		Balance  string `json:"balance"`
		Available string `json:"available"`
	}
	if err := json.Unmarshal(raw, &accs); err != nil {
		return nil, err
	}
	var out []types.Balance
	for _, a := range accs {
		f := mustParseFloat(a.Available)
		if f == 0 {
			f = mustParseFloat(a.Balance)
		}
		if f == 0 {
			continue
		}
		out = append(out, types.Balance{Venue: c.Name(), Asset: a.Currency, Free: f})
	}
	return out, nil
}

func (c *CoinbaseExchange) FetchPositions(ctx context.Context) ([]types.Position, error) {
	return nil, nil
}

func (c *CoinbaseExchange) PlaceOrder(ctx context.Context, o types.OrderIntent) (types.Order, error) {
	pid := coinbaseProductID(o.Instrument)
	p := map[string]string{
		"product_id": pid,
		"side":       string(o.Side),
	}
	if o.Type == types.OrderMarket {
		p["type"] = "market"
		p["size"] = fmtQty(o.Quantity)
	} else {
		p["type"] = "limit"
		p["price"] = fmtQty(o.LimitPrice)
		p["size"] = fmtQty(o.Quantity)
	}
	if o.PostOnly {
		p["post_only"] = "true"
	}
	b, _ := json.Marshal(p)
	raw, err := c.sign(ctx, http.MethodPost, "/orders", string(b))
	if err != nil {
		return types.Order{}, err
	}
	var ord struct {
		ID   string `json:"id"`
		Size string `json:"size"`
	}
	_ = json.Unmarshal(raw, &ord)
	now := time.Now().UTC()
	return types.Order{ID: o.ID, ExchangeID: ord.ID, Intent: o, State: types.OrderOpen, SubmittedAt: now, UpdatedAt: now}, nil
}

func (c *CoinbaseExchange) CancelOrder(ctx context.Context, exchangeOrderID string, ins types.Instrument) error {
	path := "/orders/" + url.PathEscape(exchangeOrderID)
	_, err := c.sign(ctx, http.MethodDelete, path, "")
	return err
}

func (c *CoinbaseExchange) CancelAll(ctx context.Context) error { return nil }
