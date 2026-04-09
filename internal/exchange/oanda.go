package exchange

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"tradingbot/pkg/types"
)

// OANDA v20 REST (forex CFDs). api_key = personal access token; extra account_id required.
type OANDA struct {
	BaseURL   string
	Token     string
	AccountID string
	HTTP      HTTPDoer
}

func NewOANDA(practice bool, token, accountID string) *OANDA {
	base := "https://api-fxtrade.oanda.com"
	if practice {
		base = "https://api-fxpractice.oanda.com"
	}
	return &OANDA{BaseURL: base + "/v3", Token: token, AccountID: accountID, HTTP: defaultClient()}
}

func (o *OANDA) Name() types.Venue { return "OANDA" }

func (o *OANDA) hdr() map[string]string {
	return map[string]string{
		"Authorization": "Bearer " + o.Token,
		"Content-Type":  "application/json",
	}
}

func (o *OANDA) Health(ctx context.Context) error {
	if o.Token == "" || o.AccountID == "" {
		return ErrAuthRequired
	}
	u := fmt.Sprintf("%s/accounts/%s/summary", o.BaseURL, o.AccountID)
	return jsonGet(ctx, o.HTTP, u, o.hdr(), &json.RawMessage{})
}

func (o *OANDA) FetchBookTop(ctx context.Context, ins types.Instrument) (types.BookTop, error) {
	// ins.Symbol like EUR_USD
	u := fmt.Sprintf("%s/accounts/%s/pricing?instruments=%s", o.BaseURL, o.AccountID, ins.Symbol)
	var wrap struct {
		Prices []struct {
			Bids []struct {
				Price string `json:"price"`
				Liquidity int `json:"liquidity"`
			} `json:"bids"`
			Asks []struct {
				Price string `json:"price"`
				Liquidity int `json:"liquidity"`
			} `json:"asks"`
		} `json:"prices"`
	}
	if err := jsonGet(ctx, o.HTTP, u, o.hdr(), &wrap); err != nil {
		return types.BookTop{}, err
	}
	if len(wrap.Prices) == 0 {
		return types.BookTop{}, fmt.Errorf("oanda: no price")
	}
	p := wrap.Prices[0]
	bt := types.BookTop{Instrument: ins, Timestamp: time.Now().UTC()}
	if len(p.Bids) > 0 {
		bt.BidPrice = mustParseFloat(p.Bids[0].Price)
		bt.BidSize = float64(p.Bids[0].Liquidity)
	}
	if len(p.Asks) > 0 {
		bt.AskPrice = mustParseFloat(p.Asks[0].Price)
		bt.AskSize = float64(p.Asks[0].Liquidity)
	}
	return bt, nil
}

func (o *OANDA) FetchBalances(ctx context.Context) ([]types.Balance, error) {
	if o.Token == "" || o.AccountID == "" {
		return nil, ErrAuthRequired
	}
	u := fmt.Sprintf("%s/accounts/%s/summary", o.BaseURL, o.AccountID)
	var wrap struct {
		Account struct {
			Balance  string `json:"balance"`
			Currency string `json:"currency"`
		} `json:"account"`
	}
	if err := jsonGet(ctx, o.HTTP, u, o.hdr(), &wrap); err != nil {
		return nil, err
	}
	return []types.Balance{{
		Venue: o.Name(), Asset: wrap.Account.Currency, Free: mustParseFloat(wrap.Account.Balance),
	}}, nil
}

func (o *OANDA) FetchPositions(ctx context.Context) ([]types.Position, error) {
	u := fmt.Sprintf("%s/accounts/%s/openPositions", o.BaseURL, o.AccountID)
	var wrap struct {
		Positions []struct {
			Instrument string `json:"instrument"`
			Long       struct {
				Units string `json:"units"`
				Avg   string `json:"averagePrice"`
			} `json:"long"`
			Short struct {
				Units string `json:"units"`
				Avg   string `json:"averagePrice"`
			} `json:"short"`
			Unrealized string `json:"unrealizedPL"`
		} `json:"positions"`
	}
	if err := jsonGet(ctx, o.HTTP, u, o.hdr(), &wrap); err != nil {
		return nil, err
	}
	var out []types.Position
	for _, p := range wrap.Positions {
		lu := mustParseFloat(p.Long.Units)
		su := mustParseFloat(p.Short.Units)
		if lu == 0 && su == 0 {
			continue
		}
		qty := lu - su
		avg := mustParseFloat(p.Long.Avg)
		if su != 0 {
			avg = mustParseFloat(p.Short.Avg)
		}
		out = append(out, types.Position{
			Instrument: types.Instrument{Venue: o.Name(), Symbol: p.Instrument, Class: types.AssetForex},
			Qty:        qty,
			AvgEntry:   avg,
			Unrealized: mustParseFloat(p.Unrealized),
			UpdatedAt:  time.Now().UTC(),
		})
	}
	return out, nil
}

func (o *OANDA) PlaceOrder(ctx context.Context, intent types.OrderIntent) (types.Order, error) {
	if o.Token == "" {
		return types.Order{}, ErrAuthRequired
	}
	u := fmt.Sprintf("%s/accounts/%s/orders", o.BaseURL, o.AccountID)
	units := fmtQty(intent.Quantity)
	if intent.Side == types.SideSell {
		units = "-" + units
	}
	body := map[string]any{
		"order": map[string]any{
			"type":        "MARKET",
			"instrument":  intent.Instrument.Symbol,
			"units":       units,
			"timeInForce": "FOK",
		},
	}
	if intent.Type == types.OrderLimit {
		body = map[string]any{
			"order": map[string]any{
				"type":        "LIMIT",
				"instrument":  intent.Instrument.Symbol,
				"units":       units,
				"price":       fmtQty(intent.LimitPrice),
				"timeInForce": "GTC",
			},
		}
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return types.Order{}, err
	}
	for k, v := range o.hdr() {
		req.Header.Set(k, v)
	}
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return types.Order{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return types.Order{}, fmt.Errorf("oanda %d: %s", resp.StatusCode, string(raw))
	}
	var ord struct {
		OrderCreateTransaction struct {
			ID string `json:"id"`
		} `json:"orderCreateTransaction"`
	}
	_ = json.Unmarshal(raw, &ord)
	now := time.Now().UTC()
	return types.Order{
		ID: intent.ID, ExchangeID: ord.OrderCreateTransaction.ID, Intent: intent,
		State: types.OrderOpen, SubmittedAt: now, UpdatedAt: now,
	}, nil
}

func (o *OANDA) CancelOrder(ctx context.Context, exchangeOrderID string, ins types.Instrument) error {
	u := fmt.Sprintf("%s/accounts/%s/orders/%s/cancel", o.BaseURL, o.AccountID, exchangeOrderID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, strings.NewReader("{}"))
	if err != nil {
		return err
	}
	for k, v := range o.hdr() {
		req.Header.Set(k, v)
	}
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (o *OANDA) CancelAll(ctx context.Context) error { return nil }
