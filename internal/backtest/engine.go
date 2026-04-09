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
	EquityFinal float64
	MaxDDPct    float64
	Trades      int
}

// RunSimple walks bars, applies stack signals, simulates fills on close.
func RunSimple(ctx context.Context, bs *BarSeries, st *strategy.Stack, feeBps, slipBps float64, initial float64) Result {
	sim := execution.FillSim{FeeBps: feeBps, SlippageBps: slipBps}
	rng := rand.New(rand.NewSource(42))
	equity := initial
	peak := equity
	maxDD := 0.0
	trades := 0
	prevEq := equity
	for i := range bs.Closes {
		select {
		case <-ctx.Done():
			return Result{EquityFinal: equity, MaxDDPct: maxDD, Trades: trades}
		default:
		}
		b := strategy.Bar{
			Instrument: bs.Instrument,
			Timestamp:  bs.Timestamps[i],
			Open:       bs.Opens[i],
			High:       bs.Highs[i],
			Low:        bs.Lows[i],
			Close:      bs.Closes[i],
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
			qty := (equity * 0.005) / b.Close * s.Direction // 0.5% notional directional stub
			if qty == 0 {
				continue
			}
			side := types.SideBuy
			if qty < 0 {
				side = types.SideSell
				qty = -qty
			}
			px, fee := sim.Apply(b.Close, side, qty, rng)
			if side == types.SideBuy {
				equity -= qty * px
				equity -= fee
			} else {
				equity += qty * px
				equity -= fee
			}
			trades++
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
		_ = prevEq
		prevEq = equity
	}
	return Result{EquityFinal: equity, MaxDDPct: maxDD, Trades: trades}
}
