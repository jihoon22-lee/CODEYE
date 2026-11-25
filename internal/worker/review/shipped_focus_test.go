package review

import (
	"encoding/json"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// The review is about shipped code (docs/REVIEW.md#검토-대상은-배포-코드다): a
// request names the class of a path that is not shipped code, a candidate
// whose evidence all lies on such paths is not sent to verification and is
// counted, not listed, and one that also cites shipped code is untouched.
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
	for _, f := range r.Findings {
		if strings.Contains(f.Title, "시험 코드의 단언 누락") {
			t.Fatalf("a candidate about test code is a finding: %s", f.ID)
		}
		if f.Validation == record.VerdictUnreviewed && slices.Contains(f.Limitations, record.LimCandidateNotShipped) {
			t.Fatalf("an unsent candidate is listed: %s", f.ID)
		}
	}
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
	for _, f := range r.Findings {
		if strings.Contains(f.Title, "시험 코드의 단언 누락") {
			t.Fatalf("listed: %s", f.ID)
		}
	}
}

// A scope with no shipped source (examples and tests only) has nothing else
// to review: its candidates are not set aside.
func TestShippedSourceInTheScope(t *testing.T) {
	cases := []struct {
		name  string
		tree  map[string]string
		scope domain.Scope
		// changed makes the job a pull request of these files.
		changed []string
		want    bool
	}{
		{"shipped and tests", map[string]string{"go.mod": "module x\n", "a.go": "package x\n", "a_test.go": "package x\n"}, domain.Scope{Kind: "full"}, nil, true},
		{"tests and examples only", map[string]string{"go.mod": "module x\n", "a_test.go": "package x\n", "examples/main.go": "package main\n"}, domain.Scope{Kind: "full"}, nil, false},
		{"scope inside the examples", map[string]string{"go.mod": "module x\n", "a.go": "package x\n", "examples/main.go": "package main\n"}, domain.Scope{Kind: "paths", Paths: []string{"examples"}}, nil, false},
		{"pull request that changes tests only", map[string]string{"go.mod": "module x\n", "a.go": "package x\n", "a_test.go": "package x\n"}, domain.Scope{Kind: "full"}, []string{"a_test.go"}, false},
		{"pull request that changes shipped code", map[string]string{"go.mod": "module x\n", "a.go": "package x\n", "a_test.go": "package x\n"}, domain.Scope{Kind: "full"}, []string{"a.go", "a_test.go"}, true},
	}
	for _, c := range cases {
		j := treeJob(t, c.tree)
		j.Scope = c.scope
		if c.changed != nil {
			j.PullRequest = &domain.PullRequestJob{Number: 7, Changed: c.changed}
		}
		if got := (&roleStage{j: j}).hasShippedSource(); got != c.want {
			t.Fatalf("%s: %v", c.name, got)
		}
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
			for _, f := range r.Findings {
				if strings.Contains(f.Title, "about a test path") {
					t.Fatalf("listed: %s", f.ID)
				}
			}
		})
	}
}
