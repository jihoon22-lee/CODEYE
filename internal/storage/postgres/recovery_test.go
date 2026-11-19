package postgres

import (
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
)

// expiredOnNode claims a run on n, fixes its snapshot (so n holds it) and
// lets the lease expire into recovery_required.
func (e *nodeEnv) expiredOnNode(n testNode, key string) (Run, Ownership) {
	e.t.Helper()
	e.idle(n)
	r := e.enqueue(key, deepReview)
	o := owner(n, e.claimOK(n, n.workers[0], "claim-"+key))
	manifest := domain.Digest([]byte("manifest-" + key))
	mid := e.artifact(n, o, "source_manifest", manifest)
	e.mustEvent(n, o, nodeproto.EventSnapshotFixed, nodeproto.SnapshotFixedPayload{ManifestDigest: manifest, Kind: "local", Consistency: "stable_copy_best_effort", ManifestArtifactID: mid})
	sqlExec(e.t, e.s, "UPDATE attempts SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", o.AttemptID)
	if moved, err := e.s.SweepExpiredAttempts(e.ctx); err != nil || moved != 1 {
		e.t.Fatalf("sweep %d %v", moved, err)
	}
	return r, o
}

// TC-227: the operator path for a recovery_required run. Only that state is
// accepted; a non-member cannot; the confirmation releases the attempt and
// its reservation, fails the run with reason operator_confirmed and writes
// run.recovery_confirmed with the actor, then queues the work again (on the
// snapshot when a live node holds it, else as a new run). The dead attempt's
// late writes stay fenced.
func TestConfirmRecoveryReleasesAndRequeues(t *testing.T) {
	e := newNodeEnv(t)
	a := e.enroll("a", e.root.ID)
	r, o := e.expiredOnNode(a, "r1")
	stranger, err := e.s.CreateUser(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.s.ConfirmRecovery(e.ctx, UserPrincipal(stranger), r.ID)
	expect(t, err, domain.ErrNotFound)
	if e.count("SELECT count(*) FROM capacity_reservations WHERE released_at IS NULL") != 1 {
		t.Fatal("reservation released before confirmation")
	}
	e.idle(a) // the holder is live
	rec, err := e.s.ConfirmRecovery(e.ctx, UserPrincipal(e.user), r.ID)
	if err != nil || rec.Confirmed.Status != domain.RunFailed || rec.AttemptID != o.AttemptID || rec.Epoch != o.Epoch || rec.RequeueError != nil {
		t.Fatalf("%+v %v", rec, err)
	}
	if rec.Requeued == nil || rec.How != RetrySameRun || rec.Requeued.ID != r.ID || rec.Requeued.Status != domain.RunQueued {
		t.Fatalf("live holder: %+v", rec)
	}
	if e.count("SELECT count(*) FROM capacity_reservations WHERE released_at IS NULL") != 0 ||
		e.count("SELECT count(*) FROM attempts WHERE id=$1 AND status='released' AND cleanup_state='confirmed'", o.AttemptID) != 1 {
		t.Fatal("attempt or reservation not released")
	}
	if e.count("SELECT count(*) FROM events WHERE run_id=$1 AND type='attempt.cleanup_confirmed' AND payload->>'reason'='operator_confirmed'", r.ID) != 1 {
		t.Fatal("confirmation event missing")
	}
	if e.count("SELECT count(*) FROM audit_events WHERE action='run.recovery_confirmed' AND actor_kind='user' AND actor_user_id=$1 AND target_id=$2 AND (detail->>'epoch')::int=$3", e.user, r.ID, o.Epoch) != 1 {
		t.Fatal("confirmation audit row missing")
	}
	// The dead attempt's late completion and renewal stay fenced.
	_, err = e.s.CompleteAttempt(e.ctx, a.id, o, Completion{Outcome: "failed", Cleanup: "confirmed"})
	expect(t, err, domain.ErrStaleAttempt)
	_, err = e.s.RenewAttempt(e.ctx, a.id, o)
	expect(t, err, domain.ErrStaleAttempt)
	// Only a recovery_required run is accepted.
	_, err = e.s.ConfirmRecovery(e.ctx, UserPrincipal(e.user), r.ID)
	expect(t, err, domain.ErrConflict)

	// A holder that went silent: the work becomes a new run, by the
	// operator CLI on behalf of the requester (audit actor system).
	b := e.enroll("b", e.root.ID)
	r2, _ := e.expiredOnNode(b, "r2")
	sqlExec(t, e.s, "UPDATE nodes SET last_heartbeat_at=clock_timestamp()-interval '10 minutes' WHERE id=$1", b.id)
	rec, err = e.s.ConfirmRecovery(e.ctx, OperatorPrincipal(), r2.ID)
	if err != nil || rec.Requeued == nil || rec.How != RetryNewRun || rec.Requeued.ID == r2.ID || rec.Requeued.ActorID != e.user {
		t.Fatalf("stale holder: %+v %v", rec, err)
	}
	if e.count("SELECT count(*) FROM audit_events WHERE action='run.recovery_confirmed' AND actor_kind='operator_cli' AND target_id=$1", r2.ID) != 1 ||
		e.count("SELECT count(*) FROM audit_events WHERE action='run.requested' AND actor_kind='system' AND actor_user_id=$1 AND target_id=$2", e.user, rec.Requeued.ID) != 1 {
		t.Fatal("operator confirmation or system requeue not audited")
	}
}

// TC-228: a node that reports snapshot_unavailable for a pinned retry no longer
// holds the snapshot: the next retry acquires anew instead of returning to
// that node.
func TestSnapshotUnavailableDropsTheLocation(t *testing.T) {
	e := newNodeEnv(t)
	a := e.enroll("a", e.root.ID)
	r, o := e.expiredOnNode(a, "r1")
	if _, err := e.s.ConfirmCleanupNode(e.ctx, a.id, o); err != nil {
		t.Fatal(err)
	}
	e.idle(a)
	next, how, err := e.s.RetryRun(e.ctx, e.user, r.ID)
	if err != nil || how != RetrySameRun || next.ID != r.ID {
		t.Fatalf("%v %v %v", next, how, err)
	}
	o2 := owner(a, e.claimOK(a, a.workers[0], "pinned"))
	reason := "snapshot_unavailable"
	if _, err := e.s.CompleteAttempt(e.ctx, a.id, o2, Completion{Outcome: "failed", Cleanup: "confirmed", Reason: &reason}); err != nil {
		t.Fatal(err)
	}
	if e.count("SELECT count(*) FROM snapshot_locations WHERE node_id=$1", a.id) != 0 {
		t.Fatal("location kept after snapshot_unavailable")
	}
	e.idle(a)
	again, how, err := e.s.RetryRun(e.ctx, e.user, r.ID)
	if err != nil || how != RetryNewRun || again.ID == r.ID {
		t.Fatalf("%v %v %v", again, how, err)
	}
}
