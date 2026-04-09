package execution

import (
	"context"
	"sync"
	"time"

	"golang.org/x/time/rate"
	"tradingbot/internal/exchange"
	"tradingbot/pkg/types"
)

// LatencyProbe measures round-trip health per venue.
type LatencyProbe struct {
	WarnMs  float64
	HardMs  float64
}

// Router fans out to venues with concurrency caps and rate limits.
type Router struct {
	reg     *exchange.Registry
	maxPar  int
	probe   LatencyProbe
	lim     *rate.Limiter
}

func NewRouter(reg *exchange.Registry, maxParallel int, warnMs, hardMs float64, rps float64) *Router {
	if maxParallel <= 0 {
		maxParallel = 4
	}
	if rps <= 0 {
		rps = 20
	}
	return &Router{
		reg:    reg,
		maxPar: maxParallel,
		probe:  LatencyProbe{WarnMs: warnMs, HardMs: hardMs},
		lim:    rate.NewLimiter(rate.Limit(rps), int(rps)),
	}
}

// RouteResult carries per-venue outcome.
type RouteResult struct {
	Venue types.Venue
	Order types.Order
	Err   error
	RTT   time.Duration
}

// PlaceParallel submits the same intent to selected venues (first fill wins pattern: customize per strategy).
func (r *Router) PlaceParallel(ctx context.Context, venues []types.Venue, intent types.OrderIntent) []RouteResult {
	sem := make(chan struct{}, r.maxPar)
	var wg sync.WaitGroup
	mu := sync.Mutex{}
	out := make([]RouteResult, 0)

	for _, v := range venues {
		v := v
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if err := r.lim.Wait(ctx); err != nil {
				mu.Lock()
				out = append(out, RouteResult{Venue: v, Err: err})
				mu.Unlock()
				return
			}
			c, ok := r.reg.Get(string(v))
			if !ok {
				mu.Lock()
				out = append(out, RouteResult{Venue: v, Err: exchange.ErrNotImplemented})
				mu.Unlock()
				return
			}
			t0 := time.Now()
			ord, err := c.PlaceOrder(ctx, intent)
			rt := time.Since(t0)
			if r.probe.HardMs > 0 && rt.Seconds()*1000 > r.probe.HardMs {
				_ = c.CancelOrder(ctx, ord.ExchangeID, intent.Instrument)
			}
			mu.Lock()
			out = append(out, RouteResult{Venue: v, Order: ord, Err: err, RTT: rt})
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// CancelAllVenues emergency flatten hook.
func (r *Router) CancelAllVenues(ctx context.Context) []error {
	var errs []error
	for _, c := range r.reg.All() {
		if err := c.CancelAll(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}
