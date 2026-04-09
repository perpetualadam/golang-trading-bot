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

// KrakenSpot REST (spot balances + orders).
type KrakenSpot struct {
	BaseURL string
	Key     string
	Secret  string // base64
	HTTP    HTTPDoer
}

func NewKrakenSpot(key, secretB64 string) *KrakenSpot {
	return &KrakenSpot{
		BaseURL: "https://api.kraken.com",
		Key:     key,
		Secret:  secretB64,
		HTTP:    defaultClient(),
	}
}

func (k *KrakenSpot) Name() types.Venue { return "KRAKEN" }

func (k *KrakenSpot) Health(ctx context.Context) error {
	return jsonGet(ctx, k.HTTP, k.BaseURL+"/0/public/SystemStatus", nil, &json.RawMessage{})
}

func (k *KrakenSpot) privatePost(ctx context.Context, urlpath string, data url.Values) ([]byte, error) {
	if k.Key == "" || k.Secret == "" {
		return nil, ErrAuthRequired
	}
	nonce := strconv.FormatInt(time.Now().UnixNano(), 10)
	if data == nil {
		data = url.Values{}
	}
	data.Set("nonce", nonce)
	postData := data.Encode()
	sig, err := KrakenSign(urlpath, nonce, postData, k.Secret)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, k.BaseURL+urlpath, strings.NewReader(postData))
	if err != nil {
		return nil, err
	}
	req.Header.Set("API-Key", k.Key)
	req.Header.Set("API-Sign", sig)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := k.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("kraken %d: %s", resp.StatusCode, truncate(string(raw), 400))
	}
	var wrap struct {
		Error  []string        `json:"error"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return nil, err
	}
	if len(wrap.Error) > 0 && wrap.Error[0] != "" {
		return nil, fmt.Errorf("kraken: %v", wrap.Error)
	}
	return wrap.Result, nil
}

func (k *KrakenSpot) FetchBookTop(ctx context.Context, ins types.Instrument) (types.BookTop, error) {
	pair := krakenPair(ins.Symbol)
	u := fmt.Sprintf("%s/0/public/Ticker?pair=%s", k.BaseURL, url.QueryEscape(pair))
	var wrap map[string]map[string]any
	if err := jsonGet(ctx, k.HTTP, u, nil, &wrap); err != nil {
		return types.BookTop{}, err
	}
	var tick map[string]any
	for _, v := range wrap {
		tick = v
		break
	}
	if tick == nil {
		return types.BookTop{}, fmt.Errorf("kraken: no ticker")
	}
	bid := parseKrakenArr(tick["b"])
	ask := parseKrakenArr(tick["a"])
	return types.BookTop{
		Instrument: ins,
		BidPrice:   bid[0],
		BidSize:    bid[1],
		AskPrice:   ask[0],
		AskSize:    ask[1],
		Timestamp:  time.Now().UTC(),
	}, nil
}

func parseKrakenArr(v any) [2]float64 {
	var out [2]float64
	arr, ok := v.([]any)
	if !ok || len(arr) < 2 {
		return out
	}
	out[0] = mustParseFloat(fmt.Sprint(arr[0]))
	out[1] = mustParseFloat(fmt.Sprint(arr[1]))
	return out
}

func krakenPair(sym string) string {
	if sym != "" {
		return sym
	}
	return "XBTUSD"
}

func (k *KrakenSpot) FetchBalances(ctx context.Context) ([]types.Balance, error) {
	raw, err := k.privatePost(ctx, "/0/private/Balance", nil)
	if err != nil {
		return nil, err
	}
	var bal map[string]string
	if err := json.Unmarshal(raw, &bal); err != nil {
		return nil, err
	}
	var out []types.Balance
	for asset, s := range bal {
		f := mustParseFloat(s)
		if f == 0 {
			continue
		}
		out = append(out, types.Balance{Venue: k.Name(), Asset: asset, Free: f})
	}
	return out, nil
}

func (k *KrakenSpot) FetchPositions(ctx context.Context) ([]types.Position, error) {
	return nil, nil
}

func (k *KrakenSpot) PlaceOrder(ctx context.Context, o types.OrderIntent) (types.Order, error) {
	pair := krakenPair(o.Instrument.Symbol)
	v := url.Values{}
	v.Set("pair", pair)
	v.Set("type", string(o.Side))
	v.Set("ordertype", "market")
	if o.Type == types.OrderLimit {
		v.Set("ordertype", "limit")
		v.Set("price", fmtQty(o.LimitPrice))
	}
	v.Set("volume", fmtQty(o.Quantity))
	raw, err := k.privatePost(ctx, "/0/private/AddOrder", v)
	if err != nil {
		return types.Order{}, err
	}
	var res struct {
		TxID []string `json:"txid"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return types.Order{}, err
	}
	oid := ""
	if len(res.TxID) > 0 {
		oid = res.TxID[0]
	}
	now := time.Now().UTC()
	return types.Order{ID: o.ID, ExchangeID: oid, Intent: o, State: types.OrderOpen, SubmittedAt: now, UpdatedAt: now}, nil
}

func (k *KrakenSpot) CancelOrder(ctx context.Context, exchangeOrderID string, ins types.Instrument) error {
	v := url.Values{}
	v.Set("txid", exchangeOrderID)
	_, err := k.privatePost(ctx, "/0/private/CancelOrder", v)
	return err
}

func (k *KrakenSpot) CancelAll(ctx context.Context) error { return nil }
