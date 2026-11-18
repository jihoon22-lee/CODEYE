package agent

import (
	"sync"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// Scaler decides which workers the agent starts. StaticScaler (W06 default,
// `--workers N`) keeps N workers and restarts exited ones. ControllerScaler
// (W07, `--autoscale`) starts workers only for controller start_worker
// commands and never restarts on its own; drain/stop are applied by the agent.
type Scaler interface {
	// Plan returns worker IDs to start now. alive counts live non-draining
	// workers, total every supervised worker process (draining included).
	Plan(alive, total int) []string
	// Requested records a controller start_worker command.
	Requested(workerID string)
}

// StaticScaler keeps N workers alive. It also honours controller start
// requests without exceeding N + requests (the controller never sends them to
// static nodes; W07 commands only nodes that declared controller scaling).
type StaticScaler struct {
	N         int
	mu        sync.Mutex
	requested []string
}

func (s *StaticScaler) Plan(alive, _ int) []string {
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

// ControllerScaler starts exactly the workers the controller asked for, at
// most once per worker ID, never more than Cap supervised processes. A start
// beyond Cap is refused (the controller stops counting it after its start
// timeout); the agent reports Cap as max_workers so a correct controller
// never asks for more.
type ControllerScaler struct {
	Cap int

	mu        sync.Mutex
	requested []string
	seen      map[string]bool
	refused   []string
}

func (s *ControllerScaler) Plan(_, total int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	room := s.Cap - total
	var out []string
	for _, id := range s.requested {
		if room > 0 {
			out = append(out, id)
			room--
		} else {
			s.refused = append(s.refused, id)
		}
	}
	s.requested = nil
	return out
}

func (s *ControllerScaler) Requested(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	if s.seen[id] {
		return
	}
	s.seen[id] = true
	s.requested = append(s.requested, id)
}

// Refused returns and clears the start requests dropped for the cap.
func (s *ControllerScaler) Refused() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.refused
	s.refused = nil
	return out
}
