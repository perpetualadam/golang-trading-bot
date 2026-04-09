package exchange

import (
	"fmt"
	"strings"

	"github.com/rs/zerolog"
	"tradingbot/internal/config"
)

// RegisterFromConfig builds and registers enabled connectors. Keys in YAML should be plaintext
// after main decrypts api_key_enc / api_secret_enc / passphrase_enc when TRADING_MASTER_KEY is set.
func RegisterFromConfig(reg *Registry, cfgs []config.ExchangeCfg, log zerolog.Logger) error {
	for _, e := range cfgs {
		if !e.Enabled {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(e.Name))
		c, err := buildConnector(name, e)
		if err != nil {
			return fmt.Errorf("exchange %q: %w", name, err)
		}
		if c == nil {
			log.Warn().Str("exchange", name).Msg("unknown exchange name, skipped")
			continue
		}
		reg.Register(c)
		log.Info().Str("venue", string(c.Name())).Msg("registered exchange connector")
	}
	return nil
}

func extra(e config.ExchangeCfg, k string) string {
	if e.Extra == nil {
		return ""
	}
	return strings.TrimSpace(e.Extra[k])
}

func buildConnector(name string, e config.ExchangeCfg) (Connector, error) {
	key := e.APIKeyEnc
	sec := e.APISecretEnc
	pass := e.PassphraseEnc

	switch name {
	case "binance":
		return NewBinanceSpot(e.Testnet, key, sec), nil
	case "bybit":
		return NewBybitV5(e.Testnet, key, sec), nil
	case "okx":
		base := extra(e, "base_url")
		if base != "" {
			return NewOKXV5WithBase(base, key, sec, pass), nil
		}
		return NewOKXV5(key, sec, pass), nil
	case "deribit":
		return NewDeribit(e.Testnet, key, sec), nil
	case "kraken":
		return NewKrakenSpot(key, sec), nil
	case "coinbase":
		return NewCoinbaseExchange(key, sec, pass), nil
	case "alpaca":
		return NewAlpaca(e.Testnet, key, sec), nil
	case "oanda":
		acct := extra(e, "account_id")
		if acct == "" {
			return nil, fmt.Errorf("oanda requires extra.account_id")
		}
		return NewOANDA(e.Testnet, key, acct), nil
	case "yahoo", "yfinance":
		return NewYahooChart(), nil
	case "finnhub":
		return NewFinnhub(key), nil
	case "twelvedata", "twelve_data":
		return NewTwelveData(key), nil
	case "polygon":
		return NewPolygon(key), nil
	case "ccxt", "ccxt_sidecar":
		u := extra(e, "sidecar_url")
		if u == "" {
			u = extra(e, "base_url")
		}
		return NewCcxtSidecar(u), nil
	default:
		return nil, nil
	}
}
