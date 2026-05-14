package exchange

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
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
	token = strings.TrimSpace(token)
	accountID = strings.TrimSpace(accountID)
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

// oandaUnits formats MARKET/LIMIT order units: OANDA rejects high-precision floats (UNITS_PRECISION_EXCEEDED).
// Most FX majors use whole base-currency units; we round using |quantity| and apply sign from side.
func oandaUnits(quantity float64, side types.Side) (string, error) {
	if quantity <= 0 {
		return "", fmt.Errorf("oanda: quantity must be positive")
	}
	u := math.Round(quantity)
	if u < 1 {
		return "", fmt.Errorf("oanda: quantity %g rounds below 1 base unit (OANDA minimum step)", quantity)
	}
	// Whole units only (EUR_USD, etc.); extend later if instrument.tradeUnitsPrecision is fetched from API.
	s := strconv.FormatInt(int64(u), 10)
	if side == types.SideSell {
		s = "-" + s
	}
	return s, nil
}

// formatOANDAPrice formats stop/take-profit prices for v20 (majors ~5 dp; JPY quotes ~3 dp).
func formatOANDAPrice(p float64) string {
	if math.Abs(p) >= 12 {
		return strconv.FormatFloat(p, 'f', 3, 64)
	}
	return strconv.FormatFloat(p, 'f', 5, 64)
}

func applyOANDABrackets(order map[string]any, intent types.OrderIntent) {
	if intent.StopLossPrice > 0 {
		order["stopLossOnFill"] = map[string]any{
			"price":         formatOANDAPrice(intent.StopLossPrice),
			"timeInForce":   "GTC",
		}
	}
	if intent.TakeProfitPrice > 0 {
		order["takeProfitOnFill"] = map[string]any{
			"price":         formatOANDAPrice(intent.TakeProfitPrice),
			"timeInForce":   "GTC",
		}
	}
}

// oandaSanitizeClientID normalizes OANDA client order id: [A-Za-z0-9_-], max 50.
func oandaSanitizeClientID(s string) string {
	if s == "" {
		return "ord"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		out = "ord"
	}
	if len(out) > 50 {
		out = out[:50]
	}
	return out
}

func applyOANDAClientExtensions(order map[string]any, intent types.OrderIntent) {
	cid := strings.TrimSpace(intent.ClientOrderID)
	if cid == "" {
		cid = oandaSanitizeClientID(intent.ID)
	} else {
		cid = oandaSanitizeClientID(cid)
	}
	tag := strings.TrimSpace(intent.ClientTag)
	if tag == "" {
		tag = strings.TrimSpace(intent.StrategyID)
	}
	if len(tag) > 50 {
		tag = tag[:50]
	}
	order["clientExtensions"] = map[string]any{
		"id":  cid,
		"tag": tag,
	}
}

func parseOANDAHTTPError(status int, raw []byte) error {
	var wrap struct {
		ErrorMessage string `json:"errorMessage"`
		ErrorCode    string `json:"errorCode"`
	}
	if err := json.Unmarshal(raw, &wrap); err == nil && (wrap.ErrorMessage != "" || wrap.ErrorCode != "") {
		if wrap.ErrorMessage != "" && wrap.ErrorCode != "" {
			return fmt.Errorf("oanda %d: %s (%s)", status, wrap.ErrorMessage, wrap.ErrorCode)
		}
		if wrap.ErrorMessage != "" {
			return fmt.Errorf("oanda %d: %s", status, wrap.ErrorMessage)
		}
		return fmt.Errorf("oanda %d: %s", status, wrap.ErrorCode)
	}
	return fmt.Errorf("oanda %d: %s", status, string(raw))
}

func parseOANDAOrderCreateResponse(raw []byte, intent types.OrderIntent) (types.Order, error) {
	var env struct {
		OCT *struct {
			ID    string `json:"id"`
			Units string `json:"units"`
		} `json:"orderCreateTransaction"`
		OFT *struct {
			ID            string `json:"id"`
			OrderID       string `json:"orderID"`
			Instrument    string `json:"instrument"`
			Units         string `json:"units"`
			Price         string `json:"price"`
			Commission    string `json:"commission"`
			Guaranteed    string `json:"guaranteedExecutionFee"`
			TradeOpenedID string `json:"tradeOpenedID"`
		} `json:"orderFillTransaction"`
		OCaT *struct {
			Reason string `json:"reason"`
		} `json:"orderCancelTransaction"`
		ORejT *struct {
			RejectReason string `json:"rejectReason"`
		} `json:"orderRejectTransaction"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return types.Order{}, fmt.Errorf("oanda response: %w", err)
	}
	now := time.Now().UTC()
	out := types.Order{
		ID: intent.ID, Intent: intent, SubmittedAt: now, UpdatedAt: now,
		State: types.OrderOpen,
	}
	if env.OCT != nil && env.OCT.ID != "" {
		out.ExchangeID = env.OCT.ID
	}
	if env.OFT == nil {
		if env.ORejT != nil && strings.TrimSpace(env.ORejT.RejectReason) != "" {
			out.Error = strings.TrimSpace(env.ORejT.RejectReason)
			out.State = types.OrderRejected
			return out, fmt.Errorf("oanda: order rejected: %s", out.Error)
		}
		if env.OCaT != nil {
			reason := strings.TrimSpace(env.OCaT.Reason)
			if reason == "" {
				reason = "order canceled"
			}
			out.State = types.OrderCanceled
			out.Error = reason
			return out, fmt.Errorf("oanda: %s", reason)
		}
		return out, nil
	}
	if env.OFT != nil && strings.TrimSpace(env.OFT.OrderID) != "" {
		out.ExchangeID = strings.TrimSpace(env.OFT.OrderID)
	}
	if env.OFT != nil {
		trID := strings.TrimSpace(env.OFT.TradeOpenedID)
		out.ExchangeTradeID = trID
		qty := math.Abs(mustParseFloat(env.OFT.Units))
		px := mustParseFloat(env.OFT.Price)
		reqQty := 0.0
		if env.OCT != nil {
			reqQty = math.Abs(mustParseFloat(env.OCT.Units))
		}
		if qty > 0 && px > 0 {
			out.FilledQty = qty
			out.AvgPrice = px
			if reqQty > 1e-9 && qty+1e-6 < reqQty {
				out.State = types.OrderPartial
			} else {
				out.State = types.OrderFilled
			}
		}
		out.FeesPaid = math.Abs(mustParseFloat(env.OFT.Commission))
		out.FeesPaid += math.Abs(mustParseFloat(env.OFT.Guaranteed))
	}
	return out, nil
}

// UpdateTradeExits sets or updates stop-loss and take-profit on an open trade (post-fill management / amend).
func (o *OANDA) UpdateTradeExits(ctx context.Context, tradeID string, _ types.Instrument, stopLoss, takeProfit float64) error {
	if o.Token == "" || o.AccountID == "" {
		return ErrAuthRequired
	}
	tradeID = strings.TrimSpace(tradeID)
	if tradeID == "" {
		return fmt.Errorf("oanda: missing trade id")
	}
	body := map[string]any{}
	if stopLoss > 0 {
		body["stopLoss"] = map[string]any{
			"price":         formatOANDAPrice(stopLoss),
			"timeInForce":   "GTC",
		}
	}
	if takeProfit > 0 {
		body["takeProfit"] = map[string]any{
			"price":         formatOANDAPrice(takeProfit),
			"timeInForce":   "GTC",
		}
	}
	if len(body) == 0 {
		return nil
	}
	b, _ := json.Marshal(body)
	u := fmt.Sprintf("%s/accounts/%s/trades/%s", o.BaseURL, o.AccountID, tradeID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, bytes.NewReader(b))
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
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return parseOANDAHTTPError(resp.StatusCode, raw)
	}
	return nil
}

func (o *OANDA) PlaceOrder(ctx context.Context, intent types.OrderIntent) (types.Order, error) {
	if o.Token == "" {
		return types.Order{}, ErrAuthRequired
	}
	u := fmt.Sprintf("%s/accounts/%s/orders", o.BaseURL, o.AccountID)
	units, err := oandaUnits(intent.Quantity, intent.Side)
	if err != nil {
		return types.Order{}, err
	}
	inner := map[string]any{
		"type":        "MARKET",
		"instrument":  intent.Instrument.Symbol,
		"units":       units,
		"timeInForce": "FOK",
	}
	applyOANDAClientExtensions(inner, intent)
	applyOANDABrackets(inner, intent)
	body := map[string]any{"order": inner}
	if intent.Type == types.OrderLimit {
		inner = map[string]any{
			"type":        "LIMIT",
			"instrument":  intent.Instrument.Symbol,
			"units":       units,
			"price":       fmtQty(intent.LimitPrice),
			"timeInForce": "GTC",
		}
		applyOANDAClientExtensions(inner, intent)
		applyOANDABrackets(inner, intent)
		body = map[string]any{"order": inner}
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
		return types.Order{}, parseOANDAHTTPError(resp.StatusCode, raw)
	}
	return parseOANDAOrderCreateResponse(raw, intent)
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
