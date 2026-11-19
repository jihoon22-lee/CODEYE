// Package progress derives the per-stage display of one run (docs/ARCHITECTURE.md#표시-단계와-task-단계)
// from stored, structured facts only. It is pure: the store loads the facts in
// one statement and both the project list and the run detail call Compute, so
// the two views cannot disagree about the same facts. Model narratives, worker
// clocks and task keys are never inputs.
package progress

import (
	"math"
	"sort"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// Server-derived display stages around the task stages (domain.TaskStages).
const (
	KeySource = "source"
	KeyReport = "report"
)

// State is a display stage state (docs/ARCHITECTURE.md#표시-단계와-task-단계 table).
type State string

const (
	PlanPending State = "plan_pending"
	NoTasks     State = "no_tasks"
	Waiting     State = "waiting"
	Running     State = "running"
	Succeeded   State = "succeeded"
	Skipped     State = "skipped"
	Failed      State = "failed"
	Cancelled   State = "cancelled"
	Partial     State = "partial"
)

// States lists every state value (API enum cross-checks).
var States = []State{PlanPending, NoTasks, Waiting, Running, Succeeded, Skipped, Failed, Cancelled, Partial}

// Detail tells whether the run's tasks carry stage records.
type Detail string

const (
	Recorded    Detail = "recorded"
	NotRecorded Detail = "not_recorded"
)

// Task is one logical task of the run as stored. Stage "" is a task recorded
// without a stage (a worker that declares no stage, or a legacy record); it is never inferred.
type Task struct {
	Stage      domain.Stage
	Status     domain.TaskStatus
	Reason     string
	StartedAt  *time.Time
	FinishedAt *time.Time
	// Weight is the worker's declared estimate weight; nil means not
	// declared and counts as DefaultWeight.
	Weight *int
	// CreatedSeq is the run event sequence of the task's creation and
	// CreatedAt its database time; both nil for tasks recorded before plan revisions existed.
	CreatedSeq *int64
	CreatedAt  *time.Time
}

// DefaultWeight is the weight of a task without a declared weight: the
// estimate is then the share of finished tasks (task_count basis).
const DefaultWeight = 1

// Weight bases (Plan.WeightBasis).
const (
	BasisTaskCount = "task_count" // no task declared a weight
	BasisDeclared  = "declared"   // every task declared one
	BasisMixed     = "mixed"
)

// PlanChangeTaskAdded is the only plan change reason: a task was created after
// the current attempt fixed its plan.
const PlanChangeTaskAdded = "task_added"

// Run is the stored state of one run. Times are database times of accepted
// records of the run's current attempt (after its latest lease or retry); nil
// means no such record exists.
type Run struct {
	Status        domain.RunStatus
	SnapshotFixed bool
	// LeasedAt is the current attempt's lease (source acquisition start).
	LeasedAt *time.Time
	// SnapshotAt is when the current attempt fixed or confirmed the snapshot.
	SnapshotAt *time.Time
	// RunningAt is when the current attempt advanced the run to running,
	// i.e. its task plan was fixed; RunningSeq is that event's sequence.
	RunningAt  *time.Time
	RunningSeq *int64
	// FinalizingAt is when the current attempt advanced the run to finalizing.
	FinalizingAt *time.Time
	// ReportAt is when a report revision of the current attempt was published.
	ReportAt *time.Time
	// AttemptReason is the current attempt's completion reason ("" if none).
	AttemptReason string
	// ReportRejection is the current attempt's publication refusal reason.
	ReportRejection string
	Tasks           []Task
}

// Stage is the computed state of one display stage. Planned is set only once
// the plan is fixed, and never for the server-derived source/report stages.
// Counts holds the non-zero terminal non-success statuses; Reasons the sorted,
// distinct reason codes of the stage's finished tasks.
type Stage struct {
	Key        string
	State      State
	Planned    *int
	Finished   int
	Succeeded  int
	Counts     map[string]int
	Reasons    []string
	StartedAt  *time.Time
	FinishedAt *time.Time
}

// View is the ordered stage list of a run. Stages is empty (never nil) when
// Detail is NotRecorded. Plan does not depend on stage records.
type View struct {
	Detail Detail
	Stages []Stage
	Plan   Plan
}

// Plan is the weighted progress estimate of the current attempt
// (REQ-017). Before the plan is fixed every pointer is nil and Changes is
// empty. Revision is 1 when the plan is fixed and grows by one for each task
// created afterwards (Changes says which task, with what weight, moved the
// denominator). The estimate is finished weight / planned weight of the
// current revision; it is computed only from stored task records, never
// from heartbeats, model tokens or elapsed time.
type Plan struct {
	Revision         *int
	PlannedWeight    *int
	FinishedWeight   int
	EstimatedPercent *float64
	WeightBasis      string
	Changes          []PlanChange
}

// PlanChange records one plan revision after the first. PlannedWeight is the
// denominator after the change.
type PlanChange struct {
	Revision      int
	Reason        string
	Stage         domain.Stage
	Weight        int
	PlannedWeight int
	At            *time.Time
}

// planFixed reports whether the current attempt fixed its task plan. queued
// and preparing never have a fixed plan; a run that is (or ended) past
// running fixed it; cancel/recovery/failed/cancelled depend on whether the
// current attempt reached running.
func planFixed(r Run) bool {
	switch r.Status {
	case domain.RunQueued, domain.RunPreparing:
		return false
	case domain.RunRunning, domain.RunFinalizing, domain.RunCompleted, domain.RunPartial:
		return true
	}
	return r.RunningAt != nil
}

// Compute derives the ordered stage list: source, the task stages in
// domain.TaskStages order (build/test only when planned), then report.
func Compute(r Run) View {
	fixed := planFixed(r)
	plan := computePlan(r, fixed)
	byStage := map[domain.Stage][]Task{}
	for _, t := range r.Tasks {
		if !t.Stage.Valid() {
			return View{Detail: NotRecorded, Stages: []Stage{}, Plan: plan}
		}
		byStage[t.Stage] = append(byStage[t.Stage], t)
	}
	stages := []Stage{source(r)}
	for _, key := range domain.TaskStages {
		tasks := byStage[key]
		if len(tasks) == 0 && (key == domain.StageBuild || key == domain.StageTest) {
			continue
		}
		stages = append(stages, taskStage(string(key), tasks, fixed))
	}
	stages = append(stages, report(r))
	return View{Detail: Recorded, Stages: stages, Plan: plan}
}

func weightOf(t Task) int {
	if t.Weight != nil {
		return *t.Weight
	}
	return DefaultWeight
}

// computePlan derives the plan revision and weighted estimate. A task belongs
// to the first revision unless it was created after the current attempt's
// plan-fixing event (CreatedSeq > RunningSeq); tasks without a creation
// sequence (legacy tasks) belong to the first revision.
func computePlan(r Run, fixed bool) Plan {
	p := Plan{Changes: []PlanChange{}}
	if !fixed {
		return p
	}
	var added []Task
	base, finished, declared := 0, 0, 0
	for _, t := range r.Tasks {
		if t.Weight != nil {
			declared++
		}
		if t.Status.Terminal() {
			finished += weightOf(t)
		}
		if r.RunningSeq != nil && t.CreatedSeq != nil && *t.CreatedSeq > *r.RunningSeq {
			added = append(added, t)
			continue
		}
		base += weightOf(t)
	}
	sort.SliceStable(added, func(i, j int) bool { return *added[i].CreatedSeq < *added[j].CreatedSeq })
	revision, planned := 1, base
	for _, t := range added {
		revision++
		planned += weightOf(t)
		p.Changes = append(p.Changes, PlanChange{Revision: revision, Reason: PlanChangeTaskAdded, Stage: t.Stage, Weight: weightOf(t), PlannedWeight: planned, At: t.CreatedAt})
	}
	p.Revision, p.PlannedWeight, p.FinishedWeight = &revision, &planned, finished
	switch {
	case declared == 0:
		p.WeightBasis = BasisTaskCount
	case declared == len(r.Tasks):
		p.WeightBasis = BasisDeclared
	default:
		p.WeightBasis = BasisMixed
	}
	if planned > 0 {
		percent := math.Round(float64(finished)*1000/float64(planned)) / 10
		p.EstimatedPercent = &percent
	}
	return p
}

func newStage(key string) Stage {
	return Stage{Key: key, Counts: map[string]int{}, Reasons: []string{}}
}

func withReason(s Stage, reason string) Stage {
	if reason != "" {
		s.Reasons = []string{reason}
	}
	return s
}

// source: queued is waiting (a queue position is not acquisition), a fixed
// snapshot is succeeded, an unfinished acquisition (including cancel and
// recovery in progress) keeps running, and a run that ended without a
// snapshot failed or was cancelled.
func source(r Run) Stage {
	s := newStage(KeySource)
	switch {
	case r.Status == domain.RunQueued:
		s.State = Waiting
		return s
	case r.SnapshotFixed:
		s.State, s.StartedAt, s.FinishedAt = Succeeded, r.LeasedAt, r.SnapshotAt
	case r.Status == domain.RunCancelled:
		s.State, s.StartedAt = Cancelled, r.LeasedAt
		s = withReason(s, r.AttemptReason)
	case r.Status.Terminal():
		s.State, s.StartedAt = Failed, r.LeasedAt
		s = withReason(s, r.AttemptReason)
	default:
		s.State, s.StartedAt = Running, r.LeasedAt
	}
	return s
}

// report: waiting before finalizing, running in finalizing, succeeded once a
// revision of the current attempt is published, failed/cancelled when the run
// ended without one. Cancel and recovery keep the last confirmed state.
func report(r Run) Stage {
	s := newStage(KeyReport)
	switch r.Status {
	case domain.RunQueued, domain.RunPreparing, domain.RunRunning:
		s.State = Waiting
		return s
	case domain.RunFinalizing:
		s.State, s.StartedAt = Running, r.FinalizingAt
		return s
	}
	s.StartedAt = r.FinalizingAt
	switch {
	case r.ReportAt != nil:
		s.State, s.FinishedAt = Succeeded, r.ReportAt
	case r.Status == domain.RunCancelRequested || r.Status == domain.RunCancelling || r.Status == domain.RunRecoveryRequired:
		s.State = Waiting
		if r.FinalizingAt != nil {
			s.State = Running
		}
	case r.Status == domain.RunCancelled:
		s.State = Cancelled
		s = withReason(s, r.ReportRejection)
	default:
		s.State = Failed
		s = withReason(s, r.ReportRejection)
	}
	return s
}

// TaskStage classifies the tasks of one task stage; fixed tells whether the
// plan is fixed. Compute uses it for every task stage, and the report
// builder for its sections, so a report and the run detail give the same
// tasks the same state.
func TaskStage(key string, tasks []Task, fixed bool) Stage { return taskStage(key, tasks, fixed) }

func taskStage(key string, tasks []Task, fixed bool) Stage {
	s := newStage(key)
	if fixed {
		n := len(tasks)
		s.Planned = &n
	}
	if len(tasks) == 0 {
		s.State = PlanPending
		if fixed {
			s.State = NoTasks
		}
		return s
	}
	reasons := map[string]bool{}
	waiting, nothing := 0, 0
	var finishedAt *time.Time
	for _, t := range tasks {
		if t.StartedAt != nil && (s.StartedAt == nil || t.StartedAt.Before(*s.StartedAt)) {
			s.StartedAt = t.StartedAt
		}
		switch {
		case t.Status == domain.TaskQueued || t.Status == domain.TaskBlocked:
			waiting++
			continue
		case !t.Status.Terminal():
			continue
		}
		s.Finished++
		if t.Status == domain.TaskSucceeded {
			s.Succeeded++
		} else {
			s.Counts[string(t.Status)]++
			if t.Status == domain.TaskSkippedPolicy && domain.NothingToDo(t.Reason) {
				nothing++
			}
		}
		if t.Reason != "" {
			reasons[t.Reason] = true
		}
		if t.FinishedAt != nil && (finishedAt == nil || t.FinishedAt.After(*finishedAt)) {
			finishedAt = t.FinishedAt
		}
	}
	for r := range reasons {
		s.Reasons = append(s.Reasons, r)
	}
	sort.Strings(s.Reasons)
	total := len(tasks)
	switch {
	case waiting == total:
		s.State = Waiting
	case s.Finished < total:
		// Something started or ended while work remains; failures so far
		// are already in Counts.
		s.State = Running
	default:
		s.FinishedAt = finishedAt
		s.State = settled(s, total, nothing)
	}
	return s
}

// settled classifies a stage whose tasks have all ended.
func settled(s Stage, total, nothingToReview int) State {
	sum := func(statuses ...domain.TaskStatus) int {
		n := 0
		for _, st := range statuses {
			n += s.Counts[string(st)]
		}
		return n
	}
	switch {
	case s.Succeeded == total:
		return Succeeded
	// A role skipped because it had nothing to review (no documents, no tool
	// observations, no environment gap) does not degrade a stage whose other
	// tasks all succeeded; its count and reason stay visible.
	case s.Succeeded > 0 && s.Succeeded+nothingToReview == total:
		return Succeeded
	case s.Succeeded > 0:
		return Partial
	case sum(domain.TaskSkippedPolicy, domain.TaskUnsupported) == total:
		return Skipped
	case sum(domain.TaskFailed, domain.TaskTimedOut, domain.TaskBudgetExhausted) == total:
		return Failed
	case sum(domain.TaskCancelled) == total:
		return Cancelled
	}
	return Partial
}
