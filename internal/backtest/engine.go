package backtest

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"math/rand"
	"os"
	"strconv"

	"tradingbot/internal/execution"
	"tradingbot/internal/strategy"
	"tradingbot/pkg/types"
)

// BarSeries is close-only series for CSV backtests.
type BarSeries struct {
	Instrument types.Instrument
	Timestamps []int64
	Opens      []float64
	Highs      []float64
	Lows       []float64
	Closes     []float64
	Volumes    []float64
}

// LoadCSV expects columns: ts,open,high,low,close,volume
func LoadCSV(path string, ins types.Instrument) (*BarSeries, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.ReuseRecord = true
	bs := &BarSeries{Instrument: ins}
	first := true
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if first {
			first = false
			if len(rec) > 0 && rec[0] == "ts" {
				continue
			}
		}
		if len(rec) < 6 {
			return nil, fmt.Errorf("bad row: %v", rec)
		}
		ts, _ := strconv.ParseInt(rec[0], 10, 64)
		o, _ := strconv.ParseFloat(rec[1], 64)
		h, _ := strconv.ParseFloat(rec[2], 64)
		l, _ := strconv.ParseFloat(rec[3], 64)
		c, _ := strconv.ParseFloat(rec[4], 64)
		v, _ := strconv.ParseFloat(rec[5], 64)
		bs.Timestamps = append(bs.Timestamps, ts)
		bs.Opens = append(bs.Opens, o)
		bs.Highs = append(bs.Highs, h)
		bs.Lows = append(bs.Lows, l)
		bs.Closes = append(bs.Closes, c)
		bs.Volumes = append(bs.Volumes, v)
	}
	return bs, nil
}

// Result summarizes a run.
type Result struct {
	InitialEquity float64
	EquityFinal   float64
	ReturnPct     float64
	MaxDDPct      float64
	Trades        int
	Wins          int
	Losses        int
	WinRatePct    float64
	ProfitFactor  float64
	StartTS       int64
	EndTS         int64
}

func buildResult(initial, equity, maxDD float64, trades, wins, losses int, grossWin, grossLoss float64, startTS, endTS int64) Result {
	r := Result{
		InitialEquity: initial,
		EquityFinal:   equity,
		ReturnPct:     0,
		MaxDDPct:      maxDD,
		Trades:        trades,
		Wins:          wins,
		Losses:        losses,
		StartTS:       startTS,
		EndTS:         endTS,
	}
	if initial > 0 {
		r.ReturnPct = (equity - initial) / initial * 100
	}
	decided := wins + losses
	if decided > 0 {
		r.WinRatePct = float64(wins) / float64(decided) * 100
	}
	if grossLoss > 1e-12 {
		r.ProfitFactor = grossWin / grossLoss
	} else if grossWin > 1e-12 {
		r.ProfitFactor = 999.99 // no material losing notionals in this sim
	}
	return r
}

// RunSimple walks bars, applies stack signals, simulates fills on close.
// Portfolio: cash in quote (e.g. USD for EUR_USD), position in base units (long + / short −).
// Mark-to-market each bar: equity = cash + pos*close. Fills use the same close via FillSim.
// riskPerTradePct is percent of current marked equity as notional for the trade delta (risk.max_risk_per_trade_pct). If <= 0, defaults to 0.5.
func RunSimple(ctx context.Context, bs *BarSeries, st *strategy.Stack, feeBps, slipBps float64, initial float64, riskPerTradePct float64) Result {
	if riskPerTradePct <= 0 {
		riskPerTradePct = 0.5
	}
	riskFrac := riskPerTradePct / 100.0

	var startTS, endTS int64
	if len(bs.Timestamps) > 0 {
		startTS = bs.Timestamps[0]
		endTS = bs.Timestamps[len(bs.Timestamps)-1]
	}

	sim := execution.FillSim{FeeBps: feeBps, SlippageBps: slipBps}
	rng := rand.New(rand.NewSource(42))
	cash := initial
	pos := 0.0
	mark := func(close float64) float64 { return cash + pos*close }

	trades := 0
	wins := 0
	losses := 0
	grossWin := 0.0
	grossLoss := 0.0

	equity := initial
	peak := equity
	maxDD := 0.0
	if len(bs.Closes) == 0 {
		return buildResult(initial, equity, maxDD, trades, wins, losses, grossWin, grossLoss, startTS, endTS)
	}

	for i := range bs.Closes {
		select {
		case <-ctx.Done():
			eq := mark(bs.Closes[i])
			return buildResult(initial, eq, maxDD, trades, wins, losses, grossWin, grossLoss, startTS, endTS)
		default:
		}
		close := bs.Closes[i]
		open := bs.Opens[i]
		openMark := cash + pos*open
		tradedThisBar := false

		b := strategy.Bar{
			Instrument: bs.Instrument,
			Timestamp:  bs.Timestamps[i],
			Open:       open,
			High:       bs.Highs[i],
			Low:        bs.Lows[i],
			Close:      close,
			Volume:     bs.Volumes[i],
		}
		sigs, err := st.RunBar(ctx, b)
		if err != nil {
			break
		}
		for _, s := range sigs {
			if s.Confidence <= 0 || s.Direction == 0 {
				continue
			}
			eqMark := mark(close)
			if eqMark <= 0 || close <= 0 {
				continue
			}
			// Base-unit delta: notional in quote / price, scaled by signal direction and confidence.
			dq := (eqMark * riskFrac) / close * s.Direction * s.Confidence
			if dq > -1e-12 && dq < 1e-12 {
				continue
			}
			side := types.SideBuy
			q := dq
			if q < 0 {
				side = types.SideSell
				q = -q
			}
			px, fee := sim.Apply(close, side, q, rng)
			if side == types.SideBuy {
				cash -= q*px + fee
				pos += q
			} else {
				cash += q*px - fee
				pos -= q
			}
			trades++
			tradedThisBar = true
		}
		equity = mark(close)
		if tradedThisBar {
			barDelta := equity - openMark
			switch {
			case barDelta > 1e-9:
				wins++
				grossWin += barDelta
			case barDelta < -1e-9:
				losses++
				grossLoss += -barDelta
			}
		}
		if equity > peak {
			peak = equity
		}
		if peak > 0 {
			dd := (peak - equity) / peak * 100
			if dd > maxDD {
				maxDD = dd
			}
		}
	}
	return buildResult(initial, equity, maxDD, trades, wins, losses, grossWin, grossLoss, startTS, endTS)
}
