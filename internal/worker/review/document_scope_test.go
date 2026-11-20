package review

import (
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

func TestDocumentationTreeContractsCompareWithImplementation(t *testing.T) {
	j := modulesJob(t, func(root string) {
		writeFile(t, root, "docs/guides/behavior.md", "Forward must return the last error after a failed retry.\n")
		writeFile(t, root, "svc/a/docs/behavior.md", "Normalize removes leading and trailing whitespace.\n")
		writeFile(t, root, "svc/a/docs/AGENTS.md", "Keep examples under this directory.\n")
		writeFile(t, root, "svc/a/docs/README.md", "This directory contains internal notes.\n")
	})
	script := validScript(t)
	script["documents"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[docsInput](t, q)
		scopes := map[string]string{}
		for _, d := range in.Documents {
			scopes[d.Path] = d.AppliesTo
		}
		for p, want := range map[string]string{
			"docs/guides/behavior.md": ".", "svc/a/docs/behavior.md": "svc/a",
			"svc/a/docs/AGENTS.md": "svc/a/docs", "svc/a/docs/README.md": "svc/a/docs",
		} {
			if scopes[p] != want {
				t.Fatalf("%s was given scope %q, want %q", p, scopes[p], want)
			}
		}
		return asJSON(map[string]any{"contracts": []record.Contract{
			{Statement: "실패하면 마지막 오류를 반환한다.", AppliesTo: ".", EvidenceIDs: []string{docID(in, "docs/guides/behavior.md")}},
			{Statement: "식별자 앞뒤의 공백을 제거한다.", AppliesTo: "svc/a", EvidenceIDs: []string{docID(in, "svc/a/docs/behavior.md")}},
			{Statement: "예시는 해당 디렉터리 안에 둔다.", AppliesTo: "svc/a/docs", EvidenceIDs: []string{docID(in, "svc/a/docs/AGENTS.md")}},
		}, "conflicts": []any{}, "questions": []any{}, "instruction_notes": []any{}})
	}
	script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		if len(in.Documents.Contracts) != 3 {
			t.Fatalf("document handoff %+v", in.Documents)
		}
		return asJSON(map[string]any{"candidates": []any{}, "contract_comparisons": []record.ContractComparison{
			{ContractIndex: 1, AppliesTo: ".", Status: "match", Reason: "Forward는 마지막 호출의 오류를 반환한다.",
				DocumentEvidenceIDs: in.Documents.Contracts[0].EvidenceIDs, CodeEvidenceIDs: []string{excerptID(in.Scope.Excerpts, "gateway.go")}},
			{ContractIndex: 2, AppliesTo: "svc/a", Status: "match", Reason: "Normalize는 입력의 앞뒤 공백을 제거한다.",
				DocumentEvidenceIDs: in.Documents.Contracts[1].EvidenceIDs, CodeEvidenceIDs: []string{excerptID(in.Scope.Excerpts, "svc/a/normalize.go")}},
		}})
	}
	f := &scriptedCaller{script: script}
	_, results := runRole(t, j, vetStub{}, f, "1.27.4")
	if results["review.documents"].Status != domain.TaskSucceeded || results["review.investigate"].Status != domain.TaskSucceeded {
		t.Fatalf("documents %+v investigate %+v", results["review.documents"], results["review.investigate"])
	}
	r := readRecord(t, j, "investigate")
	if len(r.ContractComparisons) != 2 || r.ContractComparisons[1].AppliesTo != "svc/a" {
		t.Fatalf("comparisons %+v", r.ContractComparisons)
	}
}
