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
)

// Handler returns HTTP handler for /metrics.
func Handler() http.Handler {
	return promhttp.Handler()
}
