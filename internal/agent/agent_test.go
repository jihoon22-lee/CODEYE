//go:build linux

package agent

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/agent/procid"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/run"
	"github.com/jihoon22-lee/CODEYE/internal/workerproto"
)

// TestMain lets the test binary act as the worker ("worker", "worker-none")
// and as a separate agent process ("agent-helper") for crash tests. The
// worker environment is closed, so the role is selected by argv only.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
		defer stop()
		switch os.Args[1] {
		case "worker":
			os.Exit(run.Main(ctx, run.Config{In: os.Stdin, Out: os.Stdout, Stderr: os.Stderr, Handlers: func(context.Context, *workerproto.Hello, executor.Runner) ([]analysis.Handler, []string, error) {
				return []analysis.Handler{modeHandler{}}, nil, nil
			}}))
		case "worker-none":
			os.Exit(run.Main(ctx, run.Config{In: os.Stdin, Out: os.Stdout, Stderr: os.Stderr, Handlers: func(context.Context, *workerproto.Hello, executor.Runner) ([]analysis.Handler, []string, error) {
				return nil, []string{"go-vet:not_configured"}, nil
			}}))
		case "agent-helper":
			os.Exit(helperAgent(ctx, os.Args[2:]))
		}
	}
	os.Exit(m.Run())
}

// modeHandler (test-only) runs /bin/sh according to the "mode" file.
type modeHandler struct{}

func (modeHandler) Info() analysis.HandlerInfo {
	return analysis.HandlerInfo{Name: "mode", Version: "1", Capability: "analysis:deep-review"}
}
func (modeHandler) Plan(context.Context, analysis.Job) ([]analysis.TaskSpec, error) {
	return []analysis.TaskSpec{{Key: "mode.run", Required: true}}, nil
}
func (modeHandler) Run(ctx context.Context, job analysis.Job, task analysis.TaskSpec, r executor.Runner) analysis.TaskResult {
	mode, _ := os.ReadFile(filepath.Join(job.Snapshot.SnapshotDir, "mode"))
	script := "echo ok"
	if strings.TrimSpace(string(mode)) == "sleep" {
		script = "sleep 300 & sleep 300; wait"
	}
	res := r.Run(ctx, executor.Spec{Executable: "/bin/sh", Args: []string{"-c", script}, WorkspaceRoot: job.Snapshot.WorkspaceDir, Dir: job.Snapshot.WorkspaceDir, Env: map[string]string{"PATH": "/usr/bin:/bin"}, Timeout: 5 * time.Minute})
	ex, err := analysis.WriteExecution(filepath.Join(job.Scratch, task.Key), "sh", res)
	out := analysis.TaskResult{Status: domain.TaskFailed, Reason: "handler_error"}
	if err == nil {
		out.Executions = []analysis.Execution{ex}
	}
	if res.Status == executor.StatusExited && res.ExitCode != nil && *res.ExitCode == 0 {
		out.Status, out.Reason = domain.TaskSucceeded, ""
	}
	return out
}

func helperAgent(ctx context.Context, args []string) int {
	cfg := testConfig(args[0], args[1], "", args[2], args[3], "worker")
	ag, err := New(cfg)
	if err == nil {
		err = ag.Run(ctx)
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	if err != nil {
		return 1
	}
	return 0
}

func testConfig(controller, state, token, rootID, root, workerArg string) Config {
	return Config{ControllerURL: controller, StateDir: state, EnrollTokenFile: token, SourceRoots: map[string]string{rootID: root}, Workers: 1, WorkerCommand: []string{os.Args[0], workerArg}, HeartbeatInterval: 200 * time.Millisecond, LeaseDuration: 4 * time.Second, LeaseMargin: time.Second, ShutdownTimeout: 10 * time.Second}
}

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

type harness struct {
	fc     *fakeController
	state  string
	src    string
	rootID string
	token  string
}

func newHarness(t *testing.T, mode string) *harness {
	h := &harness{fc: newFakeController(t), state: filepath.Join(t.TempDir(), "state"), src: t.TempDir(), rootID: domain.NewID()}
	os.WriteFile(filepath.Join(h.src, "main.go"), []byte("package main\n"), 0600)
	os.WriteFile(filepath.Join(h.src, "mode"), []byte(mode+"\n"), 0600)
	h.token = filepath.Join(t.TempDir(), "enroll-token")
	os.WriteFile(h.token, []byte(h.fc.token+"\n"), 0600)
	return h
}

func (h *harness) job() nodeproto.Job {
	return nodeproto.Job{Profile: "deep-review", Scope: domain.Scope{Kind: "full"}, Source: nodeproto.JobSource{Kind: "local", SourceRootID: h.rootID, RelativePath: "."}, Resources: nodeproto.Resources{CPUMillis: 1000, MemoryBytes: 1 << 30}}
}

func startAgent(t *testing.T, cfg Config) (*Agent, context.CancelFunc, chan error) {
	t.Helper()
	ag, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ag.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			t.Error("agent did not stop")
		}
	})
	return ag, cancel, done
}

func waitFor[T any](t *testing.T, ch chan T, d time.Duration, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(d):
		t.Fatalf("timeout waiting for %s", what)
	}
	var zero T
	return zero
}

func (ag *Agent) workerPIDs() []int {
	ag.mu.Lock()
	defer ag.mu.Unlock()
	var out []int
	for _, w := range ag.workers {
		out = append(out, w.ident.PID)
	}
	return out
}

func TestAgentEndToEndSuccessAndNoSecretInWorker(t *testing.T) {
	h := newHarness(t, "succeed")
	h.fc.addJob(h.job())
	transcript, logs := &lockedBuf{}, &lockedBuf{}
	cfg := testConfig(h.fc.srv.URL, h.state, h.token, h.rootID, h.src, "worker")
	cfg.Transcript, cfg.Log = transcript, logs
	ag, cancel, done := startAgent(t, cfg)
	c := waitFor(t, h.fc.completeCh, 30*time.Second, "complete")
	if c.Outcome != "succeeded" || c.CleanupState != "confirmed" || c.ResultManifestID == nil {
		t.Fatalf("complete %+v\nlogs %s", c, logs.String())
	}
	// Invariant 2: the worker holds no credential (env, IPC).
	pids := ag.workerPIDs()
	if len(pids) != 1 {
		t.Fatalf("workers %v", pids)
	}
	env, err := os.ReadFile("/proc/" + itoa(pids[0]) + "/environ")
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range strings.Split(strings.TrimRight(string(env), "\x00"), "\x00") {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "HOME", "TMPDIR", "TMP", "TEMP", "LANG", "PATH":
		default:
			t.Errorf("unexpected worker env key %q", k)
		}
	}
	secret := h.fc.secret
	if strings.Contains(string(env), secret) || strings.Contains(transcript.String(), secret) || strings.Contains(transcript.String(), h.fc.token) {
		t.Fatal("node secret or enrollment token reached the worker")
	}
	if strings.Contains(logs.String(), secret) {
		t.Fatal("secret in agent log")
	}
	if info, err := os.Stat(filepath.Join(h.state, "node.json")); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("node.json mode %v %v", info, err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	done <- nil // for cleanup
}

func itoa(i int) string { return strconv.Itoa(i) }

func TestNoHandlerNeverClaims(t *testing.T) {
	h := newHarness(t, "succeed")
	h.fc.addJob(h.job())
	logs := &lockedBuf{}
	cfg := testConfig(h.fc.srv.URL, h.state, h.token, h.rootID, h.src, "worker-none")
	cfg.Log = logs
	startAgent(t, cfg)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	h.fc.mu.Lock()
	hb, caps := h.fc.hbCount, h.fc.lastHB.Capabilities
	h.fc.mu.Unlock()
	if hb < 3 {
		t.Fatalf("only %d heartbeats", hb)
	}
	for _, c := range caps {
		if strings.HasPrefix(c, "analysis:") {
			t.Fatalf("advertised %s without handler", c)
		}
	}
	if n := h.fc.claimCount(); n != 0 {
		t.Fatalf("%d claims without a handler", n)
	}
	if !strings.Contains(logs.String(), "no_analysis_handler") {
		t.Fatalf("missing no_analysis_handler log: %s", logs.String())
	}
}

func waitTaskRunning(t *testing.T, fc *fakeController) {
	t.Helper()
	deadline := time.After(30 * time.Second)
	running := 0
	for running < 1 {
		select {
		case k := <-fc.eventCh:
			if k == nodeproto.EventTaskStatus {
				running++
			}
		case <-deadline:
			t.Fatal("task never started")
		}
	}
}

// taskPGIDs returns task process groups journaled as started.
func taskPGIDs(t *testing.T, state string) []JProc {
	t.Helper()
	j, err := ReadJournal(filepath.Join(state, "journal.jsonl"))
	if err != nil {
		return nil
	}
	var out []JProc
	for _, a := range j.Attempts {
		for _, p := range a.Procs {
			if p.Started {
				out = append(out, *p)
			}
		}
	}
	return out
}

func waitStartedProc(t *testing.T, state string) JProc {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if ps := taskPGIDs(t, state); len(ps) > 0 {
			return ps[0]
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("no journaled process")
	return JProc{}
}

func groupEmpty(pgid int) bool {
	m, err := procid.GroupMembers(pgid)
	return err == nil && len(m) == 0
}

func TestCancelCommandKillsTreeAndCompletesCancelled(t *testing.T) {
	h := newHarness(t, "sleep")
	h.fc.addJob(h.job())
	startAgent(t, testConfig(h.fc.srv.URL, h.state, h.token, h.rootID, h.src, "worker"))
	p := waitStartedProc(t, h.state)
	if p.PGID != p.PID || p.Containment != string(executor.ContainmentProcessGroup) {
		t.Fatalf("journaled %+v", p)
	}
	h.fc.mu.Lock()
	var attemptID string
	for id := range h.fc.attempts {
		attemptID = id
	}
	h.fc.mu.Unlock()
	h.fc.queueCommand(nodeproto.CommandCancelAttempt, attemptID)
	c := waitFor(t, h.fc.completeCh, 30*time.Second, "complete")
	if c.Outcome != "cancelled" || c.CleanupState != "confirmed" {
		t.Fatalf("complete %+v", c)
	}
	if !groupEmpty(p.PGID) {
		t.Fatal("task process group survived cancel")
	}
}

func TestLeaseWatchdogCancelsBeforeExpiry(t *testing.T) {
	h := newHarness(t, "sleep")
	h.fc.addJob(h.job())
	cfg := testConfig(h.fc.srv.URL, h.state, h.token, h.rootID, h.src, "worker")
	h.fc.leaseFor = cfg.LeaseDuration
	h.fc.hangAttemptHB = true // network partition for lease renewals only
	startAgent(t, cfg)
	p := waitStartedProc(t, h.state)
	start := time.Now()
	// The watchdog fires at lease − margin; the server lease is still valid,
	// so the real cleanup result is accepted through complete.
	c := waitFor(t, h.fc.completeCh, 30*time.Second, "complete")
	if c.Outcome != "failed" || c.Reason == nil || *c.Reason != "lease_lost" || c.CleanupState != "confirmed" {
		t.Fatalf("complete %+v", c)
	}
	if elapsed := time.Since(start); elapsed > cfg.LeaseDuration {
		t.Fatalf("lease-loss handling took %v (lease %v)", elapsed, cfg.LeaseDuration)
	}
	if !groupEmpty(p.PGID) {
		t.Fatal("task survived lease loss")
	}
}

func TestStaleLeaseCleanupReportResentUntilAccepted(t *testing.T) {
	h := newHarness(t, "sleep")
	h.fc.addJob(h.job())
	h.fc.refuseReports = 1 // first confirmed report: attempt still leased → conflict
	startAgent(t, testConfig(h.fc.srv.URL, h.state, h.token, h.rootID, h.src, "worker"))
	p := waitStartedProc(t, h.state)
	h.fc.mu.Lock()
	h.fc.staleAttempts = true // server-side expiry: renewals, events and complete → 409
	h.fc.mu.Unlock()
	first := waitFor(t, h.fc.reportCh, 30*time.Second, "first report")
	second := waitFor(t, h.fc.reportCh, 30*time.Second, "resent report")
	if first.AttemptID != second.AttemptID || second.CleanupState != "confirmed" {
		t.Fatalf("reports %+v %+v", first, second)
	}
	if !groupEmpty(p.PGID) {
		t.Fatal("task survived")
	}
	// Once accepted, the report is settled and not sent again.
	time.Sleep(time.Second)
	h.fc.mu.Lock()
	n := 0
	for _, r := range h.fc.reports {
		if r.AttemptID == first.AttemptID {
			n++
		}
	}
	h.fc.mu.Unlock()
	if n != 2 {
		t.Fatalf("report sent %d times, want 2", n)
	}
	select {
	case c := <-h.fc.completeCh:
		t.Fatalf("stale complete accepted: %+v", c)
	default:
	}
}

func TestPinnedRetryLoadsCachedSnapshotWithoutSource(t *testing.T) {
	h := newHarness(t, "succeed")
	// State dir behind a symlink: cache paths must still match W04's resolved roots.
	real := filepath.Join(t.TempDir(), "real-state")
	os.MkdirAll(real, 0700)
	link := filepath.Join(t.TempDir(), "state-link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	h.state = link
	h.fc.addJob(h.job())
	startAgent(t, testConfig(h.fc.srv.URL, h.state, h.token, h.rootID, h.src, "worker"))
	if c := waitFor(t, h.fc.completeCh, 30*time.Second, "first complete"); c.Outcome != "succeeded" {
		t.Fatalf("first %+v", c)
	}
	h.fc.mu.Lock()
	digest := h.fc.digests[0]
	h.fc.mu.Unlock()
	// The original source disappears: a retry must use the retained snapshot.
	if err := os.RemoveAll(h.src); err != nil {
		t.Fatal(err)
	}
	retry := h.job()
	retry.Snapshot = &nodeproto.JobSnapshot{ID: domain.NewID(), ManifestDigest: digest, Kind: "local", Consistency: "stable_copy_best_effort"}
	h.fc.addJob(retry)
	c := waitFor(t, h.fc.completeCh, 30*time.Second, "retry complete")
	if c.Outcome != "succeeded" || c.CleanupState != "confirmed" {
		t.Fatalf("retry %+v", c)
	}
	h.fc.mu.Lock()
	defer h.fc.mu.Unlock()
	if len(h.fc.digests) != 2 || h.fc.digests[1] != digest {
		t.Fatalf("retry fixed a different snapshot: %v", h.fc.digests)
	}
}

func TestCrashRecoveryKillsVerifiedGroupAndNeverReruns(t *testing.T) {
	h := newHarness(t, "sleep")
	h.fc.addJob(h.job())
	// First agent as a separate process so it can be SIGKILLed.
	cmd := exec.Command(os.Args[0], "agent-helper", h.fc.srv.URL, h.state, h.rootID, h.src)
	// Enroll in-process first so the helper needs no token.
	enroll, err := New(testConfig(h.fc.srv.URL, h.state, h.token, h.rootID, h.src, "worker"))
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(h.state, 0700)
	if err := enroll.loadOrEnroll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	p := waitStartedProc(t, h.state)
	t.Cleanup(func() {
		if !groupEmpty(p.PGID) {
			_, _ = procid.TerminateVerifiedGroup(p.PGID, p.StartTime, 200*time.Millisecond, time.Second)
		}
	})
	// Stop the agent before killing its worker. Killing the agent first sends
	// the worker PDEATHSIG/SIGTERM; a fast worker then legitimately cleans up
	// the task before this test can simulate the loss of both supervisors.
	if err := cmd.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	var stopped syscall.WaitStatus
	for {
		_, err := syscall.Wait4(cmd.Process.Pid, &stopped, syscall.WUNTRACED, nil)
		if err == syscall.EINTR {
			continue
		}
		if err != nil || !stopped.Stopped() {
			t.Fatalf("wait for stopped agent: %v, %v", stopped, err)
		}
		break
	}
	j, err := ReadJournal(filepath.Join(h.state, "journal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(j.Workers) != 1 {
		t.Fatalf("expected one worker, got %d", len(j.Workers))
	}
	for _, w := range j.Workers {
		worker, err := os.FindProcess(w.PID)
		if err != nil {
			t.Fatal(err)
		}
		defer worker.Release()
		if status, err := procid.Check(w.PID, w.StartTime); err != nil || status != procid.Same || w.Exited {
			t.Fatalf("journaled worker is not alive: %v %v", status, err)
		}
		if err := worker.Kill(); err != nil {
			t.Fatal(err)
		}
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	if groupEmpty(p.PGID) {
		t.Fatal("test setup: task group should have survived the crash")
	}
	h.fc.mu.Lock()
	var crashed *fakeAttempt
	for _, a := range h.fc.attempts {
		crashed = a
	}
	eventsBefore := len(crashed.events)
	h.fc.mu.Unlock()
	// Restart in-process with the same state dir: reconciliation first.
	startAgent(t, testConfig(h.fc.srv.URL, h.state, "", h.rootID, h.src, "worker"))
	rep := waitFor(t, h.fc.reportCh, 30*time.Second, "cleanup report")
	if rep.CleanupState != "confirmed" {
		t.Fatalf("report %+v", rep)
	}
	if !groupEmpty(p.PGID) {
		t.Fatal("orphaned task group not cleaned")
	}
	if rep.AttemptID != crashed.lease.AttemptID {
		t.Fatalf("report for %s, want %s", rep.AttemptID, crashed.lease.AttemptID)
	}
	time.Sleep(time.Second)
	h.fc.mu.Lock()
	eventsAfter, attempts := len(crashed.events), len(h.fc.attempts)
	h.fc.mu.Unlock()
	// The abandoned attempt is never re-run: no new events for it, no new attempt.
	if eventsAfter != eventsBefore || attempts != 1 {
		t.Fatalf("re-run after restart: events %d→%d attempts %d", eventsBefore, eventsAfter, attempts)
	}
	if ps := taskPGIDs(t, h.state); len(ps) != 0 {
		for _, p2 := range ps {
			if p2.PID != p.PID && !groupEmpty(p2.PGID) {
				t.Fatal("a new task process was started for the abandoned attempt")
			}
		}
	}
}

func TestRecoveryNeverSignalsReusedPID(t *testing.T) {
	h := newHarness(t, "succeed")
	// An innocent process standing in for a reused PID.
	innocent := exec.Command("/bin/sleep", "300")
	innocent.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := innocent.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = innocent.Process.Kill(); _ = innocent.Wait() }()
	id, _ := procid.Read(innocent.Process.Pid)
	os.MkdirAll(h.state, 0700)
	enroll, _ := New(testConfig(h.fc.srv.URL, h.state, h.token, h.rootID, h.src, "worker"))
	if err := enroll.loadOrEnroll(context.Background()); err != nil {
		t.Fatal(err)
	}
	j, err := OpenJournal(filepath.Join(h.state, "journal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	att, wid := domain.NewID(), domain.NewID()
	for _, r := range []Record{
		// The worker itself is journaled as exited, so the reused PID is the
		// only reason for uncertainty.
		{Kind: RecWorkerStarted, WorkerID: wid, PID: 999999, StartTime: 1, PGID: 999999},
		{Kind: RecWorkerExited, WorkerID: wid, PID: 999999, StartTime: 1, Exit: "exited"},
		{Kind: RecAttemptAssigned, AttemptID: att, WorkerID: wid, Epoch: 3},
		{Kind: RecIntent, AttemptID: att, Intent: "task_exec_started", TaskKey: "k", Ordinal: 1},
		{Kind: RecProcessStarted, AttemptID: att, TaskKey: "k", Ordinal: 1, PID: id.PID, PGID: id.PID, StartTime: id.StartTime + 7, Containment: string(executor.ContainmentProcessGroup)},
	} {
		if err := j.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	j.Close()
	startAgent(t, testConfig(h.fc.srv.URL, h.state, "", h.rootID, h.src, "worker"))
	rep := waitFor(t, h.fc.reportCh, 15*time.Second, "cleanup report")
	if rep.AttemptID != att || rep.CleanupState != "uncertain" || rep.LeaseEpoch != 3 {
		t.Fatalf("report %+v", rep)
	}
	if st, _ := procid.Check(innocent.Process.Pid, id.StartTime); st != procid.Same {
		t.Fatal("innocent process with reused PID was signalled")
	}
}

func TestTornJournalMakesOpenAttemptUncertain(t *testing.T) {
	h := newHarness(t, "succeed")
	os.MkdirAll(h.state, 0700)
	enroll, _ := New(testConfig(h.fc.srv.URL, h.state, h.token, h.rootID, h.src, "worker"))
	if err := enroll.loadOrEnroll(context.Background()); err != nil {
		t.Fatal(err)
	}
	att := domain.NewID()
	path := filepath.Join(h.state, "journal.jsonl")
	j, _ := OpenJournal(path)
	_ = j.Append(Record{Kind: RecAttemptAssigned, AttemptID: att, WorkerID: domain.NewID(), Epoch: 1})
	j.Close()
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	f.WriteString(`{"kind":"intent","attempt_id":"` + att + `","intent":"task_exec_sta`) // crash mid-write
	f.Close()
	startAgent(t, testConfig(h.fc.srv.URL, h.state, "", h.rootID, h.src, "worker"))
	rep := waitFor(t, h.fc.reportCh, 15*time.Second, "cleanup report")
	if rep.AttemptID != att || rep.CleanupState != "uncertain" {
		t.Fatalf("report %+v", rep)
	}
}

func TestControllerURLPolicy(t *testing.T) {
	for _, bad := range []string{"http://example.com", "ftp://127.0.0.1", "http://user:pw@127.0.0.1", "http://127.0.0.1/?x=1"} {
		if _, err := ValidateControllerURL(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	for _, good := range []string{"http://127.0.0.1:8080", "http://localhost:1", "https://codeye.example"} {
		if _, err := ValidateControllerURL(good); err != nil {
			t.Errorf("%s rejected: %v", good, err)
		}
	}
}

func TestSecretNotPresentedToOtherController(t *testing.T) {
	h := newHarness(t, "succeed")
	os.MkdirAll(h.state, 0700)
	enroll, _ := New(testConfig(h.fc.srv.URL, h.state, h.token, h.rootID, h.src, "worker"))
	if err := enroll.loadOrEnroll(context.Background()); err != nil {
		t.Fatal(err)
	}
	other, _ := New(testConfig("http://127.0.0.1:1", h.state, "", h.rootID, h.src, "worker"))
	var ee *ExitError
	if err := other.loadOrEnroll(context.Background()); !errors.As(err, &ee) || ee.Code != ExitUsage {
		t.Fatalf("err %v", err)
	}
}

func TestShutdownCancelsRunningAttemptWithRealCleanup(t *testing.T) {
	h := newHarness(t, "sleep")
	h.fc.addJob(h.job())
	_, cancel, done := startAgent(t, testConfig(h.fc.srv.URL, h.state, h.token, h.rootID, h.src, "worker"))
	p := waitStartedProc(t, h.state)
	cancel()
	c := waitFor(t, h.fc.completeCh, 30*time.Second, "complete")
	if c.Outcome != "failed" || c.Reason == nil || *c.Reason != "agent_shutdown" || c.CleanupState != "confirmed" {
		t.Fatalf("complete %+v", c)
	}
	if err := <-done; err != nil {
		t.Fatalf("shutdown error %v", err)
	}
	done <- nil
	if !groupEmpty(p.PGID) {
		t.Fatal("task survived agent shutdown")
	}
	j, _ := ReadJournal(filepath.Join(h.state, "journal.jsonl"))
	for _, w := range j.Workers {
		if !w.Exited {
			t.Fatalf("worker %s not journaled as exited", w.ID)
		}
	}
}

func TestSecondAgentOnSameStateDirRefused(t *testing.T) {
	h := newHarness(t, "succeed")
	startAgent(t, testConfig(h.fc.srv.URL, h.state, h.token, h.rootID, h.src, "worker-none"))
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(h.state, "node.json")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first agent did not enroll")
		}
		time.Sleep(50 * time.Millisecond)
	}
	before, _ := os.ReadFile(filepath.Join(h.state, "journal.jsonl"))
	second, _ := New(testConfig(h.fc.srv.URL, h.state, "", h.rootID, h.src, "worker-none"))
	err := second.Run(context.Background())
	var ee *ExitError
	if !errors.As(err, &ee) || !strings.Contains(ee.Msg, "in use") {
		t.Fatalf("second agent: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(h.state, "journal.jsonl"))
	if len(after) < len(before) {
		t.Fatal("second agent modified the journal")
	}
}
