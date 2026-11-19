package postgres

import (
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
)

// TC-124 (W25, docs/OPERATIONS.md#저장-영역과-상한): a node storage area at its limit or the artifact
// store at its limit refuses NEW attempts with storage_quota_exceeded; the
// running attempt keeps its lease and can still upload; the artifact
// accounting counts one content once.
func TestW25StorageQuotaRefusesOnlyNewClaims(t *testing.T) {
	e := newW06(t)
	e.projectLimit(5)
	n := e.enroll("a", e.root.ID)
	first := e.enqueue("first", deepReview)

	// The heartbeat field is validated: node areas only, no duplicates.
	for _, bad := range [][]string{{"artifacts"}, {"bogus"}, {"cache", "cache"}} {
		in := hb(n.workers, "idle", capable(e.root.ID))
		in.StorageQuotaExceeded = bad
		_, err := e.s.NodeHeartbeat(e.ctx, n.id, in)
		expect(t, err, domain.ErrInvalid)
	}
	over := hb(n.workers, "idle", capable(e.root.ID))
	over.StorageQuotaExceeded = []string{"workspace", "cache"}
	e.beat(n, over)
	if got := e.count("SELECT cardinality(storage_quota_exceeded) FROM nodes WHERE id=$1", n.id); got != 2 {
		t.Fatalf("stored areas %d", got)
	}
	e.noClaim(n, n.workers[0], "q1")
	if got := e.waiting(first.ID); got != nodeproto.WaitingStorageQuotaExceeded {
		t.Fatalf("node over quota: %q", got)
	}
	ns, err := e.s.NodeStateByID(e.ctx, n.id)
	if err != nil || len(ns.QuotaExceeded) != 2 || ns.QuotaExceeded[0] != "cache" {
		t.Fatalf("node state %+v %v", ns, err)
	}
	// Capacity shortage still wins over the quota reason (not quota-only).
	full := over
	full.Capacity.DiskAvailableBytes = 1
	e.beat(n, full)
	if got := e.waiting(first.ID); got != nodeproto.WaitingCapacityFull {
		t.Fatalf("capacity first: %q", got)
	}
	// Back under the limit (field omitted): the run is claimed.
	e.idle(n)
	if got := e.waiting(first.ID); got != "" {
		t.Fatalf("claimable: %q", got)
	}
	o := owner(n, e.claimOK(n, n.workers[0], "q2"))
	// The node goes over its limit while the attempt runs: the lease is
	// renewed and uploads are accepted (existing work is protected).
	busy := hb(n.workers[1:], "idle", capable(e.root.ID))
	busy.ObservedWorkers = append(busy.ObservedWorkers, nodeproto.ObservedWorker{ID: n.workers[0], State: "busy", AttemptID: &o.AttemptID})
	busy.StorageQuotaExceeded = []string{"snapshot"}
	e.beat(n, busy)
	if _, err := e.s.RenewAttempt(e.ctx, n.id, o); err != nil {
		t.Fatalf("renew while over quota: %v", err)
	}
	sha := domain.Digest([]byte("blob-1"))
	e.artifact(n, o, "stdout", sha)
	e.artifact(n, o, "stderr", sha) // same content: counted once
	second := e.enqueue("second", deepReview)
	e.noClaim(n, n.workers[1], "q3")
	if got := e.waiting(second.ID); got != nodeproto.WaitingStorageQuotaExceeded {
		t.Fatalf("second run: %q", got)
	}

	// Artifact store limit (database accounting).
	u, err := e.s.ArtifactUsage(e.ctx)
	if err != nil || u.Bytes != 10 || u.Quota != nil {
		t.Fatalf("usage %+v %v", u, err)
	}
	busy.StorageQuotaExceeded = nil
	e.beat(n, busy)
	if err := e.s.SetArtifactQuota(e.ctx, 10); err != nil {
		t.Fatal(err)
	}
	rev := e.count("SELECT revision FROM scheduler_limits")
	if err := e.s.SetArtifactQuota(e.ctx, 10); err != nil || e.count("SELECT revision FROM scheduler_limits") != rev {
		t.Fatal("unchanged quota must not bump the revision")
	}
	e.noClaim(n, n.workers[1], "q4")
	if got := e.waiting(second.ID); got != nodeproto.WaitingStorageQuotaExceeded {
		t.Fatalf("artifact quota: %q", got)
	}
	// The running attempt may still upload past the limit.
	e.artifact(n, o, "observations", domain.Digest([]byte("blob-2")))
	if err := e.s.SetArtifactQuota(e.ctx, 1<<30); err != nil {
		t.Fatal(err)
	}
	if got := e.waiting(second.ID); got != "" {
		t.Fatalf("raised quota: %q", got)
	}
	e.claimOK(n, n.workers[1], "q5")
	if err := e.s.SetArtifactQuota(e.ctx, 0); err != nil {
		t.Fatal(err)
	}
	if u, _ := e.s.ArtifactUsage(e.ctx); u.Quota != nil || u.Bytes != 20 {
		t.Fatalf("removed quota %+v", u)
	}
}

// TC-32 store part (W25, docs/OPERATIONS.md#복원과-fencing): FenceRestored revokes every node,
// session and unused token, and fences a leased attempt whose lease has NOT
// expired exactly like an expired one; the old ownership is never honoured
// and a new node can work. The pg_dump/pg_restore round trip is
// TestTC32BackupRestoreRoundTrip (internal/backup).
func TestW25FenceRestoredNeverRevivesLeases(t *testing.T) {
	e := newW06(t)
	e.projectLimit(5)
	n := e.enroll("old", e.root.ID)
	e.idle(n)
	r := e.enqueue("r1", deepReview)
	o := owner(n, e.claimOK(n, n.workers[0], "k"))
	queued := e.enqueue("r2", deepReview)
	if err := e.s.IssueLaunchToken(e.ctx, e.user, hashOf("launch-a"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := e.s.IssueLaunchToken(e.ctx, e.user, hashOf("launch-b"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.ExchangeLaunchToken(e.ctx, hashOf("launch-a"), hashOf("session-a"), time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.IssueEnrollmentToken(e.ctx, "default", []string{e.root.ID}, hashOf("enroll-unused"), time.Hour); err != nil {
		t.Fatal(err)
	}
	f, err := e.s.FenceRestored(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if f.NodesRevoked != 1 || f.SessionsRevoked != 1 || f.LaunchTokens != 1 || f.EnrollmentTokens != 1 || f.AttemptsFenced != 1 || f.ReservationsHeld != 1 || f.QueuedRunsKept != 1 || f.RecoveryRequired != 1 {
		t.Fatalf("fence %+v", f)
	}
	got, _ := e.s.GetRun(e.ctx, e.user, r.ID)
	if got.Status != domain.RunRecoveryRequired {
		t.Fatalf("fenced run %s", got.Status)
	}
	if e.count("SELECT count(*) FROM events WHERE run_id=$1 AND type='attempt.expired'", r.ID) != 1 {
		t.Fatal("fence must record the expiry event")
	}
	_, err = e.s.RenewAttempt(e.ctx, n.id, o)
	expect(t, err, domain.ErrStaleAttempt)
	_, err = e.s.AuthenticateNode(e.ctx, hashOf(n.secret))
	expect(t, err, domain.ErrUnauthenticated)
	_, err = e.s.SessionByTokenHash(e.ctx, hashOf("session-a"))
	if err == nil {
		t.Fatal("restored session still valid")
	}
	_, err = e.s.ExchangeLaunchToken(e.ctx, hashOf("launch-b"), hashOf("session-b"), time.Hour)
	if err == nil {
		t.Fatal("restored launch token still usable")
	}
	_, err = e.s.EnrollNode(e.ctx, Enrollment{TokenSHA256: hashOf("enroll-unused"), SecretSHA256: hashOf("s"), Name: "x", OS: "linux", Arch: "amd64", Protocol: "0.1"})
	if err == nil {
		t.Fatal("restored enrollment token still usable")
	}
	// A freshly enrolled node takes the queued run; the fenced one stays.
	m := e.enroll("new", e.root.ID)
	e.idle(m)
	c := e.claimOK(m, m.workers[0], "new")
	if c.Lease.RunID != queued.ID {
		t.Fatalf("new node claimed %s", c.Lease.RunID)
	}
	if again, err := e.s.FenceRestored(e.ctx); err != nil || again.NodesRevoked != 1 || again.AttemptsFenced != 1 {
		// Fencing twice is not part of restore; it only proves the function
		// treats every leased attempt the same way.
		t.Fatalf("second fence %+v %v", again, err)
	}
}
