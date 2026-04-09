package app

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"tradingbot/internal/config"
	"tradingbot/internal/exchange"
	"tradingbot/internal/execution"
	"tradingbot/internal/metrics"
	"tradingbot/internal/portfolio"
	"tradingbot/internal/risk"
	"tradingbot/internal/storage"
	"tradingbot/internal/strategy"
	"tradingbot/pkg/types"
)

// Runner wires core loop: signals → risk → execution.
type Runner struct {
	cfg    *config.Root
	log    zerolog.Logger
	reg    *exchange.Registry
	risk   *risk.Engine
	router *execution.Router
	stack  *strategy.Stack
	pf     *portfolio.State
	store  *storage.Store
	paper  *exchange.PaperConnector
	corr   *strategy.CorrelationMonitor

	runMu      sync.Mutex
	loopCtx    context.Context
	loopCancel context.CancelFunc
	paused     bool
	halted     bool
	nukeArmed  bool
	nukeUntil  time.Time

	prevEquity float64
	eqHistory  []float64
	eqMu       sync.Mutex
}

func NewRunner(cfg *config.Root, log zerolog.Logger, reg *exchange.Registry, r *risk.Engine,
	rt *execution.Router, stack *strategy.Stack, pf *portfolio.State, store *storage.Store,
	paper *exchange.PaperConnector,
) *Runner {
	return &Runner{
		cfg:        cfg,
		log:        log,
		reg:        reg,
		risk:       r,
		router:     rt,
		stack:      stack,
		pf:         pf,
		store:      store,
		paper:      paper,
		corr:       strategy.NewCorrelationMonitor(64, 0.7),
		prevEquity: pf.Equity(),
	}
}

func (r *Runner) StartTrading(ctx context.Context) error {
	r.runMu.Lock()
	defer r.runMu.Unlock()
	if r.halted {
		return fmt.Errorf("runner halted after nuke; restart process to re-arm")
	}
	if r.loopCancel != nil {
		return nil
	}
	lctx, cancel := context.WithCancel(ctx)
	r.loopCtx = lctx
	r.loopCancel = cancel
	go r.loop(lctx)
	return nil
}

func (r *Runner) StopTrading(ctx context.Context) error {
	r.runMu.Lock()
	defer r.runMu.Unlock()
	if r.loopCancel != nil {
		r.loopCancel()
		r.loopCancel = nil
	}
	return nil
}

func (r *Runner) PauseNewEntries() {
	r.runMu.Lock()
	defer r.runMu.Unlock()
	r.paused = true
}

func (r *Runner) ResumeNewEntries() {
	r.runMu.Lock()
	defer r.runMu.Unlock()
	r.paused = false
}

func (r *Runner) RequestNuke() {
	r.runMu.Lock()
	defer r.runMu.Unlock()
	r.nukeArmed = true
	r.nukeUntil = time.Now().Add(60 * time.Second)
}

func (r *Runner) IsNukePending() bool {
	r.runMu.Lock()
	defer r.runMu.Unlock()
	return r.nukeArmed && time.Now().Before(r.nukeUntil)
}

func (r *Runner) ConfirmNuke(ctx context.Context) error {
	r.runMu.Lock()
	defer r.runMu.Unlock()
	if !r.nukeArmed || time.Now().After(r.nukeUntil) {
		r.nukeArmed = false
		return fmt.Errorf("nuke confirmation expired or not armed")
	}
	r.nukeArmed = false
	if r.paper != nil {
		_ = r.paper.FlattenAll(ctx)
	}
	_ = r.router.CancelAllVenues(ctx)
	r.halted = true
	r.paused = true
	if r.loopCancel != nil {
		r.loopCancel()
		r.loopCancel = nil
	}
	if r.store != nil {
		_ = r.store.LogEvent(ctx, "nuke", map[string]any{"ts": time.Now().Unix()})
	}
	return nil
}

func (r *Runner) StatusText(ctx context.Context) string {
	eq, _, _, pos := r.pf.Snapshot()
	var b strings.Builder
	fmt.Fprintf(&b, "Equity USD: %.2f\n", eq)
	fmt.Fprintf(&b, "Daily DD %%: %.3f\n", r.pf.DailyDrawdownPct())
	if r.paused {
		b.WriteString("State: PAUSED (no new entries)\n")
	} else if r.halted {
		b.WriteString("State: HALTED\n")
	} else {
		b.WriteString("State: RUNNING\n")
	}
	b.WriteString("Open positions:\n")
	if len(pos) == 0 {
		b.WriteString("  (none)\n")
		return b.String()
	}
	for _, p := range pos {
		if math.Abs(p.Qty) < 1e-10 {
			continue
		}
		fmt.Fprintf(&b, "  %s %s qty=%.6f entry=%.4f uPnL=%.2f\n",
			p.Instrument.Venue, p.Instrument.Symbol, p.Qty, p.AvgEntry, p.Unrealized)
	}
	return b.String()
}

func (r *Runner) BalancesText(ctx context.Context) string {
	var b strings.Builder
	for _, c := range r.reg.All() {
		bals, err := c.FetchBalances(ctx)
		if err != nil {
			fmt.Fprintf(&b, "%s: error %v\n", c.Name(), err)
			continue
		}
		fmt.Fprintf(&b, "Venue %s:\n", c.Name())
		for _, bal := range bals {
			fmt.Fprintf(&b, "  %s free=%.4f locked=%.4f\n", bal.Asset, bal.Free, bal.Locked)
		}
	}
	if b.Len() == 0 {
		return "No connectors."
	}
	return b.String()
}

func (r *Runner) DailyText(ctx context.Context) string {
	eq, dayStart, dayPnl, _ := r.pf.Snapshot()
	var fillPnl float64
	if r.store != nil {
		fillPnl, _ = r.store.DailyPnLFromFills(ctx)
	}
	return fmt.Sprintf("Equity now: %.2f\nDay start: %.2f\nDay realized (ledger): %.2f\nFills approx 24h: %.2f\n",
		eq, dayStart, dayPnl, fillPnl)
}

func (r *Runner) loop(ctx context.Context) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	ins := types.Instrument{Venue: "PAPER", Symbol: "BTCUSDT", Base: "BTC", Quote: "USDT", Kind: types.MarketSpot, Class: types.AssetCrypto}
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			r.onTick(ctx, ins)
		}
	}
}

func (r *Runner) onTick(ctx context.Context, ins types.Instrument) {
	if r.halted {
		return
	}
	r.pf.RollDailyIfNeeded(time.Now())
	now := time.Now().UTC()
	var book types.BookTop
	var err error
	if r.paper != nil {
		book, err = r.paper.FetchBookTop(ctx, ins)
		if err != nil {
			r.log.Warn().Err(err).Msg("book")
			return
		}
	}
	mid := (book.BidPrice + book.AskPrice) / 2
	// Single-interval OHLC from live top of book (strategies need a bar; no fabricated series).
	b := strategy.Bar{Instrument: ins, Timestamp: now.Unix(), Open: mid, High: mid * 1.001, Low: mid * 0.999, Close: mid, Volume: 1}

	sigs, err := r.stack.RunBar(ctx, b)
	if err != nil {
		r.log.Error().Err(err).Msg("stack")
		return
	}
	eq, _, _, _ := r.pf.Snapshot()
	mult := r.corr.ExposureMultiplier()
	maxRisk := r.cfg.Risk.MaxRiskPerTradePct * mult

	active := 0
	for _, s := range sigs {
		if s.Confidence > 0 && math.Abs(s.Direction) >= 1e-8 {
			active++
		}
	}
	scale := 1.0
	if active > 1 {
		scale = 1.0 / float64(active)
	}

	for _, s := range sigs {
		if r.paused {
			break
		}
		if s.Confidence <= 0 || math.Abs(s.Direction) < 1e-8 {
			continue
		}
		maxQ := r.risk.MaxPositionQtyFromRisk(mid, eq) * math.Abs(s.Direction) * s.Confidence * scale
		if maxQ <= 0 {
			continue
		}
		side := types.SideBuy
		if s.Direction < 0 {
			side = types.SideSell
		}
		intent := types.OrderIntent{
			ID:             s.StrategyID + "-" + ins.Symbol,
			StrategyID:     s.StrategyID,
			Instrument:     ins,
			Side:           side,
			Type:           types.OrderMarket,
			Quantity:       maxQ,
			MaxSlippageBps: r.cfg.Risk.MaxSlippageBpsDefault,
			CreatedAt:      now,
		}
		if err := r.risk.CheckKillSwitch(); err != nil {
			metrics.RiskRejects.Inc()
			continue
		}
		if _, hard := r.risk.CheckDailyDrawdown(); hard {
			metrics.RiskRejects.Inc()
			continue
		}
		if err := r.risk.PreTrade(intent, book, eq); err != nil {
			metrics.RiskRejects.Inc()
			r.log.Debug().Err(err).Str("strat", s.StrategyID).Msg("pretrade reject")
			continue
		}
		_ = maxRisk // reserved for allocator / HRP weighting

		if r.paper != nil {
			ord, err := r.paper.PlaceOrder(ctx, intent)
			if err != nil {
				metrics.OrdersTotal.WithLabelValues(string(r.paper.Name()), "error").Inc()
				continue
			}
			metrics.OrdersTotal.WithLabelValues(string(r.paper.Name()), "ok").Inc()
			_ = ord
			if r.store != nil {
				_ = r.store.LogFill(ctx, types.Fill{
					OrderID: ord.ID, Instrument: ins, Side: side, Price: ord.AvgPrice, Qty: ord.FilledQty, Fee: ord.FeesPaid, Time: now,
				})
			}
		}
	}

	// refresh portfolio from paper
	if r.paper != nil {
		pos, _ := r.paper.FetchPositions(ctx)
		for i := range pos {
			p := &pos[i]
			if math.Abs(p.Qty) >= 1e-10 && p.AvgEntry > 0 {
				p.Unrealized = (mid - p.AvgEntry) * p.Qty
			}
		}
		r.pf.UpdatePositions(pos)
		bals, _ := r.paper.FetchBalances(ctx)
		usd := 0.0
		for _, bal := range bals {
			if bal.Asset == "USD" || bal.Asset == "USDT" {
				usd += bal.Free + bal.Locked
			}
		}
		for _, p := range pos {
			usd += p.Unrealized
		}
		if usd <= 0 {
			usd = eq
		}
		r.pf.SetEquity(usd)
		r.risk.PushReturn(r.prevEquity, usd)
		r.prevEquity = usd
		metrics.EquityUSD.Set(usd)
		metrics.DailyDrawdownPct.Set(r.pf.DailyDrawdownPct())
		r.pushEqHistory(usd)
	}
}

func (r *Runner) pushEqHistory(v float64) {
	r.eqMu.Lock()
	defer r.eqMu.Unlock()
	r.eqHistory = append(r.eqHistory, v)
	if len(r.eqHistory) > 512 {
		r.eqHistory = r.eqHistory[len(r.eqHistory)-512:]
	}
}

// EquityHistory copy for charts.
func (r *Runner) EquityHistory() []float64 {
	r.eqMu.Lock()
	defer r.eqMu.Unlock()
	out := make([]float64, len(r.eqHistory))
	copy(out, r.eqHistory)
	return out
}
