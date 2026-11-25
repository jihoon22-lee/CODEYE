package review

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// The review is about shipped code (docs/REVIEW.md#검토-대상은-배포-코드다): a
// request names the class of a path that is not shipped code, a candidate
// whose evidence all lies on such paths is not sent to verification and is
// listed with the items of secondary paths as not verified (never an action
// item or a pending candidate), and one that also cites shipped code is untouched.
func TestCandidatesAboutNonShippedPathsAreNotVerified(t *testing.T) {
	j := coverageJob(t)
	script := coverageScript(t)
	var classes []string
	var sentCandidates []string
	script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		var test record.Excerpt
		for _, e := range in.Scope.Excerpts {
			if record.PathClass(e.Path) != "" && test.ID == "" {
				test = e
				// The request itself names the class (decoding drops it).
				if strings.Contains(string(q.Input), `"path_class":"`+record.PathClass(e.Path)+`"`) {
					classes = addLim(classes, record.PathClass(e.Path))
				}
			}
		}
		shipped := shippedExcerpt(in.Scope.Excerpts)
		cand := func(title string, ids ...string) map[string]any {
			return map[string]any{"title": title, "condition": "주소가 비어 있는 경우", "mechanism": "빈 주소가 검사 없이 연결에 쓰인다.", "impact": "서비스가 잘못된 주소로 시작한다.",
				"improvement": "주소를 검사한다.", "counterpoint": "기본값이 항상 채워질 수 있다.", "evidence_ids": ids}
		}
		list := []map[string]any{cand("설정 주소 검증 누락", shipped.ID)}
		if test.ID != "" {
			// One about the test code alone, one that cites it beside shipped code.
			list = append(list, cand("시험 코드의 단언 누락", test.ID), cand("시험이 보여 주는 기대 동작과 구현의 차이", shipped.ID, test.ID))
		}
		return asJSON(map[string]any{"candidates": list})
	}
	script["verify"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[verifyInput](t, q)
		var out []any
		for _, c := range in.Candidates {
			sentCandidates = append(sentCandidates, c.Title)
			out = append(out, inconclusiveFor(c, c.Evidence[0]))
		}
		return asJSON(map[string]any{"verifications": out})
	}
	r, results := reportAt(t, &j, sliceExampleStatic(t), &scriptedCaller{script: script}, smallBudget, &exampleIDs{n: 0xbd0})
	if !slices.Contains(classes, record.PathTest) {
		t.Fatalf("no request excerpt names a test path: %v", classes)
	}
	for _, title := range sentCandidates {
		if strings.Contains(title, "시험 코드의 단언 누락") {
			t.Fatalf("the candidate about test code alone was sent to verification: %v", sentCandidates)
		}
	}
	if !slices.ContainsFunc(sentCandidates, func(s string) bool { return strings.Contains(s, "시험이 보여 주는 기대 동작") }) {
		t.Fatalf("the candidate that cites shipped code too was not sent: %v", sentCandidates)
	}
	for key, res := range results {
		if strings.HasPrefix(key, "review.verify") && res.Status == domain.TaskFailed {
			t.Fatalf("%s %+v", key, res)
		}
	}
	requireListedNotVerified(t, r, "시험 코드의 단언 누락")
	notSent := r.CodeReview.CandidatesNotShipped
	if notSent == 0 || r.Overview == nil {
		t.Fatalf("not counted: %d", notSent)
	}
	n := 0
	for _, l := range r.Overview.Sections {
		if l.Key == report.SectionCodeReview {
			n = l.Counts["candidates_not_shipped"]
		}
	}
	if n != notSent {
		t.Fatalf("overview counts %d, section %d", n, notSent)
	}
	if !slices.Contains(r.Limitations, record.LimCandidateNotShipped) {
		t.Fatalf("limitations %v", r.Limitations)
	}
}

// A request carries the path class of an excerpt on a path that is not
// shipped code; a stored excerpt never does, and a request reads back as the
// worker held it.
func TestPathClassIsOnlyInRequests(t *testing.T) {
	test := record.Excerpt{ID: "ev_" + strings.Repeat("a", 24), Path: "pkg/a_test.go", Start: 1, End: 1, Kind: "test", Text: "x"}
	shipped := record.Excerpt{ID: "ev_" + strings.Repeat("b", 24), Path: "pkg/a.go", Start: 1, End: 1, Kind: "code", Text: "x"}
	b, err := json.Marshal([]record.Excerpt{test, shipped})
	if err != nil || !strings.Contains(string(b), `"path_class":"test"`) || strings.Count(string(b), "path_class") != 1 {
		t.Fatalf("%s %v", b, err)
	}
	var back []record.Excerpt
	if err := json.Unmarshal(b, &back); err != nil || back[0].PathClass != "" || back[0].Text != "x" {
		t.Fatalf("%+v %v", back, err)
	}
	test.Text = ""
	if b, _ := json.Marshal(test); strings.Contains(string(b), "path_class") {
		t.Fatalf("a stored excerpt names a path class: %s", b)
	}
}

// When every candidate of a request is about non-shipped code, nothing is sent
// to verification: the stage ends without a call and without failing.
func TestNothingToVerifyWhenAllCandidatesAreAboutNonShippedPaths(t *testing.T) {
	j := coverageJob(t)
	script := coverageScript(t)
	investigations, withCandidates := 0, 0
	script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		investigations++
		for _, e := range in.Scope.Excerpts {
			if record.PathClass(e.Path) != "" {
				withCandidates++
				return asJSON(map[string]any{"candidates": []map[string]any{{"title": "시험 코드의 단언 누락", "condition": "주소가 비어 있는 경우", "mechanism": "단언이 없다.", "impact": "시험이 통과한다.",
					"improvement": "단언을 더한다.", "counterpoint": "다른 시험이 덮을 수 있다.", "evidence_ids": []string{e.ID}}}})
			}
		}
		return asJSON(map[string]any{"candidates": []any{}})
	}
	caller := &scriptedCaller{script: script}
	r, results := reportAt(t, &j, sliceExampleStatic(t), caller, smallBudget, &exampleIDs{n: 0xbf0})
	if withCandidates == 0 || caller.calls("verify") != 0 {
		t.Fatalf("%d investigations, %d with a candidate about a test path, %d verify calls", investigations, withCandidates, caller.calls("verify"))
	}
	for key, res := range results {
		if strings.HasPrefix(key, "review.verify") && res.Status == domain.TaskFailed {
			t.Fatalf("%s %+v", key, res)
		}
	}
	if r.CodeReview.CandidatesNotShipped != withCandidates {
		t.Fatalf("counted %d of %d", r.CodeReview.CandidatesNotShipped, withCandidates)
	}
	// Setting candidates aside is not a failure or a doubt: the run's status
	// and verdict are those of a run whose model raised no candidate at all.
	none := coverageScript(t)
	none["investigate"] = func(int, opencode.Request) opencode.Reply { return asJSON(map[string]any{"candidates": []any{}}) }
	baselineJob := coverageJob(t)
	baseline, _ := reportAt(t, &baselineJob, sliceExampleStatic(t), &scriptedCaller{script: none}, smallBudget, &exampleIDs{n: 0xbf8})
	if r.Status != baseline.Status || r.QualityVerdict != baseline.QualityVerdict || r.CodeReview.CandidatesNotShipped == 0 || baseline.CodeReview.CandidatesNotShipped != 0 {
		t.Fatalf("status %s/%s against %s/%s with no candidate", r.Status, r.QualityVerdict, baseline.Status, baseline.QualityVerdict)
	}
	if n := requireListedNotVerified(t, r, "시험 코드의 단언 누락"); n != withCandidates {
		t.Fatalf("%d listed of %d", n, withCandidates)
	}
	if r.Summary.PendingCandidates != baseline.Summary.PendingCandidates || len(r.Summary.Priorities) != 0 {
		t.Fatalf("summary %+v against %+v", r.Summary, baseline.Summary)
	}
}

// The dry-run upstream (scripts/fake_model_upstream.py) raises a candidate
// about the first excerpt on a path that is not shipped code beside its
// candidate about shipped code: the product leaves the first out of the
// verification and the findings and counts it, whatever the shape errors the
// upstream injects.
func TestDryRunUpstreamRaisesACandidateAboutATestPath(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	for _, x := range []struct {
		name             string
		shape, integrity float64
	}{{"plain", 0, 0}, {"form", 1, 0}} {
		t.Run(x.name, func(t *testing.T) {
			j := coverageJob(t)
			script := coverageScript(t)
			for _, role := range []string{record.StageInvestigate, record.StageVerify} {
				script[role] = func(_ int, q opencode.Request) opencode.Reply {
					return dryRunUpstream(t, python, role, q, x.shape, x.integrity)
				}
			}
			caller := &scriptedCaller{script: script}
			r, _ := reportAt(t, &j, sliceExampleStatic(t), caller, smallBudget, &exampleIDs{n: 0xc10})
			if r.CodeReview.CandidatesNotShipped == 0 {
				t.Fatalf("no candidate about a test path was left out: %+v", r.CodeReview)
			}
			requireListedNotVerified(t, r, "about a test path")
		})
	}
}

// requireListedNotVerified checks the findings whose title holds match: each is
// the unreviewed candidate with the fixed limitation, none is an action item, a
// pending candidate or a priority, and the report lists them in the fold of
// secondary-path items with the fixed label (Markdown and the code-free
// export too). It returns how many there are.
func requireListedNotVerified(t *testing.T, r report.Report, match string) int {
	t.Helper()
	n := 0
	for _, f := range r.Findings {
		if !strings.Contains(f.Title, match) {
			continue
		}
		n++
		if !report.FindingNotShipped(f) || f.Validation != record.VerdictUnreviewed || slices.Contains(r.Summary.Priorities, f.ID) {
			t.Fatalf("candidate %s: %+v", f.ID, f)
		}
	}
	if n == 0 || r.CodeReview.CandidatesNotShipped < n {
		t.Fatalf("%d listed, section counts %d", n, r.CodeReview.CandidatesNotShipped)
	}
	raw, err := report.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range []report.Report{r, report.Project(r, false)} {
		md := string(report.RenderMarkdownAt(view, report.Digest(raw), report.Korean, report.DetailFull))
		if !strings.Contains(md, report.Korean.Label("actions.not_shipped_label")) ||
			!strings.Contains(md, fmt.Sprintf(report.Korean.Label("actions.secondary_not_shipped"), r.CodeReview.CandidatesNotShipped)) {
			t.Fatalf("the fold of secondary-path items does not list the candidates as not verified (code-free %v)", !view.Export.IncludeCode)
		}
		page, err := report.RenderHTMLAt(view, report.Digest(raw), report.Korean, report.DetailFull)
		if err != nil || !strings.Contains(string(page), report.Korean.Label("actions.not_shipped_label")) ||
			!strings.Contains(string(page), fmt.Sprintf(report.Korean.Label("actions.secondary_not_shipped"), r.CodeReview.CandidatesNotShipped)) {
			t.Fatalf("the HTML does not list the candidates as not verified (code-free %v): %v", !view.Export.IncludeCode, err)
		}
	}
	return n
}

// The decision never depends on how the verification ended: when every verify
// call fails, the report still derives it from the candidates' evidence, lists
// the test-only candidates as not verified (never as ordinary findings) and
// counts them; the failed verify record keeps the marker.
func TestCandidatesAboutNonShippedPathsAreSetAsideWhenVerifyFails(t *testing.T) {
	j := coverageJob(t)
	script := coverageScript(t)
	script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		shipped := shippedExcerpt(in.Scope.Excerpts)
		cand := func(title, id string) map[string]any {
			return map[string]any{"title": title, "condition": "주소가 비어 있는 경우", "mechanism": "빈 주소가 검사 없이 연결에 쓰인다.", "impact": "서비스가 잘못된 주소로 시작한다.",
				"improvement": "주소를 검사한다.", "counterpoint": "기본값이 항상 채워질 수 있다.", "evidence_ids": []string{id}}
		}
		list := []map[string]any{cand("설정 주소 검증 누락", shipped.ID)}
		for _, e := range in.Scope.Excerpts {
			if record.PathClass(e.Path) != "" {
				list = append(list, cand("시험 코드의 단언 누락", e.ID))
				break
			}
		}
		return asJSON(map[string]any{"candidates": list})
	}
	script["verify"] = func(int, opencode.Request) opencode.Reply { return opencode.Reply{Reason: "model_unavailable"} }
	r, results := reportAt(t, &j, sliceExampleStatic(t), &scriptedCaller{script: script}, smallBudget, &exampleIDs{n: 0xc40})
	failed := 0
	for key, res := range results {
		if strings.HasPrefix(key, "review.verify.s") && res.Status == domain.TaskFailed {
			failed++
		}
	}
	if failed == 0 {
		t.Fatal("no slice verify failed")
	}
	if n := requireListedNotVerified(t, r, "시험 코드의 단언 누락"); n == 0 || r.CodeReview.CandidatesNotShipped != n {
		t.Fatalf("%d listed, section counts %d", n, r.CodeReview.CandidatesNotShipped)
	}
	for _, f := range r.Findings {
		if strings.Contains(f.Title, "설정 주소 검증 누락") && report.FindingNotShipped(f) {
			t.Fatalf("a candidate that cites shipped code is set aside: %s", f.ID)
		}
	}
	for key, res := range results {
		stage, ok := strings.CutPrefix(key, "review.")
		if !ok || !strings.HasPrefix(stage, "verify.s") || res.Status != domain.TaskFailed {
			continue
		}
		rec := readSliceRecord(t, j, stage)
		for _, v := range rec.Verifications {
			c := rec.Candidates[slices.IndexFunc(rec.Candidates, func(c record.Candidate) bool { return c.ID == v.CandidateID })]
			if got, want := slices.Equal(v.Limitations, []string{record.LimCandidateNotShipped}), c.OnlyNotShipped(catalogOf(rec.Evidence)); got != want {
				t.Fatalf("%s %s: marker %v, decision %v", key, v.CandidateID, got, want)
			}
		}
	}
}

func readSliceRecord(t *testing.T, j analysis.Job, name string) record.Record {
	t.Helper()
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

// The marker is tied to the evidence: a record that marks a candidate of
// shipped code, or leaves an unmarked candidate whose evidence is all on other
// paths, is not a valid record, and the report rejects findings marked or
// unmarked against their places.
func TestForgedNotShippedMarkerIsRejected(t *testing.T) {
	j := coverageJob(t)
	script := coverageScript(t)
	script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		shipped := shippedExcerpt(in.Scope.Excerpts)
		cand := func(title, id string) map[string]any {
			return map[string]any{"title": title, "condition": "주소가 비어 있는 경우", "mechanism": "빈 주소가 검사 없이 연결에 쓰인다.", "impact": "서비스가 잘못된 주소로 시작한다.",
				"improvement": "주소를 검사한다.", "counterpoint": "기본값이 항상 채워질 수 있다.", "evidence_ids": []string{id}}
		}
		list := []map[string]any{cand("설정 주소 검증 누락", shipped.ID)}
		for _, e := range in.Scope.Excerpts {
			if record.PathClass(e.Path) != "" {
				list = append(list, cand("시험 코드의 단언 누락", e.ID))
				break
			}
		}
		return asJSON(map[string]any{"candidates": list})
	}
	r, results := reportAt(t, &j, sliceExampleStatic(t), &scriptedCaller{script: script}, smallBudget, &exampleIDs{n: 0xc50})
	forged := false
	for key, res := range results {
		stage, ok := strings.CutPrefix(key, "review.")
		if !ok || !strings.HasPrefix(stage, "verify.s") || res.Status != domain.TaskSucceeded {
			continue
		}
		rec := readSliceRecord(t, j, stage)
		marked := record.NotShippedSet(rec.Verifications)
		if len(marked) == 0 || len(marked) == len(rec.Candidates) {
			continue
		}
		forged = true
		for i, v := range rec.Verifications {
			bad := rec
			bad.Verifications = slices.Clone(rec.Verifications)
			if marked[v.CandidateID] {
				// The marker of a test-only candidate is dropped (and the verdict
				// made up): the evidence decides, not the record.
				bad.Verifications[i] = inconclusiveVerification(v.CandidateID, rec.Candidates[i].Evidence[0])
			} else {
				bad.Verifications[i] = unreviewed(v.CandidateID, record.LimCandidateNotShipped)
			}
			if _, err := record.Marshal(bad, expectRecord(j, stage)); err == nil {
				t.Fatalf("%s: a forged marker on %s was accepted", key, v.CandidateID)
			}
		}
		break
	}
	if !forged {
		t.Fatal("no slice verify record with both kinds of candidate")
	}
	// The report: a shipped-code finding marked as not sent, and a marked one unmarked.
	for i, f := range r.Findings {
		if !investigateFindingIDForTest(f.ID) {
			continue
		}
		bad := r
		bad.Findings = slices.Clone(r.Findings)
		if report.FindingNotShipped(f) {
			bad.Findings[i].Limitations = nil
		} else {
			bad.Findings[i].Validation = report.ValidationUnreviewed
			bad.Findings[i].Limitations = []string{record.LimCandidateNotShipped}
		}
		if err := report.ValidateReport(bad); err == nil {
			t.Fatalf("a forged marker on finding %s was accepted", f.ID)
		}
	}
}

func investigateFindingIDForTest(id string) bool {
	return strings.Contains(id, "_investigate_")
}

func inconclusiveVerification(id, evidence string) record.Verification {
	return record.Verification{CandidateID: id, Verdict: record.VerdictInconclusive, Category: "error_handling", Severity: "low", SeverityReason: "근거가 부족하다.", Impact: "영향을 확인하지 못했다.",
		ValidConditions: []string{}, InvalidConditions: []string{}, SupportingEvidence: []string{evidence}, Counterevidence: []string{}, UnresolvedAssumptions: []string{"호출자를 확인하지 못했다."},
		NextVerification: []string{"호출자를 읽는다."}, Limitations: []string{}}
}

// A scope without shipped code (a pull request that changes only tests, an
// examples-only repository, a scope inside the tests) has that code as its
// subject: the requests say so and name it, nothing is set aside, the
// candidates are verified and the report lists them as ordinary findings; the
// records and the report reject a marker that scope cannot have. A scope with
// shipped code gets requests without that flag.
func TestScopeWithoutShippedCodeIsTheSubject(t *testing.T) {
	run := func(t *testing.T, scope domain.Scope) (report.Report, *scriptedCaller, analysis.Job) {
		j := coverageJob(t)
		j.Scope = scope
		script := coverageScript(t)
		script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
			in := decodeAs[investigateInput](t, q)
			e := in.Scope.Excerpts[0]
			return asJSON(map[string]any{"candidates": []map[string]any{{"title": "시험 코드의 단언 누락", "condition": "주소가 비어 있는 경우", "mechanism": "단언이 없다.", "impact": "시험이 통과한다.",
				"improvement": "단언을 더한다.", "counterpoint": "다른 시험이 덮을 수 있다.", "evidence_ids": []string{e.ID}}}})
		}
		caller := &scriptedCaller{script: script}
		r, _ := reportAt(t, &j, sliceExampleStatic(t), caller, smallBudget, &exampleIDs{n: 0xd00})
		return r, caller, j
	}
	flagged := func(c *scriptedCaller, role string) (with, without int) {
		for _, q := range c.requests {
			if q.Role != role {
				continue
			}
			if strings.Contains(string(q.Input), `"subject":"non_shipped"`) && strings.Contains(q.Instructions, "input.scope.subject is non_shipped") {
				with++
			} else if !strings.Contains(string(q.Input), `"subject"`) && !strings.Contains(q.Instructions, "input.scope.subject") {
				without++
			}
		}
		return
	}
	for name, scope := range map[string]domain.Scope{
		"tests only":    {Kind: "paths", Paths: []string{"api/handler_test.go", "web/src/client.test.ts"}},
		"examples only": {Kind: "paths", Paths: []string{"worker/tests"}},
	} {
		t.Run(name, func(t *testing.T) {
			r, caller, _ := run(t, scope)
			if with, without := flagged(caller, "investigate"); with == 0 || without != 0 {
				t.Fatalf("investigate requests: %d flagged, %d plain", with, without)
			}
			if with, without := flagged(caller, "verify"); with == 0 || without != 0 {
				t.Fatalf("verify requests: %d flagged, %d plain", with, without)
			}
			if r.CodeReview.Subject != record.SubjectNonShipped || r.CodeReview.CandidatesNotShipped != 0 {
				t.Fatalf("section %+v", r.CodeReview)
			}
			for _, f := range r.Findings {
				if report.FindingNotShipped(f) {
					t.Fatalf("a candidate is set aside: %s", f.ID)
				}
			}
			// A marker that this scope cannot have is rejected.
			for i, f := range r.Findings {
				if !investigateFindingIDForTest(f.ID) {
					continue
				}
				bad := r
				bad.Findings = slices.Clone(r.Findings)
				bad.Findings[i].Validation, bad.Findings[i].Limitations = report.ValidationUnreviewed, []string{record.LimCandidateNotShipped}
				if err := report.ValidateReport(bad); err == nil {
					t.Fatalf("a marker on %s was accepted in a scope without shipped code", f.ID)
				}
				break
			}
		})
	}
	t.Run("scope with shipped code", func(t *testing.T) {
		r, caller, _ := run(t, domain.Scope{Kind: "full"})
		for _, q := range caller.requests {
			if strings.Contains(string(q.Input), `"subject"`) || strings.Contains(q.Instructions, "input.scope.subject") {
				t.Fatalf("a request of an ordinary scope carries the flag (role %s)", q.Role)
			}
		}
		if r.CodeReview.Subject != "" {
			t.Fatalf("subject %q", r.CodeReview.Subject)
		}
	})
}

// shippedSourceOf follows the pull request's changed files.
func TestShippedSourceOfAPullRequestJob(t *testing.T) {
	tree := map[string]string{"go.mod": "module x\n", "a.go": "package x\n", "a_test.go": "package x\n"}
	for name, c := range map[string]struct {
		changed []string
		want    bool
	}{"tests only": {[]string{"a_test.go"}, false}, "shipped and tests": {[]string{"a.go", "a_test.go"}, true}} {
		j := treeJob(t, tree)
		j.PullRequest = &domain.PullRequestJob{Number: 7, Changed: c.changed}
		if got := shippedSourceOf(j); got != c.want {
			t.Errorf("%s: %v", name, got)
		}
	}
	if !shippedSourceOf(treeJob(t, tree)) {
		t.Error("an ordinary run of this tree has shipped code")
	}
}
