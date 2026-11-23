package review

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// The verifier reads the whole declaration around a cited range: the
// investigator cites two lines of a function, and the verification request
// carries that function with its doc comment (origin declaration) right
// after the cited lines, bound to the snapshot blob like every model input.
func TestVerifyReadsEnclosingDeclaration(t *testing.T) {
	var body strings.Builder
	body.WriteString("package billing\n\n// Adjust applies the quota to n.\nfunc Adjust(n int) int {\n")
	for i := range 30 {
		fmt.Fprintf(&body, "\tn += %d\n", i)
	}
	body.WriteString("\treturn n\n}\n\nfunc Other() int { return 1 }\n")
	j := treeJob(t, map[string]string{"go.mod": "module example.org/billing\n\ngo 1.23\n", "quota.go": body.String()})
	script := coverageScript(t)
	script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		i := slices.IndexFunc(in.Scope.Excerpts, func(e record.Excerpt) bool { return e.Path == "quota.go" && e.Start == 1 })
		if i < 0 {
			t.Fatalf("quota.go not sent whole: %+v", in.Scope.Excerpts)
		}
		return asJSON(map[string]any{"candidates": []record.CandidateText{{
			Title: "누적 보정", Condition: "큰 수량이 들어온다.", Mechanism: "20~21행이 수량을 계속 더한다.", Impact: "결과가 넘칠 수 있다.",
			Improvement: "상한을 확인한다.", Counterpoint: "호출자가 수량을 제한할 수 있다.",
			Evidence: []string{in.Scope.Excerpts[i].ID + ":20-21"},
		}}})
	}
	var decl record.Excerpt
	script["verify"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[verifyInput](t, q)
		if len(in.Candidates) != 1 || len(in.Scope.Excerpts) < 2 {
			t.Fatalf("verify input %+v", in.Scope.Excerpts)
		}
		first, second := in.Scope.Excerpts[0], in.Scope.Excerpts[1]
		if first.Origin != record.OriginCited || first.Start != 20 || first.End != 21 || second.Origin != record.OriginDeclaration ||
			second.Start != 3 || second.End != 36 || !strings.HasPrefix(second.Text, "// Adjust applies") || !strings.HasSuffix(second.Text, "}") {
			t.Fatalf("cited %+v, declaration %+v", first, second)
		}
		decl = second
		return asJSON(map[string]any{"verifications": []any{verdict(in.Candidates[0].ID, "inconclusive", func(v map[string]any) {
			v["supporting_evidence_ids"] = []string{decl.ID}
			v["unresolved_assumptions"] = []string{"호출자가 수량을 제한하는지 확인하지 못했다."}
		})}})
	}
	_, res := runRoleWith(t, j, Handler{Static: secretsStub{}, Caller: &scriptedCaller{script: script}}, "1.27.4")
	if r := res["review.verify"]; r.Status != domain.TaskSucceeded {
		t.Fatalf("verify %+v", r)
	}
	v := readRecord(t, j, record.StageVerify)
	if decl.ID == "" || !slices.ContainsFunc(v.Evidence, func(e record.Excerpt) bool { return e.ID == decl.ID && e.Origin == record.OriginDeclaration }) {
		t.Fatalf("declaration not recorded: %+v", v.Evidence)
	}
	if want := record.ExcerptID(j.Snapshot.Digest, "quota.go", decl.SHA256, 3, 36); decl.ID != want {
		t.Fatalf("declaration ID %s, want %s", decl.ID, want)
	}
}
