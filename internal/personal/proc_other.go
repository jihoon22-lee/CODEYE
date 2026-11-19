//go:build !linux

package personal

import (
	"errors"
	"os"
	"os/exec"
)

// ErrUnsupported: personal process management is Linux-only. Windows and
// other systems get this explicit error instead of a guessed implementation
// (docs/OPERATIONS.md#지원-범위).
var ErrUnsupported = errors.New("codeye start/stop/status manage processes on Linux only; on this system run codeye serve and codeye agent directly")

// Supported reports whether personal process management works here.
func Supported() error { return ErrUnsupported }

func checkPrivateDir(path string) error {
	st, err := os.Lstat(path)
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return errors.New(path + " is not a directory (symlinks are refused)")
	}
	return nil
}

// ErrBusy: another start/stop/backup holds the personal lock.
var ErrBusy = errors.New("another codeye start, stop, backup or restore is running for this state_dir")

// Lock is unsupported outside Linux.
func (l Layout) Lock() (func(), error) { return nil, ErrUnsupported }

// AgentLockHeld is unsupported outside Linux.
func (l Layout) AgentLockHeld() (bool, error) { return false, ErrUnsupported }

// Spawn is unsupported outside Linux.
func Spawn(role, exe string, args, env []string, dir, logPath string) (*exec.Cmd, Record, error) {
	return nil, Record{}, ErrUnsupported
}

// Drain is unsupported outside Linux.
func (r *Record) Drain() error { return ErrUnsupported }

// Terminate is unsupported outside Linux.
func (r *Record) Terminate() error { return ErrUnsupported }

// Signal results (Drain, Terminate).
var (
	ErrProcessGone       = errors.New("process_gone")
	ErrProcessReused     = errors.New("process_identity_mismatch")
	ErrSignalUnsupported = ErrUnsupported
)
