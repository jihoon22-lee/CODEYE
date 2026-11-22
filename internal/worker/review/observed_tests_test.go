package review

import (
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

func TestVerificationObservesOnlyTestCasesInItsCitedSource(t *testing.T) {
	excerpts := []record.Excerpt{{Kind: "code", ID: "implementation", Path: "same.go", Start: 1, End: 10}, {Kind: "test", ID: "assertion", Path: "save_test.go", Start: 5, End: 9}}
	facts := []analysis.ObservedTest{{Path: "other.go", Line: 7, Status: "passed"}, {Path: "same.go", Line: 7, Status: "failed"}, {Path: "save_test.go", Line: 9, Status: "failed"}, {Path: "save_test.go", Line: 9, Status: "failed"}, {Path: "save_test.go", Line: 10, Status: "passed"}}
	if got := observedTests(excerpts, facts); !slices.Equal(got, []observedTestCase{{"assertion", 9, "failed"}}) {
		t.Fatalf("observed cases: %+v", got)
	}
}

func TestNormalVerifierReceivesMatchingExecutedTest(t *testing.T) {
	job := treeJob(t, map[string]string{"go.mod": "module example.test/store\n\ngo 1.22\n", "save.go": "package store\nfunc Visible()bool{return false}\n", "save_test.go": "package store\nimport \"testing\"\nfunc TestSave(t *testing.T){ if !Visible(){t.Fatal(\"not visible\")} }\n"})
	job.TestEvidence = []analysis.ObservedTest{{Path: "save_test.go", Line: 3, Status: "failed"}, {Path: "unrelated_test.go", Line: 3, Status: "passed"}}
	answers := coverageScript(t)
	answers[record.StageInvestigate] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		var refs []string
		for _, e := range in.Scope.Excerpts {
			if e.Path == "save.go" || e.Path == "save_test.go" {
				refs = append(refs, e.ID)
			}
		}
		if len(refs) < 2 {
			return asJSON(map[string]any{"candidates": []any{}})
		}
		return asJSON(map[string]any{"candidates": []record.CandidateText{{Title: "저장 결과가 표시되지 않음", Condition: "저장 뒤 값을 조회할 때", Impact: "표시 상태가 거짓으로 남는다.", Improvement: "저장 뒤 표시 상태를 반영한다.", Counterpoint: "호출자가 별도 상태를 조합할 수 있다.", Evidence: refs[:2]}}})
	}
	original := answers[record.StageVerify]
	seen := false
	answers[record.StageVerify] = func(n int, q opencode.Request) opencode.Reply {
		in := decodeAs[verifyInput](t, q)
		for _, fact := range in.ObservedTests {
			if !slices.ContainsFunc(in.Scope.Excerpts, func(e record.Excerpt) bool {
				return e.ID == fact.EvidenceID && e.Kind == "test" && e.Path == "save_test.go"
			}) || fact.Status != "failed" {
				t.Fatalf("unrelated observation %+v", fact)
			}
			seen = true
		}
		return original(n, q)
	}
	runRoleWith(t, job, Handler{Static: secretsStub{}, Caller: &scriptedCaller{script: answers}}, "1.27.4")
	if !seen {
		t.Fatal("normal verifier did not receive the executed case")
	}
}
