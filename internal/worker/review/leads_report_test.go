package review

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
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
	// The new lines are counted once per file however many follow-ups sent
	// them.
	newLines := map[string]map[int]bool{}
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
		for file, set := range record.LeadNewLineSet(fu.LeadFollowup.New, catalogOf(fu.Evidence), readRecord(t, j, inv).Evidence) {
			if newLines[file] == nil {
				newLines[file] = map[int]bool{}
			}
			for n := range set {
				newLines[file][n] = true
			}
		}
		for _, d := range fu.LeadFollowup.Dispositions {
			want.Dispositions[d.Disposition]++
		}
		if first == "" && slot != "" {
			first = slot
		}
	}
	want.NewLines = record.LineCount(newLines)
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

	// Each refined finding names the candidate it replaced, its lead and
	// the follow-up, with the original title; the lineage cannot be forged.
	originalTitle := chainCandidate("")["title"].(string)
	refinedIDs := []string{}
	for _, f := range r.Findings {
		x := f.LeadRefinement
		if x == nil {
			continue
		}
		slot := strings.TrimSuffix(strings.TrimPrefix(f.ID, "review_"), "_"+x.CandidateID)
		if x.TaskKey != record.LeadsTaskKey(slot) || x.CandidateID != "investigate_1" || x.Lead != "lead_1" || x.OriginalTitle != originalTitle {
			t.Fatalf("finding %s lineage %+v", f.ID, *x)
		}
		refinedIDs = append(refinedIDs, f.ID)
	}
	if len(refinedIDs) != want.Dispositions[record.DispositionRefined] {
		t.Fatalf("refined findings %v for %d refinements", refinedIDs, want.Dispositions[record.DispositionRefined])
	}
	detail := string(report.RenderMarkdownAt(r, report.Digest(raw), report.Korean, report.DetailFull))
	html, err := report.RenderHTMLAt(r, report.Digest(raw), report.Korean, report.DetailFull)
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{detail, string(html)} {
		if !strings.Contains(out, report.Korean.Label("finding.lead_refinement")) || !strings.Contains(out, originalTitle) {
			t.Fatal("the rendered report does not show the refinement lineage")
		}
	}
	if free := report.Project(r, false); slices.ContainsFunc(free.Findings, func(f report.Finding) bool {
		return f.LeadRefinement != nil && f.LeadRefinement.OriginalTitle == originalTitle
	}) {
		t.Fatal("the code-free export keeps the original title")
	}
	for name, forge := range map[string]func(r *report.Report){
		"unmarked refinement": func(r *report.Report) {
			for i := range r.Findings {
				r.Findings[i].LeadRefinement = nil
			}
		},
		"another task": func(r *report.Report) {
			i := slices.IndexFunc(r.Findings, func(f report.Finding) bool { return f.LeadRefinement != nil })
			x := *r.Findings[i].LeadRefinement
			x.TaskKey = record.TaskLeads
			r.Findings[i].LeadRefinement = &x
		},
		"a candidate of another role": func(r *report.Report) {
			i := slices.IndexFunc(r.Findings, func(f report.Finding) bool { return f.LeadRefinement != nil })
			x := *r.Findings[i].LeadRefinement
			x.CandidateID = "interpret_1"
			r.Findings[i].LeadRefinement = &x
		},
	} {
		bad := r
		bad.Findings = slices.Clone(r.Findings)
		forge(&bad)
		if err := report.ValidateReport(bad); err == nil {
			t.Fatalf("%s: forged lineage accepted", name)
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

// A contested candidate goes to verify unchanged, with the follow-up's
// reason and cited lines as counter-evidence and the contract sentence that
// says how to weigh them (only in a request that carries a contest); verify
// decides, and a refuted result is listed in the report as any refuted
// candidate.
func TestContestedCandidateGoesToVerify(t *testing.T) {
	j := treeJob(t, chainTree(12))
	j.RunID, j.AttemptID = "0195f3a0-0000-7000-8000-000000000c01", "0195f3a0-0000-7000-8000-000000000c02"
	pinExampleSnapshot(t, &j)
	reason := "끝 패키지는 0 이하의 입력에 0이나 그 값을 돌려주므로 인용한 줄의 합계에 음수가 더해지지 않는다."
	l := &leadsScript{asked: true, leads: func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[leadsInput](t, q)
		target := catalogOf(in.Scope.Excerpts)[in.Leads[0].Resolved[0]]
		line := target.Start
		for i, x := range strings.Split(target.Text, "\n") {
			if strings.Contains(x, "if n") {
				line = target.Start + i
			}
		}
		return asJSON(map[string]any{"results": []map[string]any{{"basis": in.Candidates[0].ID, "disposition": "contested", "reason": reason,
			"evidence_ids": []string{fmt.Sprintf("%s:%d-%d", target.ID, line, line)}}}})
	}}
	script := l.script(t)
	verify := script["verify"]
	var mu sync.Mutex
	contested := 0
	script["verify"] = func(n int, q opencode.Request) opencode.Reply {
		in := decodeAs[verifyInput](t, q)
		if !slices.ContainsFunc(in.Candidates, func(c verifyCandidate) bool { return c.Contested != nil }) {
			if strings.Contains(q.Instructions, verifyContestContract) {
				t.Error("the contest sentence travels without a contested candidate")
			}
			return verify(n, q)
		}
		sent := catalogOf(in.Scope.Excerpts)
		var out []any
		for _, c := range in.Candidates {
			if c.Contested == nil {
				out = append(out, inconclusiveFor(c.Candidate, c.Evidence[0]))
				continue
			}
			if c.Contested.Reason != reason || !strings.Contains(q.Instructions, verifyContestContract) ||
				slices.ContainsFunc(c.Contested.EvidenceIDs, func(id string) bool { _, ok := sent[id]; return !ok }) {
				t.Errorf("contested candidate %+v without its counter-evidence", c)
			}
			mu.Lock()
			contested++
			mu.Unlock()
			out = append(out, verdict(c.ID, "refuted", func(v map[string]any) {
				v["counterevidence_ids"] = c.Contested.EvidenceIDs
				v["invalid_conditions"] = []string{"끝 패키지가 0 이하의 입력에 음수를 돌려주지 않는다."}
			}))
		}
		return asJSON(map[string]any{"verifications": out})
	}
	h, err := discovery.Wrap(Handler{Static: vetStub{}, Caller: &scriptedCaller{script: script}, Budget: smallBudget}, discovery.Config{Observer: exampleObserver{t: t}})
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
	accepted := 0
	for _, p := range plan {
		if slot, ok := record.LeadsTask(p.Key); ok && results[p.Key].Status == domain.TaskSucceeded {
			fu := readRecord(t, j, strings.TrimPrefix(record.LeadsTaskKey(slot), "review."))
			if len(fu.LeadFollowup.Dispositions) != 1 || fu.LeadFollowup.Dispositions[0].Disposition != record.DispositionContested {
				t.Fatalf("%s dispositions %+v", p.Key, fu.LeadFollowup.Dispositions)
			}
			accepted++
		}
	}
	if accepted == 0 || contested != accepted {
		t.Fatalf("%d contested follow-ups, %d contested verify candidates", accepted, contested)
	}
	r, err := report.Build(exampleInput(t, j, h, plan, results, &exampleIDs{n: 0xc00}), report.DefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if err := report.ValidateReport(r); err != nil {
		t.Fatal(err)
	}
	if lf := r.CodeReview.LeadFollowups; lf == nil || lf.Dispositions[record.DispositionContested] != accepted {
		t.Fatalf("lead_followups %+v", lf)
	}
	refuted := 0
	for _, f := range r.Findings {
		if f.Title == chainCandidate("")["title"] && f.Validation == "refuted" {
			refuted++
		}
	}
	if refuted != accepted {
		t.Fatalf("%d refuted findings for %d contested candidates", refuted, accepted)
	}
}

// A refinement rewrites a candidate's text from the resolved code: it keeps
// every citation of the candidate it refines and cites lines of a whole
// resolved declaration. Dropping a citation (lead_refinement_citations_dropped),
// or citing only a partial window or the investigation's own lines
// (lead_refinement_evidence_not_resolved), is rejected: after its repair the
// result is left out and the candidate goes to verify as the investigation
// gave it. An accepted refinement keeps the original text in its
// disposition.
func TestLeadRefinementKeepsItsCitationsAndRestsOnNewCode(t *testing.T) {
	pad := strings.Repeat("\t// One more step of the chain.\n", 70)
	long := chainTree(12)
	for k, v := range long {
		if strings.HasSuffix(k, ".go") {
			long[k] = strings.Replace(v, "\t}\n\treturn ", "\t}\n"+pad+"\treturn ", 1)
		}
	}
	resolvedLine := func(in leadsInput) string {
		target := catalogOf(in.Scope.Excerpts)[in.Leads[0].Resolved[0]]
		return fmt.Sprintf("%s:%d-%d", target.ID, target.Start+1, target.Start+1)
	}
	for name, c := range map[string]struct {
		tree map[string]string
		// cite picks the refinement's citations.
		cite func(in leadsInput) []string
		want []string
	}{
		"keeps its citations and cites the resolved declaration": {chainTree(12), func(in leadsInput) []string {
			return []string{in.Leads[0].EvidenceID, resolvedLine(in)}
		}, nil},
		"drops the citation it started from": {chainTree(12), func(in leadsInput) []string {
			return []string{resolvedLine(in)}
		}, []string{errLeadRefinementCitations}},
		"cites a partial definition window": {long, func(in leadsInput) []string {
			return []string{in.Leads[0].EvidenceID, resolvedLine(in)}
		}, []string{errLeadRefinementLines}},
		"cites only the investigation's lines": {chainTree(12), func(in leadsInput) []string {
			return []string{in.Leads[0].EvidenceID}
		}, []string{errLeadRefinementLines}},
	} {
		t.Run(name, func(t *testing.T) {
			j := treeJob(t, c.tree)
			var mu sync.Mutex
			var repairs [][]string
			const condition = "n이 0보다 크고 다음 패키지가 0 이하를 받는 호출"
			l := &leadsScript{asked: true, leads: func(_ int, q opencode.Request) opencode.Reply {
				in := decodeAs[leadsInput](t, q)
				if in.Repair != nil {
					var codes []string
					for _, is := range in.Repair.Items {
						codes = append(codes, is.Error)
					}
					mu.Lock()
					repairs = append(repairs, codes)
					mu.Unlock()
				}
				refined := chainCandidate(in.Leads[0].EvidenceID)
				refined["condition"], refined["evidence_ids"] = condition, c.cite(in)
				refined["basis"], refined["disposition"] = in.Candidates[0].ID, "refined"
				return asJSON(map[string]any{"results": []map[string]any{refined}})
			}}
			script := l.script(t)
			verify := script["verify"]
			var conditions []string
			script["verify"] = func(n int, q opencode.Request) opencode.Reply {
				mu.Lock()
				for _, x := range decodeAs[verifyInput](t, q).Candidates {
					conditions = append(conditions, x.Condition)
				}
				mu.Unlock()
				return verify(n, q)
			}
			h, err := discovery.Wrap(Handler{Static: vetStub{}, Caller: &scriptedCaller{script: script}, Budget: smallBudget}, discovery.Config{Observer: exampleObserver{t: t}})
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
			run := 0
			for _, p := range plan {
				slot, ok := record.LeadsTask(p.Key)
				if !ok || results[p.Key].Status != domain.TaskSucceeded {
					continue
				}
				run++
				fu := readRecord(t, j, strings.TrimPrefix(record.LeadsTaskKey(slot), "review."))
				inv := readRecord(t, j, strings.TrimPrefix(strings.Replace(p.Key, "review.leads", "review.investigate", 1), "review."))
				d := fu.LeadFollowup.Dispositions
				original := inv.Candidates[0].CandidateText
				original.ID = ""
				if c.want == nil {
					if len(d) != 1 || d[0].Disposition != record.DispositionRefined || d[0].Original == nil ||
						!reflect.DeepEqual(*d[0].Original, original) || fu.Candidates[0].Condition != condition {
						t.Fatalf("%s dispositions %+v", p.Key, d)
					}
					continue
				}
				if len(d) != 1 || d[0].Disposition != record.DispositionKept || d[0].Original != nil ||
					!slices.Contains(fu.Limitations, limLeadResultOmitted) || fu.Candidates[0].Condition != original.Condition {
					t.Fatalf("%s dispositions %+v, limitations %v", p.Key, d, fu.Limitations)
				}
			}
			if run == 0 || slices.Contains(conditions, condition) != (c.want == nil) {
				t.Fatalf("%d follow-ups; verify read the refinement: %v", run, slices.Contains(conditions, condition))
			}
			if c.want == nil {
				if len(repairs) != 0 {
					t.Fatalf("repairs %v", repairs)
				}
				return
			}
			if len(repairs) != run {
				t.Fatalf("%d follow-ups, %d repairs", run, len(repairs))
			}
			for _, codes := range repairs {
				if !slices.Equal(codes, c.want) {
					t.Fatalf("repair codes %v", codes)
				}
			}
		})
	}
}

// A contest stands only on lines of a resolved excerpt that is the whole
// declaration. A definition too long for one window is sent partial (the
// request and the record say so), and a contest citing it, or citing only
// the investigation's own lines, is rejected (lead_contest_evidence_not_resolved):
// after its repair the result is left out and the candidate goes to verify
// as the investigation gave it.
func TestLeadContestNeedsAWholeResolvedExcerpt(t *testing.T) {
	pad := strings.Repeat("\t// One more step of the chain.\n", 70)
	long := chainTree(12)
	for k, v := range long {
		if strings.HasSuffix(k, ".go") {
			long[k] = strings.Replace(v, "\t}\n\treturn ", "\t}\n"+pad+"\treturn ", 1)
		}
	}
	for name, c := range map[string]struct {
		tree    map[string]string
		partial bool
		// cite picks the contest's citation: in the resolved window or the
		// lead's own line.
		cite func(in leadsInput) string
	}{
		"partial definition window": {long, true, func(in leadsInput) string {
			target := catalogOf(in.Scope.Excerpts)[in.Leads[0].Resolved[0]]
			return fmt.Sprintf("%s:%d-%d", target.ID, target.Start+1, target.Start+1)
		}},
		"the investigation's own lines": {chainTree(12), false, func(in leadsInput) string { return in.Leads[0].EvidenceID }},
	} {
		t.Run(name, func(t *testing.T) {
			j := treeJob(t, c.tree)
			var mu sync.Mutex
			var repairs [][]string
			l := &leadsScript{asked: true, leads: func(_ int, q opencode.Request) opencode.Reply {
				in := decodeAs[leadsInput](t, q)
				if got := slices.Equal(in.Leads[0].Partial, in.Leads[0].Resolved[:1]); got != c.partial {
					t.Errorf("request partial %v, resolved %v", in.Leads[0].Partial, in.Leads[0].Resolved)
				}
				if in.Repair != nil {
					var codes []string
					for _, is := range in.Repair.Items {
						codes = append(codes, is.Error)
					}
					mu.Lock()
					repairs = append(repairs, codes)
					mu.Unlock()
				}
				return asJSON(map[string]any{"results": []map[string]any{{"basis": in.Candidates[0].ID, "disposition": "contested",
					"reason": "인용한 줄이 음수 입력을 먼저 돌려보내므로 후보의 조건이 일어나지 않는다.", "evidence_ids": []string{c.cite(in)}}}})
			}}
			script := l.script(t)
			verify := script["verify"]
			script["verify"] = func(n int, q opencode.Request) opencode.Reply {
				if slices.ContainsFunc(decodeAs[verifyInput](t, q).Candidates, func(x verifyCandidate) bool { return x.Contested != nil }) {
					t.Error("a rejected contest reached verify")
				}
				return verify(n, q)
			}
			h, err := discovery.Wrap(Handler{Static: vetStub{}, Caller: &scriptedCaller{script: script}, Budget: smallBudget}, discovery.Config{Observer: exampleObserver{t: t}})
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
			run := 0
			for _, p := range plan {
				slot, ok := record.LeadsTask(p.Key)
				if !ok || results[p.Key].Status != domain.TaskSucceeded {
					continue
				}
				run++
				fu := readRecord(t, j, strings.TrimPrefix(record.LeadsTaskKey(slot), "review."))
				d := fu.LeadFollowup.Dispositions
				if len(d) != 1 || d[0].Disposition != record.DispositionKept || !slices.Contains(fu.Limitations, limLeadResultOmitted) {
					t.Fatalf("%s dispositions %+v, limitations %v", p.Key, d, fu.Limitations)
				}
				if got := len(fu.LeadFollowup.Partial) > 0; got != c.partial {
					t.Fatalf("%s record partial %v", p.Key, fu.LeadFollowup.Partial)
				}
				inv := readRecord(t, j, strings.TrimPrefix(strings.Replace(p.Key, "review.leads", "review.investigate", 1), "review."))
				if got := slices.ContainsFunc(inv.Leads, func(x record.Lead) bool {
					return slices.ContainsFunc(x.Targets, func(tg record.LeadTarget) bool { return tg.Partial })
				}); got != c.partial {
					t.Fatalf("%s investigation targets partial %v", p.Key, got)
				}
			}
			if run == 0 || len(repairs) != run {
				t.Fatalf("%d follow-ups, %d repairs", run, len(repairs))
			}
			for _, codes := range repairs {
				if !slices.Equal(codes, []string{errLeadContestLines}) {
					t.Fatalf("repair codes %v", codes)
				}
			}
		})
	}
}
