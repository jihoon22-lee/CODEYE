//go:build linux

package run

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/workerproto"
)

// testHandler runs a fixed shell script via the runner (test-only).
type testHandler struct {
	script   string
	tasks    []analysis.TaskSpec
	claimOK  bool // claim success even when the run did not exit
	noReturn bool // do not return executions
}

func (h *testHandler) Info() analysis.HandlerInfo {
	return analysis.HandlerInfo{Name: "test", Version: "1", Capability: "analysis:deep-review"}
}
func (h *testHandler) Plan(context.Context, analysis.Job) ([]analysis.TaskSpec, error) {
	if h.tasks != nil {
		return h.tasks, nil
	}
	return []analysis.TaskSpec{{Key: "t.one", Required: true}}, nil
}
func (h *testHandler) Run(ctx context.Context, job analysis.Job, task analysis.TaskSpec, r executor.Runner) analysis.TaskResult {
	res := r.Run(ctx, executor.Spec{Executable: "/bin/sh", Args: []string{"-c", h.script}, WorkspaceRoot: job.Snapshot.WorkspaceDir, Dir: job.Snapshot.WorkspaceDir, Env: map[string]string{"PATH": "/usr/bin:/bin"}, Timeout: time.Minute})
	ex, err := analysis.WriteExecution(filepath.Join(job.Scratch, task.Key), "sh", res)
	out := analysis.TaskResult{Status: domain.TaskFailed, Reason: "handler_error"}
	if err == nil && !h.noReturn {
		out.Executions = []analysis.Execution{ex}
	}
	if h.claimOK || (res.Status == executor.StatusExited && res.ExitCode != nil && *res.ExitCode == 0) {
		out.Status, out.Reason = domain.TaskSucceeded, ""
	}
	if res.Status == executor.StatusCancelled {
		out.Status, out.Reason = domain.TaskCancelled, "cancelled"
	}
	return out
}

// fakeAgent is the agent side of the pipes, answering like the controller.
type fakeAgent struct {
	t    *testing.T
	conn *workerproto.Conn
	mu   sync.Mutex
	msgs []any
	// staleOn: reply stale_attempt to the first event of this kind.
	staleOn   string
	started   chan *workerproto.ProcessStarted
	complete  chan *workerproto.Complete
	ready     chan *workerproto.Ready
	artifacts map[string][]byte // client id → bytes
	roles     map[string]string
	media     map[string]string // client id → media type
	serverIDs map[string]string // artifact id the agent returned → client id
}

func startWorker(t *testing.T, h analysis.Handler) (*fakeAgent, io.WriteCloser, chan int, *syncWriter) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan int, 1)
	transcript := &syncWriter{b: &strings.Builder{}}
	go func() {
		code := Main(context.Background(), Config{In: inR, Out: outW, Handlers: func(context.Context, *workerproto.Hello, executor.Runner) ([]analysis.Handler, []string, error) {
			if h == nil {
				return nil, nil, nil
			}
			return []analysis.Handler{h}, nil, nil
		}})
		outW.Close()
		done <- code
	}()
	fa := &fakeAgent{t: t, started: make(chan *workerproto.ProcessStarted, 8), complete: make(chan *workerproto.Complete, 1), ready: make(chan *workerproto.Ready, 1), artifacts: map[string][]byte{}, roles: map[string]string{}, media: map[string]string{}, serverIDs: map[string]string{}}
	fa.conn = workerproto.NewConn(outR, inW)
	fa.conn.Transcript = transcript
	fa.conn.Start()
	go fa.loop()
	return fa, inW, done, transcript
}

type syncWriter struct {
	mu sync.Mutex
	b  *strings.Builder
}

func (s *syncWriter) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (fa *fakeAgent) loop() {
	for m := range fa.conn.Incoming() {
		fa.mu.Lock()
		fa.msgs = append(fa.msgs, m)
		fa.mu.Unlock()
		switch v := m.(type) {
		case *workerproto.Ready:
			fa.ready <- v
		case *workerproto.Intent:
			_ = fa.conn.ReplyOK(v.ID, nil)
		case *workerproto.ProcessStarted:
			_ = fa.conn.ReplyOK(v.ID, nil)
			fa.started <- v
		case *workerproto.ProcessEnded:
			_ = fa.conn.ReplyOK(v.ID, nil)
		case *workerproto.Event:
			if fa.staleOn != "" && fa.staleOn == v.Kind {
				fa.staleOn = ""
				_ = fa.conn.ReplyError(v.ID, workerproto.ErrStaleAttempt)
				continue
			}
			var resp nodeproto.EventResponse
			resp.Sequence = 1
			switch v.Kind {
			case nodeproto.EventSnapshotFixed:
				id := domain.NewID()
				resp.SnapshotID = &id
			case nodeproto.EventTaskCreated:
				id, ver := domain.NewID(), int64(1)
				resp.TaskID, resp.Version = &id, &ver
			case nodeproto.EventTaskStatus:
				var p nodeproto.TaskStatusPayload
				_ = json.Unmarshal(v.Payload, &p)
				ver := p.ExpectedVersion + 1
				resp.Version = &ver
			}
			_ = fa.conn.ReplyOK(v.ID, resp)
		case *workerproto.Artifact:
			b, err := os.ReadFile(v.Path)
			sum := sha256.Sum256(b)
			if err != nil || hex.EncodeToString(sum[:]) != v.SHA256 || int64(len(b)) != v.SizeBytes {
				_ = fa.conn.ReplyError(v.ID, workerproto.ErrIntegrityMismatch)
				continue
			}
			fa.mu.Lock()
			fa.artifacts[v.ClientArtifactID] = b
			fa.roles[v.ClientArtifactID] = v.Role
			fa.media[v.ClientArtifactID] = v.MediaType
			sid := domain.NewID()
			fa.serverIDs[sid] = v.ClientArtifactID
			fa.mu.Unlock()
			_ = fa.conn.ReplyOK(v.ID, map[string]string{"artifact_id": sid})
		case *workerproto.Complete:
			_ = fa.conn.ReplyOK(v.ID, nil)
			fa.complete <- v
		}
	}
}

func (fa *fakeAgent) kinds() []string {
	fa.mu.Lock()
	defer fa.mu.Unlock()
	var out []string
	for _, m := range fa.msgs {
		switch v := m.(type) {
		case *workerproto.Event:
			out = append(out, "event:"+v.Kind)
		case *workerproto.Intent:
			out = append(out, "intent:"+v.Kind)
		case *workerproto.Artifact:
			out = append(out, "artifact:"+v.Role)
		case *workerproto.Complete:
			out = append(out, "complete")
		}
	}
	return out
}

type env struct {
	state, data, scratch, src string
}

func newEnv(t *testing.T) env {
	base := t.TempDir()
	e := env{state: filepath.Join(base, "state"), src: filepath.Join(base, "src")}
	e.data, e.scratch = filepath.Join(e.state, "data"), filepath.Join(e.state, "scratch")
	for _, d := range []string{e.data, e.scratch, e.src} {
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(e.src, "main.go"), []byte("package main\n"), 0600)
	return e
}

func (fa *fakeAgent) hello(e env) {
	if err := fa.conn.Send(&workerproto.Hello{Protocol: workerproto.Protocol, WorkerID: domain.NewID(), DataRoot: e.data, ScratchRoot: e.scratch}); err != nil {
		fa.t.Fatal(err)
	}
	select {
	case <-fa.ready:
	case <-time.After(5 * time.Second):
		fa.t.Fatal("no ready")
	}
}

func localJob(e env) *workerproto.Job {
	return &workerproto.Job{Attempt: workerproto.AttemptRef{ID: domain.NewID(), RunID: domain.NewID(), Epoch: 1}, Profile: "deep-review", Scope: json.RawMessage(`{"kind":"full"}`), Source: workerproto.JobSource{Kind: "local", ApprovedRoot: e.src, RelativePath: "."}}
}

func waitComplete(t *testing.T, fa *fakeAgent) *workerproto.Complete {
	t.Helper()
	select {
	case c := <-fa.complete:
		return c
	case <-time.After(30 * time.Second):
		t.Fatalf("no complete; messages %v", fa.kinds())
	}
	return nil
}

func TestJobSucceedsWithManifestAndOrderedEvents(t *testing.T) {
	e := newEnv(t)
	fa, in, done, transcript := startWorker(t, &testHandler{script: "echo ok; ls"})
	fa.hello(e)
	job := localJob(e)
	if err := fa.conn.Send(job); err != nil {
		t.Fatal(err)
	}
	c := waitComplete(t, fa)
	if c.Outcome != "succeeded" || c.CleanupState != "confirmed" || c.CleanupMethod != "process_group_empty" || c.ResultManifestClientArtifact == nil {
		t.Fatalf("complete %+v", c)
	}
	want := []string{"intent:source_acquire_started", "intent:source_acquire_done", "artifact:source_manifest", "event:snapshot_fixed", "event:task_created", "event:run_advance", "event:task_status", "intent:task_exec_started", "artifact:stdout", "artifact:stderr", "event:task_status", "event:run_advance", "artifact:result_manifest", "complete"}
	if got := strings.Join(fa.kinds(), ","); got != strings.Join(want, ",") {
		t.Fatalf("sequence\n got %s\nwant %s", got, strings.Join(want, ","))
	}
	fa.mu.Lock()
	manifest := fa.artifacts[*c.ResultManifestClientArtifact]
	fa.mu.Unlock()
	var m analysis.ResultManifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Tasks) != 1 || m.Tasks[0].Status != "succeeded" || len(m.Tasks[0].Executions) != 1 || m.Tasks[0].Executions[0].Command.Dir != "." || m.CleanupState != "confirmed" {
		t.Fatalf("manifest %s", manifest)
	}
	if strings.Contains(string(manifest), e.state) || strings.Contains(string(manifest), e.src) {
		t.Fatalf("manifest leaks node paths: %s", manifest)
	}
	if canon, _ := domain.CanonicalJSON(manifest); string(canon) != string(manifest) {
		t.Fatal("manifest not canonical")
	}
	// Attempt-private scratch is removed after confirmed cleanup; the snapshot stays.
	if _, err := os.Stat(filepath.Join(e.scratch, job.Attempt.ID)); !os.IsNotExist(err) {
		t.Fatalf("scratch kept: %v", err)
	}
	in.Close()
	if code := <-done; code != 0 {
		t.Fatalf("exit %d", code)
	}
	if strings.Contains(transcript.String(), "node_secret") {
		t.Fatal("transcript mentions secret")
	}
}

func groupAlive(pgid int) bool {
	return syscall.Kill(-pgid, 0) == nil
}

func TestCancelKillsGrandchildAndReportsCancelled(t *testing.T) {
	e := newEnv(t)
	fa, in, done, _ := startWorker(t, &testHandler{script: "sleep 300 & sleep 300; wait"})
	defer in.Close()
	fa.hello(e)
	fa.conn.Send(localJob(e))
	var ps *workerproto.ProcessStarted
	select {
	case ps = <-fa.started:
	case <-time.After(15 * time.Second):
		t.Fatal("process not started")
	}
	if ps.PGID != ps.PID || ps.StartTime == 0 {
		t.Fatalf("identity %+v", ps)
	}
	time.Sleep(200 * time.Millisecond)
	fa.conn.Send(&workerproto.Cancel{Reason: workerproto.CancelRunRequested})
	c := waitComplete(t, fa)
	if c.Outcome != "cancelled" || c.CleanupState != "confirmed" || c.Reason == nil || *c.Reason != "cancelled" || c.ResultManifestClientArtifact != nil {
		t.Fatalf("complete %+v", c)
	}
	if groupAlive(ps.PGID) {
		t.Fatal("process group survived cancel")
	}
	in.Close()
	<-done
}

func TestStaleReplyCancelsAsLeaseLost(t *testing.T) {
	e := newEnv(t)
	fa, in, done, _ := startWorker(t, &testHandler{script: "echo x"})
	fa.staleOn = nodeproto.EventTaskStatus
	fa.hello(e)
	fa.conn.Send(localJob(e))
	c := waitComplete(t, fa)
	if c.Outcome != "failed" || c.Reason == nil || *c.Reason != "lease_lost" {
		t.Fatalf("complete %+v", c)
	}
	for _, k := range fa.kinds() {
		if k == "intent:task_exec_started" {
			t.Fatal("task executed after lease loss")
		}
	}
	in.Close()
	<-done
}

func TestStdinEOFCancelsRunningTask(t *testing.T) {
	e := newEnv(t)
	fa, in, done, _ := startWorker(t, &testHandler{script: "sleep 300 & sleep 300; wait"})
	fa.hello(e)
	fa.conn.Send(localJob(e))
	ps := <-fa.started
	in.Close()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("worker did not exit on EOF")
	}
	if groupAlive(ps.PGID) {
		t.Fatal("process group survived agent EOF")
	}
}

func TestNoHandlerAdvertisesNothing(t *testing.T) {
	e := newEnv(t)
	fa, in, done, _ := startWorker(t, nil)
	if err := fa.conn.Send(&workerproto.Hello{Protocol: workerproto.Protocol, WorkerID: domain.NewID(), DataRoot: e.data, ScratchRoot: e.scratch}); err != nil {
		t.Fatal(err)
	}
	r := <-fa.ready
	if len(r.Handlers) != 0 {
		t.Fatalf("handlers %+v", r.Handlers)
	}
	in.Close()
	<-done
}

func TestHandlerCannotClaimSuccessForCancelledRun(t *testing.T) {
	e := newEnv(t)
	// The handler claims success but its process was killed by a signal.
	fa, in, done, _ := startWorker(t, &testHandler{script: "kill -9 $$", claimOK: true})
	fa.hello(e)
	fa.conn.Send(localJob(e))
	c := waitComplete(t, fa)
	if c.Outcome != "failed" {
		t.Fatalf("complete %+v", c)
	}
	in.Close()
	<-done
}

func TestUnreportedExecutionPublishesNoOutput(t *testing.T) {
	e := newEnv(t)
	fa, in, done, _ := startWorker(t, &testHandler{script: "echo SECRET-REASONING", noReturn: true})
	fa.hello(e)
	fa.conn.Send(localJob(e))
	c := waitComplete(t, fa)
	if c.CleanupState != "confirmed" {
		t.Fatalf("complete %+v", c)
	}
	fa.mu.Lock()
	defer fa.mu.Unlock()
	for id, b := range fa.artifacts {
		if strings.Contains(string(b), "SECRET-REASONING") {
			t.Fatalf("unreported output published as %s", fa.roles[id])
		}
	}
	in.Close()
	<-done
}

func TestPinnedRetryNeverReacquires(t *testing.T) {
	e := newEnv(t)
	fa, in, done, _ := startWorker(t, &testHandler{script: "echo x"})
	fa.hello(e)
	job := localJob(e)
	job.Source.ApprovedRoot = "/nonexistent-should-not-be-read"
	job.Snapshot = &workerproto.JobSnapshot{ID: domain.NewID(), ManifestDigest: strings.Repeat("a", 64), Root: ""}
	fa.conn.Send(job)
	c := waitComplete(t, fa)
	if c.Outcome != "failed" || c.Reason == nil || *c.Reason != "snapshot_unavailable" {
		t.Fatalf("complete %+v", c)
	}
	in.Close()
	<-done
}

// W09: a failed attempt whose tasks actually ran still uploads the result
// manifest (real task states, streams), never advances to finalizing and
// stays failed.
func TestFailedTasksUploadManifestWithoutFinalizing(t *testing.T) {
	e := newEnv(t)
	fa, in, done, _ := startWorker(t, &testHandler{script: "echo broken >&2; exit 3"})
	fa.hello(e)
	fa.conn.Send(localJob(e))
	c := waitComplete(t, fa)
	if c.Outcome != "failed" || c.Reason == nil || *c.Reason != "handler_error" || c.ResultManifestClientArtifact == nil {
		t.Fatalf("complete %+v", c)
	}
	kinds := strings.Join(fa.kinds(), ",")
	if strings.Count(kinds, "event:run_advance") != 1 || !strings.HasSuffix(kinds, "artifact:result_manifest,complete") {
		t.Fatalf("sequence %s", kinds)
	}
	fa.mu.Lock()
	manifest := fa.artifacts[*c.ResultManifestClientArtifact]
	fa.mu.Unlock()
	var m analysis.ResultManifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Tasks) != 1 || m.Tasks[0].Status != "failed" || len(m.Tasks[0].Executions) != 1 || m.Tasks[0].Executions[0].ExitCode == nil || *m.Tasks[0].Executions[0].ExitCode != 3 {
		t.Fatalf("manifest %s", manifest)
	}
	in.Close()
	<-done
}

// W15: the planned stage travels as the optional task_created "stage"; a task
// without one omits the field (stored without a stage, never inferred).
func TestTaskCreatedCarriesPlannedStage(t *testing.T) {
	e := newEnv(t)
	fa, in, done, _ := startWorker(t, &testHandler{script: "true", tasks: []analysis.TaskSpec{
		{Key: "t.vet", Required: true, Stage: domain.StageStaticAnalysis},
		{Key: "t.legacy", Required: true},
	}})
	fa.hello(e)
	fa.conn.Send(localJob(e))
	if c := waitComplete(t, fa); c.Outcome != "succeeded" {
		t.Fatalf("complete %+v", c)
	}
	fa.mu.Lock()
	var payloads []string
	for _, m := range fa.msgs {
		if v, ok := m.(*workerproto.Event); ok && v.Kind == nodeproto.EventTaskCreated {
			payloads = append(payloads, string(v.Payload))
		}
	}
	fa.mu.Unlock()
	want := []string{`{"key":"t.vet","required":true,"stage":"static_analysis"}`, `{"key":"t.legacy","required":true}`}
	if strings.Join(payloads, "\n") != strings.Join(want, "\n") {
		t.Fatalf("task_created payloads\n got %v\nwant %v", payloads, want)
	}
	in.Close()
	<-done
}

// An unknown stage is a handler error before any task is declared.
func TestUnknownStageFailsBeforeTaskCreated(t *testing.T) {
	e := newEnv(t)
	fa, in, done, _ := startWorker(t, &testHandler{script: "true", tasks: []analysis.TaskSpec{{Key: "t.one", Required: true, Stage: "lint"}}})
	fa.hello(e)
	fa.conn.Send(localJob(e))
	c := waitComplete(t, fa)
	if c.Outcome != "failed" || c.Reason == nil || *c.Reason != "handler_error" {
		t.Fatalf("complete %+v", c)
	}
	if kinds := strings.Join(fa.kinds(), ","); strings.Contains(kinds, "event:task_created") {
		t.Fatalf("declared a task with an unknown stage: %s", kinds)
	}
	in.Close()
	<-done
}
