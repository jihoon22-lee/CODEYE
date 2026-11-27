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

// A caller window holds only the call and three lines around it, so a guard
// further up the caller is not in it. A contest citing a caller window
// reaches verify with the declaration enclosing that window (the same step
// that gives every cited range its declaration), so verify reads the guard.
func TestContestedCallerWindowReachesVerifyWithItsDeclaration(t *testing.T) {
	const guard = "if people <= 0 {"
	steps := strings.Repeat("\tsum += 1\n", 10)
	j := treeJob(t, map[string]string{
		"go.mod":      "module example.org/guard\n\ngo 1.23\n",
		"README.md":   "# Guard\n\nThe bill is split between the people present.\n",
		"calc/div.go": "package calc\n\n// Ratio divides total by parts.\nfunc Ratio(total, parts int) int {\n\treturn total / parts\n}\n",
		"app/run.go": "package app\n\nimport \"example.org/guard/calc\"\n\n" + strings.Repeat("// setup note\n", 900) +
			"\n// Share splits the bill.\nfunc Share(total, people int) int {\n\t" + guard + "\n\t\treturn 0\n\t}\n\tsum := 0\n" + steps +
			"\treturn calc.Ratio(total, people) + sum\n}\n",
	})
	reason := "호출부가 0 이하의 인원을 먼저 돌려보내므로 0으로 나누지 않는다."
	l := &leadsScript{asked: true, leads: func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[leadsInput](t, q)
		window := catalogOf(in.Scope.Excerpts)[in.Leads[0].Resolved[0]]
		if window.Path != "app/run.go" || strings.Contains(window.Text, guard) {
			t.Fatalf("caller window %+v", window)
		}
		return asJSON(map[string]any{"results": []map[string]any{{"basis": in.Candidates[0].ID, "disposition": "contested", "reason": reason,
			"evidence_ids": []string{citeLine(window, "calc.Ratio(")}}}})
	}}
	script := l.script(t)
	script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		i := slices.IndexFunc(in.Scope.Excerpts, func(e record.Excerpt) bool { return e.Path == "calc/div.go" })
		if !in.Scope.WholeRun || i < 0 {
			return asJSON(map[string]any{"candidates": []any{}})
		}
		div := in.Scope.Excerpts[i]
		candidate := chainCandidate(citeLine(div, "total / parts"))
		candidate["title"], candidate["condition"] = "parts가 0이면 0으로 나눈다", "parts가 0인 호출"
		out := map[string]any{"candidates": []map[string]any{candidate}}
		if in.Scope.LeadLimit > 0 {
			out["leads"] = []map[string]any{{"need": "callers", "evidence_id": citeLine(div, "func Ratio("), "symbol": "Ratio",
				"suspicion": "호출부가 0을 넘기는지 이 요청에 없다.", "candidate": 1}}
		}
		return asJSON(out)
	}
	verify := script["verify"]
	var mu sync.Mutex
	read := 0
	script["verify"] = func(n int, q opencode.Request) opencode.Reply {
		in := decodeAs[verifyInput](t, q)
		if slices.ContainsFunc(in.Candidates, func(c verifyCandidate) bool { return c.Contested != nil }) {
			if slices.ContainsFunc(in.Scope.Excerpts, func(e record.Excerpt) bool {
				return e.Path == "app/run.go" && e.Origin == record.OriginDeclaration && strings.Contains(e.Text, guard)
			}) {
				mu.Lock()
				read++
				mu.Unlock()
			}
			var out []any
			for _, c := range in.Candidates {
				out = append(out, inconclusiveFor(c.Candidate, c.Evidence[0]))
			}
			return asJSON(map[string]any{"verifications": out})
		}
		return verify(n, q)
	}
	_, results := runRoleWith(t, j, Handler{Static: vetStub{}, Caller: &scriptedCaller{script: script}, Budget: smallBudget}, "1.27.4")
	if r := results[record.TaskLeads]; r.Status != domain.TaskSucceeded {
		t.Fatalf("follow-up %+v", r)
	}
	if d := readRecord(t, j, record.StageLeads).LeadFollowup.Dispositions; len(d) != 1 || d[0].Disposition != record.DispositionContested {
		t.Fatalf("dispositions %+v", d)
	}
	if read == 0 || results["review.verify"].Status != domain.TaskSucceeded {
		t.Fatalf("verify read the caller's declaration %d times: %+v", read, results["review.verify"])
	}
}

// A resolved window holds only lines the investigate request did not send.
// The investigation sends app/run.go's first unit (Share) and not Other, and
// Other calls Ratio on the line after that unit, so the caller window (the
// call and three lines around it) would overlap three sent lines. It starts
// at the call instead, every line of it counts as new, and a refinement that
// adds the call line stands.
func TestCallerWindowLeavesTheSentLinesOut(t *testing.T) {
	run := "package app\n\nimport \"example.org/guard/calc\"\n\n// Share splits the bill.\nfunc Share(total, people int) int {\n\tif people <= 0 {\n\t\treturn 0\n\t}\n\treturn total / people\n}\n" +
		"func Other(total int) int { v := calc.Ratio(total, 3)\n" + strings.Repeat("\tv += total % 7\n", 120) + "\treturn v\n}\n"
	tree := map[string]string{
		"go.mod":      "module example.org/guard\n\ngo 1.23\n",
		"README.md":   "# Guard\n\nThe bill is split between the people present.\n",
		"calc/div.go": "package calc\n\n// Ratio divides total by parts.\nfunc Ratio(total, parts int) int {\n\treturn total / parts\n}\n",
		"app/run.go":  run,
	}
	// Padding files take the request budget, so Other is not sent.
	for i := range 8 {
		tree[fmt.Sprintf("pad/p%02d.go", i)] = fmt.Sprintf("package pad\n\n// P%02d pads.\nfunc P%02d(v int) int {\n", i, i) + strings.Repeat("\tv += v % 3\n", 100) + "\treturn v\n}\n"
	}
	call := slices.IndexFunc(strings.Split(run, "\n"), func(l string) bool { return strings.Contains(l, "calc.Ratio(") }) + 1
	j := treeJob(t, tree)
	var mu sync.Mutex
	sentEnd := 0
	l := &leadsScript{asked: true, leads: func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[leadsInput](t, q)
		window := catalogOf(in.Scope.Excerpts)[in.Leads[0].Resolved[0]]
		refined := chainCandidate(in.Candidates[0].Evidence[0])
		refined["condition"] = "Other가 3으로 나누는 호출"
		refined["evidence_ids"] = append(slices.Clone(in.Candidates[0].Evidence), citeLine(window, "calc.Ratio("))
		refined["basis"], refined["disposition"] = in.Candidates[0].ID, "refined"
		return asJSON(map[string]any{"results": []map[string]any{refined}})
	}}
	script := l.script(t)
	script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		i := slices.IndexFunc(in.Scope.Excerpts, func(e record.Excerpt) bool { return e.Path == "calc/div.go" })
		if !in.Scope.WholeRun || i < 0 {
			return asJSON(map[string]any{"candidates": []any{}})
		}
		mu.Lock()
		for _, e := range in.Scope.Excerpts {
			if e.Path == "app/run.go" {
				sentEnd = max(sentEnd, e.End)
			}
		}
		mu.Unlock()
		div := in.Scope.Excerpts[i]
		candidate := chainCandidate(citeLine(div, "total / parts"))
		candidate["title"], candidate["condition"] = "parts가 0이면 0으로 나눈다", "parts가 0인 호출"
		out := map[string]any{"candidates": []map[string]any{candidate}}
		if in.Scope.LeadLimit > 0 {
			out["leads"] = []map[string]any{{"need": "callers", "evidence_id": citeLine(div, "func Ratio("), "symbol": "Ratio",
				"suspicion": "호출부가 0을 넘기는지 이 요청에 없다.", "candidate": 1}}
		}
		return asJSON(out)
	}
	_, results := runRoleWith(t, j, Handler{Static: vetStub{}, Caller: &scriptedCaller{script: script}, Budget: smallBudget}, "1.27.4")
	inv := readRecord(t, j, record.StageInvestigate)
	if len(inv.Leads) != 1 || !inv.Leads[0].Resolved() || len(inv.Leads[0].Targets) != 1 {
		t.Fatalf("leads %+v", inv.Leads)
	}
	target := inv.Leads[0].Targets[0]
	if sentEnd != call-1 || target.Path != "app/run.go" || target.Start != call || target.End != call+3 {
		t.Fatalf("sent app/run.go up to line %d, call on line %d, caller window %+v", sentEnd, call, target)
	}
	fu := readRecord(t, j, record.StageLeads)
	d := fu.LeadFollowup.Dispositions
	if results[record.TaskLeads].Status != domain.TaskSucceeded || len(d) != 1 || d[0].Disposition != record.DispositionRefined || d[0].Lead != inv.Leads[0].ID ||
		len(fu.Calls) != 1 || len(fu.Calls[0].OutputErrors) != 0 || fu.LeadFollowup.NewLines != target.End-target.Start+1 || !record.LeadFollowupMatches(fu, inv) {
		t.Fatalf("follow-up %+v, dispositions %+v, calls %+v, new lines %d", results[record.TaskLeads], d, fu.Calls, fu.LeadFollowup.NewLines)
	}
	if results["review.verify"].Status != domain.TaskSucceeded {
		t.Fatalf("verify %+v", results["review.verify"])
	}
}

// A refinement rewrites a candidate's text from the resolved code: it keeps
// every citation of the candidate it refines first and in order and adds one
// or two, each a line of a whole resolved declaration, so it cites at most
// record.MaxRefinedEvidence (a candidate citing six adds a seventh or an
// eighth). Dropping a citation (lead_refinement_citations_dropped), citing
// the resolved line first (lead_refinement_citations_reordered), adding three
// (lead_refinement_citations_over_cap), or adding none, a partial window's
// line or a line the investigation sent (lead_refinement_evidence_not_resolved)
// leaves the result out at once without a repair call, and the candidate goes
// to verify as the investigation gave it. An accepted refinement keeps the
// original text in its disposition, is verified with all its citations and
// its finding validates in the report.
func TestLeadRefinementKeepsItsCitationsAndRestsOnNewCode(t *testing.T) {
	pad := strings.Repeat("\t// One more step of the chain.\n", 70)
	long := chainTree(12)
	for k, v := range long {
		if strings.HasSuffix(k, ".go") {
			long[k] = strings.Replace(v, "\t}\n\treturn ", "\t}\n"+pad+"\treturn ", 1)
		}
	}
	// resolved is n single-line citations of the lead's first resolved
	// excerpt, from its second line.
	resolved := func(in leadsInput, n int) []string {
		target := catalogOf(in.Scope.Excerpts)[in.Leads[0].Resolved[0]]
		var out []string
		for i := 1; i <= n; i++ {
			out = append(out, fmt.Sprintf("%s:%d-%d", target.ID, target.Start+i, target.Start+i))
		}
		return out
	}
	kept := func(in leadsInput, more ...string) []string {
		return append(slices.Clone(in.Candidates[0].Evidence), more...)
	}
	// The investigation's candidate cites the chain call (the lead's line)
	// or not, and the lines above it, cites in all.
	type citing struct {
		anchor bool
		cites  int
	}
	for name, c := range map[string]struct {
		tree map[string]string
		inv  citing
		// cite picks the refinement's citations.
		cite func(in leadsInput) []string
		want string
	}{
		"a candidate citing six adds two": {chainTree(12), citing{true, 6}, func(in leadsInput) []string {
			return kept(in, resolved(in, 2)...)
		}, ""},
		"cites the resolved line first": {chainTree(12), citing{true, 2}, func(in leadsInput) []string {
			return append(resolved(in, 1), in.Candidates[0].Evidence...)
		}, errLeadRefinementOrder},
		"keeps its citations and adds a line of the resolved declaration": {chainTree(12), citing{true, 1}, func(in leadsInput) []string {
			return kept(in, resolved(in, 1)...)
		}, ""},
		"a candidate citing six adds a seventh": {chainTree(12), citing{true, 6}, func(in leadsInput) []string {
			return kept(in, resolved(in, 1)...)
		}, ""},
		"a candidate citing six adds three": {chainTree(12), citing{true, 6}, func(in leadsInput) []string {
			return kept(in, resolved(in, 3)...)
		}, errLeadRefinementOverCap},
		"drops the citation it started from": {chainTree(12), citing{true, 1}, func(in leadsInput) []string {
			return resolved(in, 1)
		}, errLeadRefinementCitations},
		"cites a partial definition window": {long, citing{true, 1}, func(in leadsInput) []string {
			return kept(in, resolved(in, 1)...)
		}, errLeadRefinementLines},
		"cites only the investigation's lines": {chainTree(12), citing{true, 1}, func(in leadsInput) []string {
			return kept(in)
		}, errLeadRefinementLines},
		"adds the lead's line beside the resolved declaration": {chainTree(12), citing{false, 2}, func(in leadsInput) []string {
			return kept(in, append([]string{in.Leads[0].EvidenceID}, resolved(in, 1)...)...)
		}, errLeadRefinementLines},
	} {
		t.Run(name, func(t *testing.T) {
			j := treeJob(t, c.tree)
			var mu sync.Mutex
			repairs, fresh := 0, 0
			const condition = "n이 0보다 크고 다음 패키지가 0 이하를 받는 호출"
			l := &leadsScript{asked: true, leads: func(_ int, q opencode.Request) opencode.Reply {
				in := decodeAs[leadsInput](t, q)
				if in.Repair != nil {
					mu.Lock()
					repairs++
					mu.Unlock()
				}
				refined := chainCandidate(in.Leads[0].EvidenceID)
				refined["condition"], refined["evidence_ids"] = condition, c.cite(in)
				mu.Lock()
				fresh = len(c.cite(in)) - len(in.Candidates[0].Evidence)
				mu.Unlock()
				refined["basis"], refined["disposition"] = in.Candidates[0].ID, "refined"
				return asJSON(map[string]any{"results": []map[string]any{refined}})
			}}
			script := l.script(t)
			investigate := script["investigate"]
			script["investigate"] = func(n int, q opencode.Request) opencode.Reply {
				in := decodeAs[investigateInput](t, q)
				cite, callee, ok := chainCite(in.Scope.Excerpts)
				if !ok || (c.inv.anchor && c.inv.cites == 1) {
					return investigate(n, q)
				}
				// The lines above the chain call, in the excerpt that holds it.
				id, line := "", 0
				if _, err := fmt.Sscanf(strings.ReplaceAll(cite, ":", " "), "%s %d", &id, &line); err != nil {
					t.Fatal(err)
				}
				var cites []string
				if c.inv.anchor {
					cites = append(cites, cite)
				}
				for k := 1; len(cites) < c.inv.cites; k++ {
					cites = append(cites, fmt.Sprintf("%s:%d-%d", id, line-k, line-k))
				}
				candidate := chainCandidate(cite)
				candidate["evidence_ids"] = cites
				out := map[string]any{"candidates": []map[string]any{candidate}}
				if in.Scope.LeadLimit > 0 {
					out["leads"] = []map[string]any{{"need": "definition", "evidence_id": cite, "symbol": callee, "suspicion": "다음 패키지 함수가 음수를 돌려주는 조건이 이 요청에 없다.", "candidate": 1}}
				}
				return asJSON(out)
			}
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
				name := strings.TrimPrefix(record.LeadsTaskKey(slot), "review.")
				fu := readRecord(t, j, name)
				inv := readRecord(t, j, strings.TrimPrefix(strings.Replace(p.Key, "review.leads", "review.investigate", 1), "review."))
				d := fu.LeadFollowup.Dispositions
				original := inv.Candidates[0].CandidateText
				original.ID = ""
				if len(original.Evidence) != c.inv.cites {
					t.Fatalf("%s: the investigation's candidate cites %v", p.Key, original.Evidence)
				}
				if len(fu.Calls) != 1 || fu.Calls[0].Outcome != record.OutcomeAccepted {
					t.Fatalf("%s calls %+v", p.Key, fu.Calls)
				}
				if c.want == "" {
					if len(d) != 1 || d[0].Disposition != record.DispositionRefined || d[0].Original == nil ||
						!reflect.DeepEqual(*d[0].Original, original) || fu.Candidates[0].Condition != condition ||
						len(fu.Candidates[0].Evidence) != c.inv.cites+fresh || len(fu.Calls[0].OutputErrors) != 0 {
						t.Fatalf("%s dispositions %+v, candidate %+v, calls %+v", p.Key, d, fu.Candidates[0], fu.Calls)
					}
					// Its verify checks it with every citation.
					v := readRecord(t, j, strings.Replace(strings.TrimPrefix(record.LeadsTaskKey(slot), "review."), "leads", "verify", 1))
					if !slices.ContainsFunc(v.Candidates, func(x record.Candidate) bool { return slices.Equal(x.Evidence, fu.Candidates[0].Evidence) }) ||
						results[strings.Replace(p.Key, "review.leads", "review.verify", 1)].Status != domain.TaskSucceeded {
						t.Fatalf("%s: verify candidates %+v", p.Key, v.Candidates)
					}
					// Only a refinement cites more than MaxCandidateEvidence:
					// the record check rejects the same candidate kept, and
					// takes it kept with six of its citations.
					if n := len(fu.Candidates[0].Evidence); n > record.MaxCandidateEvidence {
						for _, cites := range []int{record.MaxCandidateEvidence, n} {
							var kept record.Record
							b, err := json.Marshal(fu)
							if err != nil {
								t.Fatal(err)
							}
							if err := json.Unmarshal(b, &kept); err != nil {
								t.Fatal(err)
							}
							k := &kept.LeadFollowup.Dispositions[0]
							k.Disposition, k.Lead, k.Original = record.DispositionKept, "", nil
							kept.Candidates[0].Evidence = kept.Candidates[0].Evidence[:cites]
							if _, err := record.Marshal(kept, expectRecord(j, name)); (err == nil) != (cites == record.MaxCandidateEvidence) {
								t.Fatalf("a kept candidate citing %d: %v", cites, err)
							}
						}
					}
					continue
				}
				if len(d) != 1 || d[0].Disposition != record.DispositionKept || d[0].Original != nil ||
					!slices.Contains(fu.Limitations, limLeadResultOmitted) || fu.Candidates[0].Condition != original.Condition ||
					!slices.Equal(fu.Calls[0].OutputErrors, []string{c.want}) || !record.LeadFollowupMatches(fu, inv) {
					t.Fatalf("%s dispositions %+v, limitations %v, calls %+v", p.Key, d, fu.Limitations, fu.Calls)
				}
			}
			if run == 0 || repairs != 0 || slices.Contains(conditions, condition) != (c.want == "") {
				t.Fatalf("%d follow-ups, %d repairs; verify read the refinement: %v", run, repairs, slices.Contains(conditions, condition))
			}
			// The verify record and the report take a refined candidate of
			// up to MaxRefinedEvidence citations, and a follow-up whose
			// refinement was left out (its code an output error of the
			// accepted call) with no refined finding.
			r, err := report.Build(exampleInput(t, j, h, plan, results, &exampleIDs{n: 0xd00}), report.DefaultRegistry())
			if err != nil {
				t.Fatal(err)
			}
			for _, lim := range r.Limitations {
				if strings.HasPrefix(lim, report.LimReviewLineage) || strings.HasPrefix(lim, "artifact_rejected:review") || strings.HasPrefix(lim, "unrecognized_artifact") {
					t.Fatalf("limitation %s in %v", lim, r.Limitations)
				}
			}
			if slices.ContainsFunc(r.Findings, func(f report.Finding) bool { return f.LeadRefinement != nil }) != (c.want == "") {
				t.Fatalf("refined findings in the report: %v", slices.ContainsFunc(r.Findings, func(f report.Finding) bool { return f.LeadRefinement != nil }))
			}
			if err := report.ValidateReport(r); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A refinement rests on the code of the leads that name its candidate, and
// the lineage records the lead of its first new citation. Two leads anchor
// on one call line: F's for candidate 1 and G's for candidate 2 or also for
// candidate 1. A refinement of candidate 1 whose new citations include a
// line of G's resolved declaration is left out at once
// (lead_refinement_evidence_not_resolved, no repair) when G's lead names
// candidate 2. When it names candidate 1 the refinement stands, also with
// one new citation from each lead, and its disposition names the lead of
// the first; naming the other lead fails the lineage check.
func TestLeadRefinementRestsOnItsOwnLead(t *testing.T) {
	tree := twoPartsTree()
	for name, c := range map[string]struct {
		second int      // the candidate G's lead names
		cites  []string // the leads whose resolved declarations hold the new citations, in order
		want   string
		lead   string // the lead the disposition names
	}{
		"on the lead of another candidate":         {2, []string{"G"}, errLeadRefinementLines, ""},
		"on the second lead of its candidate":      {1, []string{"G"}, "", "G"},
		"split across the leads of its candidate":  {1, []string{"G", "F"}, "", "G"},
		"split onto the lead of another candidate": {2, []string{"F", "G"}, errLeadRefinementLines, ""},
	} {
		t.Run(name, func(t *testing.T) {
			j := treeJob(t, tree)
			script := goBuildFilesScript(t)
			script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
				in := decodeAs[investigateInput](t, q)
				cite := ""
				for _, e := range in.Scope.Excerpts {
					for i, line := range strings.Split(e.Text, "\n") {
						if strings.Contains(line, "a.F(n) + b.G(n)") {
							cite = fmt.Sprintf("%s:%d-%d", e.ID, e.Start+i, e.Start+i)
						}
					}
				}
				if cite == "" {
					return asJSON(map[string]any{"candidates": []map[string]any{chainCandidate(shippedExcerpt(in.Scope.Excerpts).ID)}})
				}
				second := chainCandidate(cite)
				second["title"] = "두 번째 부분의 음수 처리에 따라 합계가 달라질 수 있음"
				out := map[string]any{"candidates": []map[string]any{chainCandidate(cite), second}}
				if in.Scope.LeadLimit > 0 {
					out["leads"] = []map[string]any{
						{"need": "definition", "evidence_id": cite, "symbol": "F", "suspicion": "F가 음수를 돌려주는 조건이 이 요청에 없다.", "candidate": 1},
						{"need": "definition", "evidence_id": cite, "symbol": "G", "suspicion": "G가 음수를 돌려주는 조건이 이 요청에 없다.", "candidate": c.second},
					}
				}
				return asJSON(out)
			}
			var mu sync.Mutex
			repairs := 0
			script["leads"] = func(_ int, q opencode.Request) opencode.Reply {
				in := decodeAs[leadsInput](t, q)
				if in.Repair != nil {
					mu.Lock()
					repairs++
					mu.Unlock()
				}
				refined := chainCandidate(in.Candidates[0].Evidence[0])
				refined["condition"] = "G가 음수를 받는 호출"
				ev := slices.Clone(in.Candidates[0].Evidence)
				for _, symbol := range c.cites {
					k := slices.IndexFunc(in.Leads, func(l leadInput) bool { return l.Symbol == symbol })
					target := catalogOf(in.Scope.Excerpts)[in.Leads[k].Resolved[0]]
					ev = append(ev, fmt.Sprintf("%s:%d-%d", target.ID, target.Start+1, target.Start+1))
				}
				refined["evidence_ids"] = ev
				refined["basis"], refined["disposition"] = in.Candidates[0].ID, "refined"
				return asJSON(map[string]any{"results": []map[string]any{refined}})
			}
			_, results := runRoleWith(t, j, Handler{Static: vetStub{}, Caller: &scriptedCaller{script: script}, Budget: smallBudget}, "1.27.4")
			inv := readRecord(t, j, record.StageInvestigate)
			if len(inv.Leads) != 2 || !inv.Leads[0].Resolved() || !inv.Leads[1].Resolved() || results[record.TaskLeads].Status != domain.TaskSucceeded {
				t.Fatalf("leads %+v, follow-up %+v", inv.Leads, results[record.TaskLeads])
			}
			fu := readRecord(t, j, record.StageLeads)
			d := fu.LeadFollowup.Dispositions
			if repairs != 0 || len(fu.Calls) != 1 || len(d) == 0 || d[0].CandidateID != inv.Candidates[0].ID {
				t.Fatalf("%d repairs, calls %+v, dispositions %+v", repairs, fu.Calls, d)
			}
			leadOf := func(symbol string) string {
				return inv.Leads[slices.IndexFunc(inv.Leads, func(l record.Lead) bool { return l.Symbol == symbol })].ID
			}
			if c.want == "" {
				if d[0].Disposition != record.DispositionRefined || d[0].Lead != leadOf(c.lead) || len(fu.Calls[0].OutputErrors) != 0 ||
					len(fu.Candidates[0].Evidence) != len(inv.Candidates[0].Evidence)+len(c.cites) || !record.LeadFollowupMatches(fu, inv) {
					t.Fatalf("dispositions %+v, candidate %+v, calls %+v", d, fu.Candidates[0], fu.Calls)
				}
				// The lineage check binds the first new citation to the lead
				// the disposition names: the same record naming the other
				// lead fails.
				var other record.Record
				b, err := json.Marshal(fu)
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(b, &other); err != nil {
					t.Fatal(err)
				}
				other.LeadFollowup.Dispositions[0].Lead = leadOf("F")
				if record.LeadFollowupMatches(other, inv) {
					t.Fatal("the lineage check accepted a refinement naming another lead than the one of its first new citation")
				}
				return
			}
			if d[0].Disposition != record.DispositionKept || !slices.Equal(fu.Calls[0].OutputErrors, []string{c.want}) {
				t.Fatalf("dispositions %+v, calls %+v", d, fu.Calls)
			}
		})
	}
}

// twoPartsTree is a Go project whose Run adds a.F and b.G on one line; both
// parts are padded so the investigation sends neither declaration.
func twoPartsTree() map[string]string {
	pad := strings.Repeat("// setup note\n", 900)
	part := func(pkg, name, negative string) string {
		return "package " + pkg + "\n\n" + pad + "\n// " + name + " is one part of the total.\nfunc " + name + "(n int) int {\n\tif n < 0 {\n\t\treturn " + negative + "\n\t}\n\treturn n\n}\n"
	}
	return map[string]string{
		"go.mod":     "module example.org/two\n\ngo 1.23\n",
		"README.md":  "# Two\n\nRun adds the two parts.\n",
		"app/run.go": "package app\n\nimport (\n\t\"example.org/two/a\"\n\t\"example.org/two/b\"\n)\n\n// Run adds the two parts.\nfunc Run(n int) int {\n\treturn a.F(n) + b.G(n)\n}\n",
		"a/f.go":     part("a", "F", "-1"),
		"b/g.go":     part("b", "G", "-2"),
	}
}

// An added candidate rests on its own lead's code: one for F's lead that
// cites only G's resolved declaration is rejected
// (lead_addition_evidence_not_new) and, after its repair, left out; one that
// cites F's stands, and its disposition names F's lead.
func TestLeadAdditionRestsOnItsOwnLead(t *testing.T) {
	for name, c := range map[string]struct {
		cites string // the symbol whose resolved declaration the addition cites
		want  string
	}{
		"on another lead's code": {"G", errLeadAdditionLines},
		"on its own lead's code": {"F", ""},
	} {
		t.Run(name, func(t *testing.T) {
			j := treeJob(t, twoPartsTree())
			script := goBuildFilesScript(t)
			script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
				in := decodeAs[investigateInput](t, q)
				cite := ""
				for _, e := range in.Scope.Excerpts {
					for i, line := range strings.Split(e.Text, "\n") {
						if strings.Contains(line, "a.F(n) + b.G(n)") {
							cite = fmt.Sprintf("%s:%d-%d", e.ID, e.Start+i, e.Start+i)
						}
					}
				}
				if cite == "" {
					return asJSON(map[string]any{"candidates": []map[string]any{chainCandidate(shippedExcerpt(in.Scope.Excerpts).ID)}})
				}
				out := map[string]any{"candidates": []map[string]any{chainCandidate(cite)}}
				if in.Scope.LeadLimit > 0 {
					out["leads"] = []map[string]any{
						{"need": "definition", "evidence_id": cite, "symbol": "F", "suspicion": "F가 음수를 돌려주는 조건이 이 요청에 없다.", "candidate": 1},
						{"need": "definition", "evidence_id": cite, "symbol": "G", "suspicion": "G가 음수를 돌려주는 조건이 이 요청에 없다.", "candidate": 1},
					}
				}
				return asJSON(out)
			}
			script["leads"] = func(_ int, q opencode.Request) opencode.Reply {
				in := decodeAs[leadsInput](t, q)
				f := slices.IndexFunc(in.Leads, func(l leadInput) bool { return l.Symbol == "F" })
				on := slices.IndexFunc(in.Leads, func(l leadInput) bool { return l.Symbol == c.cites })
				target := catalogOf(in.Scope.Excerpts)[in.Leads[on].Resolved[0]]
				added := chainCandidate(fmt.Sprintf("%s:%d-%d", target.ID, target.Start+2, target.Start+2))
				added["title"] = "음수 입력이 합계를 음수로 만든다"
				added["basis"], added["disposition"] = in.Leads[f].ID, "added"
				return asJSON(map[string]any{"results": []map[string]any{added}})
			}
			_, results := runRoleWith(t, j, Handler{Static: vetStub{}, Caller: &scriptedCaller{script: script}, Budget: smallBudget}, "1.27.4")
			inv := readRecord(t, j, record.StageInvestigate)
			if len(inv.Leads) != 2 || !inv.Leads[0].Resolved() || !inv.Leads[1].Resolved() || results[record.TaskLeads].Status != domain.TaskSucceeded {
				t.Fatalf("leads %+v, follow-up %+v", inv.Leads, results[record.TaskLeads])
			}
			fu := readRecord(t, j, record.StageLeads)
			fLead := inv.Leads[slices.IndexFunc(inv.Leads, func(l record.Lead) bool { return l.Symbol == "F" })].ID
			added := slices.IndexFunc(fu.LeadFollowup.Dispositions, func(d record.LeadDisposition) bool { return d.Disposition == record.DispositionAdded })
			var errs []string
			for _, call := range fu.Calls {
				errs = append(errs, call.OutputErrors...)
			}
			if c.want == "" {
				if added < 0 || fu.LeadFollowup.Dispositions[added].Lead != fLead || len(errs) != 0 || !record.LeadFollowupMatches(fu, inv) {
					t.Fatalf("dispositions %+v, calls %+v", fu.LeadFollowup.Dispositions, fu.Calls)
				}
				return
			}
			if added >= 0 || !slices.Contains(errs, c.want) || !slices.Contains(fu.Limitations, limLeadResultOmitted) {
				t.Fatalf("dispositions %+v, limitations %v, calls %+v", fu.LeadFollowup.Dispositions, fu.Limitations, fu.Calls)
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
