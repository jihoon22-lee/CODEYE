package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
)

// Node protocol storage tests. Every test needs CODEYE_TEST_DATABASE_URL
// (real PostgreSQL, isolated schema); without it they skip = NOT executed.

type nodeEnv struct {
	t       *testing.T
	s       *Store
	ctx     context.Context
	user    string
	root    SourceRoot
	project Project
}

func newNodeEnv(t *testing.T) *nodeEnv {
	s := testStore(t)
	ctx, user, root, _ := registered(t, s)
	p, err := s.RegisterProject(ctx, user, ProjectRegistration{Source: domain.Source{Kind: domain.SourceLocal, SourceRootID: root.ID, RelativePath: "."}})
	if err != nil {
		t.Fatal(err)
	}
	return &nodeEnv{t: t, s: s, ctx: ctx, user: user, root: root, project: p}
}

const deepReview = `{"profile":"deep-review","source_policy":"latest_at_acquire","scope":{"kind":"full"}}`

func (e *nodeEnv) enqueue(key, body string) Run {
	e.t.Helper()
	r, err := e.s.Enqueue(e.ctx, e.user, e.project.ID, key, json.RawMessage(body))
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

type testNode struct {
	id, secret string
	workers    []string
}

func (e *nodeEnv) enroll(name string, roots ...string) testNode {
	e.t.Helper()
	token := domain.NewID()
	if _, err := e.s.IssueEnrollmentToken(e.ctx, "default", roots, hashOf(token), time.Minute); err != nil {
		e.t.Fatal(err)
	}
	secret := domain.NewID()
	n, err := e.s.EnrollNode(e.ctx, Enrollment{TokenSHA256: hashOf(token), SecretSHA256: hashOf(secret), Name: name, OS: "linux", Arch: "amd64", Protocol: "0.1"})
	if err != nil {
		e.t.Fatal(err)
	}
	return testNode{id: n.ID, secret: secret, workers: []string{domain.NewID(), domain.NewID()}}
}

func capable(roots ...string) []string {
	caps := []string{nodeproto.AnalysisCapability("deep-review")}
	for _, r := range roots {
		caps = append(caps, nodeproto.LocalSourceCapability(r))
	}
	return caps
}

func hb(workers []string, state string, caps []string) nodeproto.NodeHeartbeatRequest {
	if caps == nil {
		caps = []string{}
	}
	in := nodeproto.NodeHeartbeatRequest{ProtocolVersion: "0.1", Capabilities: caps, ObservedWorkers: []nodeproto.ObservedWorker{},
		Capacity: nodeproto.Capacity{CPUCores: 8, MemoryAvailableBytes: 16 << 30, DiskAvailableBytes: 100 << 30}}
	for _, w := range workers {
		in.ObservedWorkers = append(in.ObservedWorkers, nodeproto.ObservedWorker{ID: w, State: state})
	}
	return in
}

func (e *nodeEnv) beat(n testNode, in nodeproto.NodeHeartbeatRequest) nodeproto.NodeHeartbeatResponse {
	e.t.Helper()
	out, err := e.s.NodeHeartbeat(e.ctx, n.id, in)
	if err != nil {
		e.t.Fatal(err)
	}
	return out
}

func (e *nodeEnv) idle(n testNode) {
	e.t.Helper()
	e.beat(n, hb(n.workers, "idle", capable(e.root.ID)))
}

func (e *nodeEnv) claimOK(n testNode, worker, key string) *nodeproto.ClaimResponse {
	e.t.Helper()
	res, err := e.s.ClaimAttempt(e.ctx, n.id, worker, key)
	if err != nil {
		e.t.Fatal(err)
	}
	if res.Response == nil {
		e.t.Fatal("claim returned no work")
	}
	return res.Response
}

func (e *nodeEnv) noClaim(n testNode, worker, key string) {
	e.t.Helper()
	res, err := e.s.ClaimAttempt(e.ctx, n.id, worker, key)
	if err != nil {
		e.t.Fatal(err)
	}
	if res.Response != nil {
		e.t.Fatalf("unexpected claim of %s", res.Response.Lease.RunID)
	}
}

func (e *nodeEnv) waiting(run string) string {
	e.t.Helper()
	v, err := e.s.GetRunView(e.ctx, e.user, run)
	if err != nil {
		e.t.Fatal(err)
	}
	if v.WaitingReason == nil {
		return ""
	}
	return *v.WaitingReason
}

// projectLimit sets scheduler_limits.project_max_running.
func (e *nodeEnv) projectLimit(n int) {
	e.t.Helper()
	if _, err := e.s.SetSchedulerLimits(e.ctx, nil, &n); err != nil {
		e.t.Fatal(err)
	}
}

func (e *nodeEnv) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.s.pool.QueryRow(e.ctx, q, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func owner(n testNode, c *nodeproto.ClaimResponse) Ownership {
	return Ownership{AttemptID: c.Lease.AttemptID, NodeID: n.id, WorkerID: c.Lease.WorkerID, Epoch: c.Lease.LeaseEpoch}
}

// The 0003 request restore matches stored digests only if its literals are the
// exact canonical bytes Enqueue hashed.
func TestRequestBackfillLiteralsAreCanonical(t *testing.T) {
	sql, err := os.ReadFile("../../../migrations/sql/0003_nodes_and_leases.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"deep-review", "with-tests", "focused"} {
		req := domain.RunRequest{Profile: p, SourcePolicy: "latest_at_acquire", Scope: domain.Scope{Kind: "full"}}
		raw, _ := json.Marshal(req)
		canonical, err := domain.CanonicalJSON(raw)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(sql), "('"+string(canonical)+"')") {
			t.Fatalf("migration lacks canonical literal %s", canonical)
		}
	}
}

func TestEnrollmentTokenSingleUseExpiryRevocation(t *testing.T) {
	e := newNodeEnv(t)
	token := domain.NewID()
	tok, err := e.s.IssueEnrollmentToken(e.ctx, "default", []string{e.root.ID}, hashOf(token), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// Concurrent use of one token enrolls exactly one node.
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins, unauth := 0, 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := e.s.EnrollNode(e.ctx, Enrollment{TokenSHA256: hashOf(token), SecretSHA256: hashOf(fmt.Sprint("s", i)), Name: "n", OS: "linux", Arch: "amd64", Protocol: "0.1"})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				wins++
			case errors.Is(err, domain.ErrUnauthenticated):
				unauth++
			default:
				t.Errorf("enroll: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if wins != 1 || unauth != 7 {
		t.Fatalf("wins=%d unauth=%d", wins, unauth)
	}
	if n := e.count("SELECT count(*) FROM node_source_roots"); n != 1 {
		t.Fatalf("node grants copied %d", n)
	}
	// Reuse after success, revoked and expired tokens never enroll.
	_, err = e.s.EnrollNode(e.ctx, Enrollment{TokenSHA256: hashOf(token), SecretSHA256: hashOf("x"), Name: "n", OS: "linux", Arch: "amd64", Protocol: "0.1"})
	expect(t, err, domain.ErrUnauthenticated)
	expect(t, e.s.RevokeEnrollmentToken(e.ctx, tok.ID), domain.ErrConflict)

	revoked := domain.NewID()
	rt, err := e.s.IssueEnrollmentToken(e.ctx, "default", nil, hashOf(revoked), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.RevokeEnrollmentToken(e.ctx, rt.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.s.RevokeEnrollmentToken(e.ctx, rt.ID); err != nil {
		t.Fatalf("repeat revoke: %v", err)
	}
	_, err = e.s.EnrollNode(e.ctx, Enrollment{TokenSHA256: hashOf(revoked), SecretSHA256: hashOf("y"), Name: "n", OS: "linux", Arch: "amd64", Protocol: "0.1"})
	expect(t, err, domain.ErrUnauthenticated)

	expired := domain.NewID()
	if _, err := e.s.IssueEnrollmentToken(e.ctx, "default", nil, hashOf(expired), time.Second); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	_, err = e.s.EnrollNode(e.ctx, Enrollment{TokenSHA256: hashOf(expired), SecretSHA256: hashOf("z"), Name: "n", OS: "linux", Arch: "amd64", Protocol: "0.1"})
	expect(t, err, domain.ErrUnauthenticated)

	// Unsupported protocol leaves the token unused.
	later := domain.NewID()
	if _, err := e.s.IssueEnrollmentToken(e.ctx, "default", nil, hashOf(later), time.Minute); err != nil {
		t.Fatal(err)
	}
	_, err = e.s.EnrollNode(e.ctx, Enrollment{TokenSHA256: hashOf(later), SecretSHA256: hashOf("p"), Name: "n", OS: "linux", Arch: "amd64", Protocol: "9.9"})
	expect(t, err, domain.ErrUnsupported)
	if _, err := e.s.EnrollNode(e.ctx, Enrollment{TokenSHA256: hashOf(later), SecretSHA256: hashOf("q"), Name: "n", OS: "linux", Arch: "amd64", Protocol: "0.1"}); err != nil {
		t.Fatalf("token consumed by rejected protocol: %v", err)
	}
	// Unknown roots are refused at issuance.
	_, err = e.s.IssueEnrollmentToken(e.ctx, "default", []string{domain.NewID()}, hashOf(domain.NewID()), time.Minute)
	expect(t, err, domain.ErrNotFound)
	// Only the hash is stored.
	if n := e.count("SELECT count(*) FROM node_enrollment_tokens WHERE token_sha256=$1", token); n != 0 {
		t.Fatal("raw token stored")
	}
}

func TestNodeAuthenticationAndRevocation(t *testing.T) {
	e := newNodeEnv(t)
	n := e.enroll("a", e.root.ID)
	id, err := e.s.AuthenticateNode(e.ctx, hashOf(n.secret))
	if err != nil || id.ID != n.id {
		t.Fatalf("auth %v %v", id, err)
	}
	_, err = e.s.AuthenticateNode(e.ctx, hashOf("wrong"))
	expect(t, err, domain.ErrUnauthenticated)
	if err := e.s.RevokeNode(e.ctx, n.id); err != nil {
		t.Fatal(err)
	}
	_, err = e.s.AuthenticateNode(e.ctx, hashOf(n.secret))
	expect(t, err, domain.ErrUnauthenticated)
	_, err = e.s.NodeHeartbeat(e.ctx, n.id, hb(n.workers, "idle", nil))
	if err == nil {
		t.Fatal("revoked node heartbeat accepted")
	}
	_, err = e.s.ClaimAttempt(e.ctx, n.id, n.workers[0], "k")
	expect(t, err, domain.ErrUnauthenticated)
}

func TestHeartbeatWorkersCommandsAndValidation(t *testing.T) {
	e := newNodeEnv(t)
	a, b := e.enroll("a", e.root.ID), e.enroll("b")
	e.idle(a)
	if n := e.count("SELECT count(*) FROM workers WHERE node_id=$1 AND state='idle'", a.id); n != 2 {
		t.Fatalf("workers %d", n)
	}
	// A worker ID owned by another node is a conflict.
	_, err := e.s.NodeHeartbeat(e.ctx, b.id, hb(a.workers[:1], "idle", nil))
	expect(t, err, domain.ErrConflict)
	// Unreported workers become stopped.
	e.beat(a, hb(a.workers[:1], "idle", capable(e.root.ID)))
	if n := e.count("SELECT count(*) FROM workers WHERE id=$1 AND state='stopped'", a.workers[1]); n != 1 {
		t.Fatal("unreported worker not stopped")
	}
	// Cursor beyond the issued sequence and malformed bodies are invalid.
	bad := hb(a.workers, "idle", nil)
	bad.LastCommandSequence = 5
	_, err = e.s.NodeHeartbeat(e.ctx, a.id, bad)
	expect(t, err, domain.ErrInvalid)
	for _, mut := range []func(*nodeproto.NodeHeartbeatRequest){
		func(r *nodeproto.NodeHeartbeatRequest) { r.Capabilities = []string{"analysis:Bad"} },
		func(r *nodeproto.NodeHeartbeatRequest) { r.Capabilities = []string{"source:local:../x"} },
		func(r *nodeproto.NodeHeartbeatRequest) { r.ObservedWorkers[0].State = "running" },
		func(r *nodeproto.NodeHeartbeatRequest) { r.Capacity.CPUCores = -1 },
		func(r *nodeproto.NodeHeartbeatRequest) { r.ObservedWorkers = nil },
		func(r *nodeproto.NodeHeartbeatRequest) { r.Capabilities = nil },
	} {
		in := hb(a.workers, "idle", nil)
		mut(&in)
		_, err = e.s.NodeHeartbeat(e.ctx, a.id, in)
		expect(t, err, domain.ErrInvalid)
	}
	in := hb(a.workers, "idle", nil)
	in.ProtocolVersion = "0.2"
	_, err = e.s.NodeHeartbeat(e.ctx, a.id, in)
	expect(t, err, domain.ErrUnsupported)

	// A cancel request becomes exactly one cancel_attempt command, delivered
	// until acknowledged by the cursor.
	e.idle(a)
	r := e.enqueue("c1", deepReview)
	c := e.claimOK(a, a.workers[0], "k1")
	if _, _, err := e.s.CancelRun(e.ctx, e.user, r.ID); err != nil {
		t.Fatal(err)
	}
	out := e.beat(a, hb(a.workers, "busy", capable(e.root.ID)))
	if len(out.Commands) != 1 || out.Commands[0].Kind != "cancel_attempt" || *out.Commands[0].AttemptID != c.Lease.AttemptID || out.Commands[0].Sequence != 1 {
		t.Fatalf("commands %+v", out.Commands)
	}
	out = e.beat(a, hb(a.workers, "busy", capable(e.root.ID)))
	if len(out.Commands) != 1 {
		t.Fatal("unacknowledged command not redelivered")
	}
	ack := hb(a.workers, "busy", capable(e.root.ID))
	ack.LastCommandSequence = 1
	if out = e.beat(a, ack); len(out.Commands) != 0 {
		t.Fatalf("acknowledged command redelivered %+v", out.Commands)
	}
	if n := e.count("SELECT count(*) FROM node_commands WHERE acked_at IS NOT NULL"); n != 1 {
		t.Fatal("command not acknowledged")
	}
	hr, err := e.s.RenewAttempt(e.ctx, a.id, owner(a, c))
	if err != nil || !hr.CancelRequested {
		t.Fatalf("renew %+v %v", hr, err)
	}
}

// uploadManifest stores a finalized artifact row for o (bytes are the HTTP
// layer's concern; here only metadata).
func (e *nodeEnv) artifact(n testNode, o Ownership, role, sha string) string {
	e.t.Helper()
	out, err := e.s.FinalizeArtifact(e.ctx, n.id, o, ArtifactUpload{ClientArtifactID: domain.NewID(), Role: role, MediaType: "application/json", SHA256: sha, StorageKey: "sha256/" + sha[:2] + "/" + sha, SizeBytes: 10})
	if err != nil {
		e.t.Fatal(err)
	}
	return out.ArtifactID
}

func (e *nodeEnv) event(n testNode, o Ownership, kind string, payload any) (nodeproto.EventResponse, error) {
	raw, _ := json.Marshal(payload)
	return e.s.RecordAttemptEvent(e.ctx, n.id, o, AttemptEvent{ClientEventID: domain.NewID(), Kind: kind, Payload: raw})
}

func (e *nodeEnv) mustEvent(n testNode, o Ownership, kind string, payload any) nodeproto.EventResponse {
	e.t.Helper()
	out, err := e.event(n, o, kind, payload)
	if err != nil {
		e.t.Fatalf("%s: %v", kind, err)
	}
	return out
}

func TestNodeAttemptHappyPathToFinalizing(t *testing.T) {
	e := newNodeEnv(t)
	n := e.enroll("a", e.root.ID)
	e.idle(n)
	r := e.enqueue("r1", deepReview)
	c := e.claimOK(n, n.workers[0], "k")
	o := owner(n, c)
	manifest := domain.Digest([]byte("manifest"))
	// snapshot_fixed needs this attempt's source_manifest artifact with that digest.
	_, err := e.event(n, o, nodeproto.EventSnapshotFixed, nodeproto.SnapshotFixedPayload{ManifestDigest: manifest, Kind: "local", Consistency: "stable_copy_best_effort", ManifestArtifactID: domain.NewID()})
	expect(t, err, domain.ErrInvalid)
	wrongRole := e.artifact(n, o, "stdout", manifest)
	_, err = e.event(n, o, nodeproto.EventSnapshotFixed, nodeproto.SnapshotFixedPayload{ManifestDigest: manifest, Kind: "local", Consistency: "stable_copy_best_effort", ManifestArtifactID: wrongRole})
	expect(t, err, domain.ErrInvalid)
	mid := e.artifact(n, o, "source_manifest", manifest)
	_, err = e.event(n, o, nodeproto.EventSnapshotFixed, nodeproto.SnapshotFixedPayload{ManifestDigest: manifest, Kind: "git", Consistency: "stable_copy_best_effort", ManifestArtifactID: mid})
	expect(t, err, domain.ErrInvalid)
	payload := nodeproto.SnapshotFixedPayload{ManifestDigest: manifest, Kind: "local", Consistency: "stable_copy_best_effort", ManifestArtifactID: mid}
	raw, _ := json.Marshal(payload)
	ev := AttemptEvent{ClientEventID: domain.NewID(), Kind: nodeproto.EventSnapshotFixed, Payload: raw}
	snap, err := e.s.RecordAttemptEvent(e.ctx, n.id, o, ev)
	if err != nil || snap.SnapshotID == nil {
		t.Fatalf("snapshot %v", err)
	}
	// Replay returns the stored response; a different body is a conflict.
	replay, err := e.s.RecordAttemptEvent(e.ctx, n.id, o, ev)
	if err != nil || *replay.SnapshotID != *snap.SnapshotID || replay.Sequence != snap.Sequence {
		t.Fatalf("replay %+v %v", replay, err)
	}
	ev.Payload = json.RawMessage(`{"manifest_digest":"` + manifest + `","kind":"local","consistency":"stable_copy_best_effort","source_sha":null,"manifest_artifact_id":"` + wrongRole + `"}`)
	_, err = e.s.RecordAttemptEvent(e.ctx, n.id, o, ev)
	expect(t, err, domain.ErrConflict)
	if e.count("SELECT count(*) FROM snapshot_locations WHERE node_id=$1", n.id) != 1 ||
		e.count("SELECT count(*) FROM snapshots WHERE manifest_digest=digest AND manifest_digest=$1", manifest) != 1 {
		t.Fatal("snapshot location/digest not recorded")
	}
	task := e.mustEvent(n, o, nodeproto.EventTaskCreated, nodeproto.TaskCreatedPayload{Key: "go-vet", Required: true})
	if task.TaskID == nil || *task.Version != 1 {
		t.Fatalf("task %+v", task)
	}
	same := e.mustEvent(n, o, nodeproto.EventTaskCreated, nodeproto.TaskCreatedPayload{Key: "go-vet", Required: true})
	if *same.TaskID != *task.TaskID {
		t.Fatal("same key created a second task")
	}
	_, err = e.event(n, o, nodeproto.EventTaskCreated, nodeproto.TaskCreatedPayload{Key: "go-vet", Required: false})
	expect(t, err, domain.ErrConflict)
	adv := e.mustEvent(n, o, nodeproto.EventRunAdvance, nodeproto.RunAdvancePayload{Status: "running"})
	stale := adv.RunVersion - 1
	_, err = e.event(n, o, nodeproto.EventRunAdvance, nodeproto.RunAdvancePayload{Status: "finalizing", ExpectedVersion: &stale})
	expect(t, err, domain.ErrConflict)
	st := e.mustEvent(n, o, nodeproto.EventTaskStatus, nodeproto.TaskStatusPayload{TaskID: *task.TaskID, ExpectedVersion: 1, Status: "running"})
	reason := "analysis_unsupported"
	_, err = e.event(n, o, nodeproto.EventTaskStatus, nodeproto.TaskStatusPayload{TaskID: *task.TaskID, ExpectedVersion: *st.Version, Status: "succeeded", Reason: &reason})
	if err != nil {
		t.Fatal(err)
	}
	e.mustEvent(n, o, nodeproto.EventRunAdvance, nodeproto.RunAdvancePayload{Status: "finalizing"})
	// Success requires a result manifest of this attempt.
	_, err = e.s.CompleteAttempt(e.ctx, n.id, o, Completion{Outcome: "succeeded", Cleanup: "confirmed"})
	expect(t, err, domain.ErrInvalid)
	bogus := domain.NewID()
	_, err = e.s.CompleteAttempt(e.ctx, n.id, o, Completion{Outcome: "succeeded", Cleanup: "confirmed", ResultManifestID: &bogus})
	expect(t, err, domain.ErrInvalid)
	result := e.artifact(n, o, "result_manifest", domain.Digest([]byte("result")))
	done := Completion{Outcome: "succeeded", Cleanup: "confirmed", ResultManifestID: &result}
	run, err := e.s.CompleteAttempt(e.ctx, n.id, o, done)
	if err != nil || run.Status != domain.RunFinalizing {
		t.Fatalf("complete %s %v", run.Status, err)
	}
	if again, err := e.s.CompleteAttempt(e.ctx, n.id, o, done); err != nil || again.Version != run.Version {
		t.Fatalf("replay %v", err)
	}
	if e.count("SELECT count(*) FROM capacity_reservations WHERE released_at IS NULL") != 0 ||
		e.count("SELECT count(*) FROM attempts WHERE result_manifest_id=$1", result) != 1 {
		t.Fatal("reservation not released or manifest not recorded")
	}
	a, err := e.s.ArtifactByID(e.ctx, result)
	if err != nil || a.Role != "result_manifest" || a.RunID != r.ID {
		t.Fatalf("artifact %+v %v", a, err)
	}
}

// TC-22/TC-18 protocol scope: fencing of other workers, nodes, old epochs and
// expired leases; expiry keeps the reservation until real cleanup is reported.
func TestLeaseFencingExpiryAndCleanup(t *testing.T) {
	e := newNodeEnv(t)
	a, b := e.enroll("a", e.root.ID), e.enroll("b", e.root.ID)
	e.idle(a)
	e.idle(b)
	r := e.enqueue("r1", deepReview)
	c := e.claimOK(a, a.workers[0], "k")
	o := owner(a, c)
	other := o
	other.WorkerID = a.workers[1]
	_, err := e.s.RenewAttempt(e.ctx, a.id, other)
	expect(t, err, domain.ErrStaleAttempt)
	old := o
	old.Epoch = 2
	_, err = e.s.RenewAttempt(e.ctx, a.id, old)
	expect(t, err, domain.ErrStaleAttempt)
	foreign := o
	foreign.NodeID = b.id
	_, err = e.s.RenewAttempt(e.ctx, b.id, foreign)
	expect(t, err, domain.ErrNotFound)
	_, err = e.s.CompleteAttempt(e.ctx, b.id, foreign, Completion{Outcome: "failed", Cleanup: "confirmed"})
	expect(t, err, domain.ErrNotFound)
	if _, err := e.s.RenewAttempt(e.ctx, a.id, o); err != nil {
		t.Fatal(err)
	}
	// Expiry by the database clock.
	sqlExec(t, e.s, "UPDATE attempts SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", o.AttemptID)
	_, err = e.s.RenewAttempt(e.ctx, a.id, o)
	expect(t, err, domain.ErrStaleAttempt)
	_, err = e.event(a, o, nodeproto.EventTaskCreated, nodeproto.TaskCreatedPayload{Key: "x", Required: true})
	expect(t, err, domain.ErrStaleAttempt)
	_, err = e.s.CompleteAttempt(e.ctx, a.id, o, Completion{Outcome: "failed", Cleanup: "confirmed"})
	expect(t, err, domain.ErrStaleAttempt)
	if moved, err := e.s.SweepExpiredAttempts(e.ctx); err != nil || moved != 1 {
		t.Fatalf("sweep %d %v", moved, err)
	}
	got, _ := e.s.GetRun(e.ctx, e.user, r.ID)
	if got.Status != domain.RunRecoveryRequired || e.count("SELECT count(*) FROM capacity_reservations WHERE released_at IS NULL") != 1 {
		t.Fatal("expiry must keep the reservation and require recovery")
	}
	// The recovery_required run is never reassigned.
	e.idle(b)
	e.noClaim(b, b.workers[0], "after-expiry")
	// Real cleanup confirmation from the owning node releases it.
	_, err = e.s.ConfirmCleanupNode(e.ctx, b.id, foreign)
	expect(t, err, domain.ErrNotFound)
	got, err = e.s.ConfirmCleanupNode(e.ctx, a.id, o)
	if err != nil || got.Status != domain.RunFailed {
		t.Fatalf("cleanup %s %v", got.Status, err)
	}
	if e.count("SELECT count(*) FROM capacity_reservations WHERE released_at IS NULL") != 0 {
		t.Fatal("reservation not released")
	}
	if _, err := e.s.ConfirmCleanupNode(e.ctx, a.id, o); err != nil {
		t.Fatalf("cleanup replay %v", err)
	}
	if code := CleanupCode(domain.ErrStaleAttempt); code != "stale_attempt" {
		t.Fatal(code)
	}
}

func TestUncertainCompletionKeepsReservation(t *testing.T) {
	e := newNodeEnv(t)
	n := e.enroll("a", e.root.ID)
	e.idle(n)
	e.enqueue("r1", deepReview)
	c := e.claimOK(n, n.workers[0], "k")
	o := owner(n, c)
	reason := "cancelled"
	run, err := e.s.CompleteAttempt(e.ctx, n.id, o, Completion{Outcome: "failed", Cleanup: "uncertain", Reason: &reason})
	if err != nil || run.Status != domain.RunRecoveryRequired {
		t.Fatalf("%s %v", run.Status, err)
	}
	if e.count("SELECT count(*) FROM capacity_reservations WHERE released_at IS NULL") != 1 ||
		e.count("SELECT count(*) FROM attempts WHERE completion_reason='cancelled'") != 1 {
		t.Fatal("uncertain cleanup released the reservation")
	}
	// Reservations cannot be deleted (a BEFORE DELETE guard must raise, not
	// silently skip the row).
	if _, err := e.s.pool.Exec(e.ctx, "DELETE FROM capacity_reservations"); err == nil || !strings.Contains(err.Error(), "CY001") {
		t.Fatalf("reservation delete: %v", err)
	}
	if e.count("SELECT count(*) FROM capacity_reservations") != 1 {
		t.Fatal("reservation deleted")
	}
}

func TestNodeCommandsCannotBeDeleted(t *testing.T) {
	e := newNodeEnv(t)
	n := e.enroll("a", e.root.ID)
	e.idle(n)
	r := e.enqueue("r1", deepReview)
	e.claimOK(n, n.workers[0], "k")
	if _, _, err := e.s.CancelRun(e.ctx, e.user, r.ID); err != nil {
		t.Fatal(err)
	}
	e.beat(n, hb(n.workers, "busy", capable(e.root.ID)))
	if _, err := e.s.pool.Exec(e.ctx, "DELETE FROM node_commands"); err == nil || !strings.Contains(err.Error(), "CY001") {
		t.Fatalf("command delete: %v", err)
	}
	if e.count("SELECT count(*) FROM node_commands") != 1 {
		t.Fatal("command deleted")
	}
}

func TestArtifactFinalizeIdempotencyAndFencing(t *testing.T) {
	e := newNodeEnv(t)
	n := e.enroll("a", e.root.ID)
	e.idle(n)
	e.enqueue("r1", deepReview)
	c := e.claimOK(n, n.workers[0], "k")
	o := owner(n, c)
	sha := domain.Digest([]byte("bytes"))
	up := ArtifactUpload{ClientArtifactID: domain.NewID(), Role: "stdout", MediaType: "text/plain", SHA256: sha, StorageKey: "sha256/" + sha[:2] + "/" + sha, SizeBytes: 5}
	first, err := e.s.FinalizeArtifact(e.ctx, n.id, o, up)
	if err != nil {
		t.Fatal(err)
	}
	again, err := e.s.FinalizeArtifact(e.ctx, n.id, o, up)
	if err != nil || again.ArtifactID != first.ArtifactID {
		t.Fatalf("replay %v", err)
	}
	changed := up
	changed.SizeBytes = 6
	_, err = e.s.FinalizeArtifact(e.ctx, n.id, o, changed)
	expect(t, err, domain.ErrConflict)
	bad := up
	bad.StorageKey = "sha256/00/" + sha
	_, err = e.s.FinalizeArtifact(e.ctx, n.id, o, bad)
	expect(t, err, domain.ErrInvalid)
	if err := e.s.CheckArtifactLease(e.ctx, n.id, o, domain.NewID()); err != nil {
		t.Fatal(err)
	}
	sqlExec(t, e.s, "UPDATE attempts SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", o.AttemptID)
	expect(t, e.s.CheckArtifactLease(e.ctx, n.id, o, domain.NewID()), domain.ErrStaleAttempt)
	fresh := up
	fresh.ClientArtifactID = domain.NewID()
	_, err = e.s.FinalizeArtifact(e.ctx, n.id, o, fresh)
	expect(t, err, domain.ErrStaleAttempt)
	// An already finalized upload still replays after expiry.
	if _, err := e.s.FinalizeArtifact(e.ctx, n.id, o, up); err != nil {
		t.Fatalf("replay after expiry %v", err)
	}
}
