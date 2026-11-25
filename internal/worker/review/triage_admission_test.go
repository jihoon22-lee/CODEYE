package review

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/staticcheck"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// admissionTree is a Go module of a.go and files f01.go..fNN.go with fns
// functions each (function k at line 3+6k). Every function of a.go and of
// the first files carries a high-severity SA5011 on its first body line;
// every function of every file an SA4006 (medium) and an S1000 (info).
func admissionTree(files, fns int) (map[string]string, []string, []exampleDiag) {
	tree := map[string]string{"go.mod": "module example.test/triage\n\ngo 1.27\n"}
	names := []string{"a.go"}
	for i := range files {
		names = append(names, fmt.Sprintf("f%02d.go", i+1))
	}
	var diags []exampleDiag
	for n, name := range names {
		var b strings.Builder
		b.WriteString("package triage\n\n")
		stem := strings.TrimSuffix(name, ".go")
		for k := range fns {
			fmt.Fprintf(&b, "func %s_%d(x *int, y int) int {\n\tif *x > y {\n\t\ty = *x + %d\n\t}\n\treturn y\n}\n", stem, k, k)
			if n < 3 {
				diags = append(diags, exampleDiag{path: name, line: 4 + 6*k, rule: "SA5011", category: "correctness", message: "possible nil pointer dereference (synthetic)"})
			}
			diags = append(diags, exampleDiag{path: name, line: 5 + 6*k, rule: "SA4006", category: "correctness", message: "this value of y is never used (synthetic)"},
				exampleDiag{path: name, line: 7 + 6*k, rule: "S1000", category: "maintainability", message: "should use a simpler form (synthetic)"})
		}
		tree[name] = b.String()
	}
	return tree, names, diags
}

func admissionStatic(t *testing.T, files []string, diags []exampleDiag) *pack.Composite {
	t.Helper()
	sc := staticcheck.Descriptor()
	rp := obsdoc.RulePack{ID: staticcheck.RulePackID, Version: staticcheck.RulePackVersion, Selection: []string{"-checks", "all"}, RepositoryConfig: obsdoc.RepoConfigOverridden,
		SourceSuppressions: obsdoc.SuppressReported, ConfigFiles: []obsdoc.ConfigFile{}, UnreportedSuppressionRules: []string{}}
	c, err := pack.NewComposite(
		exampleMember{d: govet.Descriptor(), plan: govet.Plan, run: func(j analysis.Job, task pack.Task) analysis.TaskResult { return triageVet(t, j, task, files) }},
		exampleMember{d: sc, plan: staticcheck.Plan, run: func(j analysis.Job, task pack.Task) analysis.TaskResult {
			return exampleEnvelope(t, j, sc, task, "2026.2.2", rp, files, []string{"default_build_context_only"}, diags)
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// admissionRun runs the deep-review composition over tree at the smallest
// request budget (so the module slices are needed) and builds the report.
type admissionRun struct {
	j       analysis.Job
	results map[string]analysis.TaskResult
	report  report.Report
	scope   record.ScopeDoc
	calls   int
}

func runAdmission(t *testing.T, tree map[string]string, files []string, diags []exampleDiag, maxCalls int, onTriage func(triageInput)) admissionRun {
	t.Helper()
	j := treeJob(t, tree)
	script := triageReviewScript(t)
	if onTriage != nil {
		triage := script["triage"]
		script["triage"] = func(n int, q opencode.Request) opencode.Reply {
			onTriage(decodeAs[triageInput](t, q))
			return triage(n, q)
		}
	}
	script["interpret"] = func(_ int, q opencode.Request) opencode.Reply {
		return asJSON(map[string]any{"hypotheses": []map[string]any{}, "limits": []map[string]any{{"observation_ids": []string{decodeAs[interpretInput](t, q).Observations[0].ID}, "statement": "도구 메시지만으로는 영향을 판단할 수 없다."}}})
	}
	caller := &scriptedCaller{script: script}
	h, err := discovery.Wrap(Handler{Static: admissionStatic(t, files, diags), Caller: caller, MaxCalls: maxCalls,
		Budget: Budget{RequestBytes: MinRequestBytes}}, discovery.Config{Observer: exampleObserver{t: t}})
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
	r, err := report.Build(exampleInput(t, j, h, plan, results, &exampleIDs{n: 0xa00}), report.DefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if err := report.ValidateReport(r); err != nil {
		t.Fatal(err)
	}
	var scope record.ScopeDoc
	if b, err := os.ReadFile(scopePath(j)); err == nil {
		if err := json.Unmarshal(b, &scope); err != nil {
			t.Fatal(err)
		}
	}
	return admissionRun{j: j, results: results, report: r, scope: scope, calls: len(caller.requests)}
}

// admissionCounts are the module slice and triage slot outcomes of a run.
func (r admissionRun) counts(t *testing.T) (slicesDone, slicesExhausted int, triage map[string]domain.TaskStatus) {
	t.Helper()
	triage = map[string]domain.TaskStatus{}
	for k, res := range r.results {
		switch {
		case strings.HasPrefix(k, "review.investigate.s") && res.Status == domain.TaskSucceeded:
			slicesDone++
		case strings.HasPrefix(k, "review.investigate.s") && res.Status == domain.TaskBudgetExhausted:
			slicesExhausted++
		case strings.HasPrefix(k, "review.triage.t"):
			triage[k] = res.Status
		}
	}
	return slicesDone, slicesExhausted, triage
}

func (r admissionRun) linesSent() (sent, total int) {
	for _, f := range r.report.CodeReview.Ledger.Files {
		sent, total = sent+f.SentLines, total+f.TotalLines
	}
	return sent, total
}

// TestTriageAdmissionLeavesSlicesWhatTheyNeed: at the evaluation ceiling of
// 20 calls, a project with hundreds of diagnostics over many files still has
// every reviewable line sent when the module slices fit in the calls the
// whole-run roles left; the triage takes what the slices did not use.
func TestTriageAdmissionLeavesSlicesWhatTheyNeed(t *testing.T) {
	tree, files, diags := admissionTree(8, 40)
	var first []string
	run := runAdmission(t, tree, files, diags, 20, func(in triageInput) {
		if first == nil {
			for _, g := range in.Groups {
				first = append(first, g.DefaultSeverity)
			}
		}
	})
	done, exhausted, triage := run.counts(t)
	sent, total := run.linesSent()
	if hold, _ := os.ReadFile(triageHoldPath(run.j)); string(hold) != "0" {
		t.Fatalf("triage hold %q, want 0 when the slices fit", hold)
	}
	if len(diags) < 500 || len(files) < 9 || done < 2 || exhausted != 0 || sent != total || run.report.CodeReview.Ledger.Status != "complete" {
		t.Fatalf("diagnostics %d files %d: slices done %d exhausted %d, lines %d/%d (%s)", len(diags), len(files), done, exhausted, sent, total, run.report.CodeReview.Ledger.Status)
	}
	if len(triage) == 0 || triage["review.triage.t001"] != domain.TaskSucceeded {
		t.Fatalf("triage slots after the slices: %v", triage)
	}
	// The first slot opens with the high-severity groups.
	if len(first) == 0 || first[0] != "high" {
		t.Fatalf("first slot severities %v", first)
	}
}

// TestTriageAdmissionHoldsAShareForTheHighestSeverity: when the slices
// cannot all fit at ceiling 20, they get every call but the held share of one
// pair, and the first slot triages the high-severity groups.
func TestTriageAdmissionHoldsAShareForTheHighestSeverity(t *testing.T) {
	tree, files, diags := admissionTree(30, 40)
	var sent []triageGroupInput
	run := runAdmission(t, tree, files, diags, 20, func(in triageInput) { sent = append(sent, in.Groups...) })
	done, exhausted, triage := run.counts(t)
	if hold, _ := os.ReadFile(triageHoldPath(run.j)); string(hold) != "2" {
		t.Fatalf("triage hold %q, want one pair (2)", hold)
	}
	left := 20 - run.scope.Budget.CallsBeforeSlices
	if want := (left - 2) / record.SliceMinCalls; done != want || exhausted == 0 {
		t.Fatalf("slices done %d (want %d of %d calls left less the held pair), exhausted %d", done, want, left, exhausted)
	}
	if triage["review.triage.t001"] != domain.TaskSucceeded {
		t.Fatalf("first triage slot: %v", triage)
	}
	for k, st := range triage {
		if k != "review.triage.t001" && st != domain.TaskBudgetExhausted {
			t.Fatalf("later slot %s = %s, want budget_exhausted", k, st)
		}
	}
	// The slot is filled in priority order: its groups are high, or lower
	// ones on code a high group of the slot already shows (no extra bytes).
	highUnits := map[string]bool{}
	for _, g := range sent {
		if g.DefaultSeverity == "high" {
			for _, c := range g.Context {
				highUnits[c] = true
			}
		}
	}
	if len(sent) == 0 || sent[0].DefaultSeverity != "high" || len(highUnits) < 5 {
		t.Fatalf("first slot groups %+v, want high first", sent)
	}
	for _, g := range sent {
		if g.DefaultSeverity != "high" && !slices.ContainsFunc(g.Context, func(c string) bool { return highUnits[c] }) {
			t.Fatalf("first slot holds a %s group on code no high group shows: %+v", g.DefaultSeverity, g)
		}
	}
	if used := run.calls; used > 20 || used < 19 {
		t.Fatalf("calls used %d, want the ceiling less at most the one call no pair fits", used)
	}
}

// TestTriageVerifyFitsLargeGroupsAtTheMinimumBudget: problem groups of 64
// diagnostics each are verified at the smallest request budget; the request
// names a bounded number of each candidate's observations, the record keeps
// them all.
func TestTriageVerifyFitsLargeGroupsAtTheMinimumBudget(t *testing.T) {
	var b strings.Builder
	b.WriteString("package triage\n\n")
	var diags []exampleDiag
	line := 3
	for k := range 4 {
		fmt.Fprintf(&b, "func big%d(x *int) int {\n", k)
		for i := range 64 {
			fmt.Fprintf(&b, "\ty%d := *x\n", i)
			diags = append(diags, exampleDiag{path: "a.go", line: line + 1 + i, rule: "SA4006", category: "correctness", message: "this value is never used (synthetic)"})
		}
		b.WriteString("\treturn 0\n}\n")
		line += 67
	}
	tree := map[string]string{"go.mod": "module example.test/triage\n\ngo 1.27\n", "a.go": b.String()}
	j := treeJob(t, tree)
	script := triageReviewScript(t)
	problems := triageScript(t, func(triageGroupInput) string { return record.TriageProblem })
	// The unit is longer than a whole citation: cite the first diagnostic's line.
	script["triage"] = func(n int, q opencode.Request) opencode.Reply {
		in := decodeAs[triageInput](t, q)
		var out struct {
			Triage []map[string]any `json:"triage"`
		}
		if err := json.Unmarshal(problems(n, q).Output, &out); err != nil {
			t.Fatal(err)
		}
		for i, g := range in.Groups {
			out.Triage[i]["evidence_ids"] = []string{fmt.Sprintf("%s:%d-%d", g.Context[0], g.Lines[0], g.Lines[0])}
		}
		return asJSON(out)
	}
	var verifyBytes []int
	whole := script["verify"]
	script["verify"] = func(n int, q opencode.Request) opencode.Reply {
		verifyBytes = append(verifyBytes, len(opencode.Prompt(q)))
		return whole(n, q)
	}
	script["interpret"] = func(_ int, q opencode.Request) opencode.Reply {
		return asJSON(map[string]any{"hypotheses": []map[string]any{}, "limits": []map[string]any{{"observation_ids": []string{decodeAs[interpretInput](t, q).Observations[0].ID}, "statement": "도구 메시지만으로는 영향을 판단할 수 없다."}}})
	}
	caller := &scriptedCaller{script: script}
	h, err := discovery.Wrap(Handler{Static: admissionStatic(t, []string{"a.go"}, diags), Caller: caller, Budget: Budget{RequestBytes: MinRequestBytes}},
		discovery.Config{Observer: exampleObserver{t: t}})
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
	if r := results["review.triage.t001"]; r.Status != domain.TaskSucceeded {
		t.Fatalf("triage %+v", r)
	}
	if r := results["review.verify.t001"]; r.Status != domain.TaskSucceeded {
		t.Fatalf("verify %+v (request bytes %v)", r, verifyBytes)
	}
	var rec record.Record
	raw, err := os.ReadFile(recordPath(j, "verify.t001"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	named, most := 0, 0
	for _, c := range rec.Candidates {
		named, most = named+len(c.ObservationIDs), max(most, len(c.ObservationIDs))
	}
	if len(rec.Candidates) < 2 || most <= verifyObservationsShown || len(rec.Observations) != named ||
		!slices.Contains(rec.Limitations, limVerifyObservationsTrimmed) {
		t.Fatalf("verify record holds %d candidates (largest %d observations), %d observations, limitations %v", len(rec.Candidates), most, len(rec.Observations), rec.Limitations)
	}
}
