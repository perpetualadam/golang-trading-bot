// Package types holds shared domain types for the trading core.
package types

import (
	"time"
)

// AssetClass categorizes instruments for exposure limits.
type AssetClass string

const (
	AssetCrypto   AssetClass = "crypto"
	AssetForex    AssetClass = "forex"
	AssetEquity   AssetClass = "equity"
	AssetOption   AssetClass = "option"
	AssetFuture   AssetClass = "future"
)

// MarketKind distinguishes spot vs derivatives.
type MarketKind string

const (
	MarketSpot    MarketKind = "spot"
	MarketFuture  MarketKind = "future"
	MarketOption  MarketKind = "option"
)

// Side is order direction.
type Side string

const (
	SideBuy  Side = "buy"
	SideSell Side = "sell"
)

// OrderType for execution.
type OrderType string

const (
	OrderMarket    OrderType = "market"
	OrderLimit     OrderType = "limit"
	OrderPostOnly  OrderType = "post_only"
)

// TimeInForce for limit orders.
type TimeInForce string

const (
	TIFGTC TimeInForce = "GTC"
	TIFIOC TimeInForce = "IOC"
	TIFFOK TimeInForce = "FOK"
)

// Venue identifies an exchange or broker.
type Venue string

// Instrument is a tradable symbol on a venue.
type Instrument struct {
	Venue       Venue
	Symbol      string
	Base        string
	Quote       string
	Kind        MarketKind
	Class       AssetClass
	TickSize    float64
	LotSize     float64
	Multiplier  float64 // contract multiplier for futures/options
}

// OrderIntent is a strategy-generated order request before risk checks.
type OrderIntent struct {
	ID           string
	StrategyID   string
	Instrument   Instrument
	Side         Side
	Type         OrderType
	Quantity     float64
	LimitPrice   float64 // 0 for market
	PostOnly     bool
	ReduceOnly   bool
	MaxSlippageBps float64
	// StopLossPrice / TakeProfitPrice are absolute prices for venues that support
	// broker-held brackets (e.g. OANDA takeProfitOnFill / stopLossOnFill). 0 = none.
	StopLossPrice   float64
	TakeProfitPrice float64
	// ClientOrderID optional idempotency key (e.g. OANDA clientExtensions.id). Empty = derive from Intent.ID server-side.
	ClientOrderID string
	ClientTag     string
	CreatedAt     time.Time
}

// OrderState tracks lifecycle after submission.
type OrderState string

const (
	OrderPending   OrderState = "pending"
	OrderOpen      OrderState = "open"
	OrderPartial   OrderState = "partial"
	OrderFilled    OrderState = "filled"
	OrderCanceled  OrderState = "canceled"
	OrderRejected  OrderState = "rejected"
)

// Order represents a working or completed order.
type Order struct {
	ID           string
	ExchangeID   string
	// ExchangeTradeID is set when the venue associates a position with a trade (e.g. OANDA tradeOpenedID).
	ExchangeTradeID string
	Intent       OrderIntent
	State        OrderState
	FilledQty    float64
	AvgPrice     float64
	FeesPaid     float64
	SubmittedAt  time.Time
	UpdatedAt    time.Time
	Error        string
}

// Position is net exposure for an instrument.
type Position struct {
	Instrument Instrument
	Qty        float64 // signed: long positive, short negative
	AvgEntry   float64
	Unrealized float64
	Realized   float64
	MarginUsed float64
	UpdatedAt  time.Time
}

// Balance is wallet cash or collateral.
type Balance struct {
	Venue    Venue
	Asset    string
	Free     float64
	Locked   float64
	USDValue float64 // optional, filled when pricing available
}

// BookTop is a shallow view of the order book for liquidity checks.
type BookTop struct {
	Instrument Instrument
	BidPrice   float64
	BidSize    float64
	AskPrice   float64
	AskSize    float64
	Timestamp  time.Time
}

// Fill is an execution report.
type Fill struct {
	OrderID    string
	Instrument Instrument
	Side       Side
	Price      float64
	Qty        float64
	Fee        float64
	Time       time.Time
}

// Signal is strategy output before sizing and allocation.
type Signal struct {
	StrategyID string
	Instrument Instrument
	Direction  float64 // -1..1
	Confidence float64 // 0..1
	Reason     string
	Generated  time.Time
	// EntryReferencePrice is the strategy’s reference for exits (e.g. bar close).
	EntryReferencePrice float64
	// StopLossPrice / TakeProfitPrice are absolute prices for the proposed trade.
	StopLossPrice   float64
	TakeProfitPrice float64
	// Flatten requests flattening any open position for this instrument (no new entry from this signal).
	Flatten bool
}

// Mode selects paper vs live behavior.
type Mode string

const (
	ModePaper Mode = "paper"
	ModeLive  Mode = "live"
	ModeBacktest Mode = "backtest"
)
