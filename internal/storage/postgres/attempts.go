package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

type Lease struct {
	ID, RunID, NodeID, WorkerID string
	Number, Epoch               int64
	ExpiresAt                   time.Time
}
type Ownership struct {
	AttemptID, NodeID, WorkerID string
	Epoch                       int64
}
type attempt struct {
	Lease
	Status, Cleanup    string
	Digest, Completion *string
}

const attemptColumns = "id::text,run_id::text,node_id::text,worker_id::text,number,epoch,expires_at,status,cleanup_state,completion_digest,completion_status"

func readAttempt(row pgx.Row) (attempt, error) {
	var a attempt
	err := row.Scan(&a.ID, &a.RunID, &a.NodeID, &a.WorkerID, &a.Number, &a.Epoch, &a.ExpiresAt, &a.Status, &a.Cleanup, &a.Digest, &a.Completion)
	return a, err
}
func validOwner(o Ownership) bool { return validIDs(o.AttemptID, o.NodeID, o.WorkerID) && o.Epoch > 0 }
func lockAttempt(ctx context.Context, tx pgx.Tx, id string) (Run, attempt, error) {
	var run, project string
	if err := tx.QueryRow(ctx, "SELECT a.run_id::text,r.project_id::text FROM attempts a JOIN runs r ON r.id=a.run_id WHERE a.id=$1", id).Scan(&run, &project); err != nil {
		return Run{}, attempt{}, err
	}
	// Common lock order: project -> run -> attempt. The project is only share-locked
	// (ordering against project-level writers; no ACL decision is made here, so cleanup
	// remains possible after ACL revocation). The run and attempt rows carry the real
	// exclusion, and nothing in an attempt transaction upgrades the project lock.
	var ignored string
	if err := tx.QueryRow(ctx, "SELECT id::text FROM projects WHERE id=$1 FOR SHARE", project).Scan(&ignored); err != nil {
		return Run{}, attempt{}, err
	}
	r, err := lockRun(ctx, tx, run)
	if err != nil {
		return r, attempt{}, err
	}
	a, err := readAttempt(tx.QueryRow(ctx, "SELECT "+attemptColumns+" FROM attempts WHERE id=$1 FOR UPDATE", id))
	return r, a, err
}
func sameOwner(r Run, a attempt, o Ownership) bool {
	return a.NodeID == o.NodeID && a.WorkerID == o.WorkerID && a.Epoch == o.Epoch && r.LeaseEpoch == o.Epoch
}
func activeLease(ctx context.Context, tx pgx.Tx, r Run, a attempt, o Ownership) error {
	if !sameOwner(r, a, o) || a.Status != "leased" || r.Status.Terminal() || r.Status == domain.RunQueued || r.Status == domain.RunRecoveryRequired {
		return domain.ErrStaleAttempt
	}
	var valid bool
	if err := tx.QueryRow(ctx, "SELECT expires_at>clock_timestamp() FROM attempts WHERE id=$1", a.ID).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return domain.ErrStaleAttempt
	}
	return nil
}

// BeginAttempt reserves ownership only. W06/W07 must also perform resource admission
// atomically before exposing a production claim endpoint.
func (s *Store) BeginAttempt(ctx context.Context, run, node, worker string, ttl time.Duration) (Lease, error) {
	if !validIDs(run, node, worker) || ttl < time.Second || ttl > time.Hour {
		return Lease{}, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (Lease, error) {
		var actor string
		if err := tx.QueryRow(ctx, "SELECT actor_id::text FROM runs WHERE id=$1", run).Scan(&actor); err != nil {
			return Lease{}, err
		}
		r, err := authorizeRun(ctx, tx, actor, run, true)
		if err != nil {
			return Lease{}, err
		}
		if r.Status != domain.RunQueued {
			return Lease{}, domain.ErrConflict
		}
		var active bool
		if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM attempts WHERE run_id=$1 AND status!='released')", run).Scan(&active); err != nil {
			return Lease{}, err
		}
		if active {
			return Lease{}, domain.ErrConflict
		}
		a, err := readAttempt(tx.QueryRow(ctx, "INSERT INTO attempts(id,run_id,node_id,worker_id,number,epoch,expires_at,status) SELECT $1,$2,$3,$4,coalesce(max(number),0)+1,$5,clock_timestamp()+$6*interval '1 second','leased' FROM attempts WHERE run_id=$2 RETURNING "+attemptColumns, domain.NewID(), run, node, worker, r.LeaseEpoch+1, ttl.Seconds()))
		if err != nil {
			return Lease{}, err
		}
		_, err = tx.Exec(ctx, "UPDATE runs SET lease_epoch=$2,status='preparing' WHERE id=$1", run, a.Epoch)
		if err != nil {
			return Lease{}, err
		}
		r.Status = domain.RunPreparing
		r.LeaseEpoch = a.Epoch
		err = appendEvent(ctx, tx, &r, "attempt.leased", map[string]any{"attempt_id": a.ID, "epoch": a.Epoch}, true)
		return a.Lease, err
	})
}
func (s *Store) Heartbeat(ctx context.Context, o Ownership, ttl time.Duration) (Lease, error) {
	if !validOwner(o) || ttl < time.Second || ttl > time.Hour {
		return Lease{}, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (Lease, error) {
		r, a, err := lockAttempt(ctx, tx, o.AttemptID)
		if err != nil {
			return Lease{}, err
		}
		if err = activeLease(ctx, tx, r, a, o); err != nil {
			return Lease{}, err
		}
		err = tx.QueryRow(ctx, "UPDATE attempts SET expires_at=clock_timestamp()+$2*interval '1 second' WHERE id=$1 RETURNING expires_at", a.ID, ttl.Seconds()).Scan(&a.ExpiresAt)
		return a.Lease, err
	})
}
func (s *Store) Advance(ctx context.Context, o Ownership, expected int64, next domain.RunStatus) (Run, error) {
	if !validOwner(o) || expected < 1 {
		return Run{}, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (Run, error) {
		r, a, err := lockAttempt(ctx, tx, o.AttemptID)
		if err != nil {
			return r, err
		}
		if err = activeLease(ctx, tx, r, a, o); err != nil {
			return r, err
		}
		if r.Version != expected {
			return r, domain.ErrConflict
		}
		if (next != domain.RunRunning && next != domain.RunFinalizing && next != domain.RunCancelling) || !domain.CanTransitionRun(r.Status, next) || (next == domain.RunRunning && r.SnapshotID == nil) {
			return r, domain.ErrInvalid
		}
		err = transitionRun(ctx, tx, &r, next, "run.status", map[string]any{"status": next})
		return r, err
	})
}

// receiptCurrent reports whether an accepted completion receipt still describes the
// run. A finalizing receipt stays valid once the report service publishes the
// result (completed/partial). The caller has already matched node, worker and epoch
// against both the attempt and the run, so a requeued run or a newer epoch never
// replays, and replay never mutates anything.
func receiptCurrent(stored string, current domain.RunStatus) bool {
	if string(current) == stored {
		return true
	}
	return stored == string(domain.RunFinalizing) && (current == domain.RunCompleted || current == domain.RunPartial)
}

// FinishAttempt records execution/cleanup only. Successful outcomes remain finalizing
// until a later report service validates and publishes artifacts (W09).
func (s *Store) FinishAttempt(ctx context.Context, o Ownership, outcome, cleanup string) (Run, error) {
	if !validOwner(o) || (cleanup != "confirmed" && cleanup != "uncertain") || (outcome != "succeeded" && outcome != "partial" && outcome != "failed" && outcome != "cancelled") {
		return Run{}, domain.ErrInvalid
	}
	digest := domain.Digest([]byte(outcome + "\x00" + cleanup))
	return transaction(ctx, s, func(tx pgx.Tx) (Run, error) {
		r, a, err := lockAttempt(ctx, tx, o.AttemptID)
		if err != nil {
			return r, err
		}
		if !sameOwner(r, a, o) {
			return r, domain.ErrStaleAttempt
		}
		// Only an identical accepted payload whose receipt is still current is replayed
		// (see receiptCurrent). This path never mutates data or revives an expired lease.
		if a.Digest != nil {
			if *a.Digest == digest && a.Completion != nil && receiptCurrent(*a.Completion, r.Status) {
				return r, nil
			}
			return r, domain.ErrConflict
		}
		if err = activeLease(ctx, tx, r, a, o); err != nil {
			return r, err
		}
		next := domain.RunFailed
		if outcome == "succeeded" || outcome == "partial" {
			if r.SnapshotID == nil || r.Status != domain.RunFinalizing {
				return r, domain.ErrInvalid
			}
			var total, pending, bad int
			if err = tx.QueryRow(ctx, "SELECT count(*),count(*) FILTER(WHERE status IN('queued','blocked','running')),count(*) FILTER(WHERE required_for_verdict AND status!='succeeded') FROM tasks WHERE run_id=$1", r.ID).Scan(&total, &pending, &bad); err != nil {
				return r, err
			}
			if total == 0 || pending > 0 || (outcome == "succeeded" && bad > 0) {
				return r, domain.ErrInvalid
			}
			next = domain.RunFinalizing
		}
		if outcome == "cancelled" || r.Status == domain.RunCancelRequested || r.Status == domain.RunCancelling {
			if r.Status != domain.RunCancelRequested && r.Status != domain.RunCancelling {
				return r, domain.ErrInvalid
			}
			next = domain.RunCancelled
		}
		status := "released"
		if cleanup == "uncertain" {
			status = "recovery_required"
			next = domain.RunRecoveryRequired
		}
		_, err = tx.Exec(ctx, "UPDATE attempts SET status=$2,cleanup_state=$3,completion_digest=$4,completion_status=$5 WHERE id=$1", a.ID, status, cleanup, digest, next)
		if err != nil {
			return r, err
		}
		// Cancellation is receipted as cancel_requested -> cancelling -> cancelled,
		// each with its own event, in this one transaction.
		if next == domain.RunCancelled && r.Status == domain.RunCancelRequested {
			if err = transitionRun(ctx, tx, &r, domain.RunCancelling, "run.status", map[string]any{"status": domain.RunCancelling}); err != nil {
				return r, err
			}
		}
		payload := map[string]any{"attempt_id": a.ID, "status": next, "cleanup_state": cleanup}
		// Confirmed cleanup of a failed/cancelled run settles its open tasks; uncertain
		// cleanup leaves them untouched until ConfirmCleanup.
		if status == "released" && (next == domain.RunFailed || next == domain.RunCancelled) {
			if payload["closed_tasks"], err = closeTasks(ctx, tx, r.ID, a.ID, next); err != nil {
				return r, err
			}
		}
		err = transitionRun(ctx, tx, &r, next, "attempt.finished", payload)
		return r, err
	})
}
func (s *Store) ExpireAttempt(ctx context.Context, id string) (Run, error) {
	if !validIDs(id) {
		return Run{}, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (Run, error) {
		r, a, err := lockAttempt(ctx, tx, id)
		if err != nil {
			return r, err
		}
		var expired bool
		if err = tx.QueryRow(ctx, "SELECT expires_at<=clock_timestamp() FROM attempts WHERE id=$1", id).Scan(&expired); err != nil {
			return r, err
		}
		if a.Status != "leased" || !expired || a.Epoch != r.LeaseEpoch {
			return r, domain.ErrConflict
		}
		_, err = tx.Exec(ctx, "UPDATE attempts SET status='recovery_required',cleanup_state='uncertain' WHERE id=$1", id)
		if err != nil {
			return r, err
		}
		err = transitionRun(ctx, tx, &r, domain.RunRecoveryRequired, "attempt.expired", map[string]any{"attempt_id": id})
		return r, err
	})
}

// ConfirmCleanup is a reconciliation operation for the authenticated owning node.
// The caller must obtain actual process cleanup evidence; W02 cannot inspect the OS.
func (s *Store) ConfirmCleanup(ctx context.Context, o Ownership) (Run, error) {
	if !validOwner(o) {
		return Run{}, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (Run, error) {
		r, a, err := lockAttempt(ctx, tx, o.AttemptID)
		if err != nil {
			return r, err
		}
		if !sameOwner(r, a, o) {
			return r, domain.ErrStaleAttempt
		}
		if a.Status == "released" && a.Cleanup == "confirmed" && r.Status == domain.RunFailed {
			return r, nil
		}
		if a.Status != "recovery_required" || r.Status != domain.RunRecoveryRequired {
			return r, domain.ErrConflict
		}
		_, err = tx.Exec(ctx, "UPDATE attempts SET status='released',cleanup_state='confirmed' WHERE id=$1", a.ID)
		if err != nil {
			return r, err
		}
		closed, err := closeTasks(ctx, tx, r.ID, a.ID, domain.RunFailed)
		if err != nil {
			return r, err
		}
		err = transitionRun(ctx, tx, &r, domain.RunFailed, "attempt.cleanup_confirmed", map[string]any{"attempt_id": a.ID, "closed_tasks": closed})
		return r, err
	})
}
