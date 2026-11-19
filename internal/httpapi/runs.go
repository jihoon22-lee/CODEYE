package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

type runAck struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Version int64  `json:"version"`
	URL     string `json:"url"`
}

func ackOf(r postgres.Run) runAck {
	return runAck{r.ID, string(r.Status), r.Version, apiPrefix + "/runs/" + r.ID}
}

func validIdempotencyKey(k string) bool {
	if k == "" || len(k) > maxIdempotency {
		return false
	}
	for i := 0; i < len(k); i++ {
		if k[i] < 0x21 || k[i] > 0x7e {
			return false
		}
	}
	return true
}

func (s *Server) createRun(q *request) {
	key := q.r.Header.Get("Idempotency-Key")
	if !validIdempotencyKey(key) {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "Idempotency-Key header must be 1..200 visible ASCII characters", false)
		return
	}
	body, ok := q.readBody()
	if !ok {
		return
	}
	var req domain.RunRequest
	if domain.DecodeStrict(body, &req) != nil {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "body must be a run request object", false)
		return
	}
	if err := req.Validate(); err != nil {
		s.storeError(q, err)
		return
	}
	ctx, cancel := q.ctx()
	defer cancel()
	run, err := s.store.Enqueue(ctx, q.session.UserID, q.params[0], key, body)
	if err != nil {
		if errors.Is(err, domain.ErrConflict) {
			writeError(q.w, q.r, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency-Key was already used with a different body", false)
		} else {
			s.storeError(q, err)
		}
		return
	}
	ack := ackOf(run)
	q.w.Header().Set("Location", ack.URL)
	writeJSON(q.w, http.StatusAccepted, ack)
}

// progressView keeps the run-status fields (stage is the run status), adds the
// stage view computed exactly like the project activity and the plan
// revision and weighted estimate (progress.Plan, same computation).
type progressView struct {
	Stage            string           `json:"stage"`
	PlanRevision     *int             `json:"plan_revision"`
	CompletedTasks   int64            `json:"completed_tasks"`
	TotalTasks       *int64           `json:"total_tasks"`
	EstimatedPercent *float64         `json:"estimated_percent"`
	PlannedWeight    *int             `json:"planned_weight"`
	FinishedWeight   *int             `json:"finished_weight"`
	WeightBasis      *string          `json:"weight_basis"`
	PlanChanges      []planChangeView `json:"plan_changes"`
	WaitingReason    *string          `json:"waiting_reason"`
	Detail           string           `json:"detail"`
	Stages           []stageView      `json:"stages"`
	LastEventAt      *time.Time       `json:"last_event_at"`
	CheckedAt        time.Time        `json:"checked_at"`
}

// planChangeView explains one plan revision after the first: which task
// (stage, weight) was added after the plan was fixed and the denominator
// after it.
type planChangeView struct {
	Revision      int        `json:"revision"`
	Reason        string     `json:"reason"`
	Stage         *string    `json:"stage"`
	Weight        int        `json:"weight"`
	PlannedWeight int        `json:"planned_weight"`
	RecordedAt    *time.Time `json:"recorded_at"`
}

// triggerView says why a run exists: a manual request or a schedule
// occurrence.
type triggerView struct {
	Kind         string     `json:"kind"`
	ScheduleID   *string    `json:"schedule_id,omitempty"`
	ScheduledFor *time.Time `json:"scheduled_for,omitempty"`
}

type runView struct {
	ID             string         `json:"id"`
	ProjectID      string         `json:"project_id"`
	Status         string         `json:"status"`
	Version        int64          `json:"version"`
	EventSequence  int64          `json:"event_sequence"`
	Trigger        triggerView    `json:"trigger"`
	SnapshotID     *string        `json:"snapshot_id"`
	QualityVerdict *string        `json:"quality_verdict"`
	Report         *runReportView `json:"report"`
	Progress       progressView   `json:"progress"`
}

// runReportView points at the newest published report revision. Its quality
// verdict comes from that stored report, never from the run status alone.
type runReportView struct {
	Revision int    `json:"revision"`
	Status   string `json:"status"`
	SHA256   string `json:"sha256"`
	URL      string `json:"url"`
}

func (s *Server) getRun(q *request) {
	ctx, cancel := q.ctx()
	defer cancel()
	v, err := s.store.GetRunView(ctx, q.session.UserID, q.params[0])
	if err != nil {
		s.storeError(q, err)
		return
	}
	writeJSON(q.w, http.StatusOK, runViewOf(v))
}

// runViewOf is the GET /runs/{id} body; the SSE run.view event carries the
// same document.
func runViewOf(v postgres.RunView) runView {
	out := runView{ID: v.ID, ProjectID: v.ProjectID, Status: string(v.Status), Version: v.Version, EventSequence: v.EventSequence, SnapshotID: v.SnapshotID,
		Trigger:  triggerView{Kind: postgres.TriggerManual},
		Progress: progressView{Stage: string(v.Status), CompletedTasks: v.CompletedTasks, LastEventAt: v.Stages.LastEventAt, CheckedAt: v.Stages.CheckedAt}}
	if o := v.Origin; o != nil {
		id, at := o.ScheduleID, o.ScheduledFor
		out.Trigger = triggerView{Kind: postgres.TriggerSchedule, ScheduleID: &id, ScheduledFor: &at}
	}
	detail, stages := stagesOf(&v.Stages.View)
	out.Progress.Detail, out.Progress.Stages = *detail, stages
	plan := v.Stages.View.Plan
	out.Progress.PlanRevision, out.Progress.EstimatedPercent, out.Progress.PlannedWeight = plan.Revision, plan.EstimatedPercent, plan.PlannedWeight
	out.Progress.PlanChanges = []planChangeView{}
	if plan.Revision != nil {
		finished, basis := plan.FinishedWeight, plan.WeightBasis
		out.Progress.FinishedWeight, out.Progress.WeightBasis = &finished, &basis
	}
	for _, c := range plan.Changes {
		var stage *string
		if c.Stage != "" {
			st := string(c.Stage)
			stage = &st
		}
		out.Progress.PlanChanges = append(out.Progress.PlanChanges, planChangeView{Revision: c.Revision, Reason: c.Reason, Stage: stage, Weight: c.Weight, PlannedWeight: c.PlannedWeight, RecordedAt: c.At})
	}
	if v.TotalTasks > 0 {
		total := v.TotalTasks
		out.Progress.TotalTasks = &total
	}
	if r := v.LatestReport; r != nil {
		verdict := r.QualityVerdict
		out.QualityVerdict = &verdict
		out.Report = &runReportView{Revision: r.Revision, Status: r.Status, SHA256: r.BodySHA256, URL: apiPrefix + "/runs/" + v.ID + "/report?revision=" + strconv.Itoa(r.Revision)}
	}
	// A queued run's reason is derived by the store from enrolled nodes,
	// capabilities, grants, snapshot locations and limits (never predicted).
	if v.Status == domain.RunQueued {
		out.Progress.WaitingReason = v.WaitingReason
	}
	return out
}

// retryAck is the runs.retry answer: the run that runs again (the same run
// on its snapshot, or a new run of the project) and which of the two.
type retryAck struct {
	runAck
	Retry string `json:"retry"`
}

// retryRun runs a finished run again (Store.RetryRun). The client follows
// the returned run, which may be a new one.
func (s *Server) retryRun(q *request) {
	ctx, cancel := q.ctx()
	defer cancel()
	run, how, err := s.store.RetryRun(ctx, q.session.UserID, q.params[0])
	if err != nil {
		if errors.Is(err, domain.ErrConflict) {
			writeError(q.w, q.r, http.StatusConflict, "RUN_NOT_FINISHED", "only a finished run can run again (failed, cancelled, partial or completed)", false)
		} else {
			s.storeError(q, err)
		}
		return
	}
	ack := ackOf(run)
	q.w.Header().Set("Location", ack.URL)
	writeJSON(q.w, http.StatusAccepted, retryAck{ack, how})
}

func (s *Server) cancelRun(q *request) {
	ctx, cancel := q.ctx()
	defer cancel()
	run, changed, err := s.store.CancelRun(ctx, q.session.UserID, q.params[0])
	if err != nil {
		s.storeError(q, err)
		return
	}
	status := http.StatusOK
	if changed {
		status = http.StatusAccepted
	}
	writeJSON(q.w, status, ackOf(run))
}
