//go:build linux

package executor

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const pollInterval = 20 * time.Millisecond

func platformCheck() *specError { return nil }

// linuxProc contains the main process in a new session (sid = pgid = pid).
// The main process is waited with WNOWAIT so it stays a zombie until
// cmd.Wait; that keeps the PID/PGID/SID number reserved while the executor
// signals the group, so a reused PID is never signalled.
type linuxProc struct {
	pid    int
	start  uint64
	done   chan struct{}
	escape map[int]uint64 // descendants seen outside session/group
}

func startContained(cmd *exec.Cmd) (platformProc, *startFailure) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, &startFailure{status: StatusStartFailed, code: "exec_failed", detail: startErrorDetail(err), cleanup: CleanupNotStarted}
	}
	p := &linuxProc{pid: cmd.Process.Pid, done: make(chan struct{}), escape: map[int]uint64{}}
	if st, err := readStat(p.pid); err == nil {
		p.start = st.start
	}
	go func() {
		defer close(p.done)
		for {
			var info unix.Siginfo
			err := unix.Waitid(unix.P_PID, p.pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
			if err != unix.EINTR {
				return
			}
		}
	}()
	return p, nil
}

func startErrorDetail(err error) string {
	switch {
	case errors.Is(err, os.ErrPermission):
		return "permission denied"
	case errors.Is(err, syscall.ENOEXEC):
		return "exec format error"
	case errors.Is(err, os.ErrNotExist):
		return "executable or interpreter not found"
	}
	return "process start failed"
}

func (p *linuxProc) identity() ProcessIdentity {
	return ProcessIdentity{PID: p.pid, StartTime: p.start}
}
func (p *linuxProc) exited() <-chan struct{}  { return p.done }
func (p *linuxProc) containment() Containment { return ContainmentProcessGroup }
func (p *linuxProc) release()                 {}

func (p *linuxProc) mainExited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// members returns live processes (other than the main one) whose process
// group or session is the run's, and records descendants that have already
// moved to another session/group (escape candidates).
func (p *linuxProc) members() ([]procStat, error) {
	all, err := scanProcs()
	if err != nil {
		return nil, err
	}
	var out []procStat
	children := map[int][]procStat{}
	for _, s := range all {
		children[s.ppid] = append(children[s.ppid], s)
		if s.pid != p.pid && s.live() && (s.pgrp == p.pid || s.sid == p.pid) {
			out = append(out, s)
		}
	}
	// Walk descendants of the main process and of every member.
	queue := []int{p.pid}
	for _, m := range out {
		queue = append(queue, m.pid)
	}
	seen := map[int]bool{}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		for _, c := range children[pid] {
			if c.live() && c.pgrp != p.pid && c.sid != p.pid {
				p.escape[c.pid] = c.start
			}
			queue = append(queue, c.pid)
		}
	}
	return out, nil
}

func (p *linuxProc) leftover() (bool, error) {
	m, err := p.members()
	return len(m) > 0, err
}

func (p *linuxProc) signal(sig syscall.Signal, members []procStat) {
	// The (possibly zombie) main process keeps the group number reserved.
	_ = syscall.Kill(-p.pid, sig)
	for _, m := range members {
		if m.pgrp == p.pid {
			continue
		}
		// Same session, different group: signal only if identity still matches.
		if st, err := readStat(m.pid); err == nil && st.start == m.start && st.sid == p.pid {
			_ = syscall.Kill(m.pid, sig)
		}
	}
}

// waitEmpty polls until the main process exited and no member remains. A
// failed /proc scan never counts as empty; the pinned group is still
// re-signalled so termination does not depend on /proc being readable.
func (p *linuxProc) waitEmpty(d time.Duration, resignal syscall.Signal) (empty, scanFailed bool) {
	deadline := time.Now().Add(d)
	for {
		m, err := p.members()
		if err != nil {
			scanFailed = true
		} else if len(m) == 0 && p.mainExited() {
			return true, scanFailed
		}
		if resignal != 0 {
			p.signal(resignal, m)
		}
		if time.Now().After(deadline) {
			return false, scanFailed
		}
		time.Sleep(pollInterval)
	}
}

// terminate signals the pinned process group first and unconditionally;
// /proc is only used to find same-session members, detect escapes and
// confirm emptiness. Any scan failure leaves cleanup unconfirmed.
func (p *linuxProc) terminate(grace, killWait time.Duration) stopReport {
	rep := stopReport{}
	m, err := p.members()
	scanFailed := err != nil
	empty := false
	if grace > 0 {
		rep.graceful = true
		p.signal(syscall.SIGTERM, m)
		var failed bool
		empty, failed = p.waitEmpty(grace, 0)
		scanFailed = scanFailed || failed
	}
	if !empty {
		m, err = p.members()
		scanFailed = scanFailed || err != nil
		p.signal(syscall.SIGKILL, m)
		// Re-signal while polling: a member may fork between scans.
		var failed bool
		empty, failed = p.waitEmpty(killWait, syscall.SIGKILL)
		scanFailed = scanFailed || failed
	}
	switch {
	case scanFailed:
		rep.note = "proc_scan_failed"
	case !empty:
		rep.note = "processes_remain_after_kill"
	case p.escapedAlive():
		rep.note = "escaped_descendant_alive"
	default:
		rep.confirmed = true
	}
	return rep
}

// escapedAlive is conservative: an unreadable escape candidate counts as alive.
func (p *linuxProc) escapedAlive() bool {
	for pid, start := range p.escape {
		st, err := readStat(pid)
		if err != nil {
			if processGone(err) {
				continue
			}
			return true
		}
		if st.start == start && st.live() {
			return true
		}
	}
	return false
}

func (p *linuxProc) afterReap() (bool, string) {
	if p.escapedAlive() {
		return false, "escaped_descendant_alive"
	}
	// After reaping, an empty group yields ESRCH. Only probe (signal 0):
	// the number is no longer reserved, so it is never signalled here.
	// Members killed moments ago may remain as zombies until init reaps
	// them; zombies are dead, so fall back to a live-member scan.
	deadline := time.Now().Add(time.Second)
	for {
		if err := syscall.Kill(-p.pid, 0); errors.Is(err, syscall.ESRCH) {
			return true, ""
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(pollInterval)
	}
	m, err := p.members()
	if err != nil {
		return false, "proc_scan_failed"
	}
	if len(m) > 0 {
		return false, "process_group_still_present"
	}
	return true, ""
}

func (p *linuxProc) observe(r *ResourceReport) {}

func fillExit(res *Result, ps *os.ProcessState) {
	if ps == nil {
		return
	}
	res.Resources.UserCPU = ps.UserTime()
	res.Resources.SystemCPU = ps.SystemTime()
	res.Resources.CPUScope = "main_process_and_reaped_children"
	if ru, ok := ps.SysUsage().(*syscall.Rusage); ok && ru != nil {
		res.Resources.PeakMemoryBytes = ru.Maxrss * 1024
		res.Resources.PeakMemoryScope = "main_process_rusage"
	}
	ws, ok := ps.Sys().(syscall.WaitStatus)
	if !ok {
		return
	}
	switch {
	case ws.Exited():
		code := ws.ExitStatus()
		res.ExitCode = &code
	case ws.Signaled():
		res.Signal = unix.SignalName(ws.Signal())
	}
}

type procStat struct {
	pid, ppid, pgrp, sid int
	state                byte
	start                uint64
}

func (s procStat) live() bool { return s.state != 'Z' && s.state != 'X' && s.state != 'x' }

func readStat(pid int) (procStat, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return procStat{}, err
	}
	return parseStat(pid, string(b))
}

// parseStat parses /proc/<pid>/stat; comm may contain spaces and ')'.
func parseStat(pid int, s string) (procStat, error) {
	i := strings.LastIndexByte(s, ')')
	if i < 0 || i+2 >= len(s) {
		return procStat{}, errors.New("malformed stat")
	}
	f := strings.Fields(s[i+2:])
	// f[0]=state(3) f[1]=ppid(4) f[2]=pgrp(5) f[3]=session(6) ... f[19]=starttime(22)
	if len(f) < 20 || len(f[0]) == 0 {
		return procStat{}, errors.New("malformed stat")
	}
	st := procStat{pid: pid, state: f[0][0]}
	var err error
	if st.ppid, err = strconv.Atoi(f[1]); err != nil {
		return st, err
	}
	if st.pgrp, err = strconv.Atoi(f[2]); err != nil {
		return st, err
	}
	if st.sid, err = strconv.Atoi(f[3]); err != nil {
		return st, err
	}
	if st.start, err = strconv.ParseUint(f[19], 10, 64); err != nil {
		return st, err
	}
	return st, nil
}

// processGone reports errors meaning the process no longer exists.
func processGone(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH)
}

func scanProcs() ([]procStat, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	out := make([]procStat, 0, len(entries))
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		st, err := readStat(pid)
		if err != nil {
			if processGone(err) {
				continue // exited while scanning
			}
			return nil, err // EMFILE, EACCES, ...: cannot see the tree
		}
		out = append(out, st)
	}
	return out, nil
}
