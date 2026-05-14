package risk

import (
	"errors"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"tradingbot/internal/config"
	"tradingbot/internal/portfolio"
	"tradingbot/pkg/types"
)

var (
	ErrKillSwitch      = errors.New("kill switch active")
	ErrCircuitBreaker  = errors.New("circuit breaker tripped")
	ErrExposureLimit   = errors.New("exposure limit")
	ErrSlippage        = errors.New("slippage tolerance")
	ErrLiquidity       = errors.New("liquidity filter")
	ErrMissingExitPlan = errors.New("missing stop or take-profit for broker-held exits")
	ErrInvalidExits    = errors.New("invalid stop/take-profit vs entry reference")
)

// Engine performs pre-trade checks and portfolio risk metrics.
type Engine struct {
	cfg *config.RiskConfig
	pf  *portfolio.State
	mu  sync.RWMutex

	// simple rolling returns for VaR (fractions, not percent)
	retWindow []float64
	winCap    int
}

func NewEngine(cfg *config.RiskConfig, pf *portfolio.State) *Engine {
	return &Engine{cfg: cfg, pf: pf, winCap: 256}
}

// CheckKillSwitch reads optional file presence or env TRADING_HALT=1.
func (e *Engine) CheckKillSwitch() error {
	if os.Getenv("TRADING_HALT") == "1" {
		return ErrKillSwitch
	}
	if e.cfg.KillSwitchFile != "" {
		if _, err := os.Stat(e.cfg.KillSwitchFile); err == nil {
			return ErrKillSwitch
		}
	}
	return nil
}

// CheckDailyDrawdown returns soft/hard flags; hard blocks new risk.
func (e *Engine) CheckDailyDrawdown() (soft bool, hard bool) {
	dd := e.pf.DailyDrawdownPct() // negative if loss
	loss := -dd
	if loss >= e.cfg.DailyDrawdownHardPct {
		return true, true
	}
	if loss >= e.cfg.DailyDrawdownSoftPct {
		return true, false
	}
	if loss >= e.cfg.DailyDrawdownWarnPct {
		return true, false
	}
	return false, false
}

// PreTrade validates a single order intent against capital rules.
func (e *Engine) PreTrade(intent types.OrderIntent, book types.BookTop, equityUSD float64) error {
	if err := e.CheckKillSwitch(); err != nil {
		return err
	}
	if _, hard := e.CheckDailyDrawdown(); hard {
		return ErrCircuitBreaker
	}
	if intent.ReduceOnly {
		return e.preTradeCommon(intent, book, equityUSD, 0, true)
	}
	if equityUSD <= 0 {
		return ErrExposureLimit
	}
	ref := book.AskPrice
	if intent.Side == types.SideSell {
		ref = book.BidPrice
	}
	if ref <= 0 {
		return ErrLiquidity
	}
	if e.mustAttachOANDAExits(intent) {
		if intent.StopLossPrice <= 0 || intent.TakeProfitPrice <= 0 {
			return ErrMissingExitPlan
		}
		if err := e.validateExitGeometry(intent, ref); err != nil {
			return err
		}
		riskPerUnit := math.Abs(ref - intent.StopLossPrice)
		if riskPerUnit <= 0 {
			return ErrInvalidExits
		}
		riskUSD := intent.Quantity * riskPerUnit
		riskBudget := equityUSD * (e.cfg.MaxRiskPerTradePct / 100)
		if riskUSD > riskBudget+1e-9 {
			return ErrExposureLimit
		}
		return e.preTradeCommon(intent, book, equityUSD, ref, false)
	}
	if intent.StopLossPrice > 0 {
		if err := e.validateExitGeometry(intent, ref); err != nil {
			return err
		}
		riskPerUnit := math.Abs(ref - intent.StopLossPrice)
		if riskPerUnit <= 0 {
			return ErrInvalidExits
		}
		riskUSD := intent.Quantity * riskPerUnit
		riskBudget := equityUSD * (e.cfg.MaxRiskPerTradePct / 100)
		if riskUSD > riskBudget+1e-9 {
			return ErrExposureLimit
		}
		return e.preTradeCommon(intent, book, equityUSD, ref, false)
	}
	notional := ref * intent.Quantity
	riskPct := (notional / equityUSD) * 100
	if riskPct > e.cfg.MaxRiskPerTradePct {
		return ErrExposureLimit
	}
	return e.preTradeCommon(intent, book, equityUSD, ref, false)
}

func (e *Engine) preTradeCommon(intent types.OrderIntent, book types.BookTop, equityUSD float64, ref float64, reduceOnly bool) error {
	if !reduceOnly && equityUSD <= 0 {
		return ErrExposureLimit
	}
	if !reduceOnly && ref > 0 && e.cfg.MaxNotionalPerTradePct > 0 {
		notional := ref * intent.Quantity
		if (notional/equityUSD)*100 > e.cfg.MaxNotionalPerTradePct {
			return ErrExposureLimit
		}
	}
	depth := math.Min(book.BidSize*book.BidPrice, book.AskSize*book.AskPrice)
	if e.cfg.MinBookDepthUSD > 0 && depth < e.cfg.MinBookDepthUSD {
		return ErrLiquidity
	}
	slipBps := intent.MaxSlippageBps
	if slipBps <= 0 {
		slipBps = e.cfg.MaxSlippageBpsDefault
	}
	if slipBps > 0 && book.AskPrice > 0 && book.BidPrice > 0 {
		mid := (book.AskPrice + book.BidPrice) / 2
		spreadBps := (book.AskPrice - book.BidPrice) / mid * 10000
		if spreadBps > slipBps*2 { // coarse: wide spread vs tolerance
			return ErrSlippage
		}
	}
	return nil
}

func (e *Engine) mustAttachOANDAExits(intent types.OrderIntent) bool {
	if !e.cfg.RequireBrokerStops {
		return false
	}
	return strings.EqualFold(string(intent.Instrument.Venue), "OANDA")
}

func (e *Engine) validateExitGeometry(intent types.OrderIntent, ref float64) error {
	if ref <= 0 {
		return ErrLiquidity
	}
	tol := math.Max(1e-10, ref*1e-6)
	if intent.Side == types.SideBuy {
		if intent.StopLossPrice >= ref-tol {
			return ErrInvalidExits
		}
		if intent.TakeProfitPrice > 0 && intent.TakeProfitPrice <= ref+tol {
			return ErrInvalidExits
		}
		return nil
	}
	if intent.StopLossPrice <= ref+tol {
		return ErrInvalidExits
	}
	if intent.TakeProfitPrice > 0 && intent.TakeProfitPrice >= ref-tol {
		return ErrInvalidExits
	}
	return nil
}

// PushReturn appends a periodic portfolio return for historical VaR (r_t = equity_t/equity_{t-1}-1).
func (e *Engine) PushReturn(prevEquity, newEquity float64) {
	if prevEquity <= 0 {
		return
	}
	r := newEquity/prevEquity - 1
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.retWindow) >= e.winCap {
		copy(e.retWindow, e.retWindow[1:])
		e.retWindow = e.retWindow[:len(e.retWindow)-1]
	}
	e.retWindow = append(e.retWindow, r)
}

// VaRCVaR computes parametric-ish historical VaR and CVaR on window (confidence in (0,1)).
func (e *Engine) VaRCVaR(confidence float64) (varAbs float64, cvarAbs float64, ok bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	n := len(e.retWindow)
	if n < 8 {
		return 0, 0, false
	}
	// copy and sort ascending (losses on left if negative returns)
	cp := make([]float64, n)
	copy(cp, e.retWindow)
	sort.Float64s(cp)
	alpha := 1 - confidence
	idx := int(math.Ceil(alpha*float64(n))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= n {
		idx = n - 1
	}
	varAbs = -cp[idx] // loss as positive number at quantile
	// CVaR: mean of tail
	sum := 0.0
	count := 0
	for i := 0; i <= idx; i++ {
		sum += -cp[i]
		count++
	}
	if count > 0 {
		cvarAbs = sum / float64(count)
	}
	return varAbs, cvarAbs, true
}

// StressShock applies simple multiplicative shocks to equity (for monitoring hooks).
func StressShock(equity float64, spotShockPct, volShockPct float64) float64 {
	return equity * (1 + spotShockPct/100) * (1 + volShockPct/100)
}

// MaxPositionQtyFromRisk returns max |qty| for given price under max risk % rule.
func (e *Engine) MaxPositionQtyFromRisk(price float64, equityUSD float64) float64 {
	if price <= 0 || equityUSD <= 0 {
		return 0
	}
	maxNotional := equityUSD * (e.cfg.MaxRiskPerTradePct / 100)
	return maxNotional / price
}

// MaxPositionQtyFromStop sizes |qty| so loss at the stop is approximately MaxRiskPerTradePct of equity
// (quote-currency / USD approximation: |entryRef - stop| * qty).
func (e *Engine) MaxPositionQtyFromStop(entryRefPrice, stopPrice float64, equityUSD float64) float64 {
	if entryRefPrice <= 0 || equityUSD <= 0 {
		return 0
	}
	riskPerUnit := math.Abs(entryRefPrice - stopPrice)
	if riskPerUnit <= 0 {
		return 0
	}
	budget := equityUSD * (e.cfg.MaxRiskPerTradePct / 100)
	return budget / riskPerUnit
}

// NowUTC helper for tests.
func NowUTC() time.Time { return time.Now().UTC() }
