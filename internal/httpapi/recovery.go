package httpapi

import (
	"errors"
	"net/http"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// RecoveryNotQueued is the recover answer's retry value when the
// confirmation was recorded but the work was not queued again.
const RecoveryNotQueued = "not_queued"

// recoveryAck is the runs.recover answer: the run that runs again (or the
// confirmed, now failed run when it was not queued again), how, and the
// attempt whose cleanup the caller confirmed.
type recoveryAck struct {
	runAck
	Retry     string `json:"retry"`
	AttemptID string `json:"attempt_id"`
	Epoch     int64  `json:"epoch"`
}

// recoverRun (POST /runs/{id}/recover) records the caller's confirmation
// that the processes of a recovery_required run's attempt are gone, then
// runs the work again (Store.ConfirmRecovery). The body must state the
// confirmation explicitly: {"cleanup_confirmed": true}. Uncertain cleanup is
// never assumed clean without it.
func (s *Server) recoverRun(q *request) {
	body, ok := q.readBody()
	if !ok {
		return
	}
	var in struct {
		CleanupConfirmed *bool `json:"cleanup_confirmed"`
	}
	if domain.DecodeStrict(body, &in) != nil || in.CleanupConfirmed == nil || !*in.CleanupConfirmed {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", `body must be {"cleanup_confirmed": true}: confirm that no process of the attempt is left on its node`, false)
		return
	}
	ctx, cancel := q.ctx()
	defer cancel()
	rec, err := s.store.ConfirmRecovery(ctx, q.principal(), q.params[0])
	if err != nil {
		if errors.Is(err, domain.ErrConflict) {
			writeError(q.w, q.r, http.StatusConflict, "RUN_NOT_RECOVERY_REQUIRED", "only a run that needs recovery (recovery_required) can be recovered", false)
		} else {
			s.storeError(q, err)
		}
		return
	}
	ack := recoveryAck{runAck: ackOf(rec.Confirmed), Retry: RecoveryNotQueued, AttemptID: rec.AttemptID, Epoch: rec.Epoch}
	status := http.StatusOK
	if rec.Requeued != nil {
		ack.runAck, ack.Retry, status = ackOf(*rec.Requeued), rec.How, http.StatusAccepted
	}
	q.w.Header().Set("Location", ack.URL)
	writeJSON(q.w, status, ack)
}
