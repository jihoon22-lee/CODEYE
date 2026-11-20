package postgres

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
)

// TC-21: two agents race for one run; exactly one lease, epoch, reservation.
func TestTC21ConcurrentClaimSingleOwner(t *testing.T) {
	e := newNodeEnv(t)
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
	// r2 is in the same project as the running r1; this test is about
	// ownership, so lift the per-project limit (default 1, see TC-73).
	e.projectLimit(2)
	e.enqueue("r2", deepReview)
	e.idle(c3)
	e.noClaim(c3, c3.workers[0], "empty")
	if c := e.claimOK(c3, c3.workers[0], "next"); c.Lease.RunID == r.ID {
		t.Fatal("claimed the same run twice")
	}
}

// TC-21: an event-insert failure after the reservation leaves nothing behind.
func TestTC21ClaimFailureLeavesNoPartialState(t *testing.T) {
	e := newNodeEnv(t)
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
	e := newNodeEnv(t)
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
	e := newNodeEnv(t)
	// Three runs of one project run concurrently here; this test is about
	// node resource accounting, so lift the per-project limit (default 1).
	e.projectLimit(3)
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
	e := newNodeEnv(t)
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

func TestClaimRechecksActorPermission(t *testing.T) {
	e := newNodeEnv(t)
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
	if e.count("SELECT count(*) FROM attempts") != 0 {
		t.Fatal("attempt created for revoked actor")
	}
	// Legacy source grants do not govern platform acquisition.
	r2 := e.enqueue("o1", deepReview)
	sqlExec(t, e.s, "DELETE FROM source_root_grants WHERE source_root_id=$1 AND user_id=$2", e.root.ID, e.user)
	if got := e.claimOK(n, n.workers[0], "k2"); got.Lease.RunID != r2.ID {
		t.Fatalf("claimed run %s, want %s", got.Lease.RunID, r2.ID)
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
	e := &nodeEnv{t: t, s: s, ctx: ctx, user: user, root: root, project: p}
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
	e := newNodeEnv(t)
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
	e2 := newNodeEnv(t)
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
