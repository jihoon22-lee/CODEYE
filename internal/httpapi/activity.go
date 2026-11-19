package httpapi

import (
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/progress"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

// stageView is one display stage (docs/ARCHITECTURE.md#표시-단계와-task-단계). Planned is null until the
// plan is fixed and always null for source/report.
type stageView struct {
	Key        string         `json:"key"`
	State      string         `json:"state"`
	Planned    *int           `json:"planned"`
	Finished   int            `json:"finished"`
	Succeeded  int            `json:"succeeded"`
	Counts     map[string]int `json:"counts"`
	Reasons    []string       `json:"reasons"`
	StartedAt  *time.Time     `json:"started_at"`
	FinishedAt *time.Time     `json:"finished_at"`
}

func stagesOf(v *progress.View) (*string, []stageView) {
	out := []stageView{}
	if v == nil {
		return nil, out
	}
	for _, s := range v.Stages {
		out = append(out, stageView{Key: s.Key, State: string(s.State), Planned: s.Planned, Finished: s.Finished, Succeeded: s.Succeeded,
			Counts: s.Counts, Reasons: s.Reasons, StartedAt: s.StartedAt, FinishedAt: s.FinishedAt})
	}
	detail := string(v.Detail)
	return &detail, out
}

type activityRunView struct {
	ID          string    `json:"id"`
	Status      string    `json:"status"`
	RequestedAt time.Time `json:"requested_at"`
}

type reportRefView struct {
	RunID       string    `json:"run_id"`
	Revision    int       `json:"revision"`
	PublishedAt time.Time `json:"published_at"`
}

// activityView is the project's representative run and its stages. detail is
// null exactly when run is null (no run recorded).
type activityView struct {
	Run             *activityRunView `json:"run"`
	OtherActiveRuns int64            `json:"other_active_runs"`
	Detail          *string          `json:"detail"`
	Stages          []stageView      `json:"stages"`
	WaitingReason   *string          `json:"waiting_reason"`
	LastReport      *reportRefView   `json:"last_report"`
	LastEventAt     *time.Time       `json:"last_event_at"`
	CheckedAt       time.Time        `json:"checked_at"`
}

func activityOf(a postgres.Activity) activityView {
	v := activityView{OtherActiveRuns: a.OtherActiveRuns, WaitingReason: a.WaitingReason, LastEventAt: a.LastEventAt, CheckedAt: a.CheckedAt}
	v.Detail, v.Stages = stagesOf(a.Stages)
	if a.Run != nil {
		v.Run = &activityRunView{ID: a.Run.ID, Status: string(a.Run.Status), RequestedAt: a.Run.RequestedAt}
	}
	if r := a.LastReport; r != nil {
		v.LastReport = &reportRefView{RunID: r.RunID, Revision: r.Revision, PublishedAt: r.PublishedAt}
	}
	return v
}

// projectGaps lists the product capabilities a project view cannot claim.
// The source declaration is never verified by registration.
// execution_unavailable is a structured, global fact: no enrolled, unrevoked
// node exists. Whether a specific run can be placed (capabilities, grants,
// capacity) is reported per run as waiting_reason. report_unavailable is added
// per project by viewOf until one of its runs has a published report.
func projectGaps(executionAvailable bool) []string {
	gaps := []string{string(domain.GapSourceUnverified)}
	if !executionAvailable {
		gaps = append(gaps, string(domain.GapExecutionUnavailable))
	}
	return gaps
}

// executionGaps reads the global execution fact for a request.
func (s *Server) executionGaps(q *request) ([]string, bool) {
	ctx, cancel := q.ctx()
	defer cancel()
	ok, err := s.store.NodesEnrolled(ctx)
	if err != nil {
		s.storeError(q, err)
		return nil, false
	}
	return projectGaps(ok), true
}
