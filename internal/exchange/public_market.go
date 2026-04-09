package exchange

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"tradingbot/pkg/types"
)

const binancePublicREST = "https://api.binance.com"

// BinancePublicBookTop returns best bid/ask from Binance spot public REST (no API key).
func BinancePublicBookTop(ctx context.Context, ins types.Instrument) (types.BookTop, error) {
	sym := ins.Symbol
	if sym == "" {
		return types.BookTop{}, fmt.Errorf("instrument symbol empty")
	}
	u := binancePublicREST + "/api/v3/ticker/bookTicker?symbol=" + url.QueryEscape(sym)
	var v struct {
		Symbol   string `json:"symbol"`
		BidPrice string `json:"bidPrice"`
		BidQty   string `json:"bidQty"`
		AskPrice string `json:"askPrice"`
		AskQty   string `json:"askQty"`
	}
	if err := jsonGet(ctx, defaultClient(), u, nil, &v); err != nil {
		return types.BookTop{}, fmt.Errorf("binance public book %s: %w", sym, err)
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
