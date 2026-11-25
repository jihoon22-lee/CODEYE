package review

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// TestTriageCancelledAfterACallKeepsItsRecord: a triage slot cancelled after
// its first call (before the repair) still stores a failed record with that
// call and its groups left out, so the report knows what reached the model.
func TestTriageCancelledAfterACallKeepsItsRecord(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	script := triageReviewScript(t)
	script["triage"] = func(n int, q opencode.Request) opencode.Reply {
		cancel()
		answers, order := triageAnswers(t, n, q)
		answers[order[0]]["classification"] = "maybe"
		return reply(answers, order)
	}
	j := dirJob(t, triageFixture)
	j.RunID, j.AttemptID = "0195f3a0-0000-7000-8000-000000000a01", "0195f3a0-0000-7000-8000-000000000a02"
	pinExampleSnapshot(t, &j)
	h, err := discovery.Wrap(Handler{Static: triageStatic(t), Caller: &scriptedCaller{script: script}}, discovery.Config{Observer: exampleObserver{t: t}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := h.Plan(t.Context(), j)
	if err != nil {
		t.Fatal(err)
	}
	var res analysis.TaskResult
	for _, task := range plan {
		if task.Key == "review.triage.t001" {
			res = h.Run(ctx, j, task, nil)
			break
		}
		h.Run(t.Context(), j, task, nil)
	}
	if res.Status != domain.TaskCancelled || res.Reason != "cancelled" {
		t.Fatalf("triage result %+v", res)
	}
	var rec record.Record
	b, err := os.ReadFile(recordPath(j, "triage.t001"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != record.OutcomeFailed || len(rec.Calls) != 1 || len(rec.TriageGroups) == 0 ||
		slices.ContainsFunc(rec.TriageGroups, func(g record.TriageGroup) bool { return g.Omitted == "" }) {
		t.Fatalf("record outcome %s, %d calls, groups %+v", rec.Outcome, len(rec.Calls), rec.TriageGroups)
	}
}
