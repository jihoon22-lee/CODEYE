//go:build linux

package agent

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// spawner starts workers from one OS thread that lives as long as the agent:
// Linux PR_SET_PDEATHSIG fires when the *thread* that forked exits, so
// starting from arbitrary goroutines could kill workers spuriously.
type spawner struct {
	mu     sync.Mutex
	closed bool
	reqs   chan spawnReq
}

type spawnReq struct {
	cmd  *exec.Cmd
	done chan error
}

func newSpawner() *spawner {
	s := &spawner{reqs: make(chan spawnReq)}
	go func() {
		runtime.LockOSThread() // never unlocked: the thread stays for the process lifetime
		for r := range s.reqs {
			r.done <- r.cmd.Start()
		}
	}()
	return s
}

// start launches cmd. release is an idempotent platform hook called after the
// worker is reaped or when shutdown forces it (Linux: no-op; termination is
// the verified process-group kill).
func (s *spawner) start(cmd *exec.Cmd) (release func(), err error) {
	// Own process group (pgid == pid) and SIGTERM when the agent dies.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
	done := make(chan error, 1)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return func() {}, errSpawnerClosed
	}
	s.reqs <- spawnReq{cmd: cmd, done: done}
	err = <-done
	s.mu.Unlock()
	if err != nil {
		return func() {}, err
	}
	return func() {}, nil
}

// close ends the locked spawner thread. Call only after every worker it
// started has exited: the thread's exit fires their PDEATHSIG.
func (s *spawner) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.reqs)
	}
}

var errSpawnerClosed = errors.New("spawner closed")

// lockStateDir takes an exclusive, non-blocking flock on <dir>/agent.lock so
// two agents never share a state dir. The lock dies with the process.
func lockStateDir(dir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(dir, "agent.lock"), os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errStateLocked
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}

func platformEnv(env []string) []string { return env }

func openNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}

// hostCapacity reports MemAvailable and free disk of dir (0 when unknown).
func hostCapacity(dir string) (mem, disk int64) {
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "MemAvailable:") {
				f := strings.Fields(line)
				if len(f) >= 2 {
					if kb, err := strconv.ParseInt(f[1], 10, 64); err == nil {
						mem = kb * 1024
					}
				}
			}
		}
	}
	var st syscall.Statfs_t
	if syscall.Statfs(dir, &st) == nil {
		disk = int64(st.Bavail) * int64(st.Bsize)
	}
	return mem, disk
}
