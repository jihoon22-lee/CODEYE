//go:build linux

package procid

import (
	"errors"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Read returns the identity of pid. A missing process returns an error
// matching errGone.
func Read(pid int) (Identity, error) {
	if pid <= 0 {
		return Identity{}, errGone
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
			return Identity{}, errGone
		}
		return Identity{}, err
	}
	return parseStat(pid, string(b))
}

func parseStat(pid int, s string) (Identity, error) {
	i := strings.LastIndexByte(s, ')')
	if i < 0 || i+2 >= len(s) {
		return Identity{}, errors.New("malformed stat")
	}
	f := strings.Fields(s[i+2:])
	if len(f) < 20 || len(f[0]) == 0 {
		return Identity{}, errors.New("malformed stat")
	}
	id := Identity{PID: pid, Zombie: f[0][0] == 'Z' || f[0][0] == 'X' || f[0][0] == 'x'}
	var err error
	if id.PGID, err = strconv.Atoi(f[2]); err != nil {
		return id, err
	}
	if id.StartTime, err = strconv.ParseUint(f[19], 10, 64); err != nil {
		return id, err
	}
	return id, nil
}

// GroupMembers lists live (non-zombie) processes whose process group is pgid.
// A scan failure is returned as an error: the caller cannot claim emptiness.
func GroupMembers(pgid int) ([]Identity, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var out []Identity
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		id, err := Read(pid)
		if err != nil {
			if errors.Is(err, errGone) {
				continue
			}
			return nil, err
		}
		if id.PGID == pgid && !id.Zombie {
			out = append(out, id)
		}
	}
	return out, nil
}

// ErrPidfdUnsupported: the kernel cannot pin the leader or signal its group
// through a pidfd (PIDFD_SIGNAL_PROCESS_GROUP needs Linux 6.9). Callers
// report cleanup uncertain rather than falling back to kill(-pgid).
var ErrPidfdUnsupported = errors.New("pidfd_group_signal_unsupported")

// TerminateVerifiedGroup terminates group pgid whose leader identity (pgid,
// leaderStart) the caller recorded. The leader is pinned with pidfd_open
// BEFORE its identity is verified, and the group is signalled only through
// that pidfd (PIDFD_SIGNAL_PROCESS_GROUP), so a recycled PID/PGID can never
// be hit. It returns true when the group was observed empty.
//
//   - leader alive, same start, pgid == leader → TERM, grace, KILL, poll
//   - leader alive with other start or group   → never signalled, false
//   - leader gone                              → no signal: true only if no
//     process currently has that pgid
//   - pidfd or group signalling unsupported    → false, ErrPidfdUnsupported
func TerminateVerifiedGroup(pgid int, leaderStart uint64, grace, wait time.Duration) (bool, error) {
	if pgid <= 1 {
		return false, errors.New("invalid process group")
	}
	fd, err := unix.PidfdOpen(pgid, 0)
	if errors.Is(err, unix.ESRCH) {
		members, err := GroupMembers(pgid)
		return err == nil && len(members) == 0, err
	}
	if err != nil {
		return false, ErrPidfdUnsupported
	}
	defer unix.Close(fd)
	id, err := Read(pgid)
	if errors.Is(err, errGone) {
		members, err := GroupMembers(pgid)
		return err == nil && len(members) == 0, err
	}
	if err != nil {
		return false, err
	}
	if id.StartTime != leaderStart || id.PGID != pgid {
		return false, nil // reused PID or the leader left its group: never signal
	}
	if err := unix.PidfdSendSignal(fd, unix.SIGTERM, nil, unix.PIDFD_SIGNAL_PROCESS_GROUP); err != nil && !errors.Is(err, unix.ESRCH) {
		return false, ErrPidfdUnsupported
	}
	if empty, _ := waitEmpty(pgid, grace); empty {
		return true, nil
	}
	// ESRCH here means the pinned leader was reaped: the group can no longer
	// be signalled safely, so only observation remains.
	_ = unix.PidfdSendSignal(fd, unix.SIGKILL, nil, unix.PIDFD_SIGNAL_PROCESS_GROUP)
	return waitEmpty(pgid, wait)
}

// ErrReused: the pid is alive but is not the recorded process (other start
// time); it was not signalled.
var ErrReused = errors.New("process_identity_mismatch")

// ErrGone: no process with the recorded identity exists.
var ErrGone = errGone

// SignalVerified (W25) sends sig to the single process pid only if it is
// still the process recorded with start time start. The process is pinned
// with pidfd_open BEFORE its identity is checked and is signalled only through
// that pidfd, so a recycled PID can never be hit. Kernels without pidfd
// (before Linux 5.3) return ErrPidfdUnsupported; there is no kill(2) fallback.
func SignalVerified(pid int, start uint64, sig unix.Signal) error {
	if pid <= 1 {
		return errors.New("invalid pid")
	}
	fd, err := unix.PidfdOpen(pid, 0)
	if errors.Is(err, unix.ESRCH) {
		return ErrGone
	}
	if err != nil {
		return ErrPidfdUnsupported
	}
	defer unix.Close(fd)
	id, err := Read(pid)
	if errors.Is(err, errGone) {
		return ErrGone
	}
	if err != nil {
		return err
	}
	if id.StartTime != start {
		return ErrReused
	}
	if id.Zombie {
		return ErrGone
	}
	if err := unix.PidfdSendSignal(fd, sig, nil, 0); err != nil {
		if errors.Is(err, unix.ESRCH) {
			return ErrGone
		}
		return ErrPidfdUnsupported
	}
	return nil
}

func waitEmpty(pgid int, d time.Duration) (bool, error) {
	deadline := time.Now().Add(d)
	for {
		members, err := GroupMembers(pgid)
		if err == nil && len(members) == 0 {
			return true, nil
		}
		if time.Now().After(deadline) {
			return false, err
		}
		time.Sleep(50 * time.Millisecond)
	}
}
