package review

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

func TestWholeInvestigationAllocatesEvidenceAcrossContractComparisons(t *testing.T) {
	const service = "service/tenant.go"
	statements := []string{
		"`OpenVault`는 읽기를 허용합니다.",
		"`CloseArchive`는 닫기 성공을 반환합니다.",
		"`OpenVault`는 닫기 권한을 부여하지 않습니다.",
		"`RetireTenant`는 보관 기록을 삭제합니다.",
	}
	implementation := "package service\n" + strings.Repeat("// registry configuration\n", 62) +
		"func OpenVault() (bool, bool) {\n\treturn true, false\n}\n" + strings.Repeat("// restore and retention settings\n", 33) +
		"func CloseArchive() bool {\n\treturn false\n}\n" + strings.Repeat("// unrelated operations\n", 28) +
		"// RetireTenant is specified elsewhere.\n"
	files := map[string]string{
		"go.mod":              "module example.test/project\n\ngo 1.24\n",
		"README.md":           "# Service\n\n" + strings.Join(statements, "\n\n") + "\n",
		service:               implementation,
		"service/shadow.go":   "package service\n" + strings.Repeat("// hidden alias\n", 61) + "func keep() bool { return true } // RetireTenant\n",
		"outside/cleanup.go":  "package outside\n\nfunc RetireTenant() bool { return true }\n",
		"service/private.go":  "package service\n\nfunc RetireTenant() bool { return true }\n",
		"service/go_vault.go": "package service\n\nfunc keepVault() bool { return true }\n",
	}
	for i := range roleMaxCodeFiles + 4 {
		files[fmt.Sprintf("service/base%02d.go", i)] = "package service\n\nconst ready = true\n"
	}
	j := treeJob(t, files)
	blob := exampleSHA(t, j, service)
	script := maxScript(t)
	script[record.StageDocuments] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[docsInput](t, q)
		var accepted []record.Contract
		for _, statement := range statements {
			accepted = append(accepted, record.Contract{Statement: statement, AppliesTo: "service", EvidenceIDs: []string{in.Documents[0].EvidenceID}})
		}
		return asJSON(docsOutput{Contracts: accepted, Conflicts: []record.Conflict{}, Questions: []record.Question{}, InstructionNotes: []record.InstructionNote{}})
	}
	status := []string{"match", "mismatch", "match", "unknown"}
	want := []string{"return true, false", "return false", "return true, false", ""}
	descriptions := []string{"첫 반환값으로 읽기를 허용합니다.", "닫기 성공 대신 거짓을 반환합니다.", "두 번째 값에서 닫기 권한을 부여하지 않습니다.", "범위 내 삭제 동작의 구현을 확인하지 못했습니다."}
	var chosen [][]string
	wholeCalls := 0
	script[record.StageInvestigate] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		if !in.Scope.WholeRun {
			if len(in.Scope.ContractAnchors) != 0 {
				t.Fatal("whole-run anchors present on a slice request")
			}
			return asJSON(map[string]any{"candidates": []any{}})
		}
		wholeCalls++
		if len(in.Scope.ContractAnchors) != 3 || len(in.Scope.Excerpts) > roleMaxCodeFiles || len(opencode.Prompt(q)) > PromptBudget {
			t.Fatalf("claim allocation or bounds: %+v, %d excerpts", in.Scope.ContractAnchors, len(in.Scope.Excerpts))
		}
		catalog := catalogOf(in.Scope.Excerpts)
		var comparisons []record.ContractComparison
		chosen = make([][]string, len(statements))
		for _, anchor := range in.Scope.ContractAnchors {
			i := anchor.ContractIndex - 1
			if i >= 3 || len(anchor.EvidenceIDs) != 1 {
				t.Fatalf("unexpected claim anchor: %+v", anchor)
			}
			x := catalog[anchor.EvidenceIDs[0]]
			if x.Path != service || x.Start <= 50 || !strings.Contains(x.Text, want[i]) {
				t.Fatalf("claim %d did not receive its late implementation: %+v", i+1, x)
			}
			chosen[i] = anchor.EvidenceIDs
		}
		if !slices.Equal(chosen[0], chosen[2]) || slices.Equal(chosen[0], chosen[1]) || len(catalog) != len(in.Scope.Excerpts) || len(catalog) <= 2 {
			t.Fatalf("shared evidence or ordinary context lost: %v across %d excerpts", chosen, len(catalog))
		}
		for i, c := range in.Documents.Contracts {
			comparisons = append(comparisons, record.ContractComparison{ContractIndex: i + 1, AppliesTo: c.AppliesTo,
				Status: status[i], Reason: descriptions[i], DocumentEvidenceIDs: c.EvidenceIDs, CodeEvidenceIDs: nonNilList(chosen[i])})
		}
		return asJSON(map[string]any{"candidates": []any{}, "contract_comparisons": comparisons})
	}
	caller := &scriptedCaller{script: script}
	static := langStub{eco: "go", path: service, line: 64, secrets: "hit", secretPath: "service/private.go"}
	r := sliceReportWithCaller(t, j, static, sliceExampleCap, &exampleIDs{n: 0x214}, caller)
	if wholeCalls != 1 || caller.calls(record.StageContract) != 0 || r.Documents == nil || len(r.Documents.Comparisons) != len(status) {
		t.Fatalf("calls %d/%d, document results: %+v", wholeCalls, caller.calls(record.StageContract), r.Documents)
	}
	for i, c := range r.Documents.Comparisons {
		if c.Status != status[i] || c.Followup || !slices.Equal(c.CodeEvidenceIDs, chosen[i]) {
			t.Fatalf("comparison %d not published separately: %+v", i+1, c)
		}
		for _, id := range c.CodeEvidenceIDs {
			if !slices.ContainsFunc(r.Documents.Evidence, func(e report.SectionEvidence) bool {
				if e.ID != id || e.Path != service || e.BlobSHA256 != blob || e.StartLine <= 50 {
					return false
				}
				lines := strings.Split(implementation, "\n")
				return e.EndLine <= len(lines) && strings.Contains(strings.Join(lines[e.StartLine-1:e.EndLine], "\n"), want[i])
			}) {
				t.Fatalf("comparison %d published without its source: %+v", i+1, c)
			}
		}
	}
}

func TestContractAnchorAllocationKeepsCompleteGroupsWithSharedWindows(t *testing.T) {
	existing := []record.Excerpt{{ID: "one", Path: "value.go", SHA256: "digest", Start: 1, End: 3, Text: "three lines"}}
	contained := record.Excerpt{ID: "tiny", Path: "value.go", SHA256: "digest", Start: 2, End: 2, Text: "one line"}
	other := record.Excerpt{ID: "two", Path: "other.go", SHA256: "other", Start: 19, End: 19, Text: "a return"}
	fourth := record.Excerpt{ID: "three", Path: "additional.go", SHA256: "additional", Start: 2, End: 2, Text: "a condition"}
	if windows, ids, ok := admitClaimWindows(existing, []record.Excerpt{contained, other, fourth}, 2); ok || len(windows) != 0 || len(ids) != 0 {
		t.Fatalf("partial bundle admitted: %+v %v", windows, ids)
	}
	windows, ids, ok := admitClaimWindows(existing, []record.Excerpt{contained, other, fourth}, 3)
	if !ok || len(windows) != 3 || !slices.Equal(ids, []string{"one", "two", "three"}) {
		t.Fatalf("complete bundle or shared line lost: %+v %v", windows, ids)
	}
	allocation := contractAllocation{anchors: []contractAnchorInput{{ContractIndex: 1, EvidenceIDs: []string{"one"}}, {ContractIndex: 2, EvidenceIDs: ids}}}
	if groups := allocation.submitted(windows[:2]); len(groups) != 1 || groups[0].ContractIndex != 1 {
		t.Fatalf("trimmed bundle description still present: %+v", groups)
	}
}
