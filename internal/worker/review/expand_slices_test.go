package review

import (
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

func TestVerifyLateGoSliceReceivesCallersAndTests(t *testing.T) {
	const marker = "return amount + 1"
	const title = "계산 결과에 추가된 단위"
	tree := map[string]string{
		"go.mod": "module example.org/billing\n\ngo 1.23\n",
		"value.go": "package billing\n\nfunc Amount(amount int) int {\n" + strings.Repeat("// preprocessing\n", 70) +
			"\t" + marker + "\n}\n",
		"request.go": "package billing\n\nfunc Recalculate(amount int) int {\nreturn Amount(amount)\n}\n",
		"amount_test.go": "package billing\n\nimport \"testing\"\n" +
			"\nfunc TestBillingAmount(t *testing.T) {\nif got := Amount(4); got != 5 { t.Fatal(got) }\n}\n",
	}
	j := treeJob(t, tree)
	script := coverageScript(t)
	script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		for _, e := range in.Scope.Excerpts {
			if e.Path == "value.go" && strings.Contains(e.Text, marker) && e.Start > 1 {
				return asJSON(map[string]any{"candidates": []record.CandidateText{{
					Title: title, Condition: "Amount로 수를 계산한다.", Impact: "계산된 값이 달라진다.",
					Improvement: "경계의 단위를 확인한다.", Counterpoint: "한 단위를 의도적으로 추가할 수 있다.",
					Evidence: []string{e.ID},
				}}})
			}
		}
		return asJSON(map[string]any{"candidates": []any{}})
	}
	verify := script["verify"]
	sawCandidate := false
	script["verify"] = func(n int, q opencode.Request) opencode.Reply {
		in := decodeAs[verifyInput](t, q)
		if slices.ContainsFunc(in.Candidates, func(c record.Candidate) bool { return c.Title == title }) {
			sawCandidate = true
			if !slices.ContainsFunc(in.Scope.Excerpts, func(e record.Excerpt) bool {
				return e.Path == "request.go" && e.Symbol == "Amount" && e.Origin == "caller" && strings.Contains(e.Text, "return Amount(amount)")
			}) || !slices.ContainsFunc(in.Scope.Excerpts, func(e record.Excerpt) bool {
				return e.Path == "amount_test.go" && e.Symbol == "Amount" && e.Origin == "test" && strings.Contains(e.Text, "Amount(4)")
			}) {
				t.Fatalf("candidate from a later window did not receive actual caller and test source: %+v", in.Scope.Excerpts)
			}
		}
		return verify(n, q)
	}
	_, results := runRoleWith(t, j, Handler{Static: secretsStub{}, Caller: &scriptedCaller{script: script}}, "1.27.4")
	if !sawCandidate || !slices.ContainsFunc(filledSlots(t, j), func(key string) bool {
		return results["review.verify."+key].Status == domain.TaskSucceeded
	}) {
		t.Fatal("the later source slice did not reach verification")
	}
}
