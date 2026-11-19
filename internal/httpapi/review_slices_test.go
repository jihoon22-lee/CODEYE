package httpapi

import (
	"context"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
)

// Review slice tasks over real HTTP and PostgreSQL. Skips (NOT executed)
// without CODEYE_TEST_DATABASE_URL.

func (a *httpAttempt) optionalTask(key, stage string) {
	a.e.t.Helper()
	r := a.ev("task_created", map[string]any{"key": key, "required": false, "stage": stage})
	a.tasks[key], a.version[key] = r.body["task_id"].(string), r.body["version"].(float64)
}

func (a *httpAttempt) moveWith(key, status, reason string) {
	a.e.t.Helper()
	body := a.statusBody(key, status)
	if reason != "" {
		body["reason"] = reason
	}
	a.version[key] = a.ev("task_status", body).body["version"].(float64)
}

// sliceTasks creates the plan of a run with two review slices: the five
// whole-run roles (required as the role pack declares them), review.scope
// and the slice pairs, all before the run reaches running.
func (a *httpAttempt) sliceTasks() []string {
	a.e.t.Helper()
	a.task("gitleaks", "static_analysis")
	keys := []string{"review.environment", "review.documents", "review.investigate", "review.interpret", "review.verify",
		"review.scope", "review.investigate.s001", "review.verify.s001", "review.investigate.s002", "review.verify.s002"}
	for _, k := range keys {
		switch k {
		case "review.documents", "review.investigate", "review.verify":
			a.task(k, "code_review")
		case "review.environment":
			a.optionalTask(k, "environment")
		default:
			a.optionalTask(k, "code_review")
		}
	}
	return keys
}

// TC-168 (docs/REVIEW.md#모듈-단위-검토-분할): the server takes the slice task
// keys, budget_exhausted from queued (a verify after its refused investigate)
// and from running (a refused investigate) with review_budget_exhausted, and
// no_review_scope; the plan stays at revision 1 because every task exists
// before running; the code_review stage is partial with the reason visible.
func TestReviewSliceTasksOverNodeProtocol(t *testing.T) {
	e := newEnv(t)
	f := e.nodeFixture()
	u := f.user
	runID := e.createRun(u, f.projectID)
	n := e.enrollNode(f.root.ID)
	a := e.claimHTTP(f, n, domain.NewID(), 1)
	a.snapshot()
	keys := a.sliceTasks()
	a.ev("run_advance", map[string]any{"status": "running"})
	a.move("gitleaks", "running")
	a.move("gitleaks", "succeeded")
	for _, k := range keys[:7] {
		a.move(k, "running")
		a.move(k, "succeeded")
	}
	a.move("review.verify.s001", "running")
	a.moveWith("review.verify.s001", "skipped_policy", domain.ReasonNoReviewScope)
	a.move("review.investigate.s002", "running")
	a.moveWith("review.investigate.s002", "budget_exhausted", "review_budget_exhausted")
	a.moveWith("review.verify.s002", "budget_exhausted", "review_budget_exhausted")
	v := e.do("GET", "/api/v1/runs/"+runID, nil, u.o()).want(t, 200, "").body
	pr := v["progress"].(map[string]any)
	if pr["plan_revision"] != float64(1) || len(pr["plan_changes"].([]any)) != 0 || pr["weight_basis"] != "task_count" {
		t.Fatalf("progress %v", pr)
	}
	var review map[string]any
	for _, s := range pr["stages"].([]any) {
		if m := s.(map[string]any); m["key"] == "code_review" {
			review = m
		}
	}
	if review["state"] != "partial" || review["counts"].(map[string]any)["budget_exhausted"] != float64(2) {
		t.Fatalf("code_review stage %v", review)
	}
	var stored []string
	rows, err := e.pool.Query(context.Background(), "SELECT key || '=' || status || ':' || coalesce(reason, '') FROM tasks WHERE run_id=$1 AND key LIKE 'review.%.s002' ORDER BY key", runID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		stored = append(stored, s)
	}
	rows.Close()
	if len(stored) != 2 || stored[0] != "review.investigate.s002=budget_exhausted:review_budget_exhausted" || stored[1] != "review.verify.s002=budget_exhausted:review_budget_exhausted" {
		t.Fatalf("stored %v", stored)
	}

}

// TC-168: cancelling while a slice runs cancels the attempt like any task.
func TestReviewSliceCancellationOverNodeProtocol(t *testing.T) {
	e := newEnv(t)
	f := e.nodeFixture()
	u := f.user
	runID2 := e.createRun(u, f.projectID)
	b := e.claimHTTP(f, e.enrollNode(f.root.ID), domain.NewID(), 1)
	if b.runID != runID2 {
		t.Fatalf("claimed %s, want %s", b.runID, runID2)
	}
	b.snapshot()
	keys := b.sliceTasks()
	b.ev("run_advance", map[string]any{"status": "running"})
	b.move("gitleaks", "running")
	b.move("gitleaks", "succeeded")
	for _, k := range keys[:6] {
		b.move(k, "running")
		b.move(k, "succeeded")
	}
	b.move("review.investigate.s001", "running")
	e.do("POST", "/api/v1/runs/"+runID2+"/cancel", nil, u.o()).want(t, 202, "")
	hb := e.node("POST", b.path()+"/heartbeat", nodeproto.AttemptHeartbeatRequest{WorkerID: b.worker, LeaseEpoch: 1}, b.sec).want(t, 200, "")
	if hb.body["cancel_requested"] != true {
		t.Fatal(string(hb.raw))
	}
	b.moveWith("review.investigate.s001", "cancelled", "cancelled")
	for _, k := range keys[7:] {
		b.moveWith(k, "cancelled", "cancelled")
	}
	done := e.node("POST", b.path()+"/complete", nodeproto.CompleteRequest{WorkerID: b.worker, LeaseEpoch: 1, Outcome: "cancelled", CleanupState: "confirmed"}, b.sec).want(t, 202, "")
	if done.body["status"] != "cancelled" {
		t.Fatalf("complete %s", done.raw)
	}
	if s, _ := e.runStatus(runID2); s != "cancelled" {
		t.Fatalf("run %s", s)
	}
}
