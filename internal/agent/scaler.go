package agent

import (
	"sync"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// Scaler decides which workers the agent starts. W06 ships StaticScaler
// (fixed count, restart on exit). W07 adds a controller-driven scaler fed by
// start_worker/drain_worker/stop_worker commands; drain/stop are already
// applied by the agent itself.
type Scaler interface {
	// Plan returns worker IDs to start now given the live, non-draining count.
	Plan(alive int) []string
	// Requested records a controller start_worker command.
	Requested(workerID string)
}

// StaticScaler keeps N workers alive. It also honours controller start
// requests (W07 preview) without exceeding N + requests.
type StaticScaler struct {
	N         int
	mu        sync.Mutex
	requested []string
}

func (s *StaticScaler) Plan(alive int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requested) > 0 {
		out := s.requested
		s.requested = nil
		return out
	}
	var out []string
	for i := alive; i < s.N; i++ {
		out = append(out, domain.NewID())
	}
	return out
}

func (s *StaticScaler) Requested(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requested = append(s.requested, id)
}
