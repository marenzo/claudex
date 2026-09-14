package quota

import (
	"context"
	"sync"
	"time"
)

const RecoveryInterval = time.Minute

// State belongs to one Codex account. A probe result is applied only to the
// generation it checked, so an in-flight success cannot erase a newer 429.
type State struct {
	mu         sync.Mutex
	account    string
	generation uint64
	until      time.Time
}

func (s *State) Remaining(account string, now time.Time) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.account != account || !s.until.After(now) {
		return 0
	}
	return s.until.Sub(now)
}

func (s *State) Limited(account string, until time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.account != account || until.After(s.until) {
		s.until = until
	}
	s.account = account
	s.generation++
}

func (s *State) snapshot(now time.Time) (string, uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.account, s.generation, s.until.After(now)
}

func (s *State) recover(account string, generation uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.account != account || s.generation != generation || s.until.IsZero() {
		return false
	}
	s.until = time.Time{}
	s.generation++
	return true
}

// Run checks only a cooling account. It never generates a model response.
func (s *State) Run(ctx context.Context, check func(context.Context, string) (bool, error), recovered func()) {
	ticker := time.NewTicker(RecoveryInterval)
	defer ticker.Stop()
	s.watch(ctx, ticker.C, check, recovered)
}

func (s *State) watch(ctx context.Context, ticks <-chan time.Time, check func(context.Context, string) (bool, error), recovered func()) {
	for {
		select {
		case <-ctx.Done():
			return
		case now, ok := <-ticks:
			if !ok {
				return
			}
			account, generation, cooling := s.snapshot(now)
			if !cooling {
				continue
			}
			allowed, errCheck := check(ctx, account)
			if errCheck == nil && allowed && ctx.Err() == nil && s.recover(account, generation) && recovered != nil {
				recovered()
			}
		}
	}
}
