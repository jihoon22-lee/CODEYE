package postgres

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
	"github.com/jihoon22-lee/CODEYE/internal/progress"
)

// W15 stage display over real PostgreSQL (TC-98/TC-99). Skips (NOT executed)
// without CODEYE_TEST_DATABASE_URL.

func newW15(t *testing.T) *w06 {
	e := newW06(t)
	ten := 10
	if _, err := e.s.SetSchedulerLimits(e.ctx, &ten, &ten); err != nil {
		t.Fatal(err)
	}
	e.policy(PoolPolicyUpdate{MaxWorkers: intp(10)})
	return e
}

type plannedTask struct {
	id      string
	version int64
}

type taskDecl struct{ key, stage string }

// startRun claims the next queued run for worker and fixes a snapshot.
func (e *w06) startRun(n testNode, worker string) (Ownership, string) {
	e.t.Helper()
	c := e.claimOK(n, worker, domain.NewID())
	o := owner(n, c)
	manifest := domain.Digest([]byte(c.Lease.RunID))
	mid := e.artifact(n, o, "source_manifest", manifest)
	e.mustEvent(n, o, nodeproto.EventSnapshotFixed, nodeproto.SnapshotFixedPayload{ManifestDigest: manifest, Kind: "local", Consistency: "stable_copy_best_effort", ManifestArtifactID: mid})
	return o, c.Lease.RunID
}

func stagePtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// plan declares tasks (stage "" = omitted, like a pre-W15 worker) and, with
// run set, advances the run to running (the plan is fixed).
func (e *w06) plan(n testNode, o Ownership, run bool, decls ...taskDecl) map[string]*plannedTask {
	e.t.Helper()
	out := map[string]*plannedTask{}
	for _, d := range decls {
		r := e.mustEvent(n, o, nodeproto.EventTaskCreated, nodeproto.TaskCreatedPayload{Key: d.key, Required: true, Stage: stagePtr(d.stage)})
		out[d.key] = &plannedTask{id: *r.TaskID, version: *r.Version}
	}
	if run {
		e.mustEvent(n, o, nodeproto.EventRunAdvance, nodeproto.RunAdvancePayload{Status: "running"})
	}
	return out
}

func (e *w06) move(n testNode, o Ownership, p *plannedTask, status, reason string) {
	e.t.Helper()
	r := e.mustEvent(n, o, nodeproto.EventTaskStatus, nodeproto.TaskStatusPayload{TaskID: p.id, ExpectedVersion: p.version, Status: status, Reason: stagePtr(reason)})
	p.version = *r.Version
}

func (e *w06) runStages(run string) RunStages {
	e.t.Helper()
	v, err := e.s.GetRunView(e.ctx, e.user, run)
	if err != nil {
		e.t.Fatal(err)
	}
	return v.Stages
}

func (e *w06) activity(project string) Activity {
	e.t.Helper()
	p, err := e.s.GetProjectSummary(e.ctx, e.user, project)
	if err != nil {
		e.t.Fatal(err)
	}
	return p.Activity
}

func states(v progress.View) string {
	var out []string
	for _, s := range v.Stages {
		out = append(out, s.Key+"="+string(s.State))
	}
	return string(v.Detail) + ":" + strings.Join(out, ",")
}

func stageOf(t *testing.T, v progress.View, key string) progress.Stage {
	t.Helper()
	for _, s := range v.Stages {
		if s.Key == key {
			return s
		}
	}
	t.Fatalf("no stage %s in %s", key, states(v))
	return progress.Stage{}
}

// sameActivity compares two reads of an activity, ignoring the read time.
func sameActivity(a, b Activity) bool {
	a.CheckedAt, b.CheckedAt = time.Time{}, time.Time{}
	return reflect.DeepEqual(a, b)
}

func (e *w06) secondProject(name string) Project {
	e.t.Helper()
	p, err := e.s.RegisterProject(e.ctx, e.user, ProjectRegistration{Name: name, Source: domain.Source{Kind: domain.SourceLocal, SourceRootID: e.root.ID, RelativePath: "."}})
	if err != nil {
		e.t.Fatal(err)
	}
	return p
}

func (e *w06) enqueueIn(project, key string) Run {
	e.t.Helper()
	r, err := e.s.Enqueue(e.ctx, e.user, project, key, json.RawMessage(deepReview))
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

func (e *w06) publish(o Ownership, run, status string) {
	e.t.Helper()
	body := []byte(`{"run":"` + run + `"}`)
	if _, _, err := e.s.PublishReport(e.ctx, ReportPublication{ReportID: domain.NewID(), RunID: run, AttemptID: o.AttemptID, Epoch: o.Epoch, InputDigest: domain.Digest(body),
		Revision: 1, SchemaVersion: "0.1", BuilderVersion: "1", Status: status, QualityVerdict: "inconclusive", Body: body, ExpectRunStatus: domain.RunFinalizing, ResultManifestID: e.resultManifest(o)}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *w06) resultManifest(o Ownership) *string {
	var id *string
	if err := e.s.pool.QueryRow(e.ctx, "SELECT result_manifest_id::text FROM attempts WHERE id=$1", o.AttemptID).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// complete finishes the attempt as succeeded/partial with a result manifest.
func (e *w06) complete(n testNode, o Ownership, outcome string) {
	e.t.Helper()
	rm := e.artifact(n, o, "result_manifest", domain.Digest([]byte("result"+o.AttemptID)))
	if _, err := e.s.CompleteAttempt(e.ctx, n.id, o, Completion{Outcome: outcome, Cleanup: "confirmed", ResultManifestID: &rm}); err != nil {
		e.t.Fatal(err)
	}
}

// TC-98: two projects in different stages, one project with several runs, a
// project without runs and a foreign project; list and detail agree.
func TestTC98RepresentativeRunsAndStages(t *testing.T) {
	e := newW15(t)
	n1, n2 := e.enroll("a", e.root.ID), e.enroll("b", e.root.ID)
	e.idle(n1)
	e.idle(n2)
	sa, cr := string(domain.StageStaticAnalysis), string(domain.StageCodeReview)

	// Project A: a published run, a run needing recovery, a running run and a
	// queued run (requested in that order).
	e.enqueue("done", deepReview)
	oDone, rDone := e.startRun(n1, n1.workers[0])
	done := e.plan(n1, oDone, true, taskDecl{"vet", sa}, taskDecl{"review", cr})
	e.move(n1, oDone, done["vet"], "running", "")
	e.move(n1, oDone, done["vet"], "succeeded", "")
	e.move(n1, oDone, done["review"], "running", "")
	e.move(n1, oDone, done["review"], "succeeded", "")
	e.mustEvent(n1, oDone, nodeproto.EventRunAdvance, nodeproto.RunAdvancePayload{Status: "finalizing"})
	e.complete(n1, oDone, "succeeded")
	e.publish(oDone, rDone, "partial")

	e.enqueue("rec", deepReview)
	oRec, rRec := e.startRun(n1, n1.workers[1])
	rec := e.plan(n1, oRec, true, taskDecl{"env", string(domain.StageEnvironment)})
	e.move(n1, oRec, rec["env"], "running", "")
	sqlExec(t, e.s, "UPDATE attempts SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", oRec.AttemptID)
	if moved, err := e.s.SweepExpiredAttempts(e.ctx); err != nil || moved != 1 {
		t.Fatalf("sweep %d %v", moved, err)
	}

	e.enqueue("run", deepReview)
	oRun, rRun := e.startRun(n2, n2.workers[0])
	run := e.plan(n2, oRun, true, taskDecl{"vet", sa}, taskDecl{"review", cr})
	e.move(n2, oRun, run["vet"], "running", "")

	// Project B is in another stage: documents skipped, analysis finished,
	// review running.
	b := e.secondProject("b")
	e.enqueueIn(b.ID, "b1")
	oB, rB := e.startRun(n2, n2.workers[1])
	bt := e.plan(n2, oB, true, taskDecl{"docs", string(domain.StageDocuments)}, taskDecl{"vet", sa}, taskDecl{"review", cr})
	e.move(n2, oB, bt["docs"], "skipped_policy", domain.ReasonNoDocuments)
	e.move(n2, oB, bt["vet"], "running", "")
	e.move(n2, oB, bt["vet"], "succeeded", "")
	e.move(n2, oB, bt["review"], "running", "")

	rQ := e.enqueue("queued", deepReview)
	empty := e.secondProject("empty")

	// A foreign user's project and run never appear.
	stranger, err := e.s.CreateUserNamed(e.ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.GrantSourceRoot(e.ctx, e.root.ID, stranger); err != nil {
		t.Fatal(err)
	}
	foreign, err := e.s.RegisterProject(e.ctx, stranger, ProjectRegistration{Name: "foreign", Source: domain.Source{Kind: domain.SourceLocal, SourceRootID: e.root.ID, RelativePath: "."}})
	if err != nil {
		t.Fatal(err)
	}
	foreignRun, err := e.s.Enqueue(e.ctx, stranger, foreign.ID, "f", json.RawMessage(deepReview))
	if err != nil {
		t.Fatal(err)
	}

	page, err := e.s.ListProjects(e.ctx, e.user, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]ProjectSummary{}
	for _, p := range page.Items {
		byID[p.ID] = p
	}
	if len(page.Items) != 3 || byID[foreign.ID].ID != "" {
		t.Fatalf("list %d items, foreign listed %v", len(page.Items), byID[foreign.ID].ID != "")
	}
	if _, err := e.s.GetProjectSummary(e.ctx, e.user, foreign.ID); err == nil {
		t.Fatal("foreign project readable")
	}
	if v, err := e.s.GetRunView(e.ctx, e.user, foreignRun.ID); err == nil || v.Stages.Stages != nil {
		t.Fatalf("foreign run readable: %v", err)
	}

	a := byID[e.project.ID].Activity
	if a.Run == nil || a.Run.ID != rRun || a.Run.Status != domain.RunRunning || a.OtherActiveRuns != 2 {
		t.Fatalf("representative %+v other %d (rec %s queued %s)", a.Run, a.OtherActiveRuns, rRec, rQ.ID)
	}
	if got := states(*a.Stages); got != "recorded:source=succeeded,environment=no_tasks,documents=no_tasks,static_analysis=running,code_review=waiting,report=waiting" {
		t.Fatal(got)
	}
	if a.WaitingReason != nil || a.LastEventAt == nil || a.CheckedAt.IsZero() || a.LastEventAt.After(a.CheckedAt) {
		t.Fatalf("times/reason %+v", a)
	}
	if a.LastReport == nil || a.LastReport.RunID != rDone || a.LastReport.Revision != 1 || !byID[e.project.ID].HasReport {
		t.Fatalf("last report %+v", a.LastReport)
	}
	if lr := byID[e.project.ID].LastRun; lr == nil || lr.ID != rQ.ID || lr.Status != domain.RunQueued {
		t.Fatalf("last_run keeps its meaning (latest requested): %+v", lr)
	}
	ab := byID[b.ID].Activity
	if ab.Run == nil || ab.Run.ID != rB || ab.OtherActiveRuns != 0 || ab.LastReport != nil {
		t.Fatalf("project b %+v", ab)
	}
	if got := states(*ab.Stages); got != "recorded:source=succeeded,environment=no_tasks,documents=skipped,static_analysis=succeeded,code_review=running,report=waiting" {
		t.Fatal(got)
	}
	if docs := stageOf(t, *ab.Stages, "documents"); !reflect.DeepEqual(docs.Reasons, []string{"no_documents"}) || docs.Succeeded != 0 {
		t.Fatalf("documents %+v", docs)
	}
	ae := byID[empty.ID].Activity
	if ae.Run != nil || ae.Stages != nil || ae.OtherActiveRuns != 0 || ae.LastReport != nil || ae.LastEventAt != nil || !ae.CheckedAt.Equal(a.CheckedAt) {
		t.Fatalf("empty project %+v", ae)
	}
	// The list and both detail reads agree on every project.
	for id, p := range byID {
		if d := e.activity(id); !sameActivity(d, p.Activity) {
			t.Fatalf("list/detail differ for %s:\n%+v\n%+v", id, p.Activity, d)
		}
		if p.Activity.Run != nil {
			if rs := e.runStages(p.Activity.Run.ID); !reflect.DeepEqual(rs.View, *p.Activity.Stages) || !reflect.DeepEqual(rs.LastEventAt, p.Activity.LastEventAt) {
				t.Fatalf("run detail differs for %s: %s vs %s", id, states(rs.View), states(*p.Activity.Stages))
			}
		}
	}

	// Cancellation keeps the run representative until it settles; then the
	// recovery run, then the queued run (with its waiting reason) follow.
	if _, _, err := e.s.CancelRun(e.ctx, e.user, rRun); err != nil {
		t.Fatal(err)
	}
	if a := e.activity(e.project.ID); a.Run.ID != rRun || a.Run.Status != domain.RunCancelRequested || states(*a.Stages) != "recorded:source=succeeded,environment=no_tasks,documents=no_tasks,static_analysis=running,code_review=waiting,report=waiting" {
		t.Fatalf("cancel requested %+v %s", a.Run, states(*a.Stages))
	}
	if _, err := e.s.CompleteAttempt(e.ctx, n2.id, oRun, Completion{Outcome: "cancelled", Cleanup: "confirmed"}); err != nil {
		t.Fatal(err)
	}
	a = e.activity(e.project.ID)
	if a.Run.ID != rRec || a.Run.Status != domain.RunRecoveryRequired || a.OtherActiveRuns != 1 {
		t.Fatalf("after cancel %+v other %d", a.Run, a.OtherActiveRuns)
	}
	if got := states(*a.Stages); got != "recorded:source=succeeded,environment=running,documents=no_tasks,static_analysis=no_tasks,code_review=no_tasks,report=waiting" {
		t.Fatalf("recovery keeps the last stages: %s", got)
	}
	if got := states(e.runStages(rRun).View); got != "recorded:source=succeeded,environment=no_tasks,documents=no_tasks,static_analysis=cancelled,code_review=cancelled,report=cancelled" {
		t.Fatalf("cancelled run: %s", got)
	}
	if _, err := e.s.ConfirmCleanupNode(e.ctx, n1.id, oRec); err != nil {
		t.Fatal(err)
	}
	zero := 0
	if _, err := e.s.SetSchedulerLimits(e.ctx, &zero, nil); err != nil {
		t.Fatal(err)
	}
	a = e.activity(e.project.ID)
	want := e.waiting(rQ.ID)
	if a.Run.ID != rQ.ID || a.Run.Status != domain.RunQueued || a.OtherActiveRuns != 0 || a.WaitingReason == nil || *a.WaitingReason != want || want != nodeproto.WaitingCapacityFull {
		t.Fatalf("queued representative %+v reason %v want %q", a.Run, a.WaitingReason, want)
	}
	if got := states(*a.Stages); got != "recorded:source=waiting,environment=plan_pending,documents=plan_pending,static_analysis=plan_pending,code_review=plan_pending,report=waiting" {
		t.Fatal(got)
	}
	if a.LastReport == nil || a.LastReport.RunID != rDone {
		t.Fatalf("report link moved %+v", a.LastReport)
	}
	if got := states(e.runStages(rDone).View); got != "recorded:source=succeeded,environment=no_tasks,documents=no_tasks,static_analysis=succeeded,code_review=succeeded,report=succeeded" {
		t.Fatalf("published run %s", got)
	}
}

// TC-99: tool failure and success, policy exclusion versus no documents,
// stage and time records from the database clock.
func TestTC99MixedOutcomesAndExclusions(t *testing.T) {
	e := newW15(t)
	n := e.enroll("a", e.root.ID)
	e.idle(n)
	e.enqueue("r", deepReview)
	o, run := e.startRun(n, n.workers[0])
	pre := e.runStages(run)
	if got := states(pre.View); got != "recorded:source=succeeded,environment=plan_pending,documents=plan_pending,static_analysis=plan_pending,code_review=plan_pending,report=waiting" {
		t.Fatal(got)
	}
	tasks := e.plan(n, o, false,
		taskDecl{"env", string(domain.StageEnvironment)}, taskDecl{"docs", string(domain.StageDocuments)},
		taskDecl{"vet.a", string(domain.StageStaticAnalysis)}, taskDecl{"vet.b", string(domain.StageStaticAnalysis)},
		taskDecl{"review", string(domain.StageCodeReview)})
	if s := stageOf(t, e.runStages(run).View, "static_analysis"); s.State != progress.Waiting || s.Planned != nil {
		t.Fatalf("plan not fixed yet: %+v", s)
	}
	e.mustEvent(n, o, nodeproto.EventRunAdvance, nodeproto.RunAdvancePayload{Status: "running"})
	e.move(n, o, tasks["env"], "running", "")
	e.move(n, o, tasks["env"], "succeeded", "")
	e.move(n, o, tasks["docs"], "skipped_policy", domain.ReasonNoDocuments)
	e.move(n, o, tasks["vet.a"], "running", "")
	e.move(n, o, tasks["vet.a"], "failed", "tool_failed")
	mid := stageOf(t, e.runStages(run).View, "static_analysis")
	if mid.State != progress.Running || mid.Counts["failed"] != 1 || *mid.Planned != 2 || mid.Finished != 1 || mid.FinishedAt != nil {
		t.Fatalf("running keeps the earlier failure: %+v", mid)
	}
	e.move(n, o, tasks["vet.b"], "running", "")
	e.move(n, o, tasks["vet.b"], "succeeded", "")
	e.move(n, o, tasks["review"], "running", "")
	e.move(n, o, tasks["review"], "skipped_policy", domain.ReasonModelSourceNotApproved)
	e.mustEvent(n, o, nodeproto.EventRunAdvance, nodeproto.RunAdvancePayload{Status: "finalizing"})
	v := e.runStages(run)
	if got := states(v.View); got != "recorded:source=succeeded,environment=succeeded,documents=skipped,static_analysis=partial,code_review=skipped,report=running" {
		t.Fatal(got)
	}
	sa := stageOf(t, v.View, "static_analysis")
	if !reflect.DeepEqual(sa.Counts, map[string]int{"failed": 1}) || sa.Succeeded != 1 || sa.Finished != 2 || *sa.Planned != 2 || !reflect.DeepEqual(sa.Reasons, []string{"tool_failed"}) {
		t.Fatalf("partial %+v", sa)
	}
	docs, review := stageOf(t, v.View, "documents"), stageOf(t, v.View, "code_review")
	if !reflect.DeepEqual(docs.Reasons, []string{"no_documents"}) || !reflect.DeepEqual(review.Reasons, []string{"model_source_not_approved"}) || docs.Succeeded+review.Succeeded != 0 {
		t.Fatalf("exclusions %+v %+v", docs, review)
	}
	// Times are the database's acceptance times: a never-started skip has no
	// start, every stage start precedes its end, and none is in the future.
	if docs.StartedAt != nil || docs.FinishedAt == nil || review.StartedAt == nil {
		t.Fatalf("skip times %+v %+v", docs, review)
	}
	for _, s := range v.Stages {
		if s.StartedAt != nil && s.FinishedAt != nil && s.FinishedAt.Before(*s.StartedAt) || s.FinishedAt != nil && s.FinishedAt.After(v.CheckedAt) {
			t.Fatalf("stage %s times %v %v (checked %v)", s.Key, s.StartedAt, s.FinishedAt, v.CheckedAt)
		}
	}
	var dbStarted, dbFinished int
	if err := e.s.pool.QueryRow(e.ctx, "SELECT count(started_at),count(finished_at) FROM tasks WHERE run_id=$1", run).Scan(&dbStarted, &dbFinished); err != nil || dbStarted != 4 || dbFinished != 5 {
		t.Fatalf("task times started %d finished %d %v", dbStarted, dbFinished, err)
	}
	e.complete(n, o, "partial")
	e.publish(o, run, "partial")
	if s := stageOf(t, e.runStages(run).View, "report"); s.State != progress.Succeeded || s.StartedAt == nil || s.FinishedAt == nil || s.FinishedAt.Before(*s.StartedAt) {
		t.Fatalf("report %+v", s)
	}
}

// TC-99: a new attempt after retry; replays of the old attempt (old epoch)
// are refused as stale and never change the stage view; a duplicate
// client_event_id of the current attempt applies once.
func TestTC99StaleReplayAndDuplicateEvents(t *testing.T) {
	e := newW15(t)
	n := e.enroll("a", e.root.ID)
	e.idle(n)
	r := e.enqueue("r", deepReview)
	o1, _ := e.startRun(n, n.workers[0])
	sa := string(domain.StageStaticAnalysis)
	first := e.plan(n, o1, true, taskDecl{"vet", sa}, taskDecl{"legacy", ""})
	oldEvent := AttemptEvent{ClientEventID: domain.NewID(), Kind: nodeproto.EventTaskStatus}
	oldEvent.Payload, _ = json.Marshal(nodeproto.TaskStatusPayload{TaskID: first["vet"].id, ExpectedVersion: first["vet"].version, Status: "running"})
	if _, err := e.s.RecordAttemptEvent(e.ctx, n.id, o1, oldEvent); err != nil {
		t.Fatal(err)
	}
	reason := "tool_failed"
	if _, err := e.s.CompleteAttempt(e.ctx, n.id, o1, Completion{Outcome: "failed", Cleanup: "confirmed", Reason: &reason}); err != nil {
		t.Fatal(err)
	}
	if got := states(e.runStages(r.ID).View); got != "not_recorded:" {
		t.Fatalf("a task without a stage hides all stages: %s", got)
	}
	got, _ := e.s.GetRun(e.ctx, e.user, r.ID)
	if _, err := e.s.Retry(e.ctx, e.user, r.ID, got.Version); err != nil {
		t.Fatal(err)
	}
	var cleared int
	if err := e.s.pool.QueryRow(e.ctx, "SELECT count(*) FROM tasks WHERE run_id=$1 AND status='queued' AND reason IS NULL AND started_at IS NULL AND finished_at IS NULL", r.ID).Scan(&cleared); err != nil || cleared != 2 {
		t.Fatalf("retry left reasons or times: %d %v", cleared, err)
	}
	e.idle(n)
	c2 := e.claimOK(n, n.workers[1], "second")
	o2 := owner(n, c2)
	if o2.Epoch != 2 {
		t.Fatalf("epoch %d", o2.Epoch)
	}
	manifest := domain.Digest([]byte(r.ID))
	e.mustEvent(n, o2, nodeproto.EventSnapshotFixed, nodeproto.SnapshotFixedPayload{ManifestDigest: manifest, Kind: "local", Consistency: "stable_copy_best_effort", ManifestArtifactID: e.artifact(n, o2, "source_manifest", manifest)})
	if got := states(e.runStages(r.ID).View); got != "not_recorded:" {
		t.Fatal(got)
	}
	// Re-planning: a different stage conflicts, an invalid or empty stage is
	// malformed, an omitted stage keeps the stored one, and the task stored
	// without a stage records the declared one once.
	_, err := e.event(n, o2, nodeproto.EventTaskCreated, nodeproto.TaskCreatedPayload{Key: "vet", Required: true, Stage: stagePtr("code_review")})
	expect(t, err, domain.ErrConflict)
	for _, bad := range []string{`"lint"`, `""`, `"source"`, `1`} {
		_, err = e.s.RecordAttemptEvent(e.ctx, n.id, o2, AttemptEvent{ClientEventID: domain.NewID(), Kind: nodeproto.EventTaskCreated, Payload: json.RawMessage(`{"key":"vet","required":true,"stage":` + bad + `}`)})
		expect(t, err, domain.ErrInvalid)
	}
	e.mustEvent(n, o2, nodeproto.EventTaskCreated, nodeproto.TaskCreatedPayload{Key: "vet", Required: true})
	again := e.plan(n, o2, false, taskDecl{"vet", sa}, taskDecl{"legacy", string(domain.StageCodeReview)})
	if again["vet"].id != first["vet"].id || again["legacy"].id != first["legacy"].id {
		t.Fatal("re-planning created new tasks")
	}
	_, err = e.event(n, o2, nodeproto.EventTaskCreated, nodeproto.TaskCreatedPayload{Key: "legacy", Required: true, Stage: stagePtr(sa)})
	expect(t, err, domain.ErrConflict)
	if e.count("SELECT count(*) FROM events WHERE run_id=$1 AND type='task.stage_recorded'", r.ID) != 1 {
		t.Fatal("stage recorded more than once")
	}
	e.mustEvent(n, o2, nodeproto.EventRunAdvance, nodeproto.RunAdvancePayload{Status: "running"})
	e.move(n, o2, again["vet"], "running", "")
	before := e.activity(e.project.ID)
	if before.Run.ID != r.ID || states(*before.Stages) != "recorded:source=succeeded,environment=no_tasks,documents=no_tasks,static_analysis=running,code_review=waiting,report=waiting" {
		t.Fatalf("second attempt %s", states(*before.Stages))
	}
	beforeEvents := e.count("SELECT count(*) FROM events WHERE run_id=$1", r.ID)

	// The old attempt's exact event replayed, and a fresh old-epoch event.
	_, err = e.s.RecordAttemptEvent(e.ctx, n.id, o1, oldEvent)
	expect(t, err, domain.ErrStaleAttempt)
	_, err = e.event(n, o1, nodeproto.EventTaskStatus, nodeproto.TaskStatusPayload{TaskID: first["vet"].id, ExpectedVersion: again["vet"].version, Status: "succeeded"})
	expect(t, err, domain.ErrStaleAttempt)
	forged := o2
	forged.Epoch = 1
	_, err = e.event(n, forged, nodeproto.EventTaskStatus, nodeproto.TaskStatusPayload{TaskID: first["vet"].id, ExpectedVersion: again["vet"].version, Status: "failed"})
	expect(t, err, domain.ErrStaleAttempt)
	if after := e.activity(e.project.ID); !sameActivity(before, after) || e.count("SELECT count(*) FROM events WHERE run_id=$1", r.ID) != beforeEvents {
		t.Fatalf("stale events changed the view:\n%+v\n%+v", before, after)
	}

	// The current attempt's duplicate client_event_id applies once.
	dup := AttemptEvent{ClientEventID: domain.NewID(), Kind: nodeproto.EventTaskStatus}
	dup.Payload, _ = json.Marshal(nodeproto.TaskStatusPayload{TaskID: again["vet"].id, ExpectedVersion: again["vet"].version, Status: "succeeded"})
	out1, err := e.s.RecordAttemptEvent(e.ctx, n.id, o2, dup)
	if err != nil {
		t.Fatal(err)
	}
	once := e.activity(e.project.ID)
	out2, err := e.s.RecordAttemptEvent(e.ctx, n.id, o2, dup)
	if err != nil || !reflect.DeepEqual(out1, out2) {
		t.Fatalf("replay %+v %+v %v", out1, out2, err)
	}
	if twice := e.activity(e.project.ID); !sameActivity(once, twice) || stageOf(t, *once.Stages, "static_analysis").State != progress.Succeeded {
		t.Fatalf("duplicate applied twice:\n%+v\n%+v", once, twice)
	}
	// attempt 1: vet running; attempt 2: vet running, vet succeeded (once).
	if e.count("SELECT count(*) FROM events WHERE run_id=$1 AND type='task.status'", r.ID) != 3 {
		t.Fatal("duplicate event appended")
	}
}

// TC-99: R0 / pre-W15 rows (no stage) stay unrecorded in list and detail;
// nothing is inferred from the task key.
func TestTC99LegacyRunIsNotRecorded(t *testing.T) {
	e := newW15(t)
	n := e.enroll("a", e.root.ID)
	e.idle(n)
	e.enqueue("r", deepReview)
	o, run := e.startRun(n, n.workers[0])
	tasks := e.plan(n, o, true, taskDecl{"go-vet.root", ""}, taskDecl{"review.context", ""})
	e.move(n, o, tasks["go-vet.root"], "running", "")
	e.move(n, o, tasks["go-vet.root"], "succeeded", "")
	if e.count("SELECT count(*) FROM tasks WHERE run_id=$1 AND stage IS NULL", run) != 2 {
		t.Fatal("stage inferred")
	}
	a := e.activity(e.project.ID)
	if a.Run == nil || a.Run.ID != run || a.Stages == nil || a.Stages.Detail != progress.NotRecorded || len(a.Stages.Stages) != 0 || a.LastEventAt == nil {
		t.Fatalf("legacy activity %+v", a)
	}
	if rs := e.runStages(run); !reflect.DeepEqual(rs.View, *a.Stages) {
		t.Fatalf("detail %+v", rs.View)
	}
}
