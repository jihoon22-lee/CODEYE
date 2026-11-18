package progress

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

var t0 = time.Date(2026, 11, 18, 3, 0, 0, 0, time.UTC)

func at(sec int) *time.Time {
	v := t0.Add(time.Duration(sec) * time.Second)
	return &v
}

func task(stage domain.Stage, status domain.TaskStatus, reason string) Task {
	t := Task{Stage: stage, Status: status, Reason: reason}
	if status == domain.TaskRunning || (status.Terminal() && status != domain.TaskSkippedPolicy) {
		t.StartedAt = at(10)
	}
	if status.Terminal() {
		t.FinishedAt = at(20)
	}
	return t
}

func find(t *testing.T, v View, key string) Stage {
	t.Helper()
	for _, s := range v.Stages {
		if s.Key == key {
			return s
		}
	}
	t.Fatalf("stage %s missing in %v", key, keys(v))
	return Stage{}
}

func keys(v View) string {
	var out []string
	for _, s := range v.Stages {
		out = append(out, s.Key+"="+string(s.State))
	}
	return strings.Join(out, ",")
}

// A run moving through its lifecycle, with the plan fixed only once the
// current attempt reached running.
func TestLifecycleStates(t *testing.T) {
	sa := domain.StageStaticAnalysis
	cases := []struct {
		name string
		run  Run
		want string
	}{
		{"queued without tasks", Run{Status: domain.RunQueued},
			"source=waiting,environment=plan_pending,documents=plan_pending,static_analysis=plan_pending,code_review=plan_pending,report=waiting"},
		{"preparing acquires", Run{Status: domain.RunPreparing, LeasedAt: at(1)},
			"source=running,environment=plan_pending,documents=plan_pending,static_analysis=plan_pending,code_review=plan_pending,report=waiting"},
		{"preparing with a partial plan", Run{Status: domain.RunPreparing, SnapshotFixed: true, Tasks: []Task{task(sa, domain.TaskQueued, "")}},
			"source=succeeded,environment=plan_pending,documents=plan_pending,static_analysis=waiting,code_review=plan_pending,report=waiting"},
		{"running fixes the plan", Run{Status: domain.RunRunning, SnapshotFixed: true, RunningAt: at(3), Tasks: []Task{task(sa, domain.TaskRunning, ""), task(domain.StageCodeReview, domain.TaskQueued, "")}},
			"source=succeeded,environment=no_tasks,documents=no_tasks,static_analysis=running,code_review=waiting,report=waiting"},
		{"finalizing", Run{Status: domain.RunFinalizing, SnapshotFixed: true, FinalizingAt: at(30), Tasks: []Task{task(sa, domain.TaskSucceeded, "")}},
			"source=succeeded,environment=no_tasks,documents=no_tasks,static_analysis=succeeded,code_review=no_tasks,report=running"},
		{"published", Run{Status: domain.RunPartial, SnapshotFixed: true, FinalizingAt: at(30), ReportAt: at(31), Tasks: []Task{task(sa, domain.TaskSucceeded, "")}},
			"source=succeeded,environment=no_tasks,documents=no_tasks,static_analysis=succeeded,code_review=no_tasks,report=succeeded"},
		{"acquisition failed", Run{Status: domain.RunFailed, LeasedAt: at(1), AttemptReason: "source_unavailable"},
			"source=failed,environment=plan_pending,documents=plan_pending,static_analysis=plan_pending,code_review=plan_pending,report=failed"},
		{"cancelled before the snapshot", Run{Status: domain.RunCancelled, LeasedAt: at(1)},
			"source=cancelled,environment=plan_pending,documents=plan_pending,static_analysis=plan_pending,code_review=plan_pending,report=cancelled"},
		{"cancel requested while acquiring", Run{Status: domain.RunCancelRequested, LeasedAt: at(1)},
			"source=running,environment=plan_pending,documents=plan_pending,static_analysis=plan_pending,code_review=plan_pending,report=waiting"},
		{"build and test appear when planned", Run{Status: domain.RunRunning, SnapshotFixed: true, RunningAt: at(3), Tasks: []Task{task(domain.StageBuild, domain.TaskSucceeded, ""), task(domain.StageTest, domain.TaskRunning, "")}},
			"source=succeeded,environment=no_tasks,documents=no_tasks,build=succeeded,test=running,static_analysis=no_tasks,code_review=no_tasks,report=waiting"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := Compute(c.run)
			if v.Detail != Recorded || keys(v) != c.want {
				t.Fatalf("%s\n got %s\nwant %s", v.Detail, keys(v), c.want)
			}
		})
	}
}

func TestPlannedOnlyWhenPlanFixed(t *testing.T) {
	sa := domain.StageStaticAnalysis
	pre := Compute(Run{Status: domain.RunPreparing, SnapshotFixed: true, Tasks: []Task{task(sa, domain.TaskQueued, "")}})
	if s := find(t, pre, string(sa)); s.Planned != nil {
		t.Fatalf("planned before the plan is fixed: %v", *s.Planned)
	}
	run := Compute(Run{Status: domain.RunRunning, SnapshotFixed: true, RunningAt: at(3), Tasks: []Task{task(sa, domain.TaskQueued, ""), task(sa, domain.TaskSucceeded, "")}})
	if s := find(t, run, string(sa)); s.Planned == nil || *s.Planned != 2 || s.Finished != 1 || s.Succeeded != 1 {
		t.Fatalf("running stage %+v", s)
	}
	if s := find(t, run, string(domain.StageDocuments)); s.State != NoTasks || s.Planned == nil || *s.Planned != 0 {
		t.Fatalf("empty stage %+v", s)
	}
	for _, key := range []string{KeySource, KeyReport} {
		if s := find(t, run, key); s.Planned != nil || s.Finished != 0 || s.Succeeded != 0 {
			t.Fatalf("server stage carries task counts %+v", s)
		}
	}
	// A run that ended before its current attempt reached running never had a
	// fixed plan: queued tasks from closeTasks show as skipped, no denominator.
	failed := Compute(Run{Status: domain.RunFailed, SnapshotFixed: true, Tasks: []Task{task(sa, domain.TaskSkippedPolicy, "")}})
	if s := find(t, failed, string(sa)); s.Planned != nil || s.State != Skipped {
		t.Fatalf("failed before running %+v", s)
	}
	if s := find(t, failed, string(domain.StageCodeReview)); s.State != PlanPending || s.Planned != nil {
		t.Fatalf("stage without tasks of an unplanned run %+v", s)
	}
	// Cancelled after running: the plan was fixed by the current attempt.
	cancelled := Compute(Run{Status: domain.RunCancelled, SnapshotFixed: true, RunningAt: at(3), Tasks: []Task{task(sa, domain.TaskCancelled, "cancelled")}})
	if s := find(t, cancelled, string(sa)); s.Planned == nil || *s.Planned != 1 || s.State != Cancelled {
		t.Fatalf("cancelled after running %+v", s)
	}
}

// Every state of the docs/37 §2 table for one task stage.
func TestTaskStageStates(t *testing.T) {
	sa := domain.StageStaticAnalysis
	q, b, r := domain.TaskQueued, domain.TaskBlocked, domain.TaskRunning
	ok, fail, to, can := domain.TaskSucceeded, domain.TaskFailed, domain.TaskTimedOut, domain.TaskCancelled
	uns, skip, bud := domain.TaskUnsupported, domain.TaskSkippedPolicy, domain.TaskBudgetExhausted
	cases := []struct {
		name     string
		statuses []domain.TaskStatus
		reasons  []string
		state    State
		counts   map[string]int
		reasonsW []string
		finished int
	}{
		{"waiting", []domain.TaskStatus{q, b}, nil, Waiting, map[string]int{}, []string{}, 0},
		{"running", []domain.TaskStatus{r, q}, nil, Running, map[string]int{}, []string{}, 0},
		{"running keeps earlier failures", []domain.TaskStatus{fail, r, q}, []string{"handler_error", "", ""}, Running, map[string]int{"failed": 1}, []string{"handler_error"}, 1},
		{"skipped while a sibling waits is in progress", []domain.TaskStatus{skip, q}, []string{"no_documents", ""}, Running, map[string]int{"skipped_policy": 1}, []string{"no_documents"}, 1},
		{"succeeded", []domain.TaskStatus{ok, ok}, nil, Succeeded, map[string]int{}, []string{}, 2},
		{"skipped by policy", []domain.TaskStatus{skip}, []string{"model_source_not_approved"}, Skipped, map[string]int{"skipped_policy": 1}, []string{"model_source_not_approved"}, 1},
		{"skipped mixed unsupported", []domain.TaskStatus{skip, uns}, []string{"no_documents", "analysis_unsupported"}, Skipped, map[string]int{"skipped_policy": 1, "unsupported": 1}, []string{"analysis_unsupported", "no_documents"}, 2},
		{"failed", []domain.TaskStatus{fail, to, bud}, []string{"handler_error", "timeout", "budget_exhausted"}, Failed, map[string]int{"failed": 1, "timed_out": 1, "budget_exhausted": 1}, []string{"budget_exhausted", "handler_error", "timeout"}, 3},
		{"cancelled", []domain.TaskStatus{can, can}, []string{"cancelled", "cancelled"}, Cancelled, map[string]int{"cancelled": 2}, []string{"cancelled"}, 2},
		{"partial tool failure and success", []domain.TaskStatus{ok, fail, ok}, []string{"", "tool_failed", ""}, Partial, map[string]int{"failed": 1}, []string{"tool_failed"}, 3},
		{"partial success and policy skip", []domain.TaskStatus{ok, skip}, []string{"", "no_tool_observations"}, Partial, map[string]int{"skipped_policy": 1}, []string{"no_tool_observations"}, 2},
		{"partial without success", []domain.TaskStatus{fail, skip}, nil, Partial, map[string]int{"failed": 1, "skipped_policy": 1}, []string{}, 2},
		{"partial cancelled and failed", []domain.TaskStatus{can, fail}, nil, Partial, map[string]int{"failed": 1, "cancelled": 1}, []string{}, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run := Run{Status: domain.RunRunning, SnapshotFixed: true, RunningAt: at(3)}
			for i, st := range c.statuses {
				reason := ""
				if i < len(c.reasons) {
					reason = c.reasons[i]
				}
				run.Tasks = append(run.Tasks, task(sa, st, reason))
			}
			s := find(t, Compute(run), string(sa))
			if s.State != c.state || !reflect.DeepEqual(s.Counts, c.counts) || !reflect.DeepEqual(s.Reasons, c.reasonsW) || s.Finished != c.finished || *s.Planned != len(c.statuses) {
				t.Fatalf("got %s counts %v reasons %v finished %d planned %d", s.State, s.Counts, s.Reasons, s.Finished, *s.Planned)
			}
			succeeded := 0
			for _, st := range c.statuses {
				if st == ok {
					succeeded++
				}
			}
			if s.Succeeded != succeeded {
				t.Fatalf("succeeded %d want %d", s.Succeeded, succeeded)
			}
			if (s.FinishedAt != nil) != (c.finished == len(c.statuses)) {
				t.Fatalf("finished_at %v with %d/%d finished", s.FinishedAt, c.finished, len(c.statuses))
			}
		})
	}
}

// Policy exclusion and "nothing to review" are both skipped_policy; only the
// reason code tells them apart, and neither counts as a success.
func TestPolicyExclusionVersusNoDocuments(t *testing.T) {
	run := Run{Status: domain.RunPartial, SnapshotFixed: true, RunningAt: at(3), FinalizingAt: at(30), ReportAt: at(31), Tasks: []Task{
		task(domain.StageDocuments, domain.TaskSkippedPolicy, domain.ReasonNoDocuments),
		task(domain.StageCodeReview, domain.TaskSkippedPolicy, domain.ReasonModelSourceNotApproved),
		task(domain.StageStaticAnalysis, domain.TaskSucceeded, ""),
	}}
	v := Compute(run)
	docs, review := find(t, v, string(domain.StageDocuments)), find(t, v, string(domain.StageCodeReview))
	if docs.State != Skipped || docs.Succeeded != 0 || !reflect.DeepEqual(docs.Reasons, []string{"no_documents"}) {
		t.Fatalf("documents %+v", docs)
	}
	if review.State != Skipped || review.Succeeded != 0 || !reflect.DeepEqual(review.Reasons, []string{"model_source_not_approved"}) {
		t.Fatalf("code review %+v", review)
	}
	if docs.StartedAt != nil || docs.FinishedAt == nil {
		t.Fatalf("a never-started skip has no start time: %+v", docs)
	}
}

// Cancellation and recovery keep the last confirmed stages.
func TestCancellationAndRecoveryKeepLastStages(t *testing.T) {
	sa, cr := domain.StageStaticAnalysis, domain.StageCodeReview
	tasks := []Task{task(sa, domain.TaskSucceeded, ""), task(cr, domain.TaskRunning, "")}
	for _, st := range []domain.RunStatus{domain.RunCancelRequested, domain.RunCancelling, domain.RunRecoveryRequired} {
		v := Compute(Run{Status: st, SnapshotFixed: true, RunningAt: at(3), Tasks: tasks})
		if got := keys(v); got != "source=succeeded,environment=no_tasks,documents=no_tasks,static_analysis=succeeded,code_review=running,report=waiting" {
			t.Fatalf("%s: %s", st, got)
		}
		if s := find(t, v, string(cr)); s.StartedAt == nil || !s.StartedAt.Equal(*at(10)) {
			t.Fatalf("%s lost the start time: %+v", st, s)
		}
		// Interrupted during finalizing: the report stays running.
		if s := find(t, Compute(Run{Status: st, SnapshotFixed: true, RunningAt: at(3), FinalizingAt: at(30), Tasks: tasks}), KeyReport); s.State != Running || !s.StartedAt.Equal(*at(30)) {
			t.Fatalf("%s report after finalizing %+v", st, s)
		}
	}
	// Confirmed cancellation closes the running task as cancelled.
	v := Compute(Run{Status: domain.RunCancelled, SnapshotFixed: true, RunningAt: at(3), Tasks: []Task{task(sa, domain.TaskSucceeded, ""), task(cr, domain.TaskCancelled, "cancelled")}})
	if got := keys(v); got != "source=succeeded,environment=no_tasks,documents=no_tasks,static_analysis=succeeded,code_review=cancelled,report=cancelled" {
		t.Fatal(got)
	}
	// A cancelled run whose cancellation report was published.
	v = Compute(Run{Status: domain.RunCancelled, SnapshotFixed: true, RunningAt: at(3), ReportAt: at(40), Tasks: []Task{task(cr, domain.TaskCancelled, "cancelled")}})
	if s := find(t, v, KeyReport); s.State != Succeeded || !s.FinishedAt.Equal(*at(40)) {
		t.Fatalf("published cancellation report %+v", s)
	}
}

// R0 or pre-W15 tasks have no stage: nothing is inferred from keys or
// statuses, and a single unrecorded task hides every stage.
func TestLegacyTasksAreNotRecorded(t *testing.T) {
	for _, tasks := range [][]Task{
		{{Status: domain.TaskSucceeded}},
		{task(domain.StageStaticAnalysis, domain.TaskSucceeded, ""), {Status: domain.TaskFailed, Reason: "handler_error"}},
		{{Stage: "lint", Status: domain.TaskRunning}},
	} {
		v := Compute(Run{Status: domain.RunPartial, SnapshotFixed: true, ReportAt: at(9), Tasks: tasks})
		if v.Detail != NotRecorded || v.Stages == nil || len(v.Stages) != 0 {
			t.Fatalf("legacy %+v", v)
		}
	}
}

func TestSourceAndReportFailureReasonsAndTimes(t *testing.T) {
	v := Compute(Run{Status: domain.RunFailed, LeasedAt: at(1), AttemptReason: "snapshot_unavailable"})
	if s := find(t, v, KeySource); s.State != Failed || !reflect.DeepEqual(s.Reasons, []string{"snapshot_unavailable"}) || !s.StartedAt.Equal(*at(1)) || s.FinishedAt != nil {
		t.Fatalf("source %+v", s)
	}
	v = Compute(Run{Status: domain.RunFailed, SnapshotFixed: true, LeasedAt: at(1), SnapshotAt: at(2), RunningAt: at(3), FinalizingAt: at(30), ReportRejection: "source_manifest_unavailable",
		Tasks: []Task{task(domain.StageStaticAnalysis, domain.TaskSucceeded, "")}})
	if s := find(t, v, KeySource); s.State != Succeeded || !s.StartedAt.Equal(*at(1)) || !s.FinishedAt.Equal(*at(2)) {
		t.Fatalf("source %+v", s)
	}
	if s := find(t, v, KeyReport); s.State != Failed || !reflect.DeepEqual(s.Reasons, []string{"source_manifest_unavailable"}) || !s.StartedAt.Equal(*at(30)) {
		t.Fatalf("report %+v", s)
	}
	// A retried run back in the queue keeps its snapshot but shows the queue,
	// not the previous attempt's results.
	v = Compute(Run{Status: domain.RunQueued, SnapshotFixed: true, Tasks: []Task{{Stage: domain.StageStaticAnalysis, Status: domain.TaskQueued}}})
	if got := keys(v); got != "source=waiting,environment=plan_pending,documents=plan_pending,static_analysis=waiting,code_review=plan_pending,report=waiting" {
		t.Fatal(got)
	}
}

func TestStageTimes(t *testing.T) {
	sa := domain.StageStaticAnalysis
	a := Task{Stage: sa, Status: domain.TaskSucceeded, StartedAt: at(5), FinishedAt: at(9)}
	b := Task{Stage: sa, Status: domain.TaskFailed, StartedAt: at(4), FinishedAt: at(12)}
	c := Task{Stage: sa, Status: domain.TaskRunning, StartedAt: at(13)}
	s := find(t, Compute(Run{Status: domain.RunRunning, SnapshotFixed: true, Tasks: []Task{a, b, c}}), string(sa))
	if !s.StartedAt.Equal(*at(4)) || s.FinishedAt != nil {
		t.Fatalf("open stage %+v", s)
	}
	s = find(t, Compute(Run{Status: domain.RunRunning, SnapshotFixed: true, Tasks: []Task{a, b}}), string(sa))
	if !s.StartedAt.Equal(*at(4)) || !s.FinishedAt.Equal(*at(12)) || s.State != Partial || s.Planned == nil || *s.Planned != 2 {
		t.Fatalf("closed stage %+v", s)
	}
}

// The JSON-facing collections are never nil.
func TestEmptyCollections(t *testing.T) {
	for _, s := range Compute(Run{Status: domain.RunQueued}).Stages {
		if s.Counts == nil || s.Reasons == nil {
			t.Fatalf("nil collection in %+v", s)
		}
	}
	if len(States) != 9 {
		t.Fatal("state vocabulary changed; reconcile the API enum")
	}
}

func seq(n int64) *int64 { return &n }
func weight(n int) *int  { return &n }

// W22 (REQ-017, TC-99): plan revision and weighted estimate.
func TestPlanRevisionAndWeightedPercent(t *testing.T) {
	sa, cr := domain.StageStaticAnalysis, domain.StageCodeReview
	// Before the plan is fixed there is no revision, denominator or percent.
	for _, st := range []domain.RunStatus{domain.RunQueued, domain.RunPreparing} {
		p := Compute(Run{Status: st, Tasks: []Task{{Stage: sa, Status: domain.TaskQueued, CreatedSeq: seq(3)}}}).Plan
		if p.Revision != nil || p.PlannedWeight != nil || p.EstimatedPercent != nil || p.WeightBasis != "" || len(p.Changes) != 0 || p.Changes == nil {
			t.Fatalf("%s: plan %+v", st, p)
		}
	}
	vet := Task{Stage: sa, Status: domain.TaskSucceeded, Weight: weight(1), CreatedSeq: seq(3)}
	review := Task{Stage: cr, Status: domain.TaskRunning, Weight: weight(3), CreatedSeq: seq(4)}
	run := Run{Status: domain.RunRunning, RunningAt: at(5), RunningSeq: seq(5), Tasks: []Task{vet, review}}
	p := Compute(run).Plan
	if *p.Revision != 1 || *p.PlannedWeight != 4 || p.FinishedWeight != 1 || *p.EstimatedPercent != 25 || p.WeightBasis != BasisDeclared || len(p.Changes) != 0 {
		t.Fatalf("fixed plan %+v", p)
	}
	// A task created after the plan was fixed adds a revision and moves the
	// denominator; the change says which stage and weight caused it.
	added := Task{Stage: cr, Status: domain.TaskQueued, Weight: weight(2), CreatedSeq: seq(9), CreatedAt: at(30)}
	run.Tasks = append(run.Tasks, added)
	p = Compute(run).Plan
	if *p.Revision != 2 || *p.PlannedWeight != 6 || *p.EstimatedPercent != 16.7 || len(p.Changes) != 1 {
		t.Fatalf("revised plan %+v", p)
	}
	if c := p.Changes[0]; c.Revision != 2 || c.Reason != PlanChangeTaskAdded || c.Stage != cr || c.Weight != 2 || c.PlannedWeight != 6 || !c.At.Equal(*at(30)) {
		t.Fatalf("change %+v", c)
	}
	// Undeclared weights count as one (task_count basis); mixed is reported.
	run.Tasks = append(run.Tasks, Task{Stage: sa, Status: domain.TaskFailed, CreatedSeq: seq(11)})
	p = Compute(run).Plan
	if *p.Revision != 3 || *p.PlannedWeight != 7 || p.FinishedWeight != 2 || p.WeightBasis != BasisMixed || p.Changes[1].PlannedWeight != 7 {
		t.Fatalf("mixed plan %+v", p)
	}
	legacy := Compute(Run{Status: domain.RunCompleted, Tasks: []Task{{Status: domain.TaskSucceeded}, {Status: domain.TaskFailed}}})
	if legacy.Detail != NotRecorded || *legacy.Plan.Revision != 1 || *legacy.Plan.EstimatedPercent != 100 || legacy.Plan.WeightBasis != BasisTaskCount {
		t.Fatalf("legacy plan %+v", legacy.Plan)
	}
	// A fixed plan with no tasks has no estimate (never 0% or 100% by default).
	if p := Compute(Run{Status: domain.RunRunning, RunningSeq: seq(2)}).Plan; *p.Revision != 1 || *p.PlannedWeight != 0 || p.EstimatedPercent != nil {
		t.Fatalf("empty plan %+v", p)
	}
	// A retried run re-plans: tasks created by an earlier attempt (before this
	// attempt's running event) belong to revision 1 again.
	retried := Run{Status: domain.RunRunning, RunningSeq: seq(40), Tasks: []Task{vet, {Stage: cr, Status: domain.TaskQueued, CreatedSeq: seq(20)}}}
	if p := Compute(retried).Plan; *p.Revision != 1 || *p.PlannedWeight != 2 {
		t.Fatalf("retried plan %+v", p)
	}
}
