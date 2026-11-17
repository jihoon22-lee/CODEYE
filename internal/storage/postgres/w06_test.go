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

// W06 node protocol storage tests. Every test needs CODEYE_TEST_DATABASE_URL
// (real PostgreSQL, isolated schema); without it they skip = NOT executed.

type w06 struct {
	t       *testing.T
	s       *Store
	ctx     context.Context
	user    string
	root    SourceRoot
	project Project
}

func newW06(t *testing.T) *w06 {
	s := testStore(t)
	ctx, user, root, _ := registered(t, s)
	p, err := s.RegisterProject(ctx, user, ProjectRegistration{Source: domain.Source{Kind: domain.SourceLocal, SourceRootID: root.ID, RelativePath: "."}})
	if err != nil {
		t.Fatal(err)
	}
	return &w06{t: t, s: s, ctx: ctx, user: user, root: root, project: p}
}

const deepReview = `{"profile":"deep-review","source_policy":"latest_at_acquire","scope":{"kind":"full"}}`

func (e *w06) enqueue(key, body string) Run {
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

func (e *w06) enroll(name string, roots ...string) testNode {
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

func (e *w06) beat(n testNode, in nodeproto.NodeHeartbeatRequest) nodeproto.NodeHeartbeatResponse {
	e.t.Helper()
	out, err := e.s.NodeHeartbeat(e.ctx, n.id, in)
	if err != nil {
		e.t.Fatal(err)
	}
	return out
}

func (e *w06) idle(n testNode) {
	e.t.Helper()
	e.beat(n, hb(n.workers, "idle", capable(e.root.ID)))
}

func (e *w06) claimOK(n testNode, worker, key string) *nodeproto.ClaimResponse {
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

func (e *w06) noClaim(n testNode, worker, key string) {
	e.t.Helper()
	res, err := e.s.ClaimAttempt(e.ctx, n.id, worker, key)
	if err != nil {
		e.t.Fatal(err)
	}
	if res.Response != nil {
		e.t.Fatalf("unexpected claim of %s", res.Response.Lease.RunID)
	}
}

func (e *w06) waiting(run string) string {
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

func (e *w06) count(q string, args ...any) int {
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

// The 0003 request restore matches W03 digests only if its literals are the
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
	e := newW06(t)
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
	e := newW06(t)
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
	e := newW06(t)
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

// TC-21: two agents race for one run; exactly one lease, epoch, reservation.
func TestTC21ConcurrentClaimSingleOwner(t *testing.T) {
	e := newW06(t)
	a, b := e.enroll("a", e.root.ID), e.enroll("b", e.root.ID)
	e.idle(a)
	e.idle(b)
	r := e.enqueue("r1", deepReview)
	type result struct {
		node testNode
		res  ClaimResult
		err  error
	}
	results := make(chan result, 4)
	var wg sync.WaitGroup
	for _, n := range []testNode{a, b} {
		for _, w := range n.workers {
			wg.Add(1)
			go func(n testNode, w string) {
				defer wg.Done()
				res, err := e.s.ClaimAttempt(e.ctx, n.id, w, "key-"+w)
				results <- result{n, res, err}
			}(n, w)
		}
	}
	wg.Wait()
	close(results)
	var winner *result
	for res := range results {
		if res.err != nil {
			t.Fatalf("claim error %v", res.err)
		}
		if res.res.Response != nil {
			if winner != nil {
				t.Fatal("two claims succeeded")
			}
			res := res
			winner = &res
		}
	}
	if winner == nil {
		t.Fatal("no claim succeeded")
	}
	lease := winner.res.Response.Lease
	if lease.RunID != r.ID || lease.LeaseEpoch != 1 || winner.res.Response.Job.Profile != "deep-review" || winner.res.Response.Job.Source.SourceRootID != e.root.ID || winner.res.Response.Job.Snapshot != nil {
		t.Fatalf("lease %+v job %+v", lease, winner.res.Response.Job)
	}
	if e.count("SELECT count(*) FROM attempts WHERE run_id=$1", r.ID) != 1 ||
		e.count("SELECT count(*) FROM capacity_reservations WHERE released_at IS NULL") != 1 ||
		e.count("SELECT count(*) FROM events WHERE run_id=$1 AND type='attempt.leased'", r.ID) != 1 ||
		e.count("SELECT lease_epoch::int FROM runs WHERE id=$1", r.ID) != 1 {
		t.Fatal("duplicate ownership state")
	}
	v, _ := e.s.GetRun(e.ctx, e.user, r.ID)
	if v.Status != domain.RunPreparing || v.Version != lease.RunVersion {
		t.Fatalf("run %s v%d lease run_version %d", v.Status, v.Version, lease.RunVersion)
	}
	// Lost response: the same key and worker replays without a new reservation.
	again := e.claimOK(winner.node, lease.WorkerID, "key-"+lease.WorkerID)
	if again.Lease.AttemptID != lease.AttemptID || e.count("SELECT count(*) FROM capacity_reservations") != 1 {
		t.Fatal("replay created new state")
	}
	// The same key for another worker is an idempotency conflict.
	other := winner.node.workers[0]
	if other == lease.WorkerID {
		other = winner.node.workers[1]
	}
	_, err := e.s.ClaimAttempt(e.ctx, winner.node.id, other, "key-"+lease.WorkerID)
	expect(t, err, ErrIdempotencyConflict)
	// The busy worker cannot claim again even with a fresh key, nor after
	// reporting itself idle while it still owns the lease.
	_, err = e.s.ClaimAttempt(e.ctx, winner.node.id, lease.WorkerID, "fresh")
	expect(t, err, domain.ErrConflict)
	e.idle(winner.node)
	_, err = e.s.ClaimAttempt(e.ctx, winner.node.id, lease.WorkerID, "fresh2")
	expect(t, err, domain.ErrConflict)
	// Unknown worker and a worker not reported idle are refused.
	_, err = e.s.ClaimAttempt(e.ctx, winner.node.id, domain.NewID(), "unknown")
	expect(t, err, domain.ErrConflict)
	e.beat(a, hb(a.workers, "starting", capable(e.root.ID)))
	_, err = e.s.ClaimAttempt(e.ctx, a.id, a.workers[0], "starting")
	expect(t, err, domain.ErrConflict)
	// 204 is also replayed for its key (a third node, so no worker of the
	// race above is involved whichever node won it).
	c3 := e.enroll("c", e.root.ID)
	e.idle(c3)
	e.noClaim(c3, c3.workers[0], "empty")
	e.enqueue("r2", deepReview)
	e.idle(c3)
	e.noClaim(c3, c3.workers[0], "empty")
	if c := e.claimOK(c3, c3.workers[0], "next"); c.Lease.RunID == r.ID {
		t.Fatal("claimed the same run twice")
	}
}

// TC-21: an event-insert failure after the reservation leaves nothing behind.
func TestTC21ClaimFailureLeavesNoPartialState(t *testing.T) {
	e := newW06(t)
	a := e.enroll("a", e.root.ID)
	e.idle(a)
	r := e.enqueue("r1", deepReview)
	sqlExec(t, e.s, `CREATE FUNCTION fail_leased() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.type='attempt.leased' THEN RAISE EXCEPTION 'injected'; END IF; RETURN NEW; END $$`)
	sqlExec(t, e.s, "CREATE TRIGGER fail_leased BEFORE INSERT ON events FOR EACH ROW EXECUTE FUNCTION fail_leased()")
	if _, err := e.s.ClaimAttempt(e.ctx, a.id, a.workers[0], "k"); err == nil {
		t.Fatal("claim succeeded despite injected failure")
	}
	if e.count("SELECT count(*) FROM attempts") != 0 || e.count("SELECT count(*) FROM capacity_reservations") != 0 ||
		e.count("SELECT count(*) FROM node_claims") != 0 || e.count("SELECT lease_epoch::int FROM runs WHERE id=$1", r.ID) != 0 ||
		e.count("SELECT count(*) FROM runs WHERE id=$1 AND status='queued'", r.ID) != 1 ||
		e.count("SELECT count(*) FROM workers WHERE state='idle'") != 2 {
		t.Fatal("partial claim committed")
	}
	sqlExec(t, e.s, "DROP TRIGGER fail_leased ON events")
	if c := e.claimOK(a, a.workers[0], "k"); c.Lease.LeaseEpoch != 1 {
		t.Fatalf("epoch %d", c.Lease.LeaseEpoch)
	}
}

func TestClaimRefusalsAndWaitingReasons(t *testing.T) {
	e := newW06(t)
	r := e.enqueue("r1", deepReview)
	if got := e.waiting(r.ID); got != nodeproto.WaitingExecutionUnavailable {
		t.Fatalf("no node: %q", got)
	}
	ok, _ := e.s.NodesEnrolled(e.ctx)
	if ok {
		t.Fatal("nodes enrolled before enrollment")
	}
	// Node without the source-root grant (even if it advertises the root).
	ungranted := e.enroll("ungranted")
	e.beat(ungranted, hb(ungranted.workers, "idle", capable(e.root.ID)))
	e.noClaim(ungranted, ungranted.workers[0], "k1")
	if got := e.waiting(r.ID); got != nodeproto.WaitingNoCapableNode {
		t.Fatalf("ungranted: %q", got)
	}
	if ok, _ := e.s.NodesEnrolled(e.ctx); !ok {
		t.Fatal("enrolled node not reported")
	}
	if err := e.s.RevokeNode(e.ctx, ungranted.id); err != nil {
		t.Fatal(err)
	}
	n := e.enroll("granted", e.root.ID)
	// Production registry: no analysis handler -> no analysis capability.
	e.beat(n, hb(n.workers, "idle", []string{nodeproto.LocalSourceCapability(e.root.ID)}))
	e.noClaim(n, n.workers[0], "k2")
	if got := e.waiting(r.ID); got != nodeproto.WaitingNoCapableNode {
		t.Fatalf("no analysis capability: %q", got)
	}
	// Grant without the matching source capability (agent has no mapping).
	e.beat(n, hb(n.workers, "idle", []string{nodeproto.AnalysisCapability("deep-review")}))
	e.noClaim(n, n.workers[0], "k3")
	// Stale heartbeat.
	e.idle(n)
	sqlExec(t, e.s, "UPDATE nodes SET last_heartbeat_at=clock_timestamp()-interval '1 minute' WHERE id=$1", n.id)
	e.noClaim(n, n.workers[0], "k4")
	if got := e.waiting(r.ID); got != nodeproto.WaitingNodeStale {
		t.Fatalf("stale: %q", got)
	}
	// Resource shortage: CPU budget, observed memory, observed disk.
	for i, capy := range []nodeproto.Capacity{
		{CPUCores: 0.5, MemoryAvailableBytes: 16 << 30, DiskAvailableBytes: 100 << 30},
		{CPUCores: 8, MemoryAvailableBytes: 512 << 20, DiskAvailableBytes: 100 << 30},
		{CPUCores: 8, MemoryAvailableBytes: 16 << 30, DiskAvailableBytes: 100 << 20},
	} {
		in := hb(n.workers, "idle", capable(e.root.ID))
		in.Capacity = capy
		e.beat(n, in)
		e.noClaim(n, n.workers[0], fmt.Sprint("cap", i))
		if got := e.waiting(r.ID); got != nodeproto.WaitingCapacityFull {
			t.Fatalf("capacity %d: %q", i, got)
		}
	}
	// Global limit 0.
	e.idle(n)
	if err := e.s.SetGlobalMaxRunning(e.ctx, 0); err != nil {
		t.Fatal(err)
	}
	e.noClaim(n, n.workers[0], "global")
	if got := e.waiting(r.ID); got != nodeproto.WaitingCapacityFull {
		t.Fatalf("global: %q", got)
	}
	if err := e.s.SetGlobalMaxRunning(e.ctx, 2); err != nil {
		t.Fatal(err)
	}
	if got := e.waiting(r.ID); got != "" {
		t.Fatalf("claimable run has reason %q", got)
	}
	e.claimOK(n, n.workers[0], "go")
	// Legacy run without a stored request is never claimed.
	legacy := e.enqueue("legacy", deepReview)
	sqlExec(t, e.s, "ALTER TABLE runs DISABLE TRIGGER runs_request_guard")
	sqlExec(t, e.s, "UPDATE runs SET request=NULL WHERE id=$1", legacy.ID)
	sqlExec(t, e.s, "ALTER TABLE runs ENABLE TRIGGER runs_request_guard")
	if got := e.waiting(legacy.ID); got != nodeproto.WaitingRequestUnavailable {
		t.Fatalf("legacy: %q", got)
	}
	e.idle(n)
	e.noClaim(n, n.workers[1], "legacy")
}

// Accounting model: observed free memory is not reduced by every reservation
// again; only reservations made after the last heartbeat are subtracted.
func TestCapacityAccountingWithoutDoubleCounting(t *testing.T) {
	e := newW06(t)
	n := e.enroll("a", e.root.ID)
	in := hb(n.workers, "idle", capable(e.root.ID))
	in.Capacity.MemoryAvailableBytes = 1536 << 20 // 1.5 GiB free
	e.beat(n, in)
	e.enqueue("r1", deepReview)
	e.enqueue("r2", deepReview)
	e.claimOK(n, n.workers[0], "a")
	// The first reservation is not yet visible in the observation: 0.5 GiB left.
	e.noClaim(n, n.workers[1], "b")
	// The next observation already reflects the running worker (same 1.5 GiB
	// still free means the host really has room); it is not charged again.
	in = hb(n.workers[1:], "idle", capable(e.root.ID))
	in.ObservedWorkers = append(in.ObservedWorkers, nodeproto.ObservedWorker{ID: n.workers[0], State: "busy"})
	in.Capacity.MemoryAvailableBytes = 1536 << 20
	e.beat(n, in)
	e.claimOK(n, n.workers[1], "c")
	// CPU is a policy budget: every active reservation counts.
	in.Capacity.CPUCores = 2
	e.enqueue("r3", deepReview)
	w3 := domain.NewID()
	in.ObservedWorkers = append(in.ObservedWorkers, nodeproto.ObservedWorker{ID: w3, State: "idle"})
	in.Capacity.MemoryAvailableBytes = 16 << 30
	e.beat(n, in)
	e.noClaim(n, w3, "d")
}

// Incompatible runs ahead in the queue must not starve a compatible one.
func TestClaimPagesPastIncompatibleQueueHead(t *testing.T) {
	e := newW06(t)
	n := e.enroll("a", e.root.ID)
	e.idle(n)
	for i := 0; i < candidatePage+5; i++ {
		e.enqueue(fmt.Sprint("focused-", i), `{"profile":"focused","source_policy":"latest_at_acquire","scope":{"kind":"full"}}`)
	}
	want := e.enqueue("deep", deepReview)
	if c := e.claimOK(n, n.workers[0], "k"); c.Lease.RunID != want.ID {
		t.Fatalf("claimed %s", c.Lease.RunID)
	}
}

func TestClaimRechecksActorPermissionAndSourceGrant(t *testing.T) {
	e := newW06(t)
	n := e.enroll("a", e.root.ID)
	e.idle(n)
	member, err := e.s.CreateUser(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.SetMember(e.ctx, e.user, e.project.ID, member, domain.RoleRunner); err != nil {
		t.Fatal(err)
	}
	r1, err := e.s.Enqueue(e.ctx, member, e.project.ID, "m1", json.RawMessage(deepReview))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.SetMember(e.ctx, e.user, e.project.ID, member, ""); err != nil {
		t.Fatal(err)
	}
	e.noClaim(n, n.workers[0], "k1")
	got, _ := e.s.GetRun(e.ctx, e.user, r1.ID)
	if got.Status != domain.RunFailed || e.count("SELECT count(*) FROM events WHERE run_id=$1 AND payload->>'reason'='actor_revoked'", r1.ID) != 1 {
		t.Fatalf("revoked actor run %s", got.Status)
	}
	// The owner lost the source-root grant (no revoke CLI yet; direct SQL).
	r2 := e.enqueue("o1", deepReview)
	sqlExec(t, e.s, "DELETE FROM source_root_grants WHERE source_root_id=$1 AND user_id=$2", e.root.ID, e.user)
	e.noClaim(n, n.workers[0], "k2")
	got, _ = e.s.GetRun(e.ctx, e.user, r2.ID)
	if got.Status != domain.RunFailed || e.count("SELECT count(*) FROM events WHERE run_id=$1 AND payload->>'reason'='source_grant_revoked'", r2.ID) != 1 {
		t.Fatalf("revoked grant run %s", got.Status)
	}
	if e.count("SELECT count(*) FROM attempts") != 0 {
		t.Fatal("attempt created for revoked actor")
	}
}

// uploadManifest stores a finalized artifact row for o (bytes are the HTTP
// layer's concern; here only metadata).
func (e *w06) artifact(n testNode, o Ownership, role, sha string) string {
	e.t.Helper()
	out, err := e.s.FinalizeArtifact(e.ctx, n.id, o, ArtifactUpload{ClientArtifactID: domain.NewID(), Role: role, MediaType: "application/json", SHA256: sha, StorageKey: "sha256/" + sha[:2] + "/" + sha, SizeBytes: 10})
	if err != nil {
		e.t.Fatal(err)
	}
	return out.ArtifactID
}

func (e *w06) event(n testNode, o Ownership, kind string, payload any) (nodeproto.EventResponse, error) {
	raw, _ := json.Marshal(payload)
	return e.s.RecordAttemptEvent(e.ctx, n.id, o, AttemptEvent{ClientEventID: domain.NewID(), Kind: kind, Payload: raw})
}

func (e *w06) mustEvent(n testNode, o Ownership, kind string, payload any) nodeproto.EventResponse {
	e.t.Helper()
	out, err := e.event(n, o, kind, payload)
	if err != nil {
		e.t.Fatalf("%s: %v", kind, err)
	}
	return out
}

func TestNodeAttemptHappyPathToFinalizing(t *testing.T) {
	e := newW06(t)
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
	e := newW06(t)
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
	e := newW06(t)
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
	e := newW06(t)
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

// Git sources need the source:git:<host> capability; the job carries the URL,
// branch and host and never a path.
func TestGitSourceClaimEligibility(t *testing.T) {
	s := testStore(t)
	ctx, user, root, prov := registered(t, s)
	p, err := s.RegisterProject(ctx, user, ProjectRegistration{Source: domain.Source{Kind: domain.SourceGit, ProviderID: prov.ID, RepositoryURL: "https://github.com/acme/widget.git", Branch: "main"}})
	if err != nil {
		t.Fatal(err)
	}
	e := &w06{t: t, s: s, ctx: ctx, user: user, root: root, project: p}
	r := e.enqueue("g1", deepReview)
	without := e.enroll("no-git")
	e.beat(without, hb(without.workers, "idle", []string{nodeproto.AnalysisCapability("deep-review"), nodeproto.GitSourceCapability("gitlab.example")}))
	e.noClaim(without, without.workers[0], "k")
	if got := e.waiting(r.ID); got != nodeproto.WaitingNoCapableNode {
		t.Fatalf("waiting %q", got)
	}
	with := e.enroll("git")
	e.beat(with, hb(with.workers, "idle", []string{nodeproto.AnalysisCapability("deep-review"), nodeproto.GitSourceCapability("github.com")}))
	c := e.claimOK(with, with.workers[0], "k")
	src := c.Job.Source
	if c.Lease.RunID != r.ID || src.Kind != "git" || src.RepositoryURL != "https://github.com/acme/widget.git" || src.Branch != "main" || src.Host != "github.com" || src.SourceRootID != "" {
		t.Fatalf("git job %+v", src)
	}
}

// A failed run with a fixed snapshot is retried only on a node holding it.
func TestPinnedRetryRequiresSnapshotLocation(t *testing.T) {
	e := newW06(t)
	a, b := e.enroll("a", e.root.ID), e.enroll("b", e.root.ID)
	e.idle(a)
	r := e.enqueue("r1", deepReview)
	c := e.claimOK(a, a.workers[0], "k")
	o := owner(a, c)
	manifest := domain.Digest([]byte("m"))
	mid := e.artifact(a, o, "source_manifest", manifest)
	snap := e.mustEvent(a, o, nodeproto.EventSnapshotFixed, nodeproto.SnapshotFixedPayload{ManifestDigest: manifest, Kind: "local", Consistency: "stable_copy_best_effort", ManifestArtifactID: mid})
	if _, err := e.s.CompleteAttempt(e.ctx, a.id, o, Completion{Outcome: "failed", Cleanup: "confirmed"}); err != nil {
		t.Fatal(err)
	}
	got, _ := e.s.GetRun(e.ctx, e.user, r.ID)
	if _, err := e.s.Retry(e.ctx, e.user, r.ID, got.Version); err != nil {
		t.Fatal(err)
	}
	e.idle(b)
	e.noClaim(b, b.workers[0], "b")
	if err := e.s.RevokeNode(e.ctx, a.id); err != nil {
		t.Fatal(err)
	}
	if got := e.waiting(r.ID); got != nodeproto.WaitingSnapshotUnavailable {
		t.Fatalf("revoked holder: %q", got)
	}
	// A fresh test with the holder alive.
	e2 := newW06(t)
	h := e2.enroll("h", e2.root.ID)
	e2.idle(h)
	r2 := e2.enqueue("r", deepReview)
	c2 := e2.claimOK(h, h.workers[0], "1")
	o2 := owner(h, c2)
	mid2 := e2.artifact(h, o2, "source_manifest", manifest)
	e2.mustEvent(h, o2, nodeproto.EventSnapshotFixed, nodeproto.SnapshotFixedPayload{ManifestDigest: manifest, Kind: "local", Consistency: "stable_copy_best_effort", ManifestArtifactID: mid2})
	first := e2.mustEvent(h, o2, nodeproto.EventTaskCreated, nodeproto.TaskCreatedPayload{Key: "go-vet", Required: true})
	taskID := *first.TaskID
	if _, err := e2.s.CompleteAttempt(e2.ctx, h.id, o2, Completion{Outcome: "failed", Cleanup: "confirmed"}); err != nil {
		t.Fatal(err)
	}
	g2, _ := e2.s.GetRun(e2.ctx, e2.user, r2.ID)
	if _, err := e2.s.Retry(e2.ctx, e2.user, r2.ID, g2.Version); err != nil {
		t.Fatal(err)
	}
	e2.idle(h)
	c3 := e2.claimOK(h, h.workers[1], "2")
	if c3.Job.Snapshot == nil || c3.Job.Snapshot.ManifestDigest != manifest || c3.Lease.LeaseEpoch != 2 || c3.Lease.SnapshotID == nil {
		t.Fatalf("retry job %+v", c3.Job.Snapshot)
	}
	o3 := owner(h, c3)
	// Re-planning the retried run reuses the task by key with its new version.
	t1 := e2.mustEvent(h, o3, nodeproto.EventTaskCreated, nodeproto.TaskCreatedPayload{Key: "go-vet", Required: true})
	if *t1.TaskID != taskID || *t1.Version < 2 {
		t.Fatalf("retried task %+v (first %s)", t1, taskID)
	}
	other := domain.Digest([]byte("different"))
	mid3 := e2.artifact(h, o3, "source_manifest", other)
	_, err := e2.event(h, o3, nodeproto.EventSnapshotFixed, nodeproto.SnapshotFixedPayload{ManifestDigest: other, Kind: "local", Consistency: "stable_copy_best_effort", ManifestArtifactID: mid3})
	expect(t, err, domain.ErrConflict)
	mid4 := e2.artifact(h, o3, "source_manifest", manifest)
	if out := e2.mustEvent(h, o3, nodeproto.EventSnapshotFixed, nodeproto.SnapshotFixedPayload{ManifestDigest: manifest, Kind: "local", Consistency: "stable_copy_best_effort", ManifestArtifactID: mid4}); *out.SnapshotID != *c3.Lease.SnapshotID {
		t.Fatal("retry snapshot id changed")
	}
	e2.mustEvent(h, o3, nodeproto.EventRunAdvance, nodeproto.RunAdvancePayload{Status: "running"})
	if st := e2.mustEvent(h, o3, nodeproto.EventTaskStatus, nodeproto.TaskStatusPayload{TaskID: taskID, ExpectedVersion: *t1.Version, Status: "running"}); *st.Version != *t1.Version+1 {
		t.Fatalf("task status after retry %+v", st)
	}
	_ = snap
}

func TestArtifactFinalizeIdempotencyAndFencing(t *testing.T) {
	e := newW06(t)
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
