package review

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// The lead follow-ups of a chain run reach the report: lead_followups counts
// what the records hold, every follow-up's verify is bound to it without a
// lineage limitation, the refined candidate is what the report shows, and
// the rendered section says how many leads were found and sent. The first
// followed slot's records are the examples of an investigation that names a
// lead, its follow-up and its verify (examples/review/leads,
// scripts/validate_bundle.py).
func TestLeadFollowupReachesTheReport(t *testing.T) {
	j := treeJob(t, chainTree(12))
	j.RunID, j.AttemptID = "0195f3a0-0000-7000-8000-000000000b01", "0195f3a0-0000-7000-8000-000000000b02"
	pinExampleSnapshot(t, &j)
	l := &leadsScript{asked: true}
	h, err := discovery.Wrap(Handler{Static: vetStub{}, Caller: &scriptedCaller{script: l.script(t)}, Budget: smallBudget}, discovery.Config{Observer: exampleObserver{t: t}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := h.Plan(t.Context(), j)
	if err != nil {
		t.Fatal(err)
	}
	results := map[string]analysis.TaskResult{}
	for _, task := range plan {
		results[task.Key] = h.Run(t.Context(), j, task, nil)
	}
	r, err := report.Build(exampleInput(t, j, h, plan, results, &exampleIDs{n: 0xb00}), report.DefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	// The vet stub writes no artifact the report reads; every review
	// artifact must be read and bound.
	for _, lim := range r.Limitations {
		if strings.HasPrefix(lim, report.LimReviewLineage) || strings.HasPrefix(lim, "artifact_rejected:review") || strings.HasPrefix(lim, "unrecognized_artifact") {
			t.Fatalf("limitation %s in %v", lim, r.Limitations)
		}
	}
	if err := report.ValidateReport(r); err != nil {
		t.Fatal(err)
	}
	if err := report.ValidateReport(report.Project(r, false)); err != nil {
		t.Fatalf("code-free export: %v", err)
	}

	// The counts the records give.
	want := report.LeadFollowups{LeadsUnresolved: map[string]int{}, TasksNotRun: map[string]int{}, Dispositions: map[string]int{}}
	first := ""
	for _, p := range plan {
		slot, ok := record.LeadsTask(p.Key)
		if !ok {
			continue
		}
		want.TasksPlanned++
		inv := "investigate"
		if slot != "" {
			inv += "." + slot
		}
		if res := results["review."+inv]; res.Status == domain.TaskSucceeded {
			for _, x := range readRecord(t, j, inv).Leads {
				want.LeadsAsked++
				if x.Resolved() {
					want.LeadsResolved++
				} else {
					want.LeadsUnresolved[x.Reason]++
				}
			}
		}
		res := results[p.Key]
		if res.Status != domain.TaskSucceeded {
			want.TasksNotRun[res.Reason]++
			continue
		}
		name := strings.TrimPrefix(p.Key, "review.")
		fu := readRecord(t, j, name)
		want.TasksRun++
		want.TasksAccepted++
		want.Calls += len(fu.Calls)
		want.NewExcerpts += len(fu.LeadFollowup.New)
		want.NewLines += fu.LeadFollowup.NewLines
		for _, d := range fu.LeadFollowup.Dispositions {
			want.Dispositions[d.Disposition]++
		}
		if first == "" && slot != "" {
			first = slot
		}
	}
	have := r.CodeReview.LeadFollowups
	if have == nil || first == "" || want.TasksAccepted == 0 || want.Dispositions[record.DispositionRefined] != want.TasksAccepted {
		t.Fatalf("lead_followups %+v, records %+v", have, want)
	}
	a, _ := json.Marshal(have)
	b, _ := json.Marshal(want)
	if !bytes.Equal(a, b) {
		t.Fatalf("lead_followups %s, records %s", a, b)
	}

	// The verify of each accepted follow-up names it, and the report keeps
	// the refined text, never the investigation's.
	raw, err := report.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	refined := "n이 0보다 크고 다음 패키지가 0 이하를 받는 호출"
	if !bytes.Contains(raw, []byte(refined)) {
		t.Fatal("the report lacks the refined candidate")
	}
	for _, task := range r.Coverage.AIReview.OptionalTasks {
		if record.LeadsTaskKey(first) == task.Key && task.Result != "accepted" {
			t.Fatalf("follow-up %+v", task)
		}
	}
	md := string(report.RenderMarkdownAt(r, report.Digest(raw), report.Korean, report.DetailSummary))
	for _, s := range []string{"### 단서 후속 조사", "조사가 남긴 단서", "후속 조사 뒤 후보", "| 고침 |"} {
		if !strings.Contains(md, s) {
			t.Fatalf("rendered report lacks %q", s)
		}
	}

	for _, name := range []string{"investigate." + first, "leads." + first, "verify." + first} {
		b, err := os.ReadFile(recordPath(j, name))
		if err != nil {
			t.Fatal(err)
		}
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, b, "", "  "); err != nil {
			t.Fatal(err)
		}
		pretty.WriteByte('\n')
		p := filepath.Join(exampleDir, "leads", name+".json")
		if os.Getenv("CODEYE_UPDATE_EXAMPLES") == "1" {
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, pretty.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		compareStoredExample(t, p, pretty.Bytes())
	}
	inv := readRecord(t, j, "investigate."+first)
	if !slices.ContainsFunc(inv.Leads, record.Lead.Resolved) {
		t.Fatalf("example investigation %+v", inv.Leads)
	}
}
