package postgres

import (
	"errors"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
	"github.com/jihoon22-lee/CODEYE/internal/progress"
)

func weightp(n int) *int { return &n }

// TC-99: the plan revision grows when a task is created after the plan
// was fixed, the denominator change is explained, the estimate is weighted,
// the same values reach the project activity, and a retried run re-plans from
// revision 1. Heartbeats change nothing.
func TestTC99PlanRevisionAndWeightedProgress(t *testing.T) {
	e := newStageEnv(t)
	n := e.enroll("a", e.root.ID)
	e.idle(n)
	r := e.enqueue("plan", deepReview)
	o, _ := e.startRun(n, n.workers[0])
	sa, cr := string(domain.StageStaticAnalysis), string(domain.StageCodeReview)
	create := func(key, stage string, w *int) *plannedTask {
		t.Helper()
		resp := e.mustEvent(n, o, nodeproto.EventTaskCreated, nodeproto.TaskCreatedPayload{Key: key, Required: true, Stage: stagePtr(stage), Weight: w})
		return &plannedTask{id: *resp.TaskID, version: *resp.Version}
	}
	vet := create("vet", sa, weightp(1))
	review := create("review", cr, weightp(3))
	if p := e.runStages(r.ID).Plan; p.Revision != nil || p.EstimatedPercent != nil {
		t.Fatalf("plan before running %+v", p)
	}
	e.mustEvent(n, o, nodeproto.EventRunAdvance, nodeproto.RunAdvancePayload{Status: "running"})
	e.move(n, o, vet, "running", "")
	e.move(n, o, vet, "succeeded", "")
	p := e.runStages(r.ID).Plan
	if *p.Revision != 1 || *p.PlannedWeight != 4 || *p.EstimatedPercent != 25 || p.WeightBasis != progress.BasisDeclared {
		t.Fatalf("revision 1 %+v", p)
	}
	// Heartbeats are not progress.
	if _, err := e.s.RenewAttempt(e.ctx, n.id, o); err != nil {
		t.Fatal(err)
	}
	e.beat(n, hb(n.workers, "busy", capable(e.root.ID)))
	if q := e.runStages(r.ID).Plan; *q.EstimatedPercent != 25 || *q.Revision != 1 {
		t.Fatalf("heartbeat moved progress %+v", q)
	}
	// A task created after the plan was fixed: revision 2, denominator 4 -> 6.
	extra := create("review.more", cr, weightp(2))
	p = e.runStages(r.ID).Plan
	if *p.Revision != 2 || *p.PlannedWeight != 6 || *p.EstimatedPercent != 16.7 || len(p.Changes) != 1 {
		t.Fatalf("revision 2 %+v", p)
	}
	if c := p.Changes[0]; c.Revision != 2 || c.Reason != progress.PlanChangeTaskAdded || string(c.Stage) != cr || c.Weight != 2 || c.PlannedWeight != 6 || c.At == nil {
		t.Fatalf("change %+v", c)
	}
	// List and detail read the same aggregation.
	if a := e.activity(e.project.ID); a.Stages == nil || a.Stages.Plan.Revision == nil || *a.Stages.Plan.Revision != 2 || *a.Stages.Plan.EstimatedPercent != 16.7 {
		t.Fatalf("activity plan %+v", a.Stages)
	}
	// Weight rules mirror stage: re-declaring with another weight is a
	// conflict, out of range is invalid, the same weight is the same task.
	if _, err := e.event(n, o, nodeproto.EventTaskCreated, nodeproto.TaskCreatedPayload{Key: "vet", Required: true, Stage: stagePtr(sa), Weight: weightp(5)}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("weight change %v", err)
	}
	for _, bad := range []int{0, 1001, -1} {
		if _, err := e.event(n, o, nodeproto.EventTaskCreated, nodeproto.TaskCreatedPayload{Key: "w" + strings.Repeat("x", 3), Required: true, Weight: weightp(bad)}); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("weight %d: %v", bad, err)
		}
	}
	if again := e.mustEvent(n, o, nodeproto.EventTaskCreated, nodeproto.TaskCreatedPayload{Key: "vet", Required: true, Stage: stagePtr(sa), Weight: weightp(1)}); *again.TaskID != vet.id {
		t.Fatal("re-declared task is a new task")
	}
	e.move(n, o, review, "running", "")
	e.move(n, o, review, "failed", "tool_failed")
	e.move(n, o, extra, "running", "")
	e.move(n, o, extra, "succeeded", "")
	if p = e.runStages(r.ID).Plan; *p.EstimatedPercent != 100 || p.FinishedWeight != 6 {
		t.Fatalf("finished %+v", p)
	}
	// Retry: the new attempt re-plans the same keys before running; the
	// earlier attempt's late task is part of revision 1 now.
	reason := "tool_failed"
	if _, err := e.s.CompleteAttempt(e.ctx, n.id, o, Completion{Outcome: "failed", Cleanup: "confirmed", Reason: &reason}); err != nil {
		t.Fatal(err)
	}
	got, _ := e.s.GetRun(e.ctx, e.user, r.ID)
	if _, err := e.s.Retry(e.ctx, e.user, r.ID, got.Version); err != nil {
		t.Fatal(err)
	}
	if p = e.runStages(r.ID).Plan; p.Revision != nil {
		t.Fatalf("retried run before plan %+v", p)
	}
	e.idle(n)
	c2 := e.claimOK(n, n.workers[1], "second")
	o2 := owner(n, c2)
	for _, d := range []struct {
		key, stage string
		w          int
	}{{"vet", sa, 1}, {"review", cr, 3}, {"review.more", cr, 2}} {
		e.mustEvent(n, o2, nodeproto.EventTaskCreated, nodeproto.TaskCreatedPayload{Key: d.key, Required: true, Stage: stagePtr(d.stage), Weight: weightp(d.w)})
	}
	e.mustEvent(n, o2, nodeproto.EventRunAdvance, nodeproto.RunAdvancePayload{Status: "running"})
	if p = e.runStages(r.ID).Plan; *p.Revision != 1 || *p.PlannedWeight != 6 || *p.EstimatedPercent != 0 || len(p.Changes) != 0 {
		t.Fatalf("retried plan %+v", p)
	}
}

// The SSE event page: head and contiguous sequences, ACL per call.
func TestRunEventsPage(t *testing.T) {
	e := newStageEnv(t)
	r := e.enqueue("events", deepReview)
	page, err := e.s.RunEvents(e.ctx, e.user, r.ID, 0, 10)
	if err != nil || page.Head != 1 || len(page.Events) != 1 || page.Events[0].Sequence != 1 || page.Events[0].Type != "run.queued" || page.Events[0].CreatedAt.IsZero() {
		t.Fatalf("page %+v %v", page, err)
	}
	if page, err = e.s.RunEvents(e.ctx, e.user, r.ID, 1, 10); err != nil || page.Head != 1 || len(page.Events) != 0 {
		t.Fatalf("empty page %+v %v", page, err)
	}
	if _, err := e.s.RunEvents(e.ctx, e.newUser(), r.ID, 0, 10); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("stranger %v", err)
	}
	if _, err := e.s.RunEvents(e.ctx, e.user, r.ID, -1, 10); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("negative cursor %v", err)
	}
}
