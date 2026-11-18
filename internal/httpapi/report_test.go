package httpapi

import (
	"context"
	"encoding/json"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/reportsvc"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// W09 report publication over real HTTP and PostgreSQL. Artifacts are the
// real W08 observations document (examples/observations-go-vet.json) bound to
// bytes uploaded through the node protocol. Skips (NOT executed) without
// CODEYE_TEST_DATABASE_URL.

type attemptRun struct {
	f                 nodeFixture
	runID, attemptID  string
	node              nodeproto.EnrollResponse
	worker            string
	sec               nreq
	sourceManifestSHA string
	message           string
	resultManifestID  string
}

func canonicalJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	c, err := domain.CanonicalJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// driveAttempt runs one attempt of a go-vet task through the node protocol
// and completes it with taskStatus (succeeded → finalizing; failed → failed
// outcome that still uploads the result manifest).
func (e *env) driveAttempt(taskStatus string) attemptRun {
	e.t.Helper()
	return e.drive(nil, driveOpts{taskStatus: taskStatus})
}

// driveOpts varies one driven attempt (W23 runs several in one project).
type driveOpts struct {
	taskStatus string
	scope      map[string]any       // nil: full
	edit       func(map[string]any) // edits the observations document before upload
}

func (e *env) drive(f *nodeFixture, o driveOpts) attemptRun {
	t := e.t
	t.Helper()
	taskStatus := o.taskStatus
	if f == nil {
		fx := e.nodeFixture()
		f = &fx
	}
	scope := o.scope
	if scope == nil {
		scope = map[string]any{"kind": "full"}
	}
	a := attemptRun{f: *f, worker: domain.NewID()}
	run := e.do("POST", "/api/v1/projects/"+a.f.projectID+"/runs", map[string]any{"profile": "deep-review", "source_policy": "latest_at_acquire", "scope": scope},
		opts{cookie: a.f.user.cookie, csrf: a.f.user.csrf, idem: domain.NewID()}).want(t, 202, "")
	a.runID = run.body["id"].(string)
	a.node = e.enrollNode(a.f.root.ID)
	a.sec = nreq{secret: a.node.NodeSecret}
	caps := []string{nodeproto.AnalysisCapability("deep-review"), nodeproto.LocalSourceCapability(a.f.root.ID)}
	e.node("POST", "/api/v1/nodes/"+a.node.NodeID+"/heartbeat", heartbeatBody([]string{a.worker}, caps), a.sec).want(t, 200, "")
	cl := e.node("POST", "/api/v1/nodes/"+a.node.NodeID+"/claims", nodeproto.ClaimRequest{WorkerID: a.worker}, nreq{secret: a.node.NodeSecret, header: map[string]string{"Idempotency-Key": domain.NewID()}}).want(t, 200, "")
	var claim nodeproto.ClaimResponse
	_ = json.Unmarshal(cl.raw, &claim)
	a.attemptID = claim.Lease.AttemptID
	att := "/api/v1/attempts/" + a.attemptID
	put := func(role, media string, body []byte) string {
		o := nreq{secret: a.node.NodeSecret, ctype: media, header: map[string]string{nodeproto.HeaderWorkerID: a.worker, nodeproto.HeaderLeaseEpoch: "1", nodeproto.HeaderArtifactRole: role, nodeproto.HeaderContentSHA256: domain.Digest(body)}}
		return e.node("PUT", att+"/artifacts/"+domain.NewID(), body, o).want(t, 201, "").body["artifact_id"].(string)
	}
	ev := func(kind string, payload any) resp {
		return e.node("POST", att+"/events", map[string]any{"worker_id": a.worker, "lease_epoch": 1, "client_event_id": domain.NewID(), "kind": kind, "payload": payload}, a.sec).want(t, 200, "")
	}
	raw, err := os.ReadFile("../../examples/observations-go-vet.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	_ = json.Unmarshal(raw, &doc)
	entries := map[string]string{"go.mod": strings.Repeat("a", 64)}
	for _, o := range doc["observations"].([]any) {
		if loc, ok := o.(map[string]any)["location"].(map[string]any); ok {
			entries[loc["path"].(string)] = loc["blob_sha256"].(string)
		}
	}
	list := []any{}
	for _, p := range []string{"bad/bad.go", "bad/bad_test.go", "bad/x_test.go", "go.mod"} {
		list = append(list, map[string]any{"path": p, "kind": "file", "size_bytes": 10, "mode": 420, "sha256": entries[p], "encoding": "utf-8"})
	}
	src := canonicalJSON(t, map[string]any{"schema_version": "0.1", "kind": "local", "consistency": "stable_copy_best_effort", "source_fixed_at": "2026-11-18T00:00:00Z", "entries": list, "limitations": []any{"local_copy_is_not_atomic"}})
	a.sourceManifestSHA = domain.Digest(src)
	mid := put("source_manifest", "application/json", src)
	snap := ev("snapshot_fixed", map[string]any{"manifest_digest": a.sourceManifestSHA, "kind": "local", "consistency": "stable_copy_best_effort", "source_sha": nil, "manifest_artifact_id": mid})
	task := ev("task_created", map[string]any{"key": "go-vet", "required": true})
	taskID := task.body["task_id"].(string)
	ev("run_advance", map[string]any{"status": "running"})
	st := ev("task_status", map[string]any{"task_id": taskID, "expected_version": 1, "status": "running", "reason": nil})
	var reason any
	if taskStatus != "succeeded" {
		reason = "package_errors"
	}
	ev("task_status", map[string]any{"task_id": taskID, "expected_version": st.body["version"], "status": taskStatus, "reason": reason})
	// Raw go vet stdout bytes; the document is re-bound to their hash.
	stdout := []byte(strings.Repeat("{}\n", 400))
	stdoutID := put("stdout", "text/plain", stdout)
	stderrID := put("stderr", "text/plain", []byte(""))
	vetSHA := domain.Digest(stdout)
	doc["task_key"], doc["snapshot_manifest_digest"] = "go-vet", a.sourceManifestSHA
	for _, x := range doc["executions"].([]any) {
		if ex := x.(map[string]any); ex["name"] == "go.vet" {
			stream := ex["stdout"].(map[string]any)
			stream["sha256"], stream["captured_bytes"], stream["total_bytes"] = vetSHA, len(stdout), len(stdout)
		}
	}
	for _, o := range doc["observations"].([]any) {
		m := o.(map[string]any)
		m["raw_ref"].(map[string]any)["stream_sha256"] = vetSHA
		a.message = m["raw_message"].(string)
	}
	if o.edit != nil {
		o.edit(doc)
	}
	docBody := canonicalJSON(t, doc)
	docID := put("observations", analysis.MediaObservations, docBody)
	if taskStatus == "succeeded" {
		ev("run_advance", map[string]any{"status": "finalizing"})
	}
	code := 0
	ts := domain.TaskStatus(taskStatus)
	var treason *string
	if r, ok := reason.(string); ok {
		treason = &r
	}
	rm := analysis.ResultManifest{SchemaVersion: "0.1", RunID: a.runID, AttemptID: a.attemptID, LeaseEpoch: 1,
		Snapshot: analysis.ManifestSnapshot{ID: *snapshotID(snap), ManifestDigest: a.sourceManifestSHA},
		Handlers: []analysis.ManifestHandler{{Name: "go-vet", Version: "1", Capability: "analysis:deep-review"}}, CleanupState: "confirmed", Limitations: []string{},
		Tasks: []analysis.ManifestTask{{TaskID: taskID, Key: "go-vet", Required: true, Status: ts, Reason: treason, Limitations: []string{},
			Executions: []analysis.ManifestExecution{{Name: "go.vet", Status: executor.StatusExited, ExitCode: &code, Cleanup: executor.CleanupConfirmed,
				Command: analysis.ManifestCommand{Argv: []string{"go", "vet", "-json", "./..."}, Dir: ".", EnvKeys: []string{"PATH"}}, StdoutArtifactID: &stdoutID, StderrArtifactID: &stderrID,
				StartedAt: "2026-11-18T00:00:00Z", EndedAt: "2026-11-18T00:00:01Z"}},
			Artifacts: []analysis.ManifestArtifact{{ArtifactID: docID, Role: "observations", MediaType: analysis.MediaObservations, SHA256: domain.Digest(docBody), SizeBytes: int64(len(docBody))}}}}}
	body, err := rm.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	a.resultManifestID = put("result_manifest", analysis.MediaResultManifest, body)
	outcome := "succeeded"
	var creason *string
	if taskStatus != "succeeded" {
		outcome, creason = "failed", treason
	}
	e.node("POST", att+"/complete", nodeproto.CompleteRequest{WorkerID: a.worker, LeaseEpoch: 1, Outcome: outcome, CleanupState: "confirmed", ResultManifestID: &a.resultManifestID, Reason: creason}, a.sec).want(t, 202, "")
	return a
}

func snapshotID(r resp) *string {
	s, _ := r.body["snapshot_id"].(string)
	return &s
}

func (e *env) runStatus(id string) (string, int64) {
	var s string
	var v int64
	if err := e.pool.QueryRow(context.Background(), "SELECT status,version FROM runs WHERE id=$1", id).Scan(&s, &v); err != nil {
		e.t.Fatal(err)
	}
	return s, v
}

// TC-83: success stays finalizing until the report is durably published; the
// publish transaction settles the run; reconcile is idempotent.
func TestReportPublicationSettlesFinalizingRun(t *testing.T) {
	e := newEnv(t)
	a := e.driveAttempt("succeeded")
	if s, _ := e.runStatus(a.runID); s != "finalizing" {
		t.Fatalf("run %s before publication", s)
	}
	e.do("GET", "/api/v1/runs/"+a.runID+"/report", nil, a.f.user.o()).want(t, 404, "NOT_FOUND")
	ctx := context.Background()
	out, err := e.srv.publisher.PublishRun(ctx, a.runID)
	if err != nil || out != reportsvc.OutcomePublished {
		t.Fatalf("publish %s %v", out, err)
	}
	if s, _ := e.runStatus(a.runID); s != "partial" {
		t.Fatalf("run %s after publication (AI review absent must be partial)", s)
	}
	// A second pass and a concurrent reconcile publish nothing more.
	if n, err := e.srv.publisher.Reconcile(ctx); err != nil || n != 0 {
		t.Fatalf("reconcile again %d %v", n, err)
	}
	var reports, events int
	_ = e.pool.QueryRow(ctx, "SELECT count(*) FROM reports WHERE run_id=$1", a.runID).Scan(&reports)
	_ = e.pool.QueryRow(ctx, "SELECT count(*) FROM events WHERE run_id=$1 AND type='report.published'", a.runID).Scan(&events)
	if reports != 1 || events != 1 {
		t.Fatalf("reports %d events %d", reports, events)
	}
	// The worker's retried complete still replays after publication.
	e.node("POST", "/api/v1/attempts/"+a.attemptID+"/complete", nodeproto.CompleteRequest{WorkerID: a.worker, LeaseEpoch: 1, Outcome: "succeeded", CleanupState: "confirmed", ResultManifestID: &a.resultManifestID}, a.sec).want(t, 202, "")
	v := e.do("GET", "/api/v1/runs/"+a.runID, nil, a.f.user.o()).want(t, 200, "")
	rep := v.body["report"].(map[string]any)
	if v.body["quality_verdict"] != "inconclusive" || rep["revision"] != float64(1) || rep["status"] != "partial" {
		t.Fatalf("run view %s", v.raw)
	}
	p := e.do("GET", "/api/v1/projects/"+a.f.projectID, nil, a.f.user.o()).want(t, 200, "")
	for _, g := range p.body["capability_gaps"].([]any) {
		if g == "report_unavailable" {
			t.Fatalf("gap kept after publication: %s", p.raw)
		}
	}
	// Stored rows are immutable.
	if _, err := e.pool.Exec(ctx, "UPDATE reports SET status='completed' WHERE run_id=$1", a.runID); err == nil {
		t.Fatal("report row was mutable")
	}
}

// TC-83: concurrent publishers produce one report.
func TestConcurrentPublishersPublishOnce(t *testing.T) {
	e := newEnv(t)
	a := e.driveAttempt("succeeded")
	other := &reportsvc.Publisher{Store: e.store, Artifacts: e.srv.artifacts, Registry: report.DefaultRegistry()}
	var wg sync.WaitGroup
	outs := make([]reportsvc.Outcome, 8)
	for i := range outs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := e.srv.publisher
			if i%2 == 1 {
				p = other
			}
			outs[i], _ = p.PublishRun(context.Background(), a.runID)
		}(i)
	}
	wg.Wait()
	published := 0
	for _, o := range outs {
		if o == reportsvc.OutcomePublished {
			published++
		}
	}
	var n int
	_ = e.pool.QueryRow(context.Background(), "SELECT count(*) FROM reports WHERE run_id=$1", a.runID).Scan(&n)
	if published != 1 || n != 1 {
		t.Fatalf("published %d rows %d (%v)", published, n, outs)
	}
}

// TC-83: a publication built for a state that changed is refused (stale), and
// a cancel request after completion is settled as cancelled, not published over.
func TestStalePublicationRefusedAndCancelSettled(t *testing.T) {
	e := newEnv(t)
	a := e.driveAttempt("succeeded")
	ctx := context.Background()
	body := []byte(`{}`)
	stale := postgres.ReportPublication{ReportID: domain.NewID(), RunID: a.runID, AttemptID: a.attemptID, Epoch: 1, ResultManifestID: &a.resultManifestID, InputDigest: strings.Repeat("1", 64),
		Revision: 1, SchemaVersion: "0.1", BuilderVersion: "1", Status: "partial", QualityVerdict: "inconclusive", Body: body, ExpectRunStatus: domain.RunFinalizing}
	e.do("POST", "/api/v1/runs/"+a.runID+"/cancel", nil, a.f.user.o()).want(t, 202, "")
	if _, _, err := e.store.PublishReport(ctx, stale); err == nil || !strings.Contains(err.Error(), domain.ErrStaleAttempt.Error()) {
		t.Fatalf("stale publication: %v", err)
	}
	wrongEpoch := stale
	wrongEpoch.Epoch, wrongEpoch.ExpectRunStatus = 2, domain.RunCancelRequested
	if _, _, err := e.store.PublishReport(ctx, wrongEpoch); err == nil {
		t.Fatal("wrong epoch accepted")
	}
	out, err := e.srv.publisher.PublishRun(ctx, a.runID)
	if err != nil || out != reportsvc.OutcomePublished {
		t.Fatalf("settle %s %v", out, err)
	}
	if s, _ := e.runStatus(a.runID); s != "cancelled" {
		t.Fatalf("run %s", s)
	}
	r := e.do("GET", "/api/v1/runs/"+a.runID+"/report", nil, a.f.user.o()).want(t, 200, "")
	if r.body["status"] != "cancelled" || r.body["quality_verdict"] != "inconclusive" {
		t.Fatalf("report %s", r.raw)
	}
}

// TC-83: invalid input is rejected once (run failed, no retry loop); the
// worker's completion receipt still replays.
func TestRejectedPublicationFailsRunOnce(t *testing.T) {
	e := newEnv(t)
	a := e.driveAttempt("succeeded")
	// Same-size tampered source manifest bytes in the artifact store.
	path := filepath.Join(e.artifacts, "sha256", a.sourceManifestSHA[:2], a.sourceManifestSHA)
	b, _ := os.ReadFile(path)
	b[len(b)-2] ^= 1
	_ = os.Chmod(path, 0o600)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := e.srv.publisher.PublishRun(context.Background(), a.runID)
	if err != nil || out != reportsvc.OutcomeRejected {
		t.Fatalf("publish %s %v", out, err)
	}
	if s, _ := e.runStatus(a.runID); s != "failed" {
		t.Fatalf("run %s", s)
	}
	if ids, _ := e.store.PendingReports(context.Background(), 10); len(ids) != 0 {
		t.Fatalf("rejected run still pending: %v", ids)
	}
	var reason string
	_ = e.pool.QueryRow(context.Background(), "SELECT reason FROM report_rejections WHERE run_id=$1", a.runID).Scan(&reason)
	if reason != "source_manifest_integrity_failed" {
		t.Fatalf("reason %q", reason)
	}
	e.node("POST", "/api/v1/attempts/"+a.attemptID+"/complete", nodeproto.CompleteRequest{WorkerID: a.worker, LeaseEpoch: 1, Outcome: "succeeded", CleanupState: "confirmed", ResultManifestID: &a.resultManifestID}, a.sec).want(t, 202, "")
}

// TC-83: a failed attempt with a result manifest gets a report-only
// publication; the run stays failed and its version does not change.
func TestFailedRunReportOnly(t *testing.T) {
	e := newEnv(t)
	a := e.driveAttempt("failed")
	s, version := e.runStatus(a.runID)
	if s != "failed" {
		t.Fatalf("run %s", s)
	}
	if out, err := e.srv.publisher.PublishRun(context.Background(), a.runID); err != nil || out != reportsvc.OutcomePublished {
		t.Fatalf("publish %s %v", out, err)
	}
	if s2, v2 := e.runStatus(a.runID); s2 != "failed" || v2 != version {
		t.Fatalf("run changed %s %d->%d", s2, version, v2)
	}
	r := e.do("GET", "/api/v1/runs/"+a.runID+"/report?include_code=true", nil, a.f.user.o()).want(t, 200, "")
	if r.body["status"] != "failed" || r.body["quality_verdict"] != "inconclusive" || len(r.body["observations"].([]any)) != 5 {
		t.Fatalf("failed report %s", r.raw)
	}
	tasks := r.body["tasks"].([]any)
	if tasks[0].(map[string]any)["status"] != "failed" || tasks[0].(map[string]any)["reason"] != "package_errors" {
		t.Fatalf("tasks %v", tasks)
	}
}

// TC-84: ACL, formats, code-free default, revision selection and headers.
func TestReportAPIFormatsAndACL(t *testing.T) {
	e := newEnv(t)
	a := e.driveAttempt("succeeded")
	if _, err := e.srv.publisher.PublishRun(context.Background(), a.runID); err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/runs/" + a.runID + "/report"
	u := a.f.user.o()
	def := e.do("GET", base, nil, u).want(t, 200, "")
	if def.body["export"].(map[string]any)["include_code"] != false || strings.Contains(string(def.raw), a.message) {
		t.Fatalf("default export is not code-free: %s", def.raw)
	}
	if def.header.Get("Cache-Control") != "private, no-store" || def.header.Get("X-Content-Type-Options") != "nosniff" || def.header.Get("ETag") != `"`+domain.Digest(def.raw)+`"` {
		t.Fatalf("headers %v", def.header)
	}
	full := e.do("GET", base+"?include_code=true&revision=1&format=json", nil, u).want(t, 200, "")
	if !strings.Contains(string(full.raw), a.message) || full.header.Get("X-CODEYE-Report-SHA256") != domain.Digest(full.raw) {
		t.Fatal("full export lacks evidence or digest")
	}
	var stored []byte
	_ = e.pool.QueryRow(context.Background(), "SELECT body FROM reports WHERE run_id=$1", a.runID).Scan(&stored)
	if string(stored) != string(full.raw) {
		t.Fatal("include_code json is not the stored canonical bytes")
	}
	md := e.do("GET", base+"?format=markdown", nil, u).want(t, 200, "")
	if !strings.HasPrefix(md.header.Get("Content-Type"), "text/markdown") || !strings.Contains(md.header.Get("Content-Disposition"), "attachment") || strings.Contains(string(md.raw), a.message) {
		t.Fatalf("markdown %v", md.header)
	}
	html := e.do("GET", base+"?format=html&include_code=true", nil, u).want(t, 200, "")
	if html.header.Get("Content-Security-Policy") != report.HTMLContentSecurityPolicy || !strings.Contains(html.header.Get("Content-Disposition"), "attachment") || !strings.Contains(string(html.raw), html.header.Get("X-CODEYE-Report-SHA256")) {
		t.Fatalf("html headers %v", html.header)
	}
	for _, response := range []resp{md, html} {
		if response.header.Get("ETag") != `"`+domain.Digest(response.raw)+`"` {
			t.Fatal("ETag must identify the rendered representation")
		}
	}
	for _, bad := range []string{"?format=pdf", "?revision=0", "?include_code=yes", "?other=1"} {
		e.do("GET", base+bad, nil, u).want(t, 400, "INVALID_REQUEST")
	}
	e.do("GET", base+"?revision=2", nil, u).want(t, 404, "NOT_FOUND")
	list := e.do("GET", "/api/v1/runs/"+a.runID+"/reports", nil, u).want(t, 200, "")
	if list.body["latest_revision"] != float64(1) || len(list.body["formats"].([]any)) != 3 || list.body["default_include_code"] != false {
		t.Fatalf("list %s", list.raw)
	}
	// Non-member: 404 for report and list (existence hidden); viewer: allowed.
	stranger := e.newUser()
	e.do("GET", base, nil, stranger.o()).want(t, 404, "NOT_FOUND")
	e.do("GET", "/api/v1/runs/"+a.runID+"/reports", nil, stranger.o()).want(t, 404, "NOT_FOUND")
	if err := e.store.SetMember(context.Background(), a.f.user.id, a.f.projectID, stranger.id, domain.RoleViewer); err != nil {
		t.Fatal(err)
	}
	e.do("GET", base+"?include_code=true", nil, stranger.o()).want(t, 200, "")
	// Revocation takes effect on the next request.
	if err := e.store.SetMember(context.Background(), a.f.user.id, a.f.projectID, stranger.id, ""); err != nil {
		t.Fatal(err)
	}
	e.do("GET", base, nil, stranger.o()).want(t, 404, "NOT_FOUND")
	e.do("GET", base, nil, opts{}).want(t, 401, "UNAUTHENTICATED")
}

// TC-83: a controller started after the attempt completed publishes pending
// reports from its startup reconcile (restart recovery).
func TestServeReconcilesPendingReportsOnStart(t *testing.T) {
	e := newEnv(t)
	a := e.driveAttempt("succeeded")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(Config{Listen: netip.MustParseAddrPort(ln.Addr().String()), ArtifactDir: e.artifacts, SweepInterval: -1, PublishInterval: time.Hour}, e.store)
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()
	deadline := time.Now().Add(10 * time.Second)
	status := ""
	for time.Now().Before(deadline) {
		if status, _ = e.runStatus(a.runID); status == "partial" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if status != "partial" {
		t.Fatalf("startup reconcile did not publish: %s", status)
	}
}
