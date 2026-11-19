package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

// Node routes over real HTTP and PostgreSQL. Skips (NOT executed) without
// CODEYE_TEST_DATABASE_URL.

type nreq struct {
	secret, origin, ctype string
	header                map[string]string
	chunked               bool
}

func (e *env) node(method, path string, body any, o nreq) resp {
	e.t.Helper()
	var raw []byte
	switch b := body.(type) {
	case nil:
	case []byte:
		raw = b
	case string:
		raw = []byte(b)
	default:
		raw, _ = json.Marshal(b)
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(raw)
		if o.chunked {
			rd = io.MultiReader(bytes.NewReader(raw))
		}
	}
	req, err := http.NewRequest(method, e.hs.URL+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	req.Host = e.host
	if body != nil {
		ct := "application/json"
		if o.ctype != "" {
			ct = o.ctype
		}
		req.Header.Set("Content-Type", ct)
	}
	if o.secret != "" {
		req.Header.Set("Authorization", "Bearer "+o.secret)
	}
	if o.origin != "" {
		req.Header.Set("Origin", o.origin)
	}
	for k, v := range o.header {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	r := resp{status: res.StatusCode, header: res.Header, raw: out}
	_ = json.Unmarshal(out, &r.body)
	return r
}

type nodeFixture struct {
	user      user
	root      postgres.SourceRoot
	projectID string
}

func (e *env) nodeFixture() nodeFixture {
	e.t.Helper()
	ctx := context.Background()
	u := e.newUser()
	root, err := e.store.AddSourceRoot(ctx, "work", "/srv/work")
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.store.GrantSourceRoot(ctx, root.ID, u.id); err != nil {
		e.t.Fatal(err)
	}
	r := e.do("POST", "/api/v1/projects", map[string]any{"source": map[string]any{"kind": "local", "source_root_id": root.ID, "relative_path": "."}}, u.o()).want(e.t, 201, "")
	return nodeFixture{user: u, root: root, projectID: r.body["id"].(string)}
}

func (e *env) enrollToken(roots ...string) string {
	e.t.Helper()
	token := NewToken()
	if _, err := e.store.IssueEnrollmentToken(context.Background(), "default", roots, TokenHash(token), time.Minute); err != nil {
		e.t.Fatal(err)
	}
	return token
}

func (e *env) enrollNode(roots ...string) nodeproto.EnrollResponse {
	e.t.Helper()
	r := e.node("POST", "/api/v1/nodes/enroll", nodeproto.EnrollRequest{EnrollmentToken: e.enrollToken(roots...), Name: "pc-a", ProtocolVersion: "0.1", OS: "linux", Arch: "amd64"}, nreq{}).want(e.t, 201, "")
	var out nodeproto.EnrollResponse
	if err := json.Unmarshal(r.raw, &out); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func heartbeatBody(workers []string, caps []string) nodeproto.NodeHeartbeatRequest {
	in := nodeproto.NodeHeartbeatRequest{ProtocolVersion: "0.1", Capabilities: caps, ObservedWorkers: []nodeproto.ObservedWorker{},
		Capacity: nodeproto.Capacity{CPUCores: 4, MemoryAvailableBytes: 8 << 30, DiskAvailableBytes: 50 << 30}}
	for _, w := range workers {
		in.ObservedWorkers = append(in.ObservedWorkers, nodeproto.ObservedWorker{ID: w, State: "idle"})
	}
	return in
}

func TestNodeEnrollmentAuthenticationBoundaries(t *testing.T) {
	e := newEnv(t)
	f := e.nodeFixture()
	token := e.enrollToken(f.root.ID)
	body := nodeproto.EnrollRequest{EnrollmentToken: token, Name: "pc-a", ProtocolVersion: "0.1", OS: "linux", Arch: "amd64"}
	// Browsers always send Origin on POST: node routes reject it.
	e.node("POST", "/api/v1/nodes/enroll", body, nreq{origin: "http://" + e.host}).want(t, 403, "ORIGIN_REJECTED")
	bad := body
	bad.ProtocolVersion = "9.9"
	e.node("POST", "/api/v1/nodes/enroll", bad, nreq{}).want(t, 422, nodeproto.CodeProtocolUnsupported)
	e.node("POST", "/api/v1/nodes/enroll", body, nreq{ctype: "text/plain"}).want(t, 415, "UNSUPPORTED_MEDIA_TYPE")
	r := e.node("POST", "/api/v1/nodes/enroll", body, nreq{}).want(t, 201, "")
	var n nodeproto.EnrollResponse
	_ = json.Unmarshal(r.raw, &n)
	if !domain.ValidID(n.NodeID) || len(n.NodeSecret) != 43 || n.HeartbeatSeconds != 5 || n.LeaseSeconds != 30 {
		t.Fatalf("enroll %s", r.raw)
	}
	e.node("POST", "/api/v1/nodes/enroll", body, nreq{}).want(t, 401, "UNAUTHENTICATED")
	e.node("POST", "/api/v1/nodes/enroll", `{"enrollment_token":"x"}`, nreq{}).want(t, 401, "UNAUTHENTICATED")
	e.node("POST", "/api/v1/nodes/enroll", `{"enrollment_token":"x","extra":1}`, nreq{}).want(t, 400, "INVALID_REQUEST")

	hbPath := "/api/v1/nodes/" + n.NodeID + "/heartbeat"
	hbBody := heartbeatBody([]string{domain.NewID()}, []string{})
	e.node("POST", hbPath, hbBody, nreq{}).want(t, 401, nodeproto.CodeNodeUnauthenticated)
	e.node("POST", hbPath, hbBody, nreq{secret: NewToken()}).want(t, 401, nodeproto.CodeNodeUnauthenticated)
	e.node("POST", hbPath, hbBody, nreq{secret: "short"}).want(t, 401, nodeproto.CodeNodeUnauthenticated)
	e.node("POST", hbPath, hbBody, nreq{secret: n.NodeSecret, origin: "http://" + e.host}).want(t, 403, "ORIGIN_REJECTED")
	// A browser session cookie is not a node credential.
	req, _ := http.NewRequest("POST", e.hs.URL+hbPath, strings.NewReader(`{}`))
	req.Host = e.host
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: f.user.cookie})
	res, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("session on node route: %d", res.StatusCode)
	}
	// A node secret is not a user session.
	req, _ = http.NewRequest("GET", e.hs.URL+"/api/v1/projects", nil)
	req.Host = e.host
	req.Header.Set("Authorization", "Bearer "+n.NodeSecret)
	res, err = http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("node secret on user route: %d", res.StatusCode)
	}
	// Another node's path is 404 for an authenticated node.
	other := e.enrollNode()
	e.node("POST", "/api/v1/nodes/"+other.NodeID+"/heartbeat", hbBody, nreq{secret: n.NodeSecret}).want(t, 404, "NOT_FOUND")
	e.node("POST", hbPath, hbBody, nreq{secret: n.NodeSecret}).want(t, 200, "")
	bad2 := hbBody
	bad2.Capabilities = []string{"analysis:Bad"}
	e.node("POST", hbPath, bad2, nreq{secret: n.NodeSecret}).want(t, 422, "VALIDATION_FAILED")
	// Revocation takes effect immediately.
	if err := e.store.RevokeNode(context.Background(), n.NodeID); err != nil {
		t.Fatal(err)
	}
	e.node("POST", hbPath, hbBody, nreq{secret: n.NodeSecret}).want(t, 401, nodeproto.CodeNodeUnauthenticated)
	// Secrets and tokens never reach logs.
	logs := e.logs.String()
	if strings.Contains(logs, n.NodeSecret) || strings.Contains(logs, token) {
		t.Fatal("credential in logs")
	}
}

func TestNodeHTTPVerticalPathToFinalizing(t *testing.T) {
	e := newEnv(t)
	f := e.nodeFixture()
	run := e.do("POST", "/api/v1/projects/"+f.projectID+"/runs", map[string]any{"profile": "deep-review", "source_policy": "latest_at_acquire", "scope": map[string]any{"kind": "full"}}, opts{cookie: f.user.cookie, csrf: f.user.csrf, idem: "r1"}).want(t, 202, "")
	runID := run.body["id"].(string)
	reason := func() any {
		v := e.do("GET", "/api/v1/runs/"+runID, nil, f.user.o()).want(t, 200, "")
		return v.body["progress"].(map[string]any)["waiting_reason"]
	}
	gaps := func() []any {
		p := e.do("GET", "/api/v1/projects/"+f.projectID, nil, f.user.o()).want(t, 200, "")
		return p.body["capability_gaps"].([]any)
	}
	if reason() != "execution_unavailable" || len(gaps()) != 3 {
		t.Fatalf("before enrollment: %v %v", reason(), gaps())
	}
	n := e.enrollNode(f.root.ID)
	sec := nreq{secret: n.NodeSecret}
	workers := []string{domain.NewID()}
	// Production registry (no analysis handler): enrolled but not capable.
	e.node("POST", "/api/v1/nodes/"+n.NodeID+"/heartbeat", heartbeatBody(workers, []string{nodeproto.LocalSourceCapability(f.root.ID)}), sec).want(t, 200, "")
	if g := gaps(); len(g) != 2 || g[0] != "source_access_unverified" || g[1] != "report_unavailable" {
		t.Fatalf("gaps after enrollment %v", g)
	}
	if reason() != "no_capable_node" {
		t.Fatalf("no handler: %v", reason())
	}
	claimPath := "/api/v1/nodes/" + n.NodeID + "/claims"
	c204 := e.node("POST", claimPath, nodeproto.ClaimRequest{WorkerID: workers[0]}, nreq{secret: n.NodeSecret, header: map[string]string{"Idempotency-Key": "c0"}})
	if c204.status != 204 {
		t.Fatalf("claim without capability %d %s", c204.status, c204.raw)
	}
	caps := []string{nodeproto.AnalysisCapability("deep-review"), nodeproto.LocalSourceCapability(f.root.ID)}
	e.node("POST", "/api/v1/nodes/"+n.NodeID+"/heartbeat", heartbeatBody(workers, caps), sec).want(t, 200, "")
	if reason() != nil {
		t.Fatalf("claimable run reason %v", reason())
	}
	e.node("POST", claimPath, nodeproto.ClaimRequest{WorkerID: workers[0]}, sec).want(t, 400, "INVALID_REQUEST")
	cl := e.node("POST", claimPath, nodeproto.ClaimRequest{WorkerID: workers[0]}, nreq{secret: n.NodeSecret, header: map[string]string{"Idempotency-Key": "c1"}}).want(t, 200, "")
	var claim nodeproto.ClaimResponse
	if err := json.Unmarshal(cl.raw, &claim); err != nil {
		t.Fatal(err)
	}
	if claim.Lease.RunID != runID || claim.Lease.LeaseEpoch != 1 || claim.Job.Source.Kind != "local" || claim.Job.Source.SourceRootID != f.root.ID || strings.Contains(string(cl.raw), "/srv/work") {
		t.Fatalf("claim %s", cl.raw)
	}
	again := e.node("POST", claimPath, nodeproto.ClaimRequest{WorkerID: workers[0]}, nreq{secret: n.NodeSecret, header: map[string]string{"Idempotency-Key": "c1"}}).want(t, 200, "")
	if !bytes.Equal(bytes.TrimSpace(again.raw), bytes.TrimSpace(cl.raw)) {
		t.Fatalf("replay differs:\n%s\n%s", again.raw, cl.raw)
	}
	e.node("POST", claimPath, nodeproto.ClaimRequest{WorkerID: domain.NewID()}, nreq{secret: n.NodeSecret, header: map[string]string{"Idempotency-Key": "c1"}}).want(t, 409, "IDEMPOTENCY_CONFLICT")
	e.node("POST", claimPath, nodeproto.ClaimRequest{WorkerID: workers[0]}, nreq{secret: n.NodeSecret, header: map[string]string{"Idempotency-Key": "c2"}}).want(t, 409, "CONFLICT")

	att := "/api/v1/attempts/" + claim.Lease.AttemptID
	own := nodeproto.AttemptHeartbeatRequest{WorkerID: workers[0], LeaseEpoch: 1}
	hb := e.node("POST", att+"/heartbeat", own, sec).want(t, 200, "")
	if hb.body["cancel_requested"] != false {
		t.Fatal(string(hb.raw))
	}
	e.node("POST", att+"/heartbeat", nodeproto.AttemptHeartbeatRequest{WorkerID: domain.NewID(), LeaseEpoch: 1}, sec).want(t, 409, nodeproto.CodeStaleAttempt)
	e.node("POST", att+"/heartbeat", nodeproto.AttemptHeartbeatRequest{WorkerID: workers[0], LeaseEpoch: 2}, sec).want(t, 409, nodeproto.CodeStaleAttempt)
	stranger := e.enrollNode(f.root.ID)
	e.node("POST", att+"/heartbeat", own, nreq{secret: stranger.NodeSecret}).want(t, 404, "NOT_FOUND")

	// Artifact upload.
	manifest := []byte(`{"schema_version":"0.1"}`)
	msha := domain.Digest(manifest)
	put := func(client string, body []byte, sha, role string, o nreq) resp {
		o.secret = n.NodeSecret
		o.ctype = "application/json"
		o.header = map[string]string{nodeproto.HeaderWorkerID: workers[0], nodeproto.HeaderLeaseEpoch: "1", nodeproto.HeaderArtifactRole: role, nodeproto.HeaderContentSHA256: sha}
		return e.node("PUT", att+"/artifacts/"+client, body, o)
	}
	put(domain.NewID(), manifest, domain.Digest([]byte("other")), "source_manifest", nreq{}).want(t, 422, nodeproto.CodeIntegrityMismatch)
	put(domain.NewID(), manifest, msha, "bogus", nreq{}).want(t, 422, "VALIDATION_FAILED")
	put(domain.NewID(), manifest, msha, "source_manifest", nreq{chunked: true}).want(t, 411, "LENGTH_REQUIRED")
	client := domain.NewID()
	up := put(client, manifest, msha, "source_manifest", nreq{}).want(t, 201, "")
	upAgain := put(client, manifest, msha, "source_manifest", nreq{}).want(t, 201, "")
	if up.body["artifact_id"] != upAgain.body["artifact_id"] || up.body["sha256"] != msha {
		t.Fatalf("artifact %s %s", up.raw, upAgain.raw)
	}
	stored, err := os.ReadFile(filepath.Join(e.artifacts, "sha256", msha[:2], msha))
	if err != nil || !bytes.Equal(stored, manifest) {
		t.Fatalf("stored bytes %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(e.artifacts, "tmp")); len(entries) != 0 {
		t.Fatal("rejected upload left temp files")
	}
	ev := func(kind string, payload any) resp {
		return e.node("POST", att+"/events", map[string]any{"worker_id": workers[0], "lease_epoch": 1, "client_event_id": domain.NewID(), "kind": kind, "payload": payload}, sec)
	}
	ev("snapshot_fixed", map[string]any{"manifest_digest": msha, "kind": "local", "consistency": "stable_copy_best_effort", "source_sha": nil, "manifest_artifact_id": up.body["artifact_id"]}).want(t, 200, "")
	task := ev("task_created", map[string]any{"key": "fixture", "required": true}).want(t, 200, "")
	if task.body["version"] != float64(1) {
		t.Fatal(string(task.raw))
	}
	ev("unknown_kind", map[string]any{}).want(t, 422, "VALIDATION_FAILED")
	ev("run_advance", map[string]any{"status": "running"}).want(t, 200, "")
	st := ev("task_status", map[string]any{"task_id": task.body["task_id"], "expected_version": 1, "status": "running", "reason": nil}).want(t, 200, "")
	ev("task_status", map[string]any{"task_id": task.body["task_id"], "expected_version": st.body["version"], "status": "succeeded", "reason": nil}).want(t, 200, "")
	ev("run_advance", map[string]any{"status": "finalizing"}).want(t, 200, "")
	result := []byte(`{"result":"fixture"}`)
	rsha := domain.Digest(result)
	ru := put(domain.NewID(), result, rsha, "result_manifest", nreq{}).want(t, 201, "")
	rid := ru.body["artifact_id"].(string)
	complete := nodeproto.CompleteRequest{WorkerID: workers[0], LeaseEpoch: 1, Outcome: "succeeded", CleanupState: "confirmed", ResultManifestID: &rid}
	done := e.node("POST", att+"/complete", complete, sec).want(t, 202, "")
	if done.body["status"] != "finalizing" || done.body["id"] != runID {
		t.Fatalf("complete %s", done.raw)
	}
	e.node("POST", att+"/complete", complete, sec).want(t, 202, "")
	failed := complete
	failed.Outcome = "failed"
	failed.ResultManifestID = nil
	e.node("POST", att+"/complete", failed, sec).want(t, 409, "CONFLICT")
	e.node("POST", att+"/heartbeat", own, sec).want(t, 409, nodeproto.CodeStaleAttempt)
	v := e.do("GET", "/api/v1/runs/"+runID, nil, f.user.o()).want(t, 200, "")
	if v.body["status"] != "finalizing" || v.body["snapshot_id"] == nil {
		t.Fatalf("run %s", v.raw)
	}
}

func TestNodeHTTPCancellationAndCleanup(t *testing.T) {
	e := newEnv(t)
	f := e.nodeFixture()
	run := e.do("POST", "/api/v1/projects/"+f.projectID+"/runs", map[string]any{"profile": "deep-review", "source_policy": "latest_at_acquire", "scope": map[string]any{"kind": "full"}}, opts{cookie: f.user.cookie, csrf: f.user.csrf, idem: "r1"}).want(t, 202, "")
	runID := run.body["id"].(string)
	n := e.enrollNode(f.root.ID)
	sec := nreq{secret: n.NodeSecret}
	w := domain.NewID()
	caps := []string{nodeproto.AnalysisCapability("deep-review"), nodeproto.LocalSourceCapability(f.root.ID)}
	hbPath := "/api/v1/nodes/" + n.NodeID + "/heartbeat"
	e.node("POST", hbPath, heartbeatBody([]string{w}, caps), sec).want(t, 200, "")
	cl := e.node("POST", "/api/v1/nodes/"+n.NodeID+"/claims", nodeproto.ClaimRequest{WorkerID: w}, nreq{secret: n.NodeSecret, header: map[string]string{"Idempotency-Key": w + ":1"}}).want(t, 200, "")
	var claim nodeproto.ClaimResponse
	_ = json.Unmarshal(cl.raw, &claim)
	att := "/api/v1/attempts/" + claim.Lease.AttemptID
	e.do("POST", "/api/v1/runs/"+runID+"/cancel", nil, f.user.o()).want(t, 202, "")
	hb := e.node("POST", att+"/heartbeat", nodeproto.AttemptHeartbeatRequest{WorkerID: w, LeaseEpoch: 1}, sec).want(t, 200, "")
	if hb.body["cancel_requested"] != true {
		t.Fatal(string(hb.raw))
	}
	busy := heartbeatBody([]string{w}, caps)
	busy.ObservedWorkers[0].State = "busy"
	nh := e.node("POST", hbPath, busy, sec).want(t, 200, "")
	cmds := nh.body["commands"].([]any)
	if len(cmds) != 1 || cmds[0].(map[string]any)["kind"] != "cancel_attempt" || cmds[0].(map[string]any)["attempt_id"] != claim.Lease.AttemptID {
		t.Fatalf("commands %s", nh.raw)
	}
	// Cleanup could not be confirmed: the run needs recovery.
	unc := e.node("POST", att+"/complete", nodeproto.CompleteRequest{WorkerID: w, LeaseEpoch: 1, Outcome: "cancelled", CleanupState: "uncertain"}, sec).want(t, 202, "")
	if unc.body["status"] != "recovery_required" {
		t.Fatal(string(unc.raw))
	}
	cleanup := nodeproto.CleanupRequest{WorkerID: w, LeaseEpoch: 1, CleanupState: "uncertain", Method: "process_group_empty", VerifiedAt: time.Now().UTC()}
	e.node("POST", att+"/cleanup", cleanup, sec).want(t, 422, "VALIDATION_FAILED")
	// Report via heartbeat: uncertain is informational, confirmed applies.
	busy.ObservedWorkers[0].State = "idle"
	busy.LastCommandSequence = 1
	busy.CleanupReports = []nodeproto.CleanupReport{{AttemptID: claim.Lease.AttemptID, WorkerID: w, LeaseEpoch: 1, CleanupState: "uncertain", Method: "process_group_empty", VerifiedAt: time.Now().UTC()}}
	r := e.node("POST", hbPath, busy, sec).want(t, 200, "")
	if res := r.body["cleanup_results"].([]any)[0].(map[string]any); res["accepted"] != false || res["code"] != "uncertain_not_applied" {
		t.Fatal(string(r.raw))
	}
	busy.CleanupReports[0].CleanupState = "confirmed"
	busy.CleanupReports[0].LeaseEpoch = 7
	r = e.node("POST", hbPath, busy, sec).want(t, 200, "")
	if res := r.body["cleanup_results"].([]any)[0].(map[string]any); res["accepted"] != false || res["code"] != "stale_attempt" {
		t.Fatal(string(r.raw))
	}
	cleanup.CleanupState = "confirmed"
	ack := e.node("POST", att+"/cleanup", cleanup, sec).want(t, 202, "")
	if ack.body["status"] != "failed" {
		t.Fatal(string(ack.raw))
	}
	var active int
	if err := e.pool.QueryRow(context.Background(), "SELECT count(*) FROM capacity_reservations WHERE released_at IS NULL").Scan(&active); err != nil || active != 0 {
		t.Fatalf("reservations %d %v", active, err)
	}
	// Oversized artifact declarations are refused before reading the body.
	big := e.node("PUT", att+"/artifacts/"+domain.NewID(), nil, nreq{secret: n.NodeSecret, header: map[string]string{"Content-Length": strconv.Itoa(nodeproto.MaxArtifactBytes + 1)}})
	if big.status != 411 && big.status != 413 && big.status != 422 {
		t.Fatalf("oversize %d", big.status)
	}
}

// Serve's background sweeper expires lapsed leases into recovery_required.
func TestServeSweepsExpiredLeases(t *testing.T) {
	e := newEnv(t)
	f := e.nodeFixture()
	e.do("POST", "/api/v1/projects/"+f.projectID+"/runs", map[string]any{"profile": "deep-review", "source_policy": "latest_at_acquire", "scope": map[string]any{"kind": "full"}}, opts{cookie: f.user.cookie, csrf: f.user.csrf, idem: "r1"}).want(t, 202, "")
	n := e.enrollNode(f.root.ID)
	w := domain.NewID()
	caps := []string{nodeproto.AnalysisCapability("deep-review"), nodeproto.LocalSourceCapability(f.root.ID)}
	e.node("POST", "/api/v1/nodes/"+n.NodeID+"/heartbeat", heartbeatBody([]string{w}, caps), nreq{secret: n.NodeSecret}).want(t, 200, "")
	cl := e.node("POST", "/api/v1/nodes/"+n.NodeID+"/claims", nodeproto.ClaimRequest{WorkerID: w}, nreq{secret: n.NodeSecret, header: map[string]string{"Idempotency-Key": "s1"}}).want(t, 200, "")
	var claim nodeproto.ClaimResponse
	_ = json.Unmarshal(cl.raw, &claim)
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, "UPDATE attempts SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", claim.Lease.AttemptID); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(Config{Listen: netip.MustParseAddrPort(ln.Addr().String()), SweepInterval: 50 * time.Millisecond}, e.store)
	if err != nil {
		t.Fatal(err)
	}
	sctx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- srv.Serve(sctx, ln) }()
	deadline := time.Now().Add(10 * time.Second)
	status := ""
	for time.Now().Before(deadline) {
		if err := e.pool.QueryRow(ctx, "SELECT status FROM attempts WHERE id=$1", claim.Lease.AttemptID).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status == "recovery_required" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if status != "recovery_required" {
		t.Fatalf("sweeper did not expire the lease: %s", status)
	}
}

// Serve's autoscaler loop turns queued demand for a controller-scaled
// node (zero workers, probed capability) into a start_worker command that
// the node receives over the real heartbeat route.
func TestServeAutoscalerStartsWorkerForControllerNode(t *testing.T) {
	e := newEnv(t)
	f := e.nodeFixture()
	n := e.enrollNode(f.root.ID)
	caps := []string{nodeproto.AnalysisCapability("deep-review"), nodeproto.LocalSourceCapability(f.root.ID)}
	beat := heartbeatBody(nil, caps)
	beat.WorkerScaling, beat.MaxWorkers = nodeproto.ScalingController, 2
	e.node("POST", "/api/v1/nodes/"+n.NodeID+"/heartbeat", beat, nreq{secret: n.NodeSecret}).want(t, 200, "")
	e.do("POST", "/api/v1/projects/"+f.projectID+"/runs", map[string]any{"profile": "deep-review", "source_policy": "latest_at_acquire", "scope": map[string]any{"kind": "full"}}, opts{cookie: f.user.cookie, csrf: f.user.csrf, idem: "r1"}).want(t, 202, "")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(Config{Listen: netip.MustParseAddrPort(ln.Addr().String()), SweepInterval: -1, ReconcileInterval: 50 * time.Millisecond}, e.store)
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()
	deadline := time.Now().Add(10 * time.Second)
	var resp nodeproto.NodeHeartbeatResponse
	for time.Now().Before(deadline) {
		r := e.node("POST", "/api/v1/nodes/"+n.NodeID+"/heartbeat", beat, nreq{secret: n.NodeSecret}).want(t, 200, "")
		_ = json.Unmarshal(r.raw, &resp)
		if len(resp.Commands) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(resp.Commands) != 1 || resp.Commands[0].Kind != nodeproto.CommandStartWorker || resp.Commands[0].WorkerID == nil || !domain.ValidID(*resp.Commands[0].WorkerID) {
		t.Fatalf("commands %+v", resp.Commands)
	}
	// Invalid scaling fields are a validation error at the route.
	bad := heartbeatBody(nil, caps)
	bad.WorkerScaling = nodeproto.ScalingController
	e.node("POST", "/api/v1/nodes/"+n.NodeID+"/heartbeat", bad, nreq{secret: n.NodeSecret}).want(t, 422, "VALIDATION_FAILED")
}
