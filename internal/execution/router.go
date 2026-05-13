package execution

import (
	"context"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
	"tradingbot/internal/exchange"
	"tradingbot/internal/metrics"
	"tradingbot/pkg/types"
)

// RouterConfig tunes concurrency, latency policy, and per-venue rate limits.
type RouterConfig struct {
	MaxParallel  int
	WarnMs       float64
	HardCancelMs float64
	DefaultRPS   float64
	Burst        int
	VenueRPS     map[string]float64 // optional per-venue RPS; keys matched case-insensitively
}

// Router fans out to venues with concurrency caps and per-venue token buckets.
type Router struct {
	reg      *exchange.Registry
	maxPar   int
	cfg      RouterConfig
	limMu    sync.RWMutex
	venueLim map[string]*rate.Limiter
}

// NewRouter builds a router with sane defaults for unset fields.
func NewRouter(reg *exchange.Registry, cfg RouterConfig) *Router {
	if cfg.MaxParallel <= 0 {
		cfg.MaxParallel = 4
	}
	if cfg.DefaultRPS <= 0 {
		cfg.DefaultRPS = 25
	}
	norm := make(map[string]float64)
	for k, v := range cfg.VenueRPS {
		kk := strings.ToUpper(strings.TrimSpace(k))
		if kk != "" && v > 0 {
			norm[kk] = v
		}
	}
	cfg.VenueRPS = norm
	return &Router{
		reg:      reg,
		maxPar:   cfg.MaxParallel,
		cfg:      cfg,
		venueLim: make(map[string]*rate.Limiter),
	}
}

func (r *Router) limiterFor(venue string) *rate.Limiter {
	venue = strings.ToUpper(strings.TrimSpace(venue))
	if venue == "" {
		venue = "PAPER"
	}
	r.limMu.RLock()
	lm, ok := r.venueLim[venue]
	r.limMu.RUnlock()
	if ok {
		return lm
	}
	r.limMu.Lock()
	defer r.limMu.Unlock()
	if lm, ok = r.venueLim[venue]; ok {
		return lm
	}
	rps := r.cfg.DefaultRPS
	if v, ok := r.cfg.VenueRPS[venue]; ok && v > 0 {
		rps = v
	}
	burst := r.cfg.Burst
	if burst <= 0 {
		burst = int(rps)
		if burst < 1 {
			burst = 1
		}
	}
	lm = rate.NewLimiter(rate.Limit(rps), burst)
	r.venueLim[venue] = lm
	return lm
}

func dedupeVenues(vs []types.Venue) []types.Venue {
	seen := make(map[types.Venue]struct{})
	out := make([]types.Venue, 0, len(vs))
	for _, v := range vs {
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

// placeCtx applies an optional deadline derived from HardCancelMs (small buffer for wire latency).
func (r *Router) placeCtx(parent context.Context) (context.Context, context.CancelFunc) {
	if r.cfg.HardCancelMs <= 0 {
		return parent, func() {}
	}
	d := time.Duration(r.cfg.HardCancelMs)*time.Millisecond + 350*time.Millisecond
	return context.WithTimeout(parent, d)
}

// RouteResult carries per-venue outcome.
type RouteResult struct {
	Venue types.Venue
	Order types.Order
	Err   error
	RTT   time.Duration
}

// placeOne runs a single PlaceOrder with rate limit, metrics, and slow-RTT cancel when applicable.
func (r *Router) placeOne(ctx context.Context, v types.Venue, intent types.OrderIntent) RouteResult {
	if v == "" {
		v = "PAPER"
	}
	venueKey := string(v)
	if err := r.limiterFor(venueKey).Wait(ctx); err != nil {
		return RouteResult{Venue: v, Err: err}
	}
	c, ok := r.reg.Get(venueKey)
	if !ok {
		return RouteResult{Venue: v, Err: exchange.ErrNotImplemented}
	}
	t0 := time.Now()
	ord, err := c.PlaceOrder(ctx, intent)
	rt := time.Since(t0)
	metrics.ExecutionPlaceDuration.WithLabelValues(venueKey).Observe(rt.Seconds())
	if r.cfg.WarnMs > 0 && rt.Seconds()*1000 > r.cfg.WarnMs {
		metrics.ExecutionPlaceSlowTotal.WithLabelValues(venueKey).Inc()
	}
	if err == nil && ord.ExchangeID != "" && r.cfg.HardCancelMs > 0 && rt.Seconds()*1000 > r.cfg.HardCancelMs {
		_ = c.CancelOrder(ctx, ord.ExchangeID, intent.Instrument)
	}
	return RouteResult{Venue: v, Order: ord, Err: err, RTT: rt}
}

// Place routes to intent.Instrument.Venue (empty venue treated as PAPER).
func (r *Router) Place(ctx context.Context, intent types.OrderIntent) []RouteResult {
	v := intent.Instrument.Venue
	if v == "" {
		v = "PAPER"
	}
	return r.PlaceParallel(ctx, []types.Venue{v}, intent)
}

// PlaceParallel submits the same intent to each venue concurrently (deduped). Use PlaceFallback to avoid duplicate live orders across venues.
func (r *Router) PlaceParallel(ctx context.Context, venues []types.Venue, intent types.OrderIntent) []RouteResult {
	venues = dedupeVenues(venues)
	sem := make(chan struct{}, r.maxPar)
	var wg sync.WaitGroup
	mu := sync.Mutex{}
	out := make([]RouteResult, 0, len(venues))

	for _, v := range venues {
		v := v
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			pctx, cancel := r.placeCtx(ctx)
			defer cancel()
			res := r.placeOne(pctx, v, intent)
			mu.Lock()
			out = append(out, res)
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// PlaceFallback tries venues in order until the first successful PlaceOrder (no parallel duplicate submissions).
func (r *Router) PlaceFallback(ctx context.Context, venues []types.Venue, intent types.OrderIntent) (RouteResult, bool) {
	venues = dedupeVenues(venues)
	var last RouteResult
	for _, v := range venues {
		pctx, cancel := r.placeCtx(ctx)
		res := r.placeOne(pctx, v, intent)
		cancel()
		last = res
		if res.Err == nil {
			return res, true
		}
	}
	return last, false
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
