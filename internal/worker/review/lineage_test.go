package review

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/staticcheck"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// Evidence lineage (record.CarriesUpstreamEvidence). An evidence ID is
// bound to a snapshot location, so a range the verifier derives can be
// exactly an excerpt an earlier role recorded under another origin. These
// tests run the whole review and build the report: the verify record must
// carry the upstream entry unchanged, or the report sets it aside
// (review_lineage_mismatch) and the required review does not count.

// reportAt runs every planned task at budget b and builds the report; j
// is pinned to the example snapshot.
func reportAt(t *testing.T, j *analysis.Job, static analysis.Handler, caller *scriptedCaller, b Budget, ids *exampleIDs) (report.Report, map[string]analysis.TaskResult) {
	t.Helper()
	pinExampleSnapshot(t, j)
	h, err := discovery.Wrap(Handler{Static: static, Caller: caller, MaxCalls: record.DefaultMaxCalls, Budget: b}, discovery.Config{Observer: exampleObserver{t: t}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := h.Plan(t.Context(), *j)
	if err != nil {
		t.Fatal(err)
	}
	results := map[string]analysis.TaskResult{}
	for _, task := range plan {
		results[task.Key] = h.Run(t.Context(), *j, task, nil)
	}
	r, err := report.Build(exampleInput(t, *j, h, plan, results, ids), report.DefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	return r, results
}

// requireReviewCounted fails unless the report kept every review record
// (and the artifacts of tasks with the given key prefixes) and the required
// review was performed.
func requireReviewCounted(t *testing.T, r report.Report, tasks ...string) {
	t.Helper()
	for _, l := range r.Limitations {
		task, rejected := strings.CutPrefix(l, "artifact_rejected:")
		if strings.HasPrefix(l, report.LimReviewLineage) || (rejected && slices.ContainsFunc(tasks, func(p string) bool { return strings.HasPrefix(task, p) })) {
			t.Fatalf("report set aside an artifact: %s in %v (ai_review %s)", l, r.Limitations, r.Coverage.AIReview.Status)
		}
	}
	if r.Coverage.AIReview.Status != report.AIReviewPerformed {
		t.Fatalf("ai_review %s %v", r.Coverage.AIReview.Status, r.Coverage.AIReview.Reason)
	}
}

// requireUpstreamEntries fails unless every excerpt of the named record
// (a stage, or stage.sNNN for a slice) that shares an ID with one of the
// upstream records is that entry.
func requireUpstreamEntries(t *testing.T, j analysis.Job, name string, upstream ...string) record.Record {
	t.Helper()
	read := func(name string) record.Record {
		b, err := os.ReadFile(recordPath(j, name))
		if err != nil {
			t.Fatal(err)
		}
		r, err := record.Decode(b, expectRecord(j, name))
		if err != nil {
			t.Fatal(name, err)
		}
		return r
	}
	v := read(name)
	var priors []record.Record
	for _, s := range upstream {
		priors = append(priors, read(s))
	}
	if !record.CarriesUpstreamEvidence(v, priors...) {
		t.Fatalf("%s evidence %+v changes an upstream entry", name, v.Evidence)
	}
	return v
}

// longGoFile is a Go file of one function after its package clause, n
// lines in all: at the default budget it is one unit, the whole file.
func longGoFile(n int) string { return goFileOf(n, "\tn += %d\n") }

// goFileOf is longGoFile with each body line formatted from its number.
func goFileOf(n int, line string) string {
	var b strings.Builder
	b.WriteString("package billing\n\nfunc Adjust(n int) int {\n")
	for i := 4; i < n-1; i++ {
		fmt.Fprintf(&b, line, i)
	}
	b.WriteString("\treturn n\n}\n")
	return b.String()
}

func billingCandidate(title string, evidence ...string) record.CandidateText {
	return record.CandidateText{Title: title, Condition: "큰 수량이 들어온다.", Mechanism: "더하기가 계속 누적된다.", Impact: "결과가 넘칠 수 있다.",
		Improvement: "상한을 확인한다.", Counterpoint: "호출자가 수량을 제한할 수 있다.", Evidence: evidence}
}

// answerAll answers every candidate of the request as inconclusive,
// citing cite(candidate).
func answerAll(in verifyInput, cite func(record.Candidate) []string) opencode.Reply {
	var out []any
	for _, c := range in.Candidates {
		out = append(out, verdict(c.ID, "inconclusive", func(v map[string]any) {
			v["supporting_evidence_ids"] = cite(c)
			v["unresolved_assumptions"] = []string{"호출자가 수량을 제한하는지 확인하지 못했다."}
		}))
	}
	return asJSON(map[string]any{"verifications": out})
}

// The investigator cites the first 80 lines of a 103-line file of one
// function: the declaration enclosing that range runs from the package
// clause to the end, which is exactly the unit investigate sent. Verify
// sends and records it as the unit (origin context), not as a declaration
// twin of the same ID.
func TestVerifyDeclarationThatIsAnUpstreamUnit(t *testing.T) {
	j := treeJob(t, map[string]string{"go.mod": "module example.org/billing\n\ngo 1.23\n", "quota.go": longGoFile(103)})
	script := coverageScript(t)
	var unit record.Excerpt
	script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		i := slices.IndexFunc(in.Scope.Excerpts, func(e record.Excerpt) bool { return e.Path == "quota.go" })
		if i < 0 || in.Scope.Excerpts[i].Start != 1 || in.Scope.Excerpts[i].End != 103 {
			t.Fatalf("quota.go not sent as one unit: %+v", in.Scope.Excerpts)
		}
		unit = in.Scope.Excerpts[i]
		return asJSON(map[string]any{"candidates": []record.CandidateText{billingCandidate("누적 보정", unit.ID+":1-80")}})
	}
	script["verify"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[verifyInput](t, q)
		for _, e := range in.Scope.Excerpts {
			if e.ID == unit.ID && e.Origin != unit.Origin {
				t.Errorf("the unit travels with origin %q, upstream %q", e.Origin, unit.Origin)
			}
		}
		return answerAll(in, func(c record.Candidate) []string { return []string{c.Evidence[0], unit.ID + ":10-30"} })
	}
	r, res := reportAt(t, &j, secretsStub{}, &scriptedCaller{script: script}, Budget{}, &exampleIDs{n: 0x901})
	if s := res["review.verify"]; s.Status != "succeeded" {
		t.Fatalf("verify %+v", s)
	}
	requireReviewCounted(t, r)
	v := requireUpstreamEntries(t, j, record.StageVerify, record.StageInvestigate)
	if !slices.ContainsFunc(v.Evidence, func(e record.Excerpt) bool { return e.ID == unit.ID && e.Origin == "context" }) {
		t.Fatalf("the unit is not recorded as the upstream entry: %+v", v.Evidence)
	}
}

// staticFinding is a static composite of one staticcheck finding at
// path:line of the root module.
func staticFinding(t *testing.T, path string, line int) *pack.Composite {
	t.Helper()
	sc := staticcheck.Descriptor()
	rp := obsdoc.RulePack{ID: "codeye-go-staticcheck", Version: "2", Selection: []string{"-checks", "SA*,U1000", "-tests=true", "-show-ignored"}, RepositoryConfig: obsdoc.RepoConfigOverridden,
		SourceSuppressions: obsdoc.SuppressReported, ConfigFiles: []obsdoc.ConfigFile{}, UnreportedSuppressionRules: []string{}}
	c, err := pack.NewComposite(exampleMember{d: sc, plan: staticcheck.Plan, run: func(j analysis.Job, task pack.Task) analysis.TaskResult {
		return exampleEnvelope(t, j, sc, task, "2026.2.2", rp, []string{path}, []string{"default_build_context_only"},
			[]exampleDiag{{path: path, line: line, rule: "SA4006", category: "correctness", message: "this value of n is never used (synthetic example)"}})
	}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// citeLines is n two-line citations of id from line from on.
func citeLines(id string, from, n int) []string {
	var out []string
	for k := range n {
		out = append(out, fmt.Sprintf("%s:%d-%d", id, from+k, from+k+1))
	}
	return out
}

// The interpreter's observation window lies inside the investigated unit.
// The candidates cite enough ranges (36) that the verify request, at most
// 40 excerpts at this budget, keeps the cited ranges, the declaration, the
// unit and the two other files but not that window, the last one; the
// verifier then cites exactly the window's lines inside the unit. The
// narrowed range has the window's ID and is recorded as the interpreter's
// entry (origin observation), not as a cited twin.
func TestVerifyNarrowedToAnUpstreamWindowItDidNotResend(t *testing.T) {
	j := treeJob(t, map[string]string{"go.mod": "module example.org/billing\n\ngo 1.23\n", "a.go": longGoFile(103),
		"b.go": "package billing\n\nfunc B() int { return 1 }\n", "c.go": "package billing\n\nfunc C() int { return 2 }\n"})
	script := coverageScript(t)
	var unit, window record.Excerpt
	script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		unit = in.Scope.Excerpts[slices.IndexFunc(in.Scope.Excerpts, func(e record.Excerpt) bool { return e.Path == "a.go" })]
		var cs []record.CandidateText
		for i := range 4 {
			cs = append(cs, billingCandidate(fmt.Sprintf("누적 보정 %d", i+1), citeLines(unit.ID, 40+6*i, 6)...))
		}
		return asJSON(map[string]any{"candidates": cs})
	}
	script["interpret"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[interpretInput](t, q)
		window = in.Scope.Excerpts[slices.IndexFunc(in.Scope.Excerpts, func(e record.Excerpt) bool { return e.Path == "a.go" })]
		o := in.Observations[0]
		var hs []map[string]any
		for i := range 2 {
			hs = append(hs, map[string]any{"observation_ids": []string{o.ID}, "title": fmt.Sprintf("값이 버려질 수 있음 %d", i+1), "condition": "보정이 반복되는 경우",
				"mechanism": "앞의 값이 덮어써진다.", "impact": "보정이 빠질 수 있다.", "improvement": "값을 쓴다.", "counterpoint": "의도일 수 있다.",
				"evidence_ids": citeLines(window.ID, window.Start+6*i, 6)})
		}
		return asJSON(map[string]any{"hypotheses": hs, "limits": []map[string]any{}})
	}
	script["verify"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[verifyInput](t, q)
		if slices.ContainsFunc(in.Scope.Excerpts, func(e record.Excerpt) bool { return e.ID == window.ID }) {
			t.Fatalf("the window was resent; the case needs it trimmed: %d excerpts", len(in.Scope.Excerpts))
		}
		if !slices.ContainsFunc(in.Scope.Excerpts, func(e record.Excerpt) bool { return e.ID == unit.ID }) {
			t.Fatal("the unit was not resent")
		}
		at := fmt.Sprintf("%s:%d-%d", unit.ID, window.Start, window.End)
		return answerAll(in, func(c record.Candidate) []string { return []string{c.Evidence[0], at} })
	}
	r, res := reportAt(t, &j, staticFinding(t, "a.go", 13), &scriptedCaller{script: script}, Budget{RequestBytes: MinRequestBytes}, &exampleIDs{n: 0x902})
	for _, k := range []string{"review.investigate", "review.interpret", "review.verify"} {
		if s := res[k]; s.Status != "succeeded" {
			t.Fatalf("%s %+v (report limitations %v)", k, s, r.Limitations)
		}
	}
	requireReviewCounted(t, r, "staticcheck")
	v := requireUpstreamEntries(t, j, record.StageVerify, record.StageInvestigate, record.StageInterpret)
	if !slices.ContainsFunc(v.Evidence, func(e record.Excerpt) bool { return e.ID == window.ID && e.Origin == window.Origin }) {
		t.Fatalf("the window is not recorded as the interpreter's entry: %+v", v.Evidence)
	}
}

// The citer resolves a narrowed range whose ID is an excerpt bound to an
// earlier record (known) to that entry, not to a cited twin.
func TestCiterResolvesKnownRangeToUpstreamEntry(t *testing.T) {
	snapshot, sha := strings.Repeat("5", 64), strings.Repeat("a", 64)
	var b strings.Builder
	for i := 1; i <= 120; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	unit := record.Excerpt{ID: record.ExcerptID(snapshot, "a.go", sha, 1, 120), Path: "a.go", SHA256: sha, Start: 1, End: 120, Kind: "code",
		Origin: "context", Text: strings.TrimSuffix(b.String(), "\n")}
	window := record.Excerpt{ID: record.ExcerptID(snapshot, "a.go", sha, 10, 20), Path: "a.go", SHA256: sha, Start: 10, End: 20, Kind: "code",
		Origin: "observation", Symbol: "Adjust"}
	c := newCiter(snapshot, []record.Excerpt{unit})
	c.known = map[string]record.Excerpt{window.ID: window}
	ids, _, errs := c.resolve([]string{unit.ID + ":10-20", unit.ID + ":30-40"}, citeNarrow)
	if len(errs) != 0 || ids[0] != window.ID {
		t.Fatalf("ids %v errs %v", ids, errs)
	}
	used := c.used(ids)
	if len(used) != 2 || used[0] != window || used[1].Origin != record.OriginCited {
		t.Fatalf("used %+v", used)
	}
}

// A strength cites lines inside the unit of a long file no candidate
// cites. Verify does not resend that unit, so it does not send the
// strength's range alone either: a recorded cited range needs the excerpt
// it lies in beside it (record checkCitedRanges).
func TestVerifyLeavesAStrengthRangeWithoutItsUnit(t *testing.T) {
	j := treeJob(t, map[string]string{"go.mod": "module example.org/billing\n\ngo 1.23\n", "a.go": longGoFile(40), "b.go": goFileOf(154, "\tn = clamp(n + %d)\n")})
	script := coverageScript(t)
	var a, b record.Excerpt
	script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		for _, e := range in.Scope.Excerpts {
			switch e.Path {
			case "a.go":
				a = e
			case "b.go":
				b = e
			}
		}
		if b.End-b.Start+1 <= record.MaxCitedLines {
			t.Fatalf("b.go not sent as one long unit: %+v", b)
		}
		return asJSON(map[string]any{"candidates": []record.CandidateText{billingCandidate("누적 보정", a.ID+":5-13")},
			"strengths": []record.Strength{{Title: "단일 보정 함수", Benefit: "보정이 한 곳에 모인다.", Scope: ".", EvidenceIDs: []string{b.ID + ":5-13"}}}})
	}
	script["verify"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[verifyInput](t, q)
		for _, e := range in.Scope.Excerpts {
			if e.Path == "b.go" {
				t.Errorf("verify sent %s %d-%d (%s) of a file no candidate cites", e.Path, e.Start, e.End, e.Origin)
			}
		}
		return answerAll(in, func(c record.Candidate) []string { return []string{c.Evidence[0]} })
	}
	r, res := reportAt(t, &j, secretsStub{}, &scriptedCaller{script: script}, Budget{}, &exampleIDs{n: 0x903})
	if s := res["review.verify"]; s.Status != "succeeded" {
		t.Fatalf("verify %+v", s)
	}
	requireReviewCounted(t, r)
	requireUpstreamEntries(t, j, record.StageVerify, record.StageInvestigate)
	if inv := readRecord(t, j, record.StageInvestigate); len(inv.Strengths) != 1 {
		t.Fatalf("the strength was not kept: %v", inv.Limitations)
	}
}

// twoFunctionFile is a Go file of two functions of about 1,500 bytes each:
// at the minimum budget (2,400-byte units) each function is its own unit,
// sent with the file header.
func twoFunctionFile() string {
	var b strings.Builder
	b.WriteString("package billing\n")
	for _, name := range []string{"Charge", "Refund"} {
		fmt.Fprintf(&b, "\nfunc %s(n int) int {\n", name)
		for i := range 34 {
			fmt.Fprintf(&b, "\tn = clamp(n + %d) // step %d\n", i, i)
		}
		b.WriteString("\treturn n\n}\n")
	}
	return b.String()
}

// A slice sends each function of a file as one unit. The verifier of the
// slice gets the declaration enclosing a cited range, which is exactly
// that unit; it travels and is recorded as the slice investigator's entry.
func TestSliceVerifyDeclarationThatIsAUnit(t *testing.T) {
	tree := map[string]string{"go.mod": "module example.org/billing\n\ngo 1.23\n"}
	for i := range 6 {
		tree[fmt.Sprintf("pay%d.go", i)] = twoFunctionFile()
	}
	j := treeJob(t, tree)
	script := coverageScript(t)
	script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		i := slices.IndexFunc(in.Scope.Excerpts, func(e record.Excerpt) bool { return e.Kind == "code" && e.Origin == "context" && e.End-e.Start >= 30 })
		if i < 0 {
			return asJSON(map[string]any{"candidates": []any{}})
		}
		u := in.Scope.Excerpts[i]
		return asJSON(map[string]any{"candidates": []record.CandidateText{billingCandidate("누적 보정", fmt.Sprintf("%s:%d-%d", u.ID, u.Start+3, u.Start+8))}})
	}
	units := 0
	script["verify"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[verifyInput](t, q)
		if in.Scope.Excerpts[0].Origin == record.OriginCited && in.Scope.Excerpts[1].Origin == "context" && in.Scope.Excerpts[1].Start > 1 {
			units++ // the declaration is the slice unit, sent right after the cited range
		}
		return answerAll(in, func(c record.Candidate) []string { return []string{c.Evidence[0]} })
	}
	r, res := reportAt(t, &j, secretsStub{}, &scriptedCaller{script: script}, Budget{RequestBytes: MinRequestBytes}, &exampleIDs{n: 0x904})
	verified := 0
	for key, s := range res {
		if _, slice, ok := record.SliceTask(key); ok && strings.HasPrefix(key, "review.verify.") && s.Status == "succeeded" {
			verified++
			requireUpstreamEntries(t, j, record.StageVerify+"."+slice, record.StageInvestigate+"."+slice)
		} else if ok && strings.HasPrefix(key, "review.") && s.Status != "succeeded" && s.Status != "skipped_policy" {
			t.Errorf("%s %+v", key, s)
		}
	}
	if verified == 0 || units == 0 {
		t.Fatalf("no slice verify resent a unit (%d verified)", verified)
	}
	requireReviewCounted(t, r)
}

// A strength cites exactly one function of a long file (an upstream cited
// range) and a candidate cites lines inside that function. The enclosing
// declaration of the candidate's range is the strength's range: it goes
// to the verifier as that cited entry, beside the file unit it lies in.
func TestVerifyDeclarationThatIsAStrengthRange(t *testing.T) {
	var b strings.Builder
	b.WriteString("package billing\n")
	for _, f := range []struct {
		name  string
		lines int
	}{{"Charge", 36}, {"Refund", 76}} {
		fmt.Fprintf(&b, "\nfunc %s(n int) int {\n", f.name)
		for i := range f.lines {
			fmt.Fprintf(&b, "\tn = clamp(n + %d)\n", i)
		}
		b.WriteString("\treturn n\n}\n")
	}
	j := treeJob(t, map[string]string{"go.mod": "module example.org/billing\n\ngo 1.23\n", "pay.go": b.String()})
	script := coverageScript(t)
	var unit record.Excerpt
	const charge = "3-41" // func Charge through its closing brace
	script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		unit = in.Scope.Excerpts[slices.IndexFunc(in.Scope.Excerpts, func(e record.Excerpt) bool { return e.Path == "pay.go" })]
		if unit.Start != 1 || unit.End <= record.MaxCitedLines {
			t.Fatalf("pay.go not sent as one long unit: %+v", unit)
		}
		return asJSON(map[string]any{"candidates": []record.CandidateText{billingCandidate("누적 보정", unit.ID+":10-20")},
			"strengths": []record.Strength{{Title: "작은 보정 함수", Benefit: "보정이 한 함수에 모인다.", Scope: ".", EvidenceIDs: []string{unit.ID + ":" + charge}}}})
	}
	var sent []record.Excerpt
	script["verify"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[verifyInput](t, q)
		sent = in.Scope.Excerpts
		return answerAll(in, func(c record.Candidate) []string { return []string{c.Evidence[0]} })
	}
	r, res := reportAt(t, &j, secretsStub{}, &scriptedCaller{script: script}, Budget{}, &exampleIDs{n: 0x905})
	if s := res["review.verify"]; s.Status != "succeeded" {
		t.Fatalf("verify %+v", s)
	}
	requireReviewCounted(t, r)
	inv := requireUpstreamEntries(t, j, record.StageInvestigate)
	if len(inv.Strengths) != 1 {
		t.Fatalf("the strength was not kept: %v", inv.Limitations)
	}
	requireUpstreamEntries(t, j, record.StageVerify, record.StageInvestigate)
	if len(sent) < 3 || sent[1].ID != inv.Strengths[0].EvidenceIDs[0] || sent[1].Origin != record.OriginCited || sent[2].ID != unit.ID {
		t.Fatalf("verify request %+v", sent)
	}
}
