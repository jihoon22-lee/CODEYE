package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// RecoveryOperatorConfirmed is the reason of the attempt.cleanup_confirmed
// event when a person, not the owning node, confirmed the cleanup.
const RecoveryOperatorConfirmed = "operator_confirmed"

// Recovery is the outcome of ConfirmRecovery: the run after the
// confirmation (failed), and the run that runs again with how
// (RetrySameRun or RetryNewRun). Requeued is nil when the confirmation was
// recorded but the work was not queued again (RequeueError says why).
type Recovery struct {
	Confirmed    Run
	AttemptID    string
	Epoch        int64
	Requeued     *Run
	How          string
	RequeueError error
}

// ConfirmRecovery records a person's confirmation that the processes of a
// recovery_required run's attempt are gone and queues the work again.
//
// Cleanup that the node could not confirm (a dead or revoked node, an
// unverifiable process group) is never treated as clean on its own: this
// statement is the evidence, written as a run.recovery_confirmed audit row
// with the actor in the same transaction that releases the attempt and its
// reservation and fails the run (event attempt.cleanup_confirmed, reason
// operator_confirmed). by is the operator CLI or a user who may run the
// project (the same permission as retry). A run that is not
// recovery_required is ErrConflict.
//
// The run is then queued again through RetryRun: on its snapshot when a live
// node still holds it, otherwise as a new run of the same request. A user
// retries as themselves; the operator CLI retries on behalf of the run's
// requester (audit actor system), whose permission the claim checks again.
// When that second step fails, the confirmation stands and the run is an
// ordinary failed run (Recovery.RequeueError).
func (s *Store) ConfirmRecovery(ctx context.Context, by Principal, runID string) (Recovery, error) {
	if !by.valid() || !validIDs(runID) {
		return Recovery{}, domain.ErrInvalid
	}
	out, err := transaction(ctx, s, func(tx pgx.Tx) (Recovery, error) {
		var rec Recovery
		var r Run
		var err error
		if by.operator {
			project, err := runProject(ctx, tx, runID)
			if err != nil {
				return rec, err
			}
			if _, err := tx.Exec(ctx, "SELECT 1 FROM projects WHERE id=$1 FOR UPDATE", project); err != nil {
				return rec, err
			}
			r, err = lockRun(ctx, tx, runID)
			if err != nil {
				return rec, err
			}
		} else if r, err = authorizeRun(ctx, tx, by.user, runID, true); err != nil {
			return rec, err
		}
		if r.Status != domain.RunRecoveryRequired {
			return Recovery{Confirmed: r}, domain.ErrConflict
		}
		var a attempt
		a, err = readAttempt(tx.QueryRow(ctx, "SELECT "+attemptColumns+" FROM attempts WHERE run_id=$1 AND status='recovery_required' FOR UPDATE", r.ID))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return Recovery{Confirmed: r}, domain.ErrConflict
			}
			return rec, err
		}
		if _, err = tx.Exec(ctx, "UPDATE attempts SET status='released',cleanup_state='confirmed' WHERE id=$1", a.ID); err != nil {
			return rec, err
		}
		if err = releaseReservation(ctx, tx, a.ID); err != nil {
			return rec, err
		}
		closed, err := closeTasks(ctx, tx, r.ID, a.ID, domain.RunFailed)
		if err != nil {
			return rec, err
		}
		if err = transitionRun(ctx, tx, &r, domain.RunFailed, "attempt.cleanup_confirmed", map[string]any{"attempt_id": a.ID, "closed_tasks": closed, "reason": RecoveryOperatorConfirmed}); err != nil {
			return rec, err
		}
		e := principalAudit(by, AuditRecoveryConfirmed).target("run", r.ID).in(r.ProjectID).with(map[string]any{"epoch": a.Epoch, "status": string(domain.RunRecoveryRequired)})
		if err = writeAudit(ctx, tx, e); err != nil {
			return rec, err
		}
		return Recovery{Confirmed: r, AttemptID: a.ID, Epoch: a.Epoch}, nil
	})
	if err != nil {
		return out, err
	}
	actor, retryCtx := by.user, ctx
	if by.operator {
		actor, retryCtx = out.Confirmed.ActorID, asSystem(ctx)
	}
	next, how, err := s.RetryRun(retryCtx, actor, runID)
	if err != nil {
		out.RequeueError = err
		return out, nil
	}
	out.Requeued, out.How = &next, how
	return out, nil
}
