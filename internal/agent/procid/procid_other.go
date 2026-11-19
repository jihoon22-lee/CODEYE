//go:build !linux && !windows

package procid

import "time"

// Read is unsupported outside Linux (the Windows identity is the Job
// Object; agent reconciliation reports uncertain).
func Read(pid int) (Identity, error) { return Identity{}, ErrUnsupported }

// GroupMembers is unsupported outside Linux.
func GroupMembers(pgid int) ([]Identity, error) { return nil, ErrUnsupported }

// TerminateVerifiedGroup is unsupported outside Linux: never signals.
func TerminateVerifiedGroup(pgid int, leaderStart uint64, grace, wait time.Duration) (bool, error) {
	return false, ErrUnsupported
}
