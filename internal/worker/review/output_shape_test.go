package review

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// Faults of form after the format repair (docs/REVIEW.md#출력-오류-분류):
// one repair names the item, field and excerpt; a second answer with the
// same fault is resolved in the item or the item is left out, never the
// stage. Integrity faults keep rejecting.

// A citation without a usable range on a long excerpt stands for the hint
// ranges inside that excerpt, else the whole excerpt; repeated and surplus
// IDs go; lines outside the excerpt and unknown IDs stay faults.
func TestCiterDegrade(t *testing.T) {
	unit := record.Excerpt{ID: "ev_" + strings.Repeat("a", 24), Path: "a.go", SHA256: strings.Repeat("1", 64), Start: 1, End: 200, Kind: "code"}
	short := record.Excerpt{ID: "ev_" + strings.Repeat("b", 24), Path: "b.go", SHA256: strings.Repeat("2", 64), Start: 1, End: 20, Kind: "code"}
	inside := record.Narrow(unit, "snap", 30, 40)
	other := record.Narrow(short, "snap", 3, 4)
	doc := record.Excerpt{ID: "ev_" + strings.Repeat("d", 24), Path: "README.md", SHA256: strings.Repeat("3", 64), Start: 1, End: 40, Kind: "document"}
	c := newCiter("snap", []record.Excerpt{unit, short, inside, other, doc})
	for _, x := range []struct {
		name     string
		list     []string
		hints    []string
		limit    int
		ids      []string
		lims     []string
		errs     []string
		resolved bool
	}{
		{"missing range, hint inside", []string{unit.ID}, []string{inside.ID, other.ID}, 8, []string{inside.ID}, []string{limCitationFromCandidate}, nil, true},
		{"missing range, no hint inside", []string{unit.ID}, []string{other.ID}, 8, []string{unit.ID}, []string{limCitationUnspecified}, nil, true},
		{"range too long", []string{unit.ID + ":1-150"}, nil, 8, []string{unit.ID}, []string{limCitationUnspecified}, nil, true},
		{"malformed range", []string{unit.ID + ":x-y"}, nil, 8, []string{unit.ID}, []string{limCitationUnspecified}, nil, true},
		{"repeated and surplus", []string{short.ID, short.ID, unit.ID + ":5-9", other.ID}, nil, 2, []string{short.ID, record.Narrow(unit, "snap", 5, 9).ID},
			[]string{limCitationListTrimmed}, nil, true},
		{"range on a document", []string{doc.ID + ":3-9"}, nil, 8, []string{doc.ID}, []string{limCitationUnspecified}, nil, true},
		{"outside the excerpt", []string{short.ID + ":10-30"}, nil, 8, []string{short.ID}, nil, []string{errCitationOutside}, false},
		{"malformed unknown ID", []string{"ev_" + strings.Repeat("c", 24) + ":x"}, nil, 8, nil, nil, []string{errCitationInvalid}, false},
	} {
		t.Run(x.name, func(t *testing.T) {
			ids, lims, errs := c.degrade(x.list, citeNarrow, x.limit, x.hints)
			if !slices.Equal(lims, x.lims) || !slices.Equal(errs, x.errs) || (x.resolved && !slices.Equal(ids, x.ids)) {
				t.Fatalf("degrade(%v) = %v %v %v, want %v %v %v", x.list, ids, lims, errs, x.ids, x.lims, x.errs)
			}
		})
	}
}

// Every code the review roles emit has a class, and the table of
// docs/REVIEW.md#출력-오류-분류 lists each with that class.
func TestOutputCodesMatchTheReviewTable(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "REVIEW.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	start := strings.Index(text, "\n### 출력 오류 분류")
	if start < 0 {
		t.Fatal("docs/REVIEW.md has no output error table")
	}
	// The table section ends at the next heading of any level.
	section := text[start+1:]
	if end := regexp.MustCompile(`\n#{1,6} `).FindStringIndex(section); end != nil {
		section = section[:end[0]]
	}
	row := regexp.MustCompile("(?m)^\\| `([a-z0-9_]+)` \\| (무결성|형식) \\|")
	classes := map[string]codeClass{"무결성": classIntegrity, "형식": classForm}
	documented := map[string]codeClass{}
	for _, m := range row.FindAllStringSubmatch(section, -1) {
		if _, dup := documented[m[1]]; dup {
			t.Errorf("%s listed twice", m[1])
		}
		documented[m[1]] = classes[m[2]]
	}
	for code, class := range outputCodes {
		if documented[code] != class {
			t.Errorf("%s: registry %s, docs %q", code, class, documented[code])
		}
	}
	for code := range documented {
		if _, ok := outputCodes[code]; !ok {
			t.Errorf("docs list %s, the registry does not", code)
		}
	}
}

// requireRegisteredCodes fails on an output error code of a call that the
// registry does not classify.
func requireRegisteredCodes(t *testing.T, r record.Record) {
	t.Helper()
	for _, c := range r.Calls {
		for _, code := range c.OutputErrors {
			if _, ok := outputCodes[code]; !ok {
				t.Errorf("%s call %d: unregistered output error %s", r.Stage, c.Attempt, code)
			}
		}
	}
}

// scaleTree is limitTree with a second long unit, pkg/limit/scale.go.
func scaleTree() map[string]string {
	tree := limitTree()
	var b strings.Builder
	b.WriteString("package limit\n\n// Scale doubles n step by step.\nfunc Scale(n int) int {\n")
	for k := range 100 {
		fmt.Fprintf(&b, "\tn = n * 1 + %d - %d\n", k, k)
	}
	b.WriteString("\treturn n\n}\n")
	tree["pkg/limit/scale.go"] = b.String()
	return tree
}

const scalePath = "pkg/limit/scale.go"

// shapeRun is one run of the review roles and the report over scaleTree:
// the overview investigation answers two candidates, the first on
// limit.go lines 105-107, the second on scale.go lines 4-10; verify answers
// with answer(call, request, candidate).
type shapeRun struct {
	j        analysis.Job
	results  map[string]analysis.TaskResult
	verify   record.Record
	requests []verifyInput
	report   report.Report
	caller   *scriptedCaller
}

func runShape(t *testing.T, maxCalls int, answer func(n int, in verifyInput, c record.Candidate) opencode.Reply) shapeRun {
	t.Helper()
	run := shapeRun{j: treeJob(t, scaleTree())}
	script := coverageScript(t)
	script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		if !in.Scope.WholeRun {
			return asJSON(map[string]any{"candidates": []any{}})
		}
		return asJSON(map[string]any{"candidates": []record.CandidateText{
			billingCandidate("음수 처리", unitOf(t, in, limitPath).ID+":105-107"),
			billingCandidate("배수 누적", unitOf(t, in, scalePath).ID+":4-10"),
		}})
	}
	script["verify"] = func(n int, q opencode.Request) opencode.Reply {
		in := decodeAs[verifyInput](t, q)
		run.requests = append(run.requests, in)
		var out []any
		for _, c := range in.Candidates {
			reply := answer(n, in, c)
			if reply.Reason != "" || reply.Output == nil {
				return reply
			}
			out = append(out, rawJSON(reply.Output))
		}
		return asJSON(map[string]any{"verifications": out})
	}
	run.caller = &scriptedCaller{script: script}
	run.report, run.results = reportWithCalls(t, &run.j, run.caller, maxCalls)
	run.verify = readRecord(t, run.j, record.StageVerify)
	for _, stage := range []string{record.StageDocuments, record.StageInvestigate, record.StageVerify} {
		requireRegisteredCodes(t, readRecord(t, run.j, stage))
	}
	return run
}

// rawJSON keeps an encoded verification as it is inside the answer.
type rawJSON []byte

func (r rawJSON) MarshalJSON() ([]byte, error) { return r, nil }

// reportWithCalls is reportAt with a call ceiling (0: the default).
func reportWithCalls(t *testing.T, j *analysis.Job, caller *scriptedCaller, maxCalls int) (report.Report, map[string]analysis.TaskResult) {
	t.Helper()
	if maxCalls == 0 {
		return reportAt(t, j, secretsStub{}, caller, Budget{}, &exampleIDs{n: 0xb40})
	}
	pinExampleSnapshot(t, j)
	h, err := discovery.Wrap(Handler{Static: secretsStub{}, Caller: caller, MaxCalls: maxCalls}, discovery.Config{Observer: exampleObserver{t: t}})
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
	r, err := report.Build(exampleInput(t, *j, h, plan, results, &exampleIDs{n: 0xb40}), report.DefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	return r, results
}

// verifyUnit is the whole unit of path that a verify request sends.
func verifyUnit(t *testing.T, in verifyInput, path string) record.Excerpt {
	t.Helper()
	i := slices.IndexFunc(in.Scope.Excerpts, func(e record.Excerpt) bool { return e.Path == path && e.Start == 1 && e.Origin != record.OriginCited })
	if i < 0 {
		t.Fatalf("verify request has no unit of %s", path)
	}
	return in.Scope.Excerpts[i]
}

// citing is an inconclusive verdict of c citing ids.
func citing(c record.Candidate, ids ...string) opencode.Reply {
	return asJSON(inconclusiveFor(c, ids...))
}

func findingOf(t *testing.T, r report.Report, candidate string) report.Finding {
	t.Helper()
	i := slices.IndexFunc(r.Findings, func(f report.Finding) bool { return f.ID == "review_"+candidate })
	if i < 0 {
		t.Fatalf("no finding of %s", candidate)
	}
	return r.Findings[i]
}

// Verify plays each fault of form twice in a row: the repair names the
// candidate, field and excerpt (with the excerpts that need a range), and
// the second answer is resolved in the verdict; the task succeeds and the
// report carries the verdict with the limitation of what was resolved.
func TestVerifyFormFaultsRepairedThenResolved(t *testing.T) {
	for _, x := range []struct {
		name  string
		cite  func(t *testing.T, in verifyInput, c record.Candidate) opencode.Reply
		code  string
		field string
		cited bool
		lim   string
		check func(t *testing.T, run shapeRun, v record.Verification)
	}{
		{"long excerpt without a range, the candidate's range inside", func(t *testing.T, in verifyInput, c record.Candidate) opencode.Reply {
			return citing(c, verifyUnit(t, in, scalePath).ID)
		}, errCitationMissing, "supporting_evidence_ids", true, limCitationFromCandidate, func(t *testing.T, run shapeRun, v record.Verification) {
			if !slices.Equal(v.SupportingEvidence, run.verify.Candidates[1].Evidence) {
				t.Fatalf("supporting %v, want the candidate's range %v", v.SupportingEvidence, run.verify.Candidates[1].Evidence)
			}
		}},
		{"range longer than 80 lines of another unit", func(t *testing.T, in verifyInput, c record.Candidate) opencode.Reply {
			u := verifyUnit(t, in, limitPath)
			return citing(c, fmt.Sprintf("%s:%d-%d", u.ID, u.Start, u.End))
		}, errCitationInvalid, "supporting_evidence_ids", true, limCitationUnspecified, func(t *testing.T, run shapeRun, v record.Verification) {
			if e := catalogOf(run.verify.Evidence)[v.SupportingEvidence[0]]; len(v.SupportingEvidence) != 1 || e.Path != limitPath || e.Start != 1 {
				t.Fatalf("supporting %v (%+v), want the whole limit.go unit", v.SupportingEvidence, e)
			}
		}},
		{"repeated evidence", func(t *testing.T, in verifyInput, c record.Candidate) opencode.Reply {
			return citing(c, c.Evidence[0], c.Evidence[0])
		}, "evidence_id_duplicate", "supporting_evidence_ids", false, limCitationListTrimmed, func(t *testing.T, run shapeRun, v record.Verification) {
			if len(v.SupportingEvidence) != 1 {
				t.Fatalf("supporting %v", v.SupportingEvidence)
			}
		}},
		{"lists over their limits", func(t *testing.T, in verifyInput, c record.Candidate) opencode.Reply {
			m := inconclusiveFor(c, c.Evidence[0])
			m["unresolved_assumptions"] = []string{"가정 하나", "가정 둘", "가정 셋", "가정 넷", "가정 다섯", "가정 여섯"}
			m["next_verification"] = []string{"호출자를 읽는다.", "테스트를 읽는다.", "문서를 읽는다.", "설정을 읽는다.", "주석을 읽는다."}
			return asJSON(m)
		}, "list_too_long", "unresolved_assumptions", false, limVerificationTrimmed, func(t *testing.T, run shapeRun, v record.Verification) {
			if len(v.UnresolvedAssumptions) != 4 || len(v.NextVerification) != 3 {
				t.Fatalf("verdict %+v", v)
			}
		}},
	} {
		t.Run(x.name, func(t *testing.T) {
			run := runShape(t, 0, func(n int, in verifyInput, c record.Candidate) opencode.Reply {
				if c.ID == "investigate_2" {
					return x.cite(t, in, c)
				}
				return citing(c, c.Evidence[0])
			})
			if s := run.results["review.verify"]; s.Status != domain.TaskSucceeded || len(run.verify.Calls) != 2 {
				t.Fatalf("verify %+v, calls %+v", s, run.verify.Calls)
			}
			if !slices.Contains(run.verify.Calls[0].OutputErrors, x.code) || run.verify.Calls[1].Outcome != record.OutcomeAccepted ||
				!slices.Contains(run.verify.Calls[1].OutputErrors, x.code) {
				t.Fatalf("calls %+v", run.verify.Calls)
			}
			repair := run.requests[1].Repair
			if repair == nil || len(repair.Candidates) != 1 || repair.Candidates[0].ID != "investigate_2" || !slices.Contains(repair.Candidates[0].Errors, x.code) {
				t.Fatalf("repair %+v", repair)
			}
			if !slices.ContainsFunc(repair.Candidates[0].Issues, func(e verifyIssue) bool {
				return e.Field == x.field && e.Error == x.code && (e.ID != "") == x.cited
			}) {
				t.Fatalf("repair issues %+v", repair.Candidates[0].Issues)
			}
			for _, r := range repair.RangeRequired {
				if slices.Contains(repair.AcceptedIDs, r.ID) || r.End-r.Start+1 <= record.WholeCitationLines {
					t.Fatalf("range-required excerpt %+v listed as citable alone", r)
				}
			}
			if len(repair.RangeRequired) == 0 {
				t.Fatal("repair lists no excerpt that needs a range")
			}
			v := run.verify.Verifications[1]
			if v.Verdict != record.VerdictInconclusive || !slices.Contains(v.Limitations, x.lim) ||
				!slices.Contains(run.verify.Limitations, x.lim) || !slices.Contains(run.verify.Limitations, record.LimOutputDegraded) {
				t.Fatalf("verdict %+v, record limitations %v", v, run.verify.Limitations)
			}
			x.check(t, run, v)
			requireReviewCounted(t, run.report)
			if f := findingOf(t, run.report, "investigate_2"); f.Validation != record.VerdictInconclusive || !slices.Contains(f.Limitations, x.lim) {
				t.Fatalf("finding %+v", f)
			}
			if slices.Contains(run.report.Limitations, report.LimReviewVerificationIncomplete) {
				t.Fatalf("report limitations %v", run.report.Limitations)
			}
		})
	}
}

// One candidate keeps an integrity fault (an evidence ID the request did
// not hold) on both calls: it is quarantined and the task ends incomplete,
// but the report publishes the other verdict, shows the quarantined
// candidate unreviewed with its reason and counts the AI review as
// performed with a visible limitation; the report stays partial.
func TestReportPublishesVerdictsOfAnIncompleteVerify(t *testing.T) {
	fabricated := "ev_" + strings.Repeat("0", 24)
	run := runShape(t, 0, func(n int, in verifyInput, c record.Candidate) opencode.Reply {
		if c.ID == "investigate_2" {
			return citing(c, fabricated)
		}
		return citing(c, c.Evidence[0])
	})
	if s := run.results["review.verify"]; s.Status != domain.TaskFailed || s.Reason != record.ReasonOutputIncomplete {
		t.Fatalf("verify %+v", s)
	}
	if v := run.verify.Verifications; v[0].Verdict != record.VerdictInconclusive || v[1].Verdict != record.VerdictUnreviewed ||
		!slices.Equal(v[1].Limitations, []string{record.LimQuarantined}) {
		t.Fatalf("verifications %+v", v)
	}
	r := run.report
	if f := findingOf(t, r, "investigate_1"); f.Validation != record.VerdictInconclusive {
		t.Fatalf("accepted verdict not published: %+v", f)
	}
	if f := findingOf(t, r, "investigate_2"); f.Validation != record.VerdictUnreviewed || !slices.Contains(f.Limitations, record.LimQuarantined) {
		t.Fatalf("quarantined candidate %+v", f)
	}
	ai := r.Coverage.AIReview
	i := slices.IndexFunc(ai.RequiredTasks, func(x report.AIReviewTask) bool { return x.Key == "review.verify" })
	if ai.Status != report.AIReviewPerformed || i < 0 || ai.RequiredTasks[i].Result != report.ResultAcceptedIncomplete ||
		*ai.RequiredTasks[i].Reason != record.ReasonOutputIncomplete {
		t.Fatalf("ai_review %+v", ai)
	}
	if r.Status != report.StatusPartial || !slices.Contains(r.Limitations, report.LimReviewVerificationIncomplete) ||
		!slices.Contains(r.Limitations, report.LimRequiredTaskIncomplete) {
		t.Fatalf("report %s limitations %v", r.Status, r.Limitations)
	}
	if !slices.ContainsFunc(r.Gaps, func(g report.Gap) bool {
		return g.Section == report.SectionCodeReview && g.Code == "role_"+report.ResultAcceptedIncomplete && g.Subject != nil && *g.Subject == "review.verify"
	}) {
		t.Fatalf("gaps %+v", r.Gaps)
	}
	raw, err := report.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	md := string(report.RenderMarkdownAt(r, report.Digest(raw), report.Korean, report.DetailFull))
	if p := os.Getenv("CODEYE_SHAPE_MD"); p != "" {
		_ = os.WriteFile(p, []byte(md), 0o600)
	}
	if !strings.Contains(md, "검증 역할이 일부 후보만 판정했다") || !strings.Contains(md, "검증 작업이 일부 후보를 판정하지 못하고 끝났") {
		t.Fatal("the rendered report does not say the verification is incomplete")
	}
	// An older builder could not have published it.
	old := r
	old.Builder.Version = fmt.Sprint(report.VerifyIncompleteSince - 1)
	if err := report.ValidateReport(old); err == nil || !strings.Contains(err.Error(), report.ResultAcceptedIncomplete) {
		t.Fatalf("an older builder's accepted_incomplete: %v", err)
	}
}

// The same for a slice verify: one of its two candidates keeps an integrity
// fault. The slice findings carry the other verdict, the slice task is
// accepted_incomplete, the ledger counts its calls and the report renders.
func TestReportPublishesVerdictsOfAnIncompleteSliceVerify(t *testing.T) {
	j := coverageJob(t)
	script := coverageScript(t)
	candidate := func(in investigateInput, title string) map[string]any {
		return map[string]any{"title": title, "condition": "주소가 비어 있는 경우", "mechanism": "빈 주소가 검사 없이 연결에 쓰인다.", "impact": "서비스가 잘못된 주소로 시작한다.",
			"improvement": "주소를 검사한다.", "counterpoint": "기본값이 항상 채워질 수 있다.", "evidence_ids": []string{shippedExcerpt(in.Scope.Excerpts).ID}}
	}
	script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		list := []map[string]any{candidate(in, "설정 주소 검증 누락")}
		if !in.Scope.WholeRun {
			list = append(list, candidate(in, "설정 주소의 형식 검사 누락"))
		}
		return asJSON(map[string]any{"candidates": list})
	}
	script["verify"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[verifyInput](t, q)
		var out []any
		for _, c := range in.Candidates {
			ids := []string{c.Evidence[0]}
			if c.ID == "investigate_2" {
				ids = []string{"ev_" + strings.Repeat("0", 24)}
			}
			out = append(out, inconclusiveFor(c, ids...))
		}
		return asJSON(map[string]any{"verifications": out})
	}
	r, results := reportAt(t, &j, sliceExampleStatic(t), &scriptedCaller{script: script}, smallBudget, &exampleIDs{n: 0xbc0})
	var slice string
	for key, res := range results {
		if s, ok := strings.CutPrefix(key, "review.verify."); ok && res.Status == domain.TaskFailed && res.Reason == record.ReasonOutputIncomplete {
			slice = s
		}
	}
	if slice == "" {
		t.Fatalf("no slice verify ended incomplete: %+v", results)
	}
	if f := findingOf(t, r, slice+"_investigate_1"); f.Validation != record.VerdictInconclusive {
		t.Fatalf("slice verdict not published: %+v", f)
	}
	if f := findingOf(t, r, slice+"_investigate_2"); f.Validation != record.VerdictUnreviewed || !slices.Contains(f.Limitations, record.LimQuarantined) {
		t.Fatalf("quarantined slice candidate %+v", f)
	}
	if !slices.ContainsFunc(r.Coverage.AIReview.OptionalTasks, func(x report.AIReviewTask) bool {
		return x.Key == "review.verify."+slice && x.Result == report.ResultAcceptedIncomplete
	}) || !slices.Contains(r.Limitations, report.LimReviewVerificationIncomplete) {
		t.Fatalf("ai_review %+v limitations %v", r.Coverage.AIReview, r.Limitations)
	}
	if l := r.CodeReview.Ledger; l == nil || l.CallsUsed == nil || l.RequestBytesSent == nil {
		t.Fatalf("ledger %+v", l)
	}
	raw, err := report.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if md := string(report.RenderMarkdownAt(r, report.Digest(raw), report.Korean, report.DetailFull)); !strings.Contains(md, "review_s"+strings.TrimPrefix(slice, "s")) &&
		!strings.Contains(md, slice) {
		t.Fatal("the rendered report omits the slice")
	}
}

// When the repair cannot be sent (no call left) or fails, the held entry
// whose faults are all of form is resolved; a cancelled or first failing
// call still ends the stage.
func TestVerifyFormFaultResolvedWhenTheRepairCannotArrive(t *testing.T) {
	bare := func(n int, in verifyInput, c record.Candidate) opencode.Reply {
		if c.ID == "investigate_2" {
			return citing(c, verifyUnit(t, in, scalePath).ID)
		}
		return citing(c, c.Evidence[0])
	}
	// The calls before verify, with room for verify's first call only.
	probe := runShape(t, 0, func(n int, in verifyInput, c record.Candidate) opencode.Reply { return citing(c, c.Evidence[0]) })
	before := len(probe.caller.requests) - probe.caller.calls("verify")
	run := runShape(t, before+1, bare)
	if s := run.results["review.verify"]; s.Status != domain.TaskSucceeded || len(run.verify.Calls) != 1 || run.verify.Calls[0].Outcome != record.OutcomeAccepted ||
		!slices.Contains(run.verify.Limitations, limRepairNotSent) || !slices.Contains(run.verify.Verifications[1].Limitations, limCitationFromCandidate) {
		t.Fatalf("verify %+v, record %+v %v", s, run.verify.Calls, run.verify.Limitations)
	}
	run = runShape(t, 0, func(n int, in verifyInput, c record.Candidate) opencode.Reply {
		if n > 1 {
			return opencode.Reply{Reason: "model_timeout"}
		}
		return bare(n, in, c)
	})
	if s := run.results["review.verify"]; s.Status != domain.TaskSucceeded || len(run.verify.Calls) != 2 || run.verify.Calls[1].Outcome != "model_timeout" ||
		slices.Contains(run.verify.Limitations, "verification_call_failed") || run.verify.Verifications[1].Verdict != record.VerdictInconclusive {
		t.Fatalf("verify %+v, record %+v %v", s, run.verify.Calls, run.verify.Limitations)
	}
}

// The dry-run upstream with --shape-errors 1 answers every item with a
// fault of form on both calls (a repeated ID, a verdict citing the long
// unit without a range): every role still succeeds and the report counts
// the review. With --integrity-errors 1 every verdict cites an ID the
// request did not hold: verify keeps rejecting and fails.
func TestDryRunUpstreamFaultInjection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("dry runs with scripts/fake_model_upstream.py are a Linux procedure")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	for _, x := range []struct {
		name             string
		shape, integrity float64
	}{{"form", 1, 0}, {"integrity", 0, 1}} {
		t.Run(x.name, func(t *testing.T) {
			// The one code file is the long unit: the candidate cites its
			// first lines, and a verdict cites the unit without a range.
			tree := limitTree()
			delete(tree, "cmd/limit/main.go")
			tree["pkg/limit/README.md"] = "# Limit package\n\nClamp never returns a negative value.\n"
			j := treeJob(t, tree)
			script := coverageScript(t)
			for _, role := range []string{record.StageDocuments, record.StageInvestigate, record.StageVerify} {
				script[role] = func(_ int, q opencode.Request) opencode.Reply {
					return dryRunUpstream(t, python, role, q, x.shape, x.integrity)
				}
			}
			r, results := reportAt(t, &j, secretsStub{}, &scriptedCaller{script: script}, Budget{}, &exampleIDs{n: 0xb80})
			doc, inv, ver := readRecord(t, j, record.StageDocuments), readRecord(t, j, record.StageInvestigate), readRecord(t, j, record.StageVerify)
			for _, rec := range []record.Record{doc, inv, ver} {
				requireRegisteredCodes(t, rec)
			}
			if x.integrity > 0 {
				if s := results["review.verify"]; s.Status != domain.TaskFailed || s.Reason != reasonOutputInvalid || len(ver.Calls) != 2 ||
					!slices.Contains(ver.Calls[1].OutputErrors, "evidence_id_unknown") {
					t.Fatalf("verify %+v, calls %+v", s, ver.Calls)
				}
				return
			}
			for _, key := range []string{"review.documents", "review.investigate", "review.verify"} {
				if s := results[key]; s.Status != domain.TaskSucceeded {
					t.Fatalf("%s %+v", key, s)
				}
			}
			requireResolvedCalls(t, doc, []string{"evidence_id_duplicate"}, []string{limCitationListTrimmed})
			requireResolvedCalls(t, inv, []string{"evidence_id_duplicate", record.CompareCodeRangeMissing}, []string{limCitationListTrimmed, limCitationUnspecified})
			requireResolvedCalls(t, ver, []string{errCitationMissing}, []string{limCitationFromCandidate})
			requireReviewCounted(t, r)
		})
	}
}

// A role whose first answer has faults of form only, and whose repair times
// out or is refused by the call ceiling, keeps that answer resolved: the
// record holds an accepted call before the failed one (or none after it),
// and the report builder accepts that record.
func TestRoleFormFaultResolvedWhenTheRepairCannotArrive(t *testing.T) {
	answer := func(n int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		if !in.Scope.WholeRun {
			return asJSON(map[string]any{"candidates": []any{}})
		}
		if n > 1 && in.Repair != nil {
			return opencode.Reply{Reason: "model_timeout"}
		}
		u := unitOf(t, in, limitPath).ID + ":105-107"
		return asJSON(map[string]any{"candidates": []record.CandidateText{billingCandidate("음수 처리", u, u)}})
	}
	run := func(maxCalls int) (analysis.Job, *scriptedCaller, report.Report, map[string]analysis.TaskResult) {
		j := treeJob(t, limitTree())
		script := coverageScript(t)
		script["investigate"] = answer
		script["verify"] = func(_ int, q opencode.Request) opencode.Reply {
			return answerAll(decodeAs[verifyInput](t, q), func(c record.Candidate) []string { return []string{c.Evidence[0]} })
		}
		caller := &scriptedCaller{script: script}
		r, results := reportWithCalls(t, &j, caller, maxCalls)
		return j, caller, r, results
	}
	j, caller, r, results := run(0)
	inv := readRecord(t, j, record.StageInvestigate)
	if s := results["review.investigate"]; s.Status != domain.TaskSucceeded || len(inv.Calls) != 2 || inv.Calls[0].Outcome != record.OutcomeAccepted ||
		inv.Calls[1].Outcome != "model_timeout" || !slices.Contains(inv.Calls[0].OutputErrors, "evidence_id_duplicate") ||
		!slices.Contains(inv.Limitations, record.LimOutputDegraded) || len(inv.Candidates) != 1 || len(inv.Candidates[0].Evidence) != 1 {
		t.Fatalf("investigate %+v, record %+v %v", s, inv.Calls, inv.Limitations)
	}
	requireReviewCounted(t, r)
	// The ceiling leaves the overview investigation its first call only.
	before := slices.IndexFunc(caller.requests, func(q opencode.Request) bool {
		return q.Role == record.StageInvestigate && decodeAs[investigateInput](t, q).Scope.WholeRun
	})
	j, _, r, results = run(before + 1)
	inv = readRecord(t, j, record.StageInvestigate)
	if s := results["review.investigate"]; s.Status != domain.TaskSucceeded || len(inv.Calls) != 1 || inv.Calls[0].Outcome != record.OutcomeAccepted ||
		!slices.Contains(inv.Limitations, limRepairNotSent) || !slices.Contains(inv.Limitations, record.LimOutputDegraded) {
		t.Fatalf("investigate %+v, record %+v %v", s, inv.Calls, inv.Limitations)
	}
	if slices.ContainsFunc(r.Limitations, func(l string) bool { return strings.HasPrefix(l, report.LimReviewLineage) }) {
		t.Fatalf("report set the record aside: %v", r.Limitations)
	}
}

// requireResolvedCalls checks a role answered twice with the same faults:
// the first call rejected with codes, the second accepted with the same
// codes kept on the call, and the record marked review_output_degraded with
// lims.
func requireResolvedCalls(t *testing.T, r record.Record, codes, lims []string) {
	t.Helper()
	requireRegisteredCodes(t, r)
	if len(r.Calls) != 2 || r.Calls[0].Outcome != callRejected || r.Calls[1].Outcome != record.OutcomeAccepted {
		t.Fatalf("%s calls %+v", r.Stage, r.Calls)
	}
	for _, code := range codes {
		if !slices.Contains(r.Calls[0].OutputErrors, code) || !slices.Contains(r.Calls[1].OutputErrors, code) {
			t.Fatalf("%s calls %+v, want %s on both", r.Stage, r.Calls, code)
		}
	}
	for _, lim := range append([]string{record.LimOutputDegraded}, lims...) {
		if !slices.Contains(r.Limitations, lim) {
			t.Fatalf("%s limitations %v, want %s", r.Stage, r.Limitations, lim)
		}
	}
}

// The documents role answers the same faulty items twice: evidence lists
// over their limits are cut and kept, an item with empty prose is left out;
// the role succeeds.
func TestDocumentFormFaultsResolvedAfterTheRepair(t *testing.T) {
	files := map[string]string{
		"README.md": "The project stores values.\n",
		"app.go":    "package values\nfunc store() {}\n",
	}
	for _, name := range []string{"readme-a.md", "readme-b.md", "readme-c.md", "readme-d.md", "readme-e.md"} {
		files[name] = "Reference details.\n"
	}
	j := treeJob(t, files)
	script := maxScript(t)
	var repairs []*outputRepair
	script["documents"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[docsInput](t, q)
		repairs = append(repairs, in.Repair)
		var ids []string
		for _, d := range in.Documents {
			ids = append(ids, d.EvidenceID)
		}
		return asJSON(docsOutput{
			// The second contract puts a line range on a document handle.
			Contracts: []record.Contract{{Statement: "값을 저장한다.", AppliesTo: ".", EvidenceIDs: ids[:5]},
				{Statement: "참고 문서를 둔다.", AppliesTo: ".", EvidenceIDs: []string{ids[1] + ":1-1"}}},
			Conflicts: []record.Conflict{{Scope: ".", Summary: "두 안내가 저장 위치를 다르게 말한다.", Impact: "어느 쪽을 따를지 알 수 없다.",
				FirstEvidence: ids[:1], SecondEvidence: ids[:1]}},
			Questions: []record.Question{{Question: "", AppliesTo: ".", EvidenceIDs: ids[:1]},
				{Question: "값은 어디에 저장되는가?", AppliesTo: ".", EvidenceIDs: ids[:5]}},
			InstructionNotes: []record.InstructionNote{},
			DocumentQuality:  []record.DocumentQuality{{Aspect: "clarity", Assessment: "strength", Statement: "첫 문장에 저장 동작을 안내한다.", EvidenceIDs: append(ids[:2:2], ids[:2]...)}},
		})
	}
	_, results := runRole(t, j, vetStub{}, &scriptedCaller{script: script}, "1.27.4")
	if results["review.documents"].Status != domain.TaskSucceeded {
		t.Fatalf("documents %+v", results["review.documents"])
	}
	r := readRecord(t, j, record.StageDocuments)
	requireResolvedCalls(t, r, []string{"contracts_evidence_ids_invalid", "text_invalid", "evidence_id_duplicate", errCitationInvalid,
		"conflict_same_document", "questions_evidence_ids_invalid"},
		[]string{limCitationListTrimmed, limDocumentItemsOmitted, limCitationUnspecified})
	if len(repairs) != 2 || repairs[1] == nil || !hasIssue(repairs[1].Items, itemIssue{List: "questions", Index: 1, Field: "question", Error: "text_invalid"}) ||
		!hasIssue(repairs[1].Items, itemIssue{List: "contracts", Index: 1, Field: "contracts_evidence_ids", Error: "contracts_evidence_ids_invalid"}) {
		t.Fatalf("repair %+v", repairs)
	}
	if len(r.Contracts) != 2 || len(r.Contracts[0].EvidenceIDs) != 4 || len(r.Contracts[1].EvidenceIDs) != 1 ||
		!slices.ContainsFunc(r.Evidence, func(e record.Excerpt) bool { return e.ID == r.Contracts[1].EvidenceIDs[0] && e.Kind == "document" }) ||
		len(r.Conflicts) != 0 || len(r.Questions) != 1 || len(r.Questions[0].EvidenceIDs) != 4 ||
		len(r.DocumentQuality) != 1 || len(r.DocumentQuality[0].EvidenceIDs) != 2 {
		t.Fatalf("record contracts %+v questions %+v quality %+v", r.Contracts, r.Questions, r.DocumentQuality)
	}
}

// The overview investigation and the diagnostic interpretation answer the
// same faulty items twice: a repeated citation is removed and its item
// kept, an item with empty prose is left out, a limit's repeated
// observation ID is removed; both roles succeed and verify sees the kept
// candidates.
func TestCandidateFormFaultsResolvedAfterTheRepair(t *testing.T) {
	j := modulesJob(t, nil)
	script := validScript(t)
	var invRepairs, intRepairs []*outputRepair
	script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		invRepairs = append(invRepairs, in.Repair)
		gw := excerptID(in.Scope.Excerpts, "gateway.go")
		kept := billingCandidate("Forward가 결제 호출을 반복할 수 있음", gw, gw)
		empty := billingCandidate("", gw)
		noMechanism := billingCandidate("재시도 간격이 없음", gw)
		noMechanism.Mechanism = ""
		noEvidence := billingCandidate("응답을 확인하지 않음")
		// A strength (optional, never repaired) with a malformed range
		// stands for the whole excerpt at once.
		strength := record.Strength{Title: "재시도 횟수에 상한을 둔다", Benefit: "실패한 호출이 끝없이 반복되지 않는다.", Scope: ".", EvidenceIDs: []string{gw + ":x"}}
		return asJSON(map[string]any{"candidates": []record.CandidateText{kept, empty, noMechanism, noEvidence}, "strengths": []record.Strength{strength}})
	}
	script["interpret"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[interpretInput](t, q)
		intRepairs = append(intRepairs, in.Repair)
		o := in.Observations[0]
		h := func(title string, ids []string) map[string]any {
			return map[string]any{"observation_ids": []string{o.ID}, "title": title, "condition": "send가 실패한 뒤 다시 호출되는 경우", "mechanism": "반복마다 오류 변수가 덮어써진다.",
				"impact": "마지막 오류만 남는다.", "improvement": "각 시도의 오류를 기록한다.", "counterpoint": "마지막 오류만 반환하는 것이 계약일 수 있다.", "evidence_ids": ids}
		}
		return asJSON(map[string]any{
			"hypotheses": []map[string]any{h("재시도 분기에서 결과를 버릴 수 있음", append(slices.Clone(o.EvidenceIDs), o.EvidenceIDs...)), h("", o.EvidenceIDs)},
			"limits":     []map[string]any{{"observation_ids": []string{o.ID, o.ID}, "statement": "진단 위치 밖의 호출 조건은 이 입력으로 연결할 수 없다."}},
		})
	}
	f := &scriptedCaller{script: script}
	_, res := runRole(t, j, vetStub{obs: gatewayObservation}, f, "1.27.4")
	for _, key := range []string{"review.investigate", "review.interpret", "review.verify"} {
		if res[key].Status != domain.TaskSucceeded {
			t.Fatalf("%s %+v", key, res[key])
		}
	}
	inv, interp := readRecord(t, j, record.StageInvestigate), readRecord(t, j, record.StageInterpret)
	requireResolvedCalls(t, inv, []string{"evidence_id_duplicate", "text_invalid", "mechanism_missing", "evidence_missing", "code_evidence_missing"},
		[]string{limCitationListTrimmed, limCandidateOmitted})
	requireResolvedCalls(t, interp, []string{"evidence_id_duplicate", "text_invalid", "observation_id_duplicate"},
		[]string{limCitationListTrimmed, limInterpretItemsOmitted})
	if len(invRepairs) != 2 || invRepairs[1] == nil || !hasIssue(invRepairs[1].Items, itemIssue{List: "candidates", Index: 2, Field: "title", Error: "text_invalid"}) {
		t.Fatalf("investigate repair %+v", invRepairs)
	}
	if len(intRepairs) != 2 || intRepairs[1] == nil || !hasIssue(intRepairs[1].Items, itemIssue{List: "hypotheses", Index: 2, Field: "title", Error: "text_invalid"}) {
		t.Fatalf("interpret repair %+v", intRepairs)
	}
	if len(inv.Candidates) != 1 || len(inv.Candidates[0].Evidence) != 1 || inv.Candidates[0].ID != "investigate_1" {
		t.Fatalf("investigate candidates %+v", inv.Candidates)
	}
	if len(inv.Strengths) != 1 || inv.Strengths[0].EvidenceIDs[0] != inv.Candidates[0].Evidence[0] || !slices.Contains(inv.Limitations, limCitationUnspecified) ||
		slices.Contains(inv.Limitations, "review_strength_omitted") {
		t.Fatalf("strengths %+v limitations %v", inv.Strengths, inv.Limitations)
	}
	if len(interp.Candidates) != 1 || len(interp.Limits) != 1 || len(interp.Limits[0].ObservationIDs) != 1 {
		t.Fatalf("interpret candidates %+v limits %+v", interp.Candidates, interp.Limits)
	}
	if v := readRecord(t, j, record.StageVerify); len(v.Verifications) != 2 {
		t.Fatalf("verify %+v", v.Verifications)
	}
}
