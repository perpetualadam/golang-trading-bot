package app

import (
	"context"
	"fmt"
	"math"
	"time"

	"tradingbot/internal/metrics"
	"tradingbot/pkg/types"
)

func signF(x float64) float64 {
	switch {
	case x > 0:
		return 1
	case x < 0:
		return -1
	default:
		return 0
	}
}

func positionOpposesSignal(posQty, signalDir float64) bool {
	if math.Abs(signalDir) < 1e-12 {
		return false
	}
	pq := signF(posQty)
	if math.Abs(posQty) < 1e-12 || pq == 0 {
		return false
	}
	return signF(signalDir) != pq
}

func (r *Runner) noteFillForHoldTracking(intent types.OrderIntent, st types.OrderState, filledQty float64, now time.Time) {
	if intent.ReduceOnly || filledQty <= 0 {
		return
	}
	if st != types.OrderFilled && st != types.OrderPartial {
		return
	}
	r.markPositionOpened(intent.Instrument, now)
}

func (r *Runner) markPositionOpened(ins types.Instrument, t time.Time) {
	r.posMu.Lock()
	defer r.posMu.Unlock()
	if r.posEnteredAt == nil {
		r.posEnteredAt = make(map[string]time.Time)
	}
	r.posEnteredAt[midKey(ins)] = t.UTC()
}

func (r *Runner) pruneStalePositionHolds(posMap map[string]types.Position) {
	r.posMu.Lock()
	defer r.posMu.Unlock()
	for k := range r.posEnteredAt {
		p, ok := posMap[k]
		if !ok || math.Abs(p.Qty) < 1e-9 {
			delete(r.posEnteredAt, k)
		}
	}
}

// enforceMaxHoldExits closes positions held longer than risk.max_position_hold (positions opened by this runner since last flat).
func (r *Runner) enforceMaxHoldExits(ctx context.Context, instruments []types.Instrument) {
	if r.halted || r.cfg.Risk.MaxPositionHold <= 0 {
		return
	}
	_, _, _, posMap := r.pf.Snapshot()
	r.pruneStalePositionHolds(posMap)

	now := time.Now().UTC()
	for _, ins := range instruments {
		k := midKey(ins)
		p, ok := posMap[k]
		if !ok || math.Abs(p.Qty) < 1e-9 {
			continue
		}
		r.posMu.Lock()
		t0, tracked := r.posEnteredAt[k]
		r.posMu.Unlock()
		if !tracked {
			continue
		}
		if now.Sub(t0) < r.cfg.Risk.MaxPositionHold {
			continue
		}
		r.log.Info().Str("symbol", ins.Symbol).Str("venue", string(ins.Venue)).Dur("held", now.Sub(t0)).
			Str("exit", "max_hold").Msg("time-based exit")
		r.closeVenuePosition(ctx, ins, "max_hold")
	}
}

// closeVenuePosition sends a reduce-only market order to flatten the portfolio snapshot position for this instrument.
func (r *Runner) closeVenuePosition(ctx context.Context, ins types.Instrument, reason string) {
	if r.halted {
		return
	}
	_, _, _, posMap := r.pf.Snapshot()
	p, ok := posMap[midKey(ins)]
	if !ok || math.Abs(p.Qty) < 1e-9 {
		return
	}
	conn, err := r.connectorFor(ins)
	if err != nil {
		r.log.Warn().Err(err).Str("symbol", ins.Symbol).Msg("exit: connector")
		return
	}
	book, err := conn.FetchBookTop(ctx, ins)
	if err != nil {
		r.log.Warn().Err(err).Str("symbol", ins.Symbol).Msg("exit: book")
		return
	}
	now := time.Now().UTC()
	qty := math.Abs(p.Qty)
	side := types.SideSell
	if p.Qty < 0 {
		side = types.SideBuy
	}
	intent := types.OrderIntent{
		ID:             fmt.Sprintf("exit-%s-%d", midKey(ins), now.UnixNano()),
		StrategyID:     "exit_" + reason,
		Instrument:     ins,
		Side:           side,
		Type:           types.OrderMarket,
		Quantity:       qty,
		ReduceOnly:     true,
		MaxSlippageBps: r.cfg.Risk.MaxSlippageBpsDefault,
		CreatedAt:      now,
		ClientTag:      reason,
	}
	if err := r.risk.CheckKillSwitch(); err != nil {
		metrics.RiskRejects.Inc()
		return
	}
	if _, hard := r.risk.CheckDailyDrawdown(); hard {
		metrics.RiskRejects.Inc()
		return
	}
	eq := r.pf.Equity()
	if err := r.risk.PreTrade(intent, book, eq); err != nil {
		r.log.Warn().Err(err).Str("reason", reason).Str("symbol", ins.Symbol).Msg("exit: pretrade rejected")
		metrics.RiskRejects.Inc()
		return
	}
	refPx := book.AskPrice
	if side == types.SideSell {
		refPx = book.BidPrice
	}
	if refPx <= 0 {
		refPx = (book.BidPrice + book.AskPrice) / 2
	}
	r.log.Info().Str("reason", reason).Str("symbol", ins.Symbol).Str("venue", string(ins.Venue)).
		Float64("qty", qty).Str("side", string(side)).Str("action", tradeActionLabel(side)).
		Float64("notional_pct_equity", pctOfEquity(qty*refPx, eq)).
		Msg("manual exit order")
	r.placeOrder(ctx, intent, side, now, tradeLogCtx{RefPrice: refPx, EquityUSD: eq})
}
