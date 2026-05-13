package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	EquityUSD = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "trading_equity_usd",
		Help: "Mark-to-market equity USD",
	})
	DailyDrawdownPct = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "trading_daily_drawdown_pct",
		Help: "Intraday drawdown from day open (negative is loss)",
	})
	OrdersTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "trading_orders_total",
		Help: "Orders by venue and result",
	}, []string{"venue", "result"})
	RiskRejects = promauto.NewCounter(prometheus.CounterOpts{
		Name: "trading_risk_rejects_total",
		Help: "Orders rejected by risk engine",
	})
	// ExecutionPlaceDuration is round-trip time for a single PlaceOrder per venue (seconds).
	ExecutionPlaceDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "trading_execution_place_duration_seconds",
		Help:    "Exchange PlaceOrder round-trip time in seconds",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	}, []string{"venue"})
	// ExecutionPlaceSlowTotal counts places exceeding latency_warn_ms (if configured).
	ExecutionPlaceSlowTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "trading_execution_place_slow_total",
		Help: "PlaceOrder calls slower than latency_warn_ms",
	}, []string{"venue"})
)

// Handler returns HTTP handler for /metrics.
func Handler() http.Handler {
	return promhttp.Handler()
}
