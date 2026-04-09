package strategy

import (
	"context"
	"sync"

	"tradingbot/pkg/types"
)

// Stack runs many strategies and merges signals (upgrade: full portfolio optimizer).
type Stack struct {
	strats []Strategy
	mu     sync.RWMutex
}

func NewStack(strats []Strategy) *Stack {
	return &Stack{strats: strats}
}

func (s *Stack) RunBar(ctx context.Context, b Bar) ([]types.Signal, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var all []types.Signal
	for _, st := range s.strats {
		sig, err := st.OnBar(ctx, b)
		if err != nil {
			return nil, err
		}
		all = append(all, sig...)
	}
	return all, nil
}
