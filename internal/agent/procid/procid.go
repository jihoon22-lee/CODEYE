// Package procid reads OS process identity (pid + start time + process
// group) so the agent never signals a PID whose identity it has not verified.
package procid

import "errors"

// ErrUnsupported: identity cannot be read on this platform. Callers must
// treat cleanup as uncertain.
var ErrUnsupported = errors.New("process_identity_unsupported")

// Identity of a live process. StartTime uses the platform process marker (Linux: /proc
// stat starttime clock ticks).
type Identity struct {
	PID       int
	StartTime uint64
	PGID      int
	Zombie    bool
}

// Status of a recorded (pid, start) pair.
type Status int

const (
	// Gone: no process with that pid exists (or only a zombie with that start).
	Gone Status = iota
	// Same: the pid is alive and its start time matches.
	Same
	// Reused: the pid is alive with a different start time: never signal it.
	Reused
)

// Check compares the live process pid with the recorded start time.
func Check(pid int, start uint64) (Status, error) {
	id, err := Read(pid)
	if errors.Is(err, errGone) {
		return Gone, nil
	}
	if err != nil {
		return Gone, err
	}
	if id.StartTime != start {
		return Reused, nil
	}
	if id.Zombie {
		return Gone, nil
	}
	return Same, nil
}

var errGone = errors.New("process_gone")
