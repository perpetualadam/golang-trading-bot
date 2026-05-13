package portfolio

import (
	"strings"
	"sync"
	"time"

	"tradingbot/pkg/types"
)

// State is concurrent-safe aggregate portfolio view for risk and Telegram /status.
type State struct {
	mu sync.RWMutex

	EquityUSD       float64
	DayStartEquity  float64
	RealizedDayPnl  float64
	Positions       map[string]types.Position
	LastEquityAt    time.Time
}

func NewState(initial float64) *State {
	now := time.Now().UTC()
	return &State{
		EquityUSD:      initial,
		DayStartEquity: initial,
		Positions:      make(map[string]types.Position),
		LastEquityAt:   now,
	}
}

func posKey(ins types.Instrument) string {
	return string(ins.Venue) + ":" + ins.Symbol
}

// UpdatePositions replaces position snapshot (e.g. after exchange poll).
func (s *State) UpdatePositions(pos []types.Position) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Positions = make(map[string]types.Position, len(pos))
	for _, p := range pos {
		s.Positions[posKey(p.Instrument)] = p
	}
}

// ReplaceVenuePositions removes prior rows for venue v then applies pos (multi-venue loops).
func (s *State) ReplaceVenuePositions(v types.Venue, pos []types.Position) {
	prefix := string(v) + ":"
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Positions == nil {
		s.Positions = make(map[string]types.Position)
	}
	for k := range s.Positions {
		if strings.HasPrefix(k, prefix) {
			delete(s.Positions, k)
		}
	}
	for _, p := range pos {
		s.Positions[posKey(p.Instrument)] = p
	}
}

// SetEquity updates mark-to-market equity.
func (s *State) SetEquity(usd float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.EquityUSD = usd
	s.LastEquityAt = time.Now().UTC()
}

// RollDailyIfNeeded resets day baseline for /daily and drawdown circuit.
func (s *State) RollDailyIfNeeded(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.LastEquityAt.IsZero() {
		s.DayStartEquity = s.EquityUSD
		return
	}
	y, m, d := s.LastEquityAt.Date()
	ny, nm, nd := now.UTC().Date()
	if y != ny || m != nm || d != nd {
		s.DayStartEquity = s.EquityUSD
		s.RealizedDayPnl = 0
	}
}

// AddRealizedPnl adds to intraday realized PnL ledger.
func (s *State) AddRealizedPnl(delta float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.RealizedDayPnl += delta
}

// Snapshot returns a copy for read-only UI / risk.
// Equity returns current marked equity.
func (s *State) Equity() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.EquityUSD
}

func (s *State) Snapshot() (equity, dayStart float64, dayPnl float64, pos map[string]types.Position) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	equity = s.EquityUSD
	dayStart = s.DayStartEquity
	dayPnl = s.RealizedDayPnl
	pos = make(map[string]types.Position, len(s.Positions))
	for k, v := range s.Positions {
		pos[k] = v
	}
	return
}

// DailyDrawdownPct returns negative value if underwater from day start (percentage points).
func (s *State) DailyDrawdownPct() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.DayStartEquity <= 0 {
		return 0
	}
	return (s.EquityUSD/s.DayStartEquity - 1) * 100
}
