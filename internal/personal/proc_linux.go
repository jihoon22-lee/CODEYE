//go:build linux

package personal

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/jihoon22-lee/CODEYE/internal/agent/procid"
)

// Supported reports whether personal process management works here.
func Supported() error { return nil }

// checkPrivateDir accepts a real directory owned by this user that no one
// else can write.
func checkPrivateDir(path string) error {
	st, err := os.Lstat(path)
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return errors.New(path + " is not a directory (symlinks are refused)")
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok || int(sys.Uid) != os.Getuid() {
		return errors.New(path + " is not owned by the current user")
	}
	if st.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(path, 0o700); err != nil {
			return errors.New(path + " is accessible by other users and cannot be restricted to 0700")
		}
	}
	return nil
}

// ErrBusy: another start/stop/backup holds the personal lock.
var ErrBusy = errors.New("another codeye start, stop, backup or restore is running for this state_dir")

// Lock takes the exclusive personal lock without waiting.
func (l Layout) Lock() (func(), error) {
	f, err := os.OpenFile(l.LockFile(), os.O_RDWR|os.O_CREATE|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, errors.New("cannot open the personal lock file")
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		return nil, errors.New("cannot lock the personal state")
	}
	return func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN); _ = f.Close() }, nil
}

// AgentLockHeld reports whether some process holds the agent's state lock
// (<agent>/agent.lock, internal/agent). It is probed with a non-blocking lock
// that is released at once; a missing lock file means no agent ever ran.
func (l Layout) AgentLockHeld() (bool, error) { return AgentStateLocked(l.AgentDir()) }

// AgentStateLocked reports whether an agent runs with stateDir as its
// --state-dir (it holds <stateDir>/agent.lock).
func AgentStateLocked(stateDir string) (bool, error) {
	f, err := os.OpenFile(filepath.Join(stateDir, "agent.lock"), os.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) {
			return true, nil
		}
		return false, err
	}
	_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
	return false, nil
}

// Spawn starts exe with argv args, the explicit environment env and its
// output appended to logPath (0600). The child gets its own session, no
// terminal and stdin from /dev/null, so it outlives the start command.
func Spawn(role, exe string, args, env []string, dir, logPath string) (*exec.Cmd, Record, error) {
	log, err := os.OpenFile(logPath, os.O_WRONLY|os.O_APPEND|os.O_CREATE|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, Record{}, errors.New("cannot open log file " + logPath)
	}
	defer log.Close()
	if st, err := log.Stat(); err != nil || !st.Mode().IsRegular() {
		return nil, Record{}, errors.New("log file is not a regular file: " + logPath)
	}
	cmd := exec.Command(exe, args...)
	cmd.Env = env
	cmd.Dir = dir
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, Record{}, errors.New("cannot start " + role)
	}
	id, err := procid.Read(cmd.Process.Pid)
	if err != nil {
		// The child may already have exited; the caller reaps it.
		return cmd, Record{}, errors.New(role + " exited immediately")
	}
	return cmd, Record{Role: role, PID: cmd.Process.Pid, StartTime: id.StartTime, Executable: exe, StartedAt: time.Now().UTC()}, nil
}

// Drain asks a recorded agent to drain (SIGUSR1, verified identity).
func (r *Record) Drain() error { return procid.SignalVerified(r.PID, r.StartTime, unix.SIGUSR1) }

// Terminate asks a recorded process to shut down (SIGTERM, verified identity).
func (r *Record) Terminate() error { return procid.SignalVerified(r.PID, r.StartTime, unix.SIGTERM) }

// Signal results (Drain, Terminate).
var (
	// ErrProcessGone: the recorded process no longer exists.
	ErrProcessGone = procid.ErrGone
	// ErrProcessReused: the pid belongs to another process; not signalled.
	ErrProcessReused = procid.ErrReused
	// ErrSignalUnsupported: the kernel cannot pin the process (pidfd); not
	// signalled, and there is no kill(2) fallback.
	ErrSignalUnsupported = procid.ErrPidfdUnsupported
)
