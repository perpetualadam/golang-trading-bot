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

	startedAt time.Time
	perfMu    sync.Mutex
	perfLoop  uint64 // main loop ticker firings
	perfTick  uint64 // onTick invocations (per instrument)
	perfSig   uint64 // sum of active signals (direction+confidence)
	perfSigBk uint64 // onTicks with at least one active signal
	perfOrd   uint64 // placeOrder calls (post risk)
	perfSup   uint64 // external venue orders suppressed (paper runtime mode)

	posMu        sync.Mutex
	posEnteredAt map[string]time.Time // midKey(instrument) -> first open / last add time for max-hold tracking
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
		startedAt:  time.Now(),
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

// ReportText is the combined hourly / manual Telegram summary (status + daily + balances).
func (r *Runner) ReportText(ctx context.Context) string {
	var b strings.Builder
	b.WriteString("══ Trading report ══\n")
	b.WriteString(time.Now().UTC().Format(time.RFC3339))
	b.WriteString("\n\n")
	b.WriteString(r.StatusText(ctx))
	b.WriteString("\n")
	b.WriteString(r.DailyText(ctx))
	b.WriteString("\n")
	b.WriteString(r.BalancesText(ctx))
	return b.String()
}

// PerformanceText summarizes session counters, portfolio snapshot, and SQLite fill stats.
func (r *Runner) PerformanceText(ctx context.Context) string {
	r.perfMu.Lock()
	loop, tick, sig, sigBk, ord, sup := r.perfLoop, r.perfTick, r.perfSig, r.perfSigBk, r.perfOrd, r.perfSup
	started := r.startedAt
	r.perfMu.Unlock()

	eq, dayStart, dayPnl, _ := r.pf.Snapshot()
	var b strings.Builder
	fmt.Fprintf(&b, "══ Performance ══\n")
	fmt.Fprintf(&b, "Since: %s UTC\n", started.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "Uptime: %s\n", time.Since(started).Round(time.Second))
	fmt.Fprintf(&b, "Runtime mode: %s\n", r.cfg.Mode())

	fmt.Fprintf(&b, "\n-- Session (this process) --\n")
	fmt.Fprintf(&b, "Main loop ticks: %d\n", loop)
	fmt.Fprintf(&b, "onTick calls: %d\n", tick)
	fmt.Fprintf(&b, "Active signals (sum): %d\n", sig)
	fmt.Fprintf(&b, "Ticks with ≥1 signal: %d\n", sigBk)
	if tick > 0 {
		fmt.Fprintf(&b, "Avg signals/tick: %.3f\n", float64(sig)/float64(tick))
	}
	fmt.Fprintf(&b, "Order intents (post-risk): %d\n", ord)
	fmt.Fprintf(&b, "Suppressed (ext. venue, paper runtime): %d\n", sup)
	if ord >= sup {
		fmt.Fprintf(&b, "Sent to broker / PAPER sim: %d\n", ord-sup)
	}

	fmt.Fprintf(&b, "\n-- Portfolio --\n")
	fmt.Fprintf(&b, "Equity USD: %.2f\n", eq)
	fmt.Fprintf(&b, "Day start equity: %.2f\n", dayStart)
	fmt.Fprintf(&b, "Day realized (ledger): %.2f\n", dayPnl)
	fmt.Fprintf(&b, "Daily DD %%: %.3f\n", r.pf.DailyDrawdownPct())

	if r.store == nil {
		fmt.Fprintf(&b, "\n-- Fills --\nSQLite storage off (no fill history).\n")
		fmt.Fprintf(&b, "\nWin rate: use backtest or enable storage for fill counts.\n")
		return b.String()
	}

	fs, err := r.store.FillSummary(ctx)
	if err != nil {
		fmt.Fprintf(&b, "\n-- Fills --\nError: %v\n", err)
		return b.String()
	}
	fill24h, errP := r.store.DailyPnLFromFills(ctx)
	if errP != nil {
		fill24h = 0
	}
	fmt.Fprintf(&b, "\n-- Fills (SQLite) --\n")
	fmt.Fprintf(&b, "Total fills logged: %d\n", fs.TotalFills)
	fmt.Fprintf(&b, "Fills last 24h: %d\n", fs.Fills24h)
	fmt.Fprintf(&b, "Buy / sell: %d / %d\n", fs.Buys, fs.Sells)
	if fs.TotalFills > 0 {
		fmt.Fprintf(&b, "Buy %% of fills: %.1f%%\n", 100*float64(fs.Buys)/float64(fs.TotalFills))
	}
	fmt.Fprintf(&b, "Fees (sum): %.4f\n", fs.FeesTotal)
	fmt.Fprintf(&b, "Net cash flow (buy/sell rows): %.4f\n", fs.NetCashFlow)
	fmt.Fprintf(&b, "Approx flow last 24h: %.4f\n", fill24h)

	fmt.Fprintf(&b, "\nWin rate: N/A (requires closed trades).\n")
	fmt.Fprintf(&b, "Fill mix + flows above; strategy stats → backtest.\n")
	return b.String()
}

func (r *Runner) loop(ctx context.Context) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			r.perfMu.Lock()
			r.perfLoop++
			r.perfMu.Unlock()
			instruments, err := resolveLoopInstruments(r.cfg)
			if err != nil {
				r.log.Error().Err(err).Msg("loop instruments")
				continue
			}
			mids := make(map[string]float64)
			for _, ins := range instruments {
				mid := r.onTick(ctx, ins)
				if mid > 0 {
					mids[midKey(ins)] = mid
				}
			}
			r.reconcilePortfolio(ctx, instruments, mids)
			r.enforceMaxHoldExits(ctx, instruments)
		}
	}
}

func midKey(ins types.Instrument) string {
	v := ins.Venue
	if v == "" {
		v = "PAPER"
	}
	return string(v) + ":" + ins.Symbol
}

func uniqueVenues(instruments []types.Instrument) []types.Venue {
	seen := make(map[types.Venue]struct{})
	var out []types.Venue
	for _, ins := range instruments {
		v := ins.Venue
		if v == "" {
			v = "PAPER"
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func (r *Runner) connectorForVenue(v types.Venue) (exchange.Connector, error) {
	if v == "" || v == "PAPER" {
		if r.paper == nil {
			return nil, fmt.Errorf("paper connector not available")
		}
		return r.paper, nil
	}
	c, ok := r.reg.Get(string(v))
	if !ok {
		return nil, fmt.Errorf("no connector registered for venue %q", v)
	}
	return c, nil
}

func (r *Runner) connectorFor(ins types.Instrument) (exchange.Connector, error) {
	v := ins.Venue
	if v == "" {
		v = "PAPER"
	}
	return r.connectorForVenue(v)
}

// tradeLogCtx supplies reference price and equity for notional / %-of-equity log fields.
type tradeLogCtx struct {
	RefPrice  float64
	EquityUSD float64
}

func tradeActionLabel(side types.Side) string {
	switch side {
	case types.SideBuy:
		return "BUY"
	case types.SideSell:
		return "SELL"
	default:
		return string(side)
	}
}

func zerologSubmitNotional(ev *zerolog.Event, qty, refPx, equity float64) *zerolog.Event {
	if refPx > 0 && qty > 0 {
		n := qty * refPx
		ev = ev.Float64("notional_usd", n)
		if equity > 0 {
			ev = ev.Float64("notional_pct_equity", 100*n/equity)
		}
	}
	return ev
}

func zerologFilledNotional(ev *zerolog.Event, filledQty, avgPx, equity float64) *zerolog.Event {
	if avgPx > 0 && filledQty > 0 {
		n := filledQty * avgPx
		ev = ev.Float64("filled_notional_usd", n)
		if equity > 0 {
			ev = ev.Float64("filled_notional_pct_equity", 100*n/equity)
		}
	}
	return ev
}

func pctOfEquity(notional, equity float64) float64 {
	if equity <= 0 || notional <= 0 {
		return 0
	}
	return 100 * notional / equity
}

// onTick pulls top-of-book from the instrument's venue, runs the strategy stack, and submits orders via paper or execution router.
func (r *Runner) onTick(ctx context.Context, ins types.Instrument) float64 {
	if r.halted {
		return 0
	}
	defer func() {
		r.perfMu.Lock()
		r.perfTick++
		r.perfMu.Unlock()
	}()
	r.pf.RollDailyIfNeeded(time.Now())
	now := time.Now().UTC()
	conn, err := r.connectorFor(ins)
	if err != nil {
		r.log.Warn().Err(err).Str("venue", string(ins.Venue)).Msg("connector")
		return 0
	}
	book, err := conn.FetchBookTop(ctx, ins)
	if err != nil {
		r.log.Warn().Err(err).Str("venue", string(ins.Venue)).Str("symbol", ins.Symbol).Msg("book")
		return 0
	}
	mid := (book.BidPrice + book.AskPrice) / 2
	if mid <= 0 {
		return 0
	}
	// Single-interval OHLC from live top of book (strategies need a bar; no fabricated series).
	b := strategy.Bar{Instrument: ins, Timestamp: now.Unix(), Open: mid, High: mid * 1.001, Low: mid * 0.999, Close: mid, Volume: 1}

	sigs, err := r.stack.RunBar(ctx, b)
	if err != nil {
		r.log.Error().Err(err).Msg("stack")
		return mid
	}
	if r.cfg.Runtime.LoopLogVerbose {
		nAct := 0
		for _, s := range sigs {
			if s.Confidence > 0 && math.Abs(s.Direction) >= 1e-8 {
				nAct++
			}
		}
		r.log.Info().Str("venue", string(ins.Venue)).Str("symbol", ins.Symbol).
			Int("utc_hour", now.Hour()).Float64("bid", book.BidPrice).Float64("ask", book.AskPrice).Float64("mid", mid).
			Int("signals", len(sigs)).Int("active_signals", nAct).
			Msg("tick")
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

	r.perfMu.Lock()
	r.perfSig += uint64(active)
	if active > 0 {
		r.perfSigBk++
	}
	r.perfMu.Unlock()

	for _, s := range sigs {
		if s.Flatten {
			if !r.halted {
				_, _, _, posMap := r.pf.Snapshot()
				if p, ok := posMap[midKey(s.Instrument)]; ok && math.Abs(p.Qty) >= 1e-9 {
					r.log.Info().Str("strat", s.StrategyID).Str("symbol", s.Instrument.Symbol).Str("exit", "flatten_signal").Msg("signal exit")
					r.closeVenuePosition(ctx, s.Instrument, "flatten_signal")
				}
			}
			continue
		}
		if r.paused {
			break
		}
		if r.cfg.Risk.ExitOnOppositeSignal && !r.halted {
			_, _, _, posMap := r.pf.Snapshot()
			if p, ok := posMap[midKey(s.Instrument)]; ok && positionOpposesSignal(p.Qty, s.Direction) {
				r.log.Info().Str("strat", s.StrategyID).Str("symbol", s.Instrument.Symbol).Str("exit", "opposite_signal").Msg("signal exit")
				r.closeVenuePosition(ctx, s.Instrument, "opposite_signal")
				continue
			}
		}
		if s.Confidence <= 0 || math.Abs(s.Direction) < 1e-8 {
			continue
		}
		side := types.SideBuy
		if s.Direction < 0 {
			side = types.SideSell
		}

		needOANDAExits := r.cfg.Risk.RequireBrokerStops && strings.EqualFold(string(s.Instrument.Venue), "OANDA")
		if needOANDAExits && (s.StopLossPrice <= 0 || s.TakeProfitPrice <= 0) {
			r.log.Debug().Str("strat", s.StrategyID).Str("symbol", s.Instrument.Symbol).
				Msg("skip entry: broker stops required but signal has no stop/target")
			continue
		}

		refPx := book.AskPrice
		if side == types.SideSell {
			refPx = book.BidPrice
		}
		if refPx <= 0 {
			refPx = mid
		}

		var maxQ float64
		if s.StopLossPrice > 0 {
			riskPerUnit := math.Abs(refPx - s.StopLossPrice)
			if riskPerUnit < 1e-12 {
				continue
			}
			maxQ = r.risk.MaxPositionQtyFromStop(refPx, s.StopLossPrice, eq) * math.Abs(s.Direction) * s.Confidence * scale
		} else {
			maxQ = r.risk.MaxPositionQtyFromRisk(mid, eq) * math.Abs(s.Direction) * s.Confidence * scale
		}
		if r.cfg.Risk.MaxNotionalPerTradePct > 0 && refPx > 0 && eq > 0 {
			capQ := eq * (r.cfg.Risk.MaxNotionalPerTradePct / 100) / refPx
			if capQ > 0 {
				maxQ = math.Min(maxQ, capQ)
			}
		}
		if maxQ <= 0 {
			continue
		}
		intentID := fmt.Sprintf("%s-%s-%s-%d", s.StrategyID, string(s.Instrument.Venue), s.Instrument.Symbol, now.UnixNano())
		intent := types.OrderIntent{
			ID:              intentID,
			StrategyID:      s.StrategyID,
			Instrument:      s.Instrument,
			Side:            side,
			Type:            types.OrderMarket,
			Quantity:        maxQ,
			StopLossPrice:   s.StopLossPrice,
			TakeProfitPrice: s.TakeProfitPrice,
			ClientTag:       s.Reason,
			MaxSlippageBps:  r.cfg.Risk.MaxSlippageBpsDefault,
			CreatedAt:       now,
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

		r.placeOrder(ctx, intent, side, now, tradeLogCtx{RefPrice: refPx, EquityUSD: eq})
	}
	return mid
}

func (r *Runner) placeOrder(ctx context.Context, intent types.OrderIntent, side types.Side, now time.Time, tlc tradeLogCtx) {
	r.perfMu.Lock()
	r.perfOrd++
	r.perfMu.Unlock()

	v := intent.Instrument.Venue
	if v == "" {
		v = "PAPER"
	}
	tverbose := r.cfg.Runtime.TradeLogVerbose
	eqForPct := tlc.EquityUSD
	if eqForPct <= 0 {
		eqForPct = r.pf.Equity()
	}
	if v == "PAPER" {
		if r.paper == nil {
			return
		}
		paperVenue := string(r.paper.Name())
		subEv := r.log.Info().Str("run_mode", string(r.cfg.Mode())).Str("venue", paperVenue).Str("symbol", intent.Instrument.Symbol).
			Str("side", string(side)).Str("action", tradeActionLabel(side)).Str("ord_type", string(intent.Type)).
			Float64("qty", intent.Quantity).Str("strategy_id", intent.StrategyID)
		if intent.ReduceOnly {
			subEv = subEv.Bool("reduce_only", true)
		}
		subEv = zerologSubmitNotional(subEv, intent.Quantity, tlc.RefPrice, tlc.EquityUSD)
		if tverbose {
			subEv = subEv.Str("intent_id", intent.ID)
		}
		subEv.Msg("order_submit")
		ord, err := r.paper.PlaceOrder(ctx, intent)
		if err != nil {
			metrics.OrdersTotal.WithLabelValues(string(r.paper.Name()), "error").Inc()
			r.log.Warn().Err(err).Str("venue", paperVenue).Str("symbol", intent.Instrument.Symbol).
				Str("side", string(side)).Str("action", tradeActionLabel(side)).Float64("qty", intent.Quantity).Msg("order_place")
			return
		}
		metrics.OrdersTotal.WithLabelValues(string(r.paper.Name()), "ok").Inc()
		fillEv := r.log.Info().Str("venue", paperVenue).Str("symbol", intent.Instrument.Symbol).
			Str("side", string(side)).Str("action", tradeActionLabel(side)).
			Float64("avg_px", ord.AvgPrice).Float64("filled_qty", ord.FilledQty).Float64("fee", ord.FeesPaid).
			Str("strategy_id", intent.StrategyID)
		if intent.ReduceOnly {
			fillEv = fillEv.Bool("reduce_only", true)
		}
		fillEv = zerologFilledNotional(fillEv, ord.FilledQty, ord.AvgPrice, eqForPct)
		if tverbose {
			fillEv = fillEv.Str("local_id", ord.ID).Str("exchange_id", ord.ExchangeID).Str("intent_id", intent.ID)
		}
		fillEv.Msg("order_filled")
		if r.store != nil && ord.FilledQty > 0 {
			_ = r.store.LogFill(ctx, types.Fill{
				OrderID: ord.ID, Instrument: intent.Instrument, Side: side, Price: ord.AvgPrice, Qty: ord.FilledQty, Fee: ord.FeesPaid, Time: now,
			})
		}
		r.noteFillForHoldTracking(intent, ord.State, ord.FilledQty, now)
		return
	}
	if r.cfg.Mode() != types.ModeLive {
		skipEv := r.log.Info().Str("run_mode", string(r.cfg.Mode())).Str("venue", string(v)).Str("symbol", intent.Instrument.Symbol).
			Str("side", string(side)).Str("action", tradeActionLabel(side)).Str("ord_type", string(intent.Type)).
			Float64("qty", intent.Quantity).Str("strategy_id", intent.StrategyID)
		if intent.ReduceOnly {
			skipEv = skipEv.Bool("reduce_only", true)
		}
		skipEv = zerologSubmitNotional(skipEv, intent.Quantity, tlc.RefPrice, tlc.EquityUSD)
		if tverbose {
			skipEv = skipEv.Str("intent_id", intent.ID)
		}
		skipEv.Msg("paper_mode: order not sent to broker (set runtime.mode: live to execute on venue)")
		r.perfMu.Lock()
		r.perfSup++
		r.perfMu.Unlock()
		return
	}
	chain := r.executionVenueChain(v)
	subLive := r.log.Info().Str("run_mode", string(r.cfg.Mode())).Str("venue", string(v)).Str("symbol", intent.Instrument.Symbol).
		Str("side", string(side)).Str("action", tradeActionLabel(side)).Str("ord_type", string(intent.Type)).
		Float64("qty", intent.Quantity).Str("strategy_id", intent.StrategyID)
	if intent.ReduceOnly {
		subLive = subLive.Bool("reduce_only", true)
	}
	subLive = zerologSubmitNotional(subLive, intent.Quantity, tlc.RefPrice, tlc.EquityUSD)
	if tverbose {
		ch := make([]string, len(chain))
		for i, vv := range chain {
			ch[i] = string(vv)
		}
		subLive = subLive.Strs("venue_chain", ch).Str("intent_id", intent.ID)
	}
	subLive.Msg("order_submit")
	rr, ok := r.router.PlaceFallback(ctx, chain, intent)
	if !ok {
		metrics.OrdersTotal.WithLabelValues(string(rr.Venue), "error").Inc()
		if rr.Err != nil {
			r.log.Warn().Err(rr.Err).Str("last_venue", string(rr.Venue)).Str("symbol", intent.Instrument.Symbol).
				Str("side", string(side)).Str("action", tradeActionLabel(side)).Float64("qty", intent.Quantity).Msg("order fallback exhausted")
		}
		return
	}
	metrics.OrdersTotal.WithLabelValues(string(rr.Venue), "ok").Inc()
	fillLive := r.log.Info().Str("venue", string(rr.Venue)).Str("symbol", intent.Instrument.Symbol).
		Str("side", string(side)).Str("action", tradeActionLabel(side)).
		Float64("avg_px", rr.Order.AvgPrice).Float64("filled_qty", rr.Order.FilledQty).Float64("fee", rr.Order.FeesPaid).
		Str("state", string(rr.Order.State)).Str("strategy_id", intent.StrategyID)
	if intent.ReduceOnly {
		fillLive = fillLive.Bool("reduce_only", true)
	}
	fillLive = zerologFilledNotional(fillLive, rr.Order.FilledQty, rr.Order.AvgPrice, eqForPct)
	if tverbose {
		fillLive = fillLive.Str("local_id", rr.Order.ID).Str("exchange_id", rr.Order.ExchangeID).Str("trade_id", rr.Order.ExchangeTradeID).
			Str("intent_id", intent.ID).Dur("rtt", rr.RTT)
	}
	fillLive.Msg("order_filled")
	if (rr.Order.State == types.OrderFilled || rr.Order.State == types.OrderPartial) && rr.Order.ExchangeTradeID != "" &&
		(intent.StopLossPrice > 0 || intent.TakeProfitPrice > 0) {
		if c, ok := r.reg.Get(string(rr.Venue)); ok {
			if tu, ok2 := c.(exchange.TradeExitUpdater); ok2 {
				if err := tu.UpdateTradeExits(ctx, rr.Order.ExchangeTradeID, intent.Instrument, intent.StopLossPrice, intent.TakeProfitPrice); err != nil {
					r.log.Warn().Err(err).Str("trade_id", rr.Order.ExchangeTradeID).Str("venue", string(rr.Venue)).
						Msg("post-trade exit sync")
				}
			}
		}
	}
	if r.store != nil && rr.Order.FilledQty > 0 {
		_ = r.store.LogFill(ctx, types.Fill{
			OrderID: rr.Order.ID, Instrument: intent.Instrument, Side: side, Price: rr.Order.AvgPrice, Qty: rr.Order.FilledQty, Fee: rr.Order.FeesPaid, Time: now,
		})
	}
	r.noteFillForHoldTracking(intent, rr.Order.State, rr.Order.FilledQty, now)
}

func (r *Runner) executionVenueChain(primary types.Venue) []types.Venue {
	seen := make(map[types.Venue]struct{})
	var out []types.Venue
	add := func(raw string) {
		s := strings.ToUpper(strings.TrimSpace(raw))
		if s == "" {
			s = "PAPER"
		}
		v := types.Venue(s)
		if _, ok := seen[v]; ok {
			return
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	if primary == "" {
		add("PAPER")
	} else {
		add(string(primary))
	}
	for _, fb := range r.cfg.Execution.RouterFallbackVenues {
		add(fb)
	}
	return out
}

func driftPositionKey(venue types.Venue, ins types.Instrument) string {
	v := string(ins.Venue)
	if v == "" {
		v = string(venue)
	}
	return v + ":" + ins.Symbol
}

// logPositionDriftVsLedger logs when open positions at the broker disagree with cumulative net qty from SQLite fills (same venue+symbol).
func (r *Runner) logPositionDriftVsLedger(ctx context.Context, venue types.Venue, brokerPos []types.Position) {
	ledger, err := r.store.NetOpenQtyByVenueSymbol(ctx)
	if err != nil {
		r.log.Debug().Err(err).Str("venue", string(venue)).Msg("reconcile drift: ledger query")
		return
	}
	prefix := string(venue) + ":"
	minU := r.cfg.Runtime.ReconcileDriftMinUnits
	effThr := minU
	if effThr <= 0 {
		effThr = 1e-9
	}

	brokerByKey := make(map[string]float64, len(brokerPos))
	for i := range brokerPos {
		p := &brokerPos[i]
		k := driftPositionKey(venue, p.Instrument)
		brokerByKey[k] = p.Qty
	}

	keys := make(map[string]struct{})
	for k := range brokerByKey {
		if strings.HasPrefix(k, prefix) {
			keys[k] = struct{}{}
		}
	}
	for k := range ledger {
		if strings.HasPrefix(k, prefix) {
			keys[k] = struct{}{}
		}
	}

	for k := range keys {
		bq := brokerByKey[k]
		lq := ledger[k]
		d := math.Abs(bq - lq)
		if d < effThr {
			continue
		}
		sym := k
		if strings.HasPrefix(k, prefix) {
			sym = strings.TrimPrefix(k, prefix)
		}
		r.log.Warn().Str("venue", string(venue)).Str("symbol", sym).Str("position_key", k).
			Float64("broker_qty", bq).Float64("ledger_qty", lq).Float64("drift_qty", bq-lq).
			Msg("position drift: broker open qty vs SQLite fill ledger")
		metrics.ReconcilePositionDriftTotal.WithLabelValues(string(venue)).Inc()
	}
}

func (r *Runner) reconcilePortfolio(ctx context.Context, instruments []types.Instrument, mids map[string]float64) {
	venues := uniqueVenues(instruments)
	eq, _, _, _ := r.pf.Snapshot()
	total := 0.0
	for _, v := range venues {
		c, err := r.connectorForVenue(v)
		if err != nil {
			r.log.Debug().Err(err).Str("venue", string(v)).Msg("reconcile skip venue")
			continue
		}
		pos, err := c.FetchPositions(ctx)
		fetchOK := err == nil
		if err != nil {
			r.log.Warn().Err(err).Str("venue", string(v)).Msg("positions")
			pos = nil
		}
		if v == types.Venue("PAPER") && r.paper != nil {
			for i := range pos {
				p := &pos[i]
				if mid, ok := mids[midKey(p.Instrument)]; ok && math.Abs(p.Qty) >= 1e-10 && p.AvgEntry > 0 {
					p.Unrealized = (mid - p.AvgEntry) * p.Qty
				}
			}
		}
		if fetchOK && r.cfg.Runtime.LogReconcilePositionDrift && r.store != nil {
			r.logPositionDriftVsLedger(ctx, v, pos)
		}
		r.pf.ReplaceVenuePositions(v, pos)
		bals, err := c.FetchBalances(ctx)
		if err != nil {
			r.log.Warn().Err(err).Str("venue", string(v)).Msg("balances")
			bals = nil
		}
		sub := 0.0
		for _, bal := range bals {
			if bal.Asset == "USD" || bal.Asset == "USDT" {
				sub += bal.Free + bal.Locked
			}
		}
		for _, p := range pos {
			sub += p.Unrealized
		}
		if sub > 0 {
			total += sub
		}
	}
	if total <= 0 {
		total = eq
	}
	r.pf.SetEquity(total)
	r.risk.PushReturn(r.prevEquity, total)
	r.prevEquity = total
	metrics.EquityUSD.Set(total)
	metrics.DailyDrawdownPct.Set(r.pf.DailyDrawdownPct())
	r.pushEqHistory(total)
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
