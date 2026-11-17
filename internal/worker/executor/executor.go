package executor

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// platformProc is the OS containment of one started main process.
type platformProc interface {
	identity() ProcessIdentity
	// exited is closed when the main process has exited. On Linux the main
	// process is not yet reaped, which keeps its PID/PGID reserved.
	exited() <-chan struct{}
	// leftover reports whether processes other than the exited main process
	// remain in the containment unit.
	leftover() (bool, error)
	// terminate stops everything in the containment unit (main included) and
	// waits up to killWait for it to become empty.
	terminate(grace, killWait time.Duration) stopReport
	// afterReap re-checks the unit after cmd.Wait reaped the main process.
	afterReap() (bool, string)
	observe(r *ResourceReport)
	release()
	containment() Containment
}

type stopReport struct {
	confirmed bool
	graceful  bool
	note      string
}

// startFailure is returned when the process could not be started inside its
// containment unit.
type startFailure struct {
	status  Status
	code    string
	detail  string
	cleanup Cleanup
	note    string
}

// readerDrain bounds how long Run waits for stdout/stderr EOF once the
// containment unit is empty.
const readerDrain = 3 * time.Second

// Run executes spec. It never returns before the main process has been
// reaped or cleanup has been classified as uncertain.
func (e *Executor) Run(ctx context.Context, spec Spec) (out Result) {
	res := Result{
		TaskID:       spec.TaskID,
		AttemptEpoch: spec.AttemptEpoch,
		Cleanup:      CleanupNotStarted,
		Containment:  ContainmentNone,
		Command:      CommandRecord{Executable: redactString(spec.Executable, spec.Redact), Argv: recordArgv(spec), Dir: spec.Dir},
		Resources:    ResourceReport{Requested: spec.Resources},
	}
	finish := func() Result {
		if res.EndedAt.IsZero() {
			res.EndedAt = time.Now()
		}
		if res.StartedAt.IsZero() {
			res.StartedAt = res.EndedAt
		}
		return res
	}
	if serr := platformCheck(); serr != nil {
		res.Status, res.Reason, res.Detail = serr.status, serr.code, serr.detail
		return finish()
	}
	p, serr := prepare(spec)
	if serr != nil {
		res.Status, res.Reason, res.Detail = serr.status, serr.code, serr.detail
		return finish()
	}
	res.Command.ResolvedExecutable = redactString(p.resolved, spec.Redact)
	if err := ctx.Err(); err != nil {
		res.Status, res.Reason = StatusCancelled, "cancelled_before_start"
		return finish()
	}
	killWait := e.KillWait
	if killWait <= 0 {
		killWait = defaultKillWait
	}
	outputSecrets := append([]string(nil), spec.Redact...)
	for _, i := range spec.SensitiveArgs {
		outputSecrets = append(outputSecrets, spec.Args[i])
	}

	// Per-run temp/home outside protected roots.
	tempDir, err := os.MkdirTemp(p.tempParent, "codeye-exec-")
	if err != nil {
		res.Status, res.Reason, res.Detail = StatusStartFailed, "temp_unavailable", "cannot create per-run temp directory"
		return finish()
	}
	keepTemp := false
	defer func() {
		if !keepTemp && !removeWithRetry(tempDir) {
			out.Command.TempRetained = true
		}
	}()
	home, tmp := filepath.Join(tempDir, "home"), filepath.Join(tempDir, "tmp")
	if os.Mkdir(home, 0o700) != nil || os.Mkdir(tmp, 0o700) != nil {
		res.Status, res.Reason, res.Detail = StatusStartFailed, "temp_unavailable", "cannot create per-run home/tmp"
		return finish()
	}
	res.Command.TempDir = tempDir
	envList, envKeys := buildEnv(p.env, home, tmp)
	res.Command.EnvKeys = envKeys

	// Own pipes: the child gets *os.File ends, so cmd.Wait does not wait for
	// grandchildren that inherited them; readers detect held-open pipes.
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		res.Status, res.Reason = StatusStartFailed, "pipe_unavailable"
		return finish()
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		closeAll(stdoutR, stdoutW)
		res.Status, res.Reason = StatusStartFailed, "pipe_unavailable"
		return finish()
	}
	var stdinR, stdinW *os.File
	if spec.Stdin != nil {
		if stdinR, stdinW, err = os.Pipe(); err != nil {
			closeAll(stdoutR, stdoutW, stderrR, stderrW)
			res.Status, res.Reason = StatusStartFailed, "pipe_unavailable"
			return finish()
		}
	}

	cmd := &exec.Cmd{
		Path:   p.executable,
		Args:   append([]string{p.executable}, spec.Args...),
		Dir:    p.dir,
		Env:    envList,
		Stdout: stdoutW,
		Stderr: stderrW,
	}
	if stdinR != nil {
		cmd.Stdin = stdinR
	}

	res.StartedAt = time.Now()
	proc, fail := startContained(cmd)
	// Parent copies of the child ends must be closed in every case.
	closeAll(stdoutW, stderrW, stdinR)
	if fail != nil {
		closeAll(stdoutR, stderrR, stdinW)
		res.Status, res.Reason, res.Detail = fail.status, fail.code, fail.detail
		res.Cleanup, res.CleanupNote = fail.cleanup, fail.note
		if fail.cleanup == CleanupUncertain {
			keepTemp = true
			res.Command.TempRetained = true
		}
		return finish()
	}
	defer proc.release()
	res.Containment = proc.containment()
	res.Process = proc.identity()

	outC := startCapture(stdoutR, p.stdoutCap)
	errC := startCapture(stderrR, p.stderrCap)
	if stdinW != nil {
		go func() {
			_, _ = io.Copy(stdinW, bytes.NewReader(spec.Stdin))
			_ = stdinW.Close()
		}()
	}
	if spec.OnStarted != nil {
		spec.OnStarted(res.Process)
	}

	timer := time.NewTimer(spec.Timeout)
	defer timer.Stop()
	var rep stopReport
	select {
	case <-proc.exited():
		res.Status = StatusExited
		left, lerr := proc.leftover()
		switch {
		case lerr != nil:
			rep = proc.terminate(p.grace, killWait)
			rep.confirmed = false
			rep.note = joinNote("containment_query_failed", rep.note)
		case left:
			res.LeftoverKilled = true
			rep = proc.terminate(p.grace, killWait)
		default:
			rep = stopReport{confirmed: true}
		}
	case <-timer.C:
		res.Status, res.Reason = StatusTimedOut, "wall_clock_timeout"
		rep = proc.terminate(p.grace, killWait)
	case <-ctx.Done():
		res.Status, res.Reason = StatusCancelled, "context_cancelled"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			res.Reason = "context_deadline"
		}
		rep = proc.terminate(p.grace, killWait)
	}
	res.Resources.GracefulStopUsed = rep.graceful
	confirmed, note := rep.confirmed, rep.note

	// Reap the main process (bounded: it may be unkillable).
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	reaped := false
	select {
	case <-proc.exited():
		select {
		case <-waitDone:
			reaped = true
		case <-time.After(killWait):
		}
	default:
	}
	if !reaped {
		confirmed = false
		note = joinNote(note, "main_process_not_reaped")
	} else {
		if ok, n := proc.afterReap(); !ok {
			confirmed = false
			note = joinNote(note, n)
		}
		fillExit(&res, cmd.ProcessState)
		proc.observe(&res.Resources)
	}

	// Output EOF means every holder of the write ends is gone.
	drain := time.Now().Add(readerDrain)
	res.Stdout = outC.finish(drain)
	res.Stderr = errC.finish(drain)
	if res.Stdout.Incomplete || res.Stderr.Incomplete {
		confirmed = false
		note = joinNote(note, "output_pipe_held_open")
	}
	res.Stdout.Data = redactBytes(res.Stdout.Data, outputSecrets)
	res.Stderr.Data = redactBytes(res.Stderr.Data, outputSecrets)

	if confirmed {
		res.Cleanup = CleanupConfirmed
	} else {
		res.Cleanup = CleanupUncertain
		keepTemp = true // a surviving process may still use it
		res.Command.TempRetained = true
		if note == "" {
			note = "termination_not_confirmed"
		}
	}
	res.CleanupNote = note
	res.EndedAt = time.Now()
	return finish()
}

// removeWithRetry tolerates short-lived Windows sharing violations right
// after the job has emptied.
func removeWithRetry(dir string) bool {
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := os.RemoveAll(dir); err == nil {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func joinNote(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "" || slices.Contains(strings.Split(a, ","), b):
		return a
	}
	return a + "," + b
}

func closeAll(files ...*os.File) {
	for _, f := range files {
		if f != nil {
			_ = f.Close()
		}
	}
}

func redactBytes(b []byte, values []string) []byte {
	for _, v := range values {
		if v != "" {
			b = bytes.ReplaceAll(b, []byte(v), []byte(redactedMarker))
		}
	}
	return b
}

type capture struct {
	r     *os.File
	limit int64
	done  chan struct{}
	mu    sync.Mutex
	data  []byte
	total int64
}

func startCapture(r *os.File, limit int64) *capture {
	c := &capture{r: r, limit: limit, done: make(chan struct{})}
	go func() {
		defer close(c.done)
		buf := make([]byte, 32*1024)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				c.mu.Lock()
				c.total += int64(n)
				if room := c.limit - int64(len(c.data)); room > 0 {
					c.data = append(c.data, buf[:min(int64(n), room)]...)
				}
				c.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	return c
}

func (c *capture) snapshot() Stream {
	c.mu.Lock()
	defer c.mu.Unlock()
	data := append([]byte(nil), c.data...)
	return Stream{Data: data, TotalBytes: c.total, Truncated: c.total > int64(len(data))}
}

// finish waits for EOF until deadline. If EOF is not reached the stream is
// marked incomplete (output captured so far is kept) and the reader is
// abandoned: the read end is closed asynchronously because on Windows Close
// may block until the pending read ends.
func (c *capture) finish(deadline time.Time) Stream {
	t := time.NewTimer(max(time.Until(deadline), time.Millisecond))
	defer t.Stop()
	select {
	case <-c.done:
		_ = c.r.Close()
		return c.snapshot()
	case <-t.C:
		s := c.snapshot()
		s.Incomplete = true
		go func() { _ = c.r.Close() }()
		return s
	}
}
