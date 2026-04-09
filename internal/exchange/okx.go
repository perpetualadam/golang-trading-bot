package exchange

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"tradingbot/pkg/types"
)

// OKXV5 REST (spot trade + account).
type OKXV5 struct {
	BaseURL    string
	Key        string
	Secret     string
	Passphrase string
	HTTP       HTTPDoer
}

func NewOKXV5(key, secret, passphrase string) *OKXV5 {
	return NewOKXV5WithBase("https://www.okx.com", key, secret, passphrase)
}

func NewOKXV5WithBase(baseURL, key, secret, passphrase string) *OKXV5 {
	if baseURL == "" {
		baseURL = "https://www.okx.com"
	}
	return &OKXV5{BaseURL: baseURL, Key: key, Secret: secret, Passphrase: passphrase, HTTP: defaultClient()}
}

func (o *OKXV5) Name() types.Venue { return "OKX" }

func (o *OKXV5) Health(ctx context.Context) error {
	u := o.BaseURL + "/api/v5/public/time"
	return jsonGet(ctx, o.HTTP, u, nil, &json.RawMessage{})
}

func (o *OKXV5) signRequest(ctx context.Context, method, path, body string) ([]byte, error) {
	if o.Key == "" || o.Secret == "" {
		return nil, ErrAuthRequired
	}
	ts := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	prehash := ts + method + path + body
	sig := hmacSHA256B64(o.Secret, prehash)
	var req *http.Request
	var err error
	if body != "" {
		req, err = http.NewRequestWithContext(ctx, method, o.BaseURL+path, bytes.NewReader([]byte(body)))
	} else {
		req, err = http.NewRequestWithContext(ctx, method, o.BaseURL+path, nil)
	}
	if err != nil {
		return nil, err
	}
	req.Header.Set("OK-ACCESS-KEY", o.Key)
	req.Header.Set("OK-ACCESS-SIGN", sig)
	req.Header.Set("OK-ACCESS-TIMESTAMP", ts)
	req.Header.Set("OK-ACCESS-PASSPHRASE", o.Passphrase)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("okx %d: %s", resp.StatusCode, truncate(string(raw), 400))
	}
	return raw, nil
}

func (o *OKXV5) FetchBookTop(ctx context.Context, ins types.Instrument) (types.BookTop, error) {
	u := fmt.Sprintf("%s/api/v5/market/ticker?instId=%s", o.BaseURL, url.QueryEscape(ins.Symbol))
	var wrap struct {
		Data []struct {
			BidPx string `json:"bidPx"`
			BidSz string `json:"bidSz"`
			AskPx string `json:"askPx"`
			AskSz string `json:"askSz"`
		} `json:"data"`
	}
	if err := jsonGet(ctx, o.HTTP, u, nil, &wrap); err != nil {
		return types.BookTop{}, err
	}
	if len(wrap.Data) == 0 {
		return types.BookTop{}, fmt.Errorf("okx: no ticker")
	}
	d := wrap.Data[0]
	return types.BookTop{
		Instrument: ins,
		BidPrice:   mustParseFloat(d.BidPx),
		BidSize:    mustParseFloat(d.BidSz),
		AskPrice:   mustParseFloat(d.AskPx),
		AskSize:    mustParseFloat(d.AskSz),
		Timestamp:  time.Now().UTC(),
	}, nil
}

func (o *OKXV5) FetchBalances(ctx context.Context) ([]types.Balance, error) {
	path := "/api/v5/account/balance"
	raw, err := o.signRequest(ctx, http.MethodGet, path, "")
	if err != nil {
		return nil, err
	}
	var wrap struct {
		Data []struct {
			Details []struct {
				Ccy  string `json:"ccy"`
				Cash string `json:"cashBal"`
			} `json:"details"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return nil, err
	}
	var out []types.Balance
	for _, d := range wrap.Data {
		for _, det := range d.Details {
			f := mustParseFloat(det.Cash)
			if f == 0 {
				continue
			}
			out = append(out, types.Balance{Venue: o.Name(), Asset: det.Ccy, Free: f})
		}
	}
	return out, nil
}

func (o *OKXV5) FetchPositions(ctx context.Context) ([]types.Position, error) {
	path := "/api/v5/account/positions?instType=SWAP"
	raw, err := o.signRequest(ctx, http.MethodGet, path, "")
	if err != nil {
		return nil, nil
	}
	var wrap struct {
		Data []struct {
			InstID string `json:"instId"`
			Pos    string `json:"pos"`
			AvgPx  string `json:"avgPx"`
			Upl    string `json:"upl"`
		} `json:"data"`
	}
	_ = json.Unmarshal(raw, &wrap)
	var out []types.Position
	for _, p := range wrap.Data {
		q := mustParseFloat(p.Pos)
		if q == 0 {
			continue
		}
		out = append(out, types.Position{
			Instrument: types.Instrument{Venue: o.Name(), Symbol: p.InstID},
			Qty:        q,
			AvgEntry:   mustParseFloat(p.AvgPx),
			Unrealized: mustParseFloat(p.Upl),
			UpdatedAt:  time.Now().UTC(),
		})
	}
	return out, nil
}

func (o *OKXV5) PlaceOrder(ctx context.Context, intent types.OrderIntent) (types.Order, error) {
	side := "buy"
	if intent.Side == types.SideSell {
		side = "sell"
	}
	ordType := "market"
	if intent.Type == types.OrderLimit {
		ordType = "limit"
	}
	p := map[string]string{
		"instId":  intent.Instrument.Symbol,
		"tdMode":  "cash",
		"side":    side,
		"ordType": ordType,
		"sz":      fmtQty(intent.Quantity),
	}
	if ordType == "limit" {
		p["px"] = fmtQty(intent.LimitPrice)
	}
	if intent.PostOnly {
		p["ordType"] = "post_only"
	}
	b, _ := json.Marshal(p)
	raw, err := o.signRequest(ctx, http.MethodPost, "/api/v5/trade/order", string(b))
	if err != nil {
		return types.Order{}, err
	}
	var wrap struct {
		Data []struct {
			OrdID string `json:"ordId"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return types.Order{}, err
	}
	oid := ""
	if len(wrap.Data) > 0 {
		oid = wrap.Data[0].OrdID
	}
	now := time.Now().UTC()
	return types.Order{ID: intent.ID, ExchangeID: oid, Intent: intent, State: types.OrderOpen, SubmittedAt: now, UpdatedAt: now}, nil
}

func (o *OKXV5) CancelOrder(ctx context.Context, exchangeOrderID string, ins types.Instrument) error {
	p := fmt.Sprintf(`{"instId":"%s","ordId":"%s"}`, ins.Symbol, exchangeOrderID)
	_, err := o.signRequest(ctx, http.MethodPost, "/api/v5/trade/cancel-order", p)
	return err
}

func (o *OKXV5) CancelAll(ctx context.Context) error { return nil }
