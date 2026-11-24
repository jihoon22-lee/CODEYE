package review

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// Contract comparisons of the overview investigation (docs/REVIEW.md#문서-계약-대조).

const limitPath = "pkg/limit/limit.go"

// limitTree is a Go module whose README states one contract about Clamp, a
// 109-line file of one function (one long unit, lines 105-107 the
// documented branch) and a caller outside pkg/limit.
func limitTree() map[string]string {
	var b strings.Builder
	b.WriteString("package limit\n\n// Clamp keeps n within bounds.\nfunc Clamp(n int) int {\n")
	for k := range 100 {
		fmt.Fprintf(&b, "\tn = n + %d - %d\n", k, k)
	}
	b.WriteString("\tif n < 0 {\n\t\treturn 0\n\t}\n\treturn n\n}\n")
	return map[string]string{
		"go.mod":            "module example.org/limit\n\ngo 1.23\n",
		"README.md":         "# Limit\n\nClamp returns zero for a negative input.\n",
		limitPath:           b.String(),
		"cmd/limit/main.go": "package main\n\nimport \"example.org/limit/pkg/limit\"\n\nfunc main() { _ = limit.Clamp(-1) }\n",
	}
}

// comparisonRun is one run of the review roles and the report over a tree
// whose documents role states contract (from the document at path) and
// whose overview investigation answers with answer.
type comparisonRun struct {
	j       analysis.Job
	results map[string]analysis.TaskResult
	inv     record.Record
	repairs []*outputRepair // the repair block of each overview investigate request
	report  report.Report
}

func runComparisons[T any](t *testing.T, tree map[string]string, docPath string, contract record.Contract, b Budget,
	answer func(in investigateInput) T) comparisonRun {
	t.Helper()
	run := comparisonRun{j: treeJob(t, tree)}
	script := coverageScript(t)
	script["documents"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[docsInput](t, q)
		c := contract
		c.EvidenceIDs = nil
		for _, d := range in.Documents {
			if d.Path == docPath && len(c.EvidenceIDs) == 0 {
				c.EvidenceIDs = []string{d.EvidenceID}
			}
		}
		if len(c.EvidenceIDs) == 0 {
			t.Fatalf("no document %s: %+v", docPath, in.Documents)
		}
		return asJSON(docsOutput{Contracts: []record.Contract{c}, Conflicts: []record.Conflict{}, Questions: []record.Question{}, InstructionNotes: []record.InstructionNote{}})
	}
	script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		if !in.Scope.WholeRun {
			return asJSON(map[string]any{"candidates": []any{}})
		}
		run.repairs = append(run.repairs, in.Repair)
		var out any = answer(in)
		if reply, ok := out.(opencode.Reply); ok {
			return reply
		}
		return asJSON(out)
	}
	script["verify"] = func(_ int, q opencode.Request) opencode.Reply {
		return answerAll(decodeAs[verifyInput](t, q), func(c record.Candidate) []string { return []string{c.Evidence[0]} })
	}
	run.report, run.results = reportAt(t, &run.j, secretsStub{}, &scriptedCaller{script: script}, b, &exampleIDs{n: 0x9a0})
	run.inv = readRecord(t, run.j, record.StageInvestigate)
	return run
}

// unitOf is the excerpt of path that starts at its first line.
func unitOf(t *testing.T, in investigateInput, path string) record.Excerpt {
	t.Helper()
	i := slices.IndexFunc(in.Scope.Excerpts, func(e record.Excerpt) bool { return e.Path == path && e.Start == 1 && e.Text != "" })
	if i < 0 {
		t.Fatalf("no unit of %s: %+v", path, in.Scope.Excerpts)
	}
	return in.Scope.Excerpts[i]
}

// lineOf is the line of e that contains text.
func lineOf(t *testing.T, e record.Excerpt, text string) int {
	t.Helper()
	for i, l := range strings.Split(e.Text, "\n") {
		if strings.Contains(l, text) {
			return e.Start + i
		}
	}
	t.Fatalf("%q not in %s %d-%d", text, e.Path, e.Start, e.End)
	return 0
}

func clampComparison(in investigateInput, code ...string) record.ContractComparison {
	return record.ContractComparison{ContractIndex: 1, AppliesTo: ".", Status: "match", Reason: "음수 입력에서 0을 돌려주는 분기가 있다.",
		DocumentEvidenceIDs: slices.Clone(in.Documents.Contracts[0].EvidenceIDs), CodeEvidenceIDs: code}
}

// cmpIssue is the repair item of a comparison fault.
func cmpIssue(index int, field, code string) itemIssue {
	return itemIssue{List: "contract_comparisons", Index: index, Field: field, Error: code}
}

// hasIssue reports an item of list equal to want; want without an evidence
// ID matches any.
func hasIssue(list []itemIssue, want itemIssue) bool {
	return slices.ContainsFunc(list, func(x itemIssue) bool {
		if want.ID == "" {
			x.ID = ""
		}
		return x == want
	})
}

// Every rule of a comparison has its own code, named in output_errors and,
// with the comparison's 1-based position and field, in the repair block. A
// comparison still invalid after the repair is left out with
// contract_comparison_omitted; the candidate and the required review stay,
// and the report shows the contract unchecked with that gap. A comparison
// whose only faults are in the form of its code citations is kept after the
// repair with them resolved (resolved: the limitations recorded).
func TestComparisonRejectionCodes(t *testing.T) {
	readme := record.Contract{Statement: "Clamp는 음수 입력에서 0을 반환한다.", AppliesTo: "."}
	for _, c := range []struct {
		name     string
		contract record.Contract
		build    func(in investigateInput, unit string) []record.ContractComparison
		want     itemIssue
		resolved []string
	}{
		{"index past the contracts", readme, func(in investigateInput, u string) []record.ContractComparison {
			x := clampComparison(in, u+":105-107")
			x.ContractIndex = 2
			return []record.ContractComparison{x}
		}, cmpIssue(1, "contract_index", record.CompareIndexInvalid), nil},
		{"contract compared twice", readme, func(in investigateInput, u string) []record.ContractComparison {
			return []record.ContractComparison{clampComparison(in, u+":105-107"), clampComparison(in, u+":106-106")}
		}, cmpIssue(2, "contract_index", record.CompareIndexDuplicate), nil},
		{"scope not a directory", readme, func(in investigateInput, u string) []record.ContractComparison {
			x := clampComparison(in, u+":105-107")
			x.AppliesTo = "pkg/missing"
			return []record.ContractComparison{x}
		}, cmpIssue(1, "applies_to", record.CompareScopeInvalid), nil},
		{"scope wider than the contract", record.Contract{Statement: readme.Statement, AppliesTo: "pkg/limit"}, func(in investigateInput, u string) []record.ContractComparison {
			return []record.ContractComparison{clampComparison(in, u+":105-107")}
		}, cmpIssue(1, "applies_to", record.CompareScopeOutside), nil},
		{"status", readme, func(in investigateInput, u string) []record.ContractComparison {
			x := clampComparison(in, u+":105-107")
			x.Status = "consistent"
			return []record.ContractComparison{x}
		}, cmpIssue(1, "status", record.CompareStatusInvalid), nil},
		{"empty reason", readme, func(in investigateInput, u string) []record.ContractComparison {
			x := clampComparison(in, u+":105-107")
			x.Reason = ""
			return []record.ContractComparison{x}
		}, cmpIssue(1, "reason", record.CompareReasonInvalid), nil},
		{"reason not in Korean", readme, func(in investigateInput, u string) []record.ContractComparison {
			x := clampComparison(in, u+":105-107")
			x.Reason = "負數輸入時返回零值"
			return []record.ContractComparison{x}
		}, cmpIssue(1, "reason", record.InsightLanguageError), nil},
		{"reason claims a run", readme, func(in investigateInput, u string) []record.ContractComparison {
			x := clampComparison(in, u+":105-107")
			x.Reason = "테스트를 실행해 음수 입력의 결과를 재현했다."
			return []record.ContractComparison{x}
		}, cmpIssue(1, "reason", record.ErrExecutionClaim), nil},
		{"document cited with a range", readme, func(in investigateInput, u string) []record.ContractComparison {
			x := clampComparison(in, u+":105-107")
			x.DocumentEvidenceIDs = []string{x.DocumentEvidenceIDs[0] + ":3-3"}
			return []record.ContractComparison{x}
		}, cmpIssue(1, "document_evidence_ids", record.CompareDocumentsChanged), nil},
		{"match without code", readme, func(in investigateInput, u string) []record.ContractComparison {
			return []record.ContractComparison{clampComparison(in)}
		}, cmpIssue(1, "code_evidence_ids", record.CompareCodeMissing), nil},
		{"unknown code ID", readme, func(in investigateInput, u string) []record.ContractComparison {
			return []record.ContractComparison{clampComparison(in, "ev_"+strings.Repeat("0", 24))}
		}, cmpIssue(1, "code_evidence_ids", record.CompareCodeInvalid), nil},
		{"same range twice", readme, func(in investigateInput, u string) []record.ContractComparison {
			return []record.ContractComparison{clampComparison(in, u+":105-107", u+":105-107")}
		}, cmpIssue(1, "code_evidence_ids", record.CompareCodeDuplicate), []string{limCitationListTrimmed}},
		{"five ranges", readme, func(in investigateInput, u string) []record.ContractComparison {
			return []record.ContractComparison{clampComparison(in, u+":4-4", u+":10-10", u+":20-20", u+":30-30", u+":105-107")}
		}, cmpIssue(1, "code_evidence_ids", record.CompareCodeTooMany), []string{limCitationListTrimmed}},
		{"code outside the scope", record.Contract{Statement: readme.Statement, AppliesTo: "pkg/limit"}, func(in investigateInput, u string) []record.ContractComparison {
			x := clampComparison(in, unitOf(t, in, "cmd/limit/main.go").ID+":5-5")
			x.AppliesTo = "pkg/limit"
			return []record.ContractComparison{x}
		}, cmpIssue(1, "code_evidence_ids", record.CompareCodeOutsideScope), nil},
		{"range longer than 80 lines", readme, func(in investigateInput, u string) []record.ContractComparison {
			return []record.ContractComparison{clampComparison(in, u+":4-109")}
		}, cmpIssue(1, "code_evidence_ids", record.CompareCodeRangeInvalid), []string{limCitationUnspecified}},
		{"long unit without a range", readme, func(in investigateInput, u string) []record.ContractComparison {
			return []record.ContractComparison{clampComparison(in, u)}
		}, cmpIssue(1, "code_evidence_ids", record.CompareCodeRangeMissing), []string{limCitationUnspecified}},
		{"seven comparisons", readme, func(in investigateInput, u string) []record.ContractComparison {
			var out []record.ContractComparison
			for range 7 {
				out = append(out, clampComparison(in, u+":105-107"))
			}
			return out
		}, cmpIssue(7, "contract_comparisons", record.CompareTooMany), nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			run := runComparisons(t, limitTree(), "README.md", c.contract, Budget{}, func(in investigateInput) map[string]any {
				u := unitOf(t, in, limitPath)
				return map[string]any{"candidates": []record.CandidateText{billingCandidate("음수 처리", u.ID+":105-107")},
					"contract_comparisons": c.build(in, u.ID)}
			})
			if c.resolved != nil {
				requireComparisonResolved(t, run, c.want, c.resolved)
				return
			}
			requireComparisonOmitted(t, run, c.want)
		})
	}
}

// requireComparisonOmitted checks the repair named want, the repaired
// output (the same, still invalid) left the comparison out, and the report
// shows the contract unchecked with the gap while the review counts.
func requireComparisonOmitted(t *testing.T, run comparisonRun, want itemIssue) {
	t.Helper()
	requireComparisonRepaired(t, run, want)
	if calls := run.inv.Calls; len(calls) != 2 || !slices.Contains(calls[0].OutputErrors, want.Error) || calls[1].Outcome != record.OutcomeAccepted {
		t.Fatalf("calls %+v", calls)
	}
	if len(run.inv.Candidates) != 1 || slices.ContainsFunc(run.inv.ContractComparisons, func(x record.ContractComparison) bool { return x.ContractIndex == 1 && want.Index == 1 }) ||
		!slices.Contains(run.inv.Limitations, record.LimContractComparisonOmitted) {
		t.Fatalf("investigate kept %d candidates, comparisons %+v, limitations %v", len(run.inv.Candidates), run.inv.ContractComparisons, run.inv.Limitations)
	}
	requireReviewCounted(t, run.report)
	d := run.report.Documents
	if d == nil || len(d.Comparisons) != 1 || (want.Index == 1 && d.Comparisons[0].Status != "unknown") {
		t.Fatalf("report comparisons %+v", d)
	}
	if !slices.ContainsFunc(run.report.Gaps, func(g report.Gap) bool {
		return g.Section == report.SectionDocuments && g.Code == record.LimContractComparisonOmitted
	}) {
		t.Fatalf("report gaps %+v", run.report.Gaps)
	}
}

// requireComparisonRepaired checks the investigation succeeded and its one
// repair named want.
func requireComparisonRepaired(t *testing.T, run comparisonRun, want itemIssue) {
	t.Helper()
	if s := run.results["review.investigate"]; s.Status != domain.TaskSucceeded {
		t.Fatalf("investigate %+v", s)
	}
	if len(run.repairs) != 2 || run.repairs[0] != nil || run.repairs[1] == nil ||
		!slices.Contains(run.repairs[1].OutputErrors, want.Error) || !hasIssue(run.repairs[1].Items, want) {
		t.Fatalf("repair blocks %+v, want %+v", run.repairs, want)
	}
}

// requireComparisonResolved checks the repair named want, the repaired
// output (the same) kept the comparison with its citations resolved and
// the limitations lims, and the report shows the contract compared.
func requireComparisonResolved(t *testing.T, run comparisonRun, want itemIssue, lims []string) {
	t.Helper()
	requireComparisonRepaired(t, run, want)
	calls := run.inv.Calls
	if len(calls) != 2 || calls[1].Outcome != record.OutcomeAccepted || !slices.Contains(calls[1].OutputErrors, want.Error) {
		t.Fatalf("calls %+v", calls)
	}
	if len(run.inv.ContractComparisons) != 1 || slices.Contains(run.inv.Limitations, record.LimContractComparisonOmitted) ||
		!slices.Contains(run.inv.Limitations, record.LimOutputDegraded) {
		t.Fatalf("comparisons %+v, limitations %v", run.inv.ContractComparisons, run.inv.Limitations)
	}
	for _, lim := range lims {
		if !slices.Contains(run.inv.Limitations, lim) {
			t.Fatalf("limitations %v, want %s", run.inv.Limitations, lim)
		}
	}
	x, catalog := run.inv.ContractComparisons[0], catalogOf(run.inv.Evidence)
	if len(x.CodeEvidenceIDs) == 0 || len(x.CodeEvidenceIDs) > record.MaxComparisonCodeEvidence {
		t.Fatalf("comparison %+v", x)
	}
	for i, id := range x.CodeEvidenceIDs {
		if _, ok := catalog[id]; !ok || slices.Contains(x.CodeEvidenceIDs[:i], id) {
			t.Fatalf("comparison cites %v", x.CodeEvidenceIDs)
		}
	}
	requireReviewCounted(t, run.report)
	if d := run.report.Documents; d == nil || len(d.Comparisons) != 1 || d.Comparisons[0].Status != "match" {
		t.Fatalf("report comparisons %+v", d)
	}
}

// The comparison codes the worker alone decides: a slice output and an
// implementation outside the documented API's body.
func TestComparisonSliceAndAPIBodyCodes(t *testing.T) {
	s := &roleStage{slice: &record.ScopeSlice{}}
	got := s.comparisonProblems(record.ContractComparison{}, nil, nil, nil, nil, nil, map[int]bool{})
	if !slices.Equal(got, []record.FieldProblem{{Field: "contract_comparisons", Code: record.CompareInSlice}}) {
		t.Fatalf("slice comparison: %+v", got)
	}
	// The other overload's line is code of the same file, outside the
	// documented method's body.
	run := runComparisons(t, map[string]string{javaDocSource: javaDocBody}, javaDocSource,
		record.Contract{Statement: "choose는 null 입력에 missing을 반환한다.", AppliesTo: javaDocPackage}, Budget{},
		func(in investigateInput) map[string]any {
			u := unitOf(t, in, javaDocSource)
			x := record.ContractComparison{ContractIndex: 1, AppliesTo: javaDocPackage, Status: "match", Reason: "널 입력에서 missing을 반환한다.",
				DocumentEvidenceIDs: in.Documents.Contracts[0].EvidenceIDs, CodeEvidenceIDs: []string{fmt.Sprintf("%s:%d-%d", u.ID, 23, 23)}}
			return map[string]any{"candidates": []record.CandidateText{billingCandidate("널 처리", fmt.Sprintf("%s:14-17", u.ID))}, "contract_comparisons": []record.ContractComparison{x}}
		})
	requireComparisonOmitted(t, run, cmpIssue(1, "code_evidence_ids", record.CompareCodeOutsideAPI))
	// The whole short file holds the Javadoc: as the only code evidence it
	// is the document itself.
	run = runComparisons(t, map[string]string{javaDocSource: javaDocBody}, javaDocSource,
		record.Contract{Statement: "choose는 null 입력에 missing을 반환한다.", AppliesTo: javaDocPackage}, Budget{},
		func(in investigateInput) map[string]any {
			u := unitOf(t, in, javaDocSource)
			x := record.ContractComparison{ContractIndex: 1, AppliesTo: javaDocPackage, Status: "match", Reason: "널 입력에서 missing을 반환한다.",
				DocumentEvidenceIDs: in.Documents.Contracts[0].EvidenceIDs, CodeEvidenceIDs: []string{u.ID}}
			return map[string]any{"candidates": []record.CandidateText{billingCandidate("널 처리", fmt.Sprintf("%s:14-17", u.ID))}, "contract_comparisons": []record.ContractComparison{x}}
		})
	requireComparisonOmitted(t, run, cmpIssue(1, "code_evidence_ids", record.CompareCodeIsDocument))
}

// inSourceTree is a project whose API is documented in its own source file:
// implementation holds the document and the body line containing code.
type inSourceTree struct {
	name, implementation, code, scope string
	tree                              map[string]string
}

// inSourceTrees are a Javadoc, a docstring a Sphinx page names and the
// rustdoc of a re-exported function, each in a unit that also holds the
// document.
func inSourceTrees() []inSourceTree {
	pythonBody := "def measure(kind: str, compact: bool = False) -> str:\n    \"\"\"Return the given kind unless the compact option is requested.\n\n    Return \"short\" when compact is True.\n    \"\"\"\n    if compact:\n        return \"short\"\n    return kind\n"
	rustBody := "// implementation\n/// Measure the specified kind.\n/// A blank kind returns no reading.\npub fn decode(kind: &str) -> Option<u8> {\n    if kind.is_empty() {\n        return None;\n    }\n    Some(kind.len() as u8)\n}\n"
	return []inSourceTree{
		{"Java Javadoc", javaDocSource, "if (text == null)", javaDocPackage, map[string]string{javaDocSource: javaDocBody}},
		{"Python docstring", "src/gauge/measurements.py", "return \"short\"", ".", map[string]string{
			"docs/reference.rst":        "Measurement\n===========\n\n" + strings.Repeat("Usage notes.\n\n", 30) + ".. currentmodule:: gauge\n\n.. autofunction:: measure\n",
			"pyproject.toml":            "[project]\nname = 'gauge'\n",
			"src/gauge/__init__.py":     "from .measurements import measure\n",
			"src/gauge/measurements.py": strings.Repeat("# library declarations\n", 60) + pythonBody,
		}},
		{"Rust rustdoc", "src/reading.rs", "return None;", ".", map[string]string{
			"README.md":      "# Measurement\n\n```rust\nuse survey_kit::measure;\nlet value = measure(\"wide\");\n```\n",
			"Cargo.toml":     "[package]\nname = \"survey-kit\"\nversion = \"0.1.0\"\n",
			"src/lib.rs":     "mod reading;\npub use crate::reading::{decode as measure};\n",
			"src/reading.rs": strings.Repeat("// library declarations\n", 60) + rustBody,
		}},
	}
}

// An API documented in its own source file (Javadoc, a docstring a guide
// names, rustdoc a guide names) is compared by citing its body lines inside
// the file's unit, which also holds the document. The comparison keeps the
// narrowed range: the record stores it (origin cited) beside its unit and
// the report's comparison points at exactly those lines.
func TestComparisonOfInSourceDocumentKeepsCitedLines(t *testing.T) {
	for _, c := range inSourceTrees() {
		t.Run(c.name, func(t *testing.T) {
			var cited record.Excerpt
			run := runComparisons(t, c.tree, c.implementation, record.Contract{Statement: "문서가 밝힌 조건에서 정해진 값을 반환한다.", AppliesTo: c.scope}, Budget{},
				func(in investigateInput) map[string]any {
					u := unitOf(t, in, c.implementation)
					doc := in.Documents.Evidence[slices.IndexFunc(in.Documents.Evidence, func(d docEvidence) bool { return d.Path == c.implementation })]
					line := lineOf(t, u, c.code)
					if u.End-u.Start+1 > record.WholeCitationLines || doc.StartLine < u.Start || doc.EndLine > u.End || line <= doc.EndLine {
						t.Fatalf("the unit %d-%d does not hold the document %d-%d and the body line %d", u.Start, u.End, doc.StartLine, doc.EndLine, line)
					}
					cited = record.Narrow(u, in.Scope.Snapshot, line, line)
					x := record.ContractComparison{ContractIndex: 1, AppliesTo: c.scope, Status: "match", Reason: "문서가 밝힌 조건의 분기와 반환값이 있다.",
						DocumentEvidenceIDs: in.Documents.Contracts[0].EvidenceIDs, CodeEvidenceIDs: []string{fmt.Sprintf("%s:%d-%d", u.ID, line, line)}}
					return map[string]any{"candidates": []any{}, "contract_comparisons": []record.ContractComparison{x}}
				})
			if s := run.results["review.investigate"]; s.Status != domain.TaskSucceeded || len(run.inv.Calls) != 1 {
				t.Fatalf("investigate %+v, calls %+v", s, run.inv.Calls)
			}
			if len(run.inv.ContractComparisons) != 1 || !slices.Equal(run.inv.ContractComparisons[0].CodeEvidenceIDs, []string{cited.ID}) {
				t.Fatalf("comparison %+v, want code %s", run.inv.ContractComparisons, cited.ID)
			}
			stored := catalogOf(run.inv.Evidence)[cited.ID]
			if stored.Origin != record.OriginCited || stored.Start != cited.Start || stored.End != cited.End {
				t.Fatalf("stored cited range %+v", stored)
			}
			requireReviewCounted(t, run.report)
			for _, withCode := range []bool{true, false} {
				raw, err := report.Marshal(report.Project(run.report, withCode))
				if err != nil {
					t.Fatal(err)
				}
				r, err := report.Unmarshal(raw)
				if err != nil {
					t.Fatal(err)
				}
				if len(r.Documents.Comparisons) != 1 || r.Documents.Comparisons[0].Status != "match" ||
					!slices.Equal(r.Documents.Comparisons[0].CodeEvidenceIDs, []string{cited.ID}) {
					t.Fatalf("report comparison %+v", r.Documents.Comparisons)
				}
				i := slices.IndexFunc(r.Documents.Evidence, func(e report.SectionEvidence) bool { return e.ID == cited.ID })
				if i < 0 {
					t.Fatalf("report evidence lacks %s", cited.ID)
				}
				e := r.Documents.Evidence[i]
				if e.Path != c.implementation || e.Kind != "code" || e.StartLine != cited.Start || e.EndLine != cited.End {
					t.Fatalf("report evidence %+v, want lines %d-%d", e, cited.Start, cited.End)
				}
			}
		})
	}
}

// The comparison rules are evaluated on the cited lines, so two ranges of
// one unit are two pieces of evidence (they used to collapse into one
// duplicate ID).
func TestComparisonTwoRangesOfOneUnit(t *testing.T) {
	var ids []string
	run := runComparisons(t, limitTree(), "README.md", record.Contract{Statement: "Clamp는 음수 입력에서 0을 반환한다.", AppliesTo: "."}, Budget{},
		func(in investigateInput) map[string]any {
			u := unitOf(t, in, limitPath)
			ids = []string{record.Narrow(u, in.Scope.Snapshot, 4, 4).ID, record.Narrow(u, in.Scope.Snapshot, 105, 107).ID}
			return map[string]any{"candidates": []any{}, "contract_comparisons": []record.ContractComparison{clampComparison(in, u.ID+":4-4", u.ID+":105-107")}}
		})
	if len(run.inv.Calls) != 1 || len(run.inv.ContractComparisons) != 1 || !slices.Equal(run.inv.ContractComparisons[0].CodeEvidenceIDs, ids) {
		t.Fatalf("calls %+v comparisons %+v", run.inv.Calls, run.inv.ContractComparisons)
	}
	requireReviewCounted(t, run.report)
}

// Whether a comparison is resolved depends on whether the format repair is
// spent, not on whether the request carries a repair block: after a reply
// that is not JSON the repair is still unused (the comparison is named in a
// repair first); after a repair spent on a candidate a newly faulty
// citation is resolved.
func TestComparisonOmittedOnlyOnceTheRepairIsSpent(t *testing.T) {
	readme := record.Contract{Statement: "Clamp는 음수 입력에서 0을 반환한다.", AppliesTo: "."}
	t.Run("not JSON first", func(t *testing.T) {
		n := 0
		run := runComparisons(t, limitTree(), "README.md", readme, Budget{}, func(in investigateInput) opencode.Reply {
			n++
			u := unitOf(t, in, limitPath)
			if n == 1 {
				return opencode.Reply{Reason: "model_output_invalid"}
			}
			return asJSON(map[string]any{"candidates": []record.CandidateText{billingCandidate("음수 처리", u.ID+":105-107")},
				"contract_comparisons": []record.ContractComparison{clampComparison(in, u.ID)}})
		})
		calls := run.inv.Calls
		if len(calls) != 3 || calls[1].Outcome != "output_rejected" || calls[2].Outcome != record.OutcomeAccepted || !slices.Contains(calls[1].OutputErrors, record.CompareCodeRangeMissing) ||
			len(run.inv.ContractComparisons) != 1 || !slices.Contains(run.inv.Limitations, limCitationUnspecified) {
			t.Fatalf("calls %+v comparisons %+v limitations %v", calls, run.inv.ContractComparisons, run.inv.Limitations)
		}
	})
	t.Run("repair spent on a candidate", func(t *testing.T) {
		run := runComparisons(t, limitTree(), "README.md", readme, Budget{}, func(in investigateInput) map[string]any {
			u := unitOf(t, in, limitPath)
			if in.Repair == nil {
				return map[string]any{"candidates": []record.CandidateText{billingCandidate("음수 처리", u.ID)},
					"contract_comparisons": []record.ContractComparison{clampComparison(in, u.ID+":105-107")}}
			}
			return map[string]any{"candidates": []record.CandidateText{billingCandidate("음수 처리", u.ID+":105-107")},
				"contract_comparisons": []record.ContractComparison{clampComparison(in, u.ID)}}
		})
		// The repair's comparison cites the long unit without a range: it
		// stands for the whole unit.
		calls := run.inv.Calls
		if len(calls) != 2 || !slices.Equal(calls[0].OutputErrors, []string{errCitationMissing}) || calls[1].Outcome != record.OutcomeAccepted ||
			len(run.inv.Candidates) != 1 || len(run.inv.ContractComparisons) != 1 || slices.Contains(run.inv.Limitations, record.LimContractComparisonOmitted) ||
			!slices.Contains(run.inv.Limitations, limCitationUnspecified) {
			t.Fatalf("calls %+v comparisons %+v limitations %v", calls, run.inv.ContractComparisons, run.inv.Limitations)
		}
	})
	t.Run("candidate still without a range", func(t *testing.T) {
		run := runComparisons(t, limitTree(), "README.md", readme, Budget{}, func(in investigateInput) map[string]any {
			u := unitOf(t, in, limitPath)
			return map[string]any{"candidates": []record.CandidateText{billingCandidate("음수 처리", u.ID)},
				"contract_comparisons": []record.ContractComparison{clampComparison(in, u.ID)}}
		})
		// A candidate whose location has no usable range after the repair is
		// left out; the role and its comparison stay.
		if s := run.results["review.investigate"]; s.Status != domain.TaskSucceeded || len(run.inv.Candidates) != 0 ||
			!slices.Contains(run.inv.Limitations, limCandidateOmitted) || len(run.inv.ContractComparisons) != 1 {
			t.Fatalf("investigate %+v, candidates %d, comparisons %+v, limitations %v", s, len(run.inv.Candidates), run.inv.ContractComparisons, run.inv.Limitations)
		}
		// The report does not read the left-out candidate as an absence of
		// findings: a code review gap names it and the verdict stays
		// inconclusive.
		r := run.report
		if r.QualityVerdict == report.VerdictNoActionable || !slices.ContainsFunc(r.Gaps, func(g report.Gap) bool {
			return g.Section == report.SectionCodeReview && g.Code == limCandidateOmitted
		}) {
			t.Fatalf("report verdict %s, gaps %+v", r.QualityVerdict, r.Gaps)
		}
	})
}

// At the smallest request six rejected comparisons still leave room for the
// repair request: the block names as many as fit.
func TestComparisonRepairFitsTheSmallestRequest(t *testing.T) {
	var contracts []record.Contract
	script := coverageScript(t)
	j := treeJob(t, limitTree())
	var sizes []int
	script["documents"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[docsInput](t, q)
		for range 6 {
			contracts = append(contracts, record.Contract{Statement: "Clamp는 음수 입력에서 0을 반환한다.", AppliesTo: ".", EvidenceIDs: []string{docID(in, "README.md")}})
		}
		return asJSON(docsOutput{Contracts: contracts, Conflicts: []record.Conflict{}, Questions: []record.Question{}, InstructionNotes: []record.InstructionNote{}})
	}
	var repaired *outputRepair
	script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		if !in.Scope.WholeRun {
			return asJSON(map[string]any{"candidates": []any{}})
		}
		sizes = append(sizes, len(opencode.Prompt(q)))
		if in.Repair != nil {
			repaired = in.Repair
		}
		var out []record.ContractComparison
		for i := range 6 {
			x := clampComparison(in, "ev_"+strings.Repeat("1", 24))
			x.ContractIndex, x.Reason, x.DocumentEvidenceIDs = i+1, strings.Repeat("가", 120), []string{"ev_" + strings.Repeat("2", 24)}
			out = append(out, x)
		}
		return asJSON(map[string]any{"candidates": []any{}, "contract_comparisons": out})
	}
	script["verify"] = func(_ int, q opencode.Request) opencode.Reply {
		return answerAll(decodeAs[verifyInput](t, q), func(c record.Candidate) []string { return []string{c.Evidence[0]} })
	}
	b := Budget{RequestBytes: MinRequestBytes, OutputTokens: MinOutputTokens}
	_, results := runRoleWith(t, j, Handler{Static: vetStub{}, Caller: &scriptedCaller{script: script}, Budget: b}, "1.27.4")
	if s := results["review.investigate"]; s.Status != domain.TaskSucceeded {
		t.Fatalf("investigate %+v", s)
	}
	if repaired == nil || len(repaired.Items) == 0 || len(sizes) != 2 || slices.Max(sizes) > b.limits().request {
		t.Fatalf("repair %+v, request sizes %v (limit %d)", repaired, sizes, b.limits().request)
	}
	t.Logf("repair block named %d of the rejections; requests %v bytes", len(repaired.Items), sizes)
}

// A comparison's cited range is an investigate excerpt like a strength's:
// verify does not resend it without its unit, and when the declaration
// verify reads is exactly that range it sends and records the upstream
// entry (the report keeps verify).
func TestVerifyDeclarationThatIsAComparisonRange(t *testing.T) {
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
	tree := map[string]string{"go.mod": "module example.org/billing\n\ngo 1.23\n", "pay.go": b.String(),
		"README.md": "# Billing\n\nCharge clamps every step.\n", "other.go": goFileOf(154, "\tn = clamp(n + %d)\n")}
	var unit record.Excerpt
	run := runComparisons(t, tree, "README.md", record.Contract{Statement: "Charge는 매 단계 값을 제한한다.", AppliesTo: "."}, Budget{},
		func(in investigateInput) map[string]any {
			unit = unitOf(t, in, "pay.go")
			other := unitOf(t, in, "other.go")
			x := record.ContractComparison{ContractIndex: 1, AppliesTo: ".", Status: "match", Reason: "매 단계 clamp를 호출한다.",
				DocumentEvidenceIDs: in.Documents.Contracts[0].EvidenceIDs, CodeEvidenceIDs: []string{unit.ID + ":3-41", other.ID + ":5-13"}}
			return map[string]any{"candidates": []record.CandidateText{billingCandidate("누적 보정", unit.ID+":10-20")}, "contract_comparisons": []record.ContractComparison{x}}
		})
	if s := run.results["review.verify"]; s.Status != domain.TaskSucceeded {
		t.Fatalf("verify %+v", s)
	}
	requireReviewCounted(t, run.report)
	inv := requireUpstreamEntries(t, run.j, record.StageInvestigate)
	if len(inv.ContractComparisons) != 1 {
		t.Fatalf("the comparison was not kept: %v", inv.Limitations)
	}
	v := requireUpstreamEntries(t, run.j, record.StageVerify, record.StageInvestigate)
	declaration := inv.ContractComparisons[0].CodeEvidenceIDs[0]
	if !slices.ContainsFunc(v.Evidence, func(e record.Excerpt) bool { return e.ID == declaration && e.Origin == record.OriginCited }) {
		t.Fatalf("verify did not read the comparison's range as the declaration: %+v", v.Evidence)
	}
	if slices.ContainsFunc(v.Evidence, func(e record.Excerpt) bool { return e.Path == "other.go" }) {
		t.Fatalf("verify sent a range of other.go without its unit: %+v", v.Evidence)
	}
}

// The contract follow-up reads the overview comparison's code evidence as
// context. A narrowed range (origin cited) whose unit the follow-up does not
// send is left out, as verify leaves out a strength's range: a record keeps
// a cited range only beside the excerpt it lies in, so sending it alone made
// the follow-up fail with review_record_failed.
func TestContractFollowupReadsARangedPriorComparison(t *testing.T) {
	j := contractExampleJob(t, nil)
	contractExampleInput(t, j)
	doc, inv := readRecord(t, j, record.StageDocuments), readRecord(t, j, record.StageInvestigate)
	doc.Conflicts = nil
	c := doc.Contracts[0]
	i := slices.IndexFunc(inv.Evidence, func(e record.Excerpt) bool { return e.Path == "svc/a/normalize.go" })
	if i < 0 || !record.ScopeUnder("svc/a", c.AppliesTo) {
		t.Fatalf("fixture: %+v %q", inv.Evidence, c.AppliesTo)
	}
	cited := record.Narrow(inv.Evidence[i], j.Snapshot.Digest, 8, 9)
	cited.Text = ""
	inv.Evidence = append(inv.Evidence, cited)
	inv.Candidates, inv.Strengths = nil, nil
	inv.ContractComparisons = []record.ContractComparison{{
		ContractIndex: 1, AppliesTo: c.AppliesTo, Status: "unknown",
		Reason: "호출 실패 뒤 재시도 횟수와 반환 경로를 확인해야 한다.", DocumentEvidenceIDs: c.EvidenceIDs, CodeEvidenceIDs: []string{cited.ID},
	}}
	const focusPath = "gateway.go"
	entry := (&roleStage{j: j}).entries()[focusPath]
	inv.ContractProbe = &record.ContractProbe{ContractIndex: 1, Candidates: []record.ContractLocation{{
		ID:   record.ContractLocationID(j.Snapshot.Digest, focusPath, entry.SHA256, 4, 16),
		Path: focusPath, SHA256: entry.SHA256, Start: 4, End: 16, Kind: "code", Symbol: "Forward",
	}}}
	inv.Evidence = slices.DeleteFunc(inv.Evidence, func(e record.Excerpt) bool { return e.Path == focusPath })
	if _, err := persistRecord(j, doc); err != nil {
		t.Fatal(err)
	}
	for k, up := range inv.Upstream {
		if up.Source == "review.documents" {
			inv.Upstream[k].SHA256 = digestOf(mustRead(t, recordPath(j, record.StageDocuments)))
		}
	}
	if _, err := persistRecord(j, inv); err != nil {
		t.Fatal(err)
	}
	f := &scriptedCaller{script: map[string]func(int, opencode.Request) opencode.Reply{
		record.StageContract: func(_ int, q opencode.Request) opencode.Reply {
			in := decodeAs[contractReviewInput](t, q)
			if slices.ContainsFunc(in.Scope.Excerpts, func(e record.Excerpt) bool { return e.Path == "svc/a/normalize.go" }) {
				t.Errorf("the prior range was sent without its unit: %+v", in.Scope.Excerpts)
			}
			return asJSON(map[string]any{"contract_comparisons": []record.ContractComparison{exampleContractReply(t, q)}})
		},
	}}
	res := (Handler{Static: vetStub{}, Caller: f, MaxCalls: record.WholeRunMaxCalls}).Run(t.Context(), j, analysis.TaskSpec{Key: record.TaskContract}, nil)
	if res.Status != domain.TaskSucceeded {
		t.Fatalf("contract %+v", res)
	}
	final := readRecord(t, j, record.StageContract)
	if !final.Accepted() || len(final.ContractComparisons) != 1 || final.ContractComparisons[0].Status != "match" {
		t.Fatalf("follow-up record %+v", final)
	}
}

// dryRunUpstream answers role with scripts/fake_model_upstream.py, the
// placeholder upstream of dry runs; rates are its --shape-errors and
// --integrity-errors (none: 0).
func dryRunUpstream(t *testing.T, python, role string, q opencode.Request, rates ...float64) opencode.Reply {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "..", "scripts"))
	if err != nil {
		t.Fatal(err)
	}
	rates = append(rates, 0, 0)
	cmd := exec.CommandContext(t.Context(), python, "-I", "-B", "-c",
		"import json, sys\nsys.path.insert(0, sys.argv[1])\nimport fake_model_upstream as f\n"+
			"print(json.dumps(f.result_for(sys.argv[2], json.load(sys.stdin.buffer), None, float(sys.argv[3]), float(sys.argv[4]))))",
		dir, role, fmt.Sprint(rates[0]), fmt.Sprint(rates[1]))
	cmd.Stdin = bytes.NewReader(q.Input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("fake upstream %s: %v %s", role, err, stderr.String())
	}
	return opencode.Reply{Output: bytes.TrimSpace(out)}
}

// The dry-run upstream's documents and overview answers pass the product's
// checks: an in-source document is compared on one body line of the
// documented declaration, kept as a cited range, on the first call; an
// even-numbered contract citing a long unit without a range is rejected,
// repaired with its position, field and excerpt, then kept standing for the
// whole unit (citation_range_unspecified), and the report shows it compared.
func TestDryRunUpstreamComparisons(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("dry runs with scripts/fake_model_upstream.py are a Linux procedure")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	long := inSourceTree{name: "Go long unit", tree: limitTree()}
	long.tree["pkg/limit/README.md"] = "# Limit package\n\nClamp never returns a negative value.\n"
	for _, c := range append(inSourceTrees(), long) {
		t.Run(c.name, func(t *testing.T) {
			j := treeJob(t, c.tree)
			script := coverageScript(t)
			var repairs []*outputRepair
			for _, role := range []string{record.StageDocuments, record.StageInvestigate} {
				script[role] = func(_ int, q opencode.Request) opencode.Reply {
					if role == record.StageInvestigate {
						if in := decodeAs[investigateInput](t, q); in.Scope.WholeRun {
							repairs = append(repairs, in.Repair)
						}
					}
					return dryRunUpstream(t, python, role, q)
				}
			}
			r, results := reportAt(t, &j, secretsStub{}, &scriptedCaller{script: script}, Budget{}, &exampleIDs{n: 0xa10})
			doc, inv := readRecord(t, j, record.StageDocuments), readRecord(t, j, record.StageInvestigate)
			if s := results["review.investigate"]; s.Status != domain.TaskSucceeded || len(doc.Contracts) < 2 {
				t.Fatalf("investigate %+v, %d contracts", s, len(doc.Contracts))
			}
			requireReviewCounted(t, r)
			if c.implementation == "" {
				// The second contract cites the long unit without a range on
				// both calls: after the repair it stands for the whole unit.
				want := cmpIssue(2, "code_evidence_ids", record.CompareCodeRangeMissing)
				if len(repairs) != 2 || repairs[1] == nil || len(repairs[1].Items) != 1 || !hasIssue(repairs[1].Items, want) || repairs[1].Items[0].ID == "" ||
					len(inv.ContractComparisons) != 2 || slices.Contains(inv.Limitations, record.LimContractComparisonOmitted) ||
					!slices.Contains(inv.Limitations, limCitationUnspecified) || !slices.Contains(inv.Limitations, record.LimOutputDegraded) {
					t.Fatalf("repairs %+v, comparisons %+v, limitations %v", repairs, inv.ContractComparisons, inv.Limitations)
				}
				if unit := catalogOf(inv.Evidence)[inv.ContractComparisons[1].CodeEvidenceIDs[0]]; unit.Start != 1 || unit.End-unit.Start+1 <= record.WholeCitationLines {
					t.Fatalf("resolved comparison cites %+v", unit)
				}
				if d := r.Documents; d == nil || len(d.Comparisons) != 2 || d.Comparisons[0].Status != "match" || d.Comparisons[1].Status != "match" ||
					slices.ContainsFunc(r.Gaps, func(g report.Gap) bool { return g.Code == record.LimContractComparisonOmitted }) {
					t.Fatalf("report comparisons %+v, gaps %+v", r.Documents, r.Gaps)
				}
				return
			}
			if len(inv.Calls) != 1 || len(inv.ContractComparisons) != len(doc.Contracts) || slices.Contains(inv.Limitations, record.LimContractComparisonOmitted) {
				t.Fatalf("calls %+v, comparisons %+v, limitations %v", inv.Calls, inv.ContractComparisons, inv.Limitations)
			}
			documents, catalog := catalogOf(doc.Evidence), catalogOf(inv.Evidence)
			ranged := 0
			for _, x := range inv.ContractComparisons {
				if documents[x.DocumentEvidenceIDs[0]].Path != c.implementation {
					continue
				}
				e := catalog[x.CodeEvidenceIDs[0]]
				if x.Status != "match" || e.Path != c.implementation || e.Origin != record.OriginCited || e.Start != e.End {
					t.Fatalf("in-source comparison %+v cites %+v", x, e)
				}
				ranged++
			}
			if ranged == 0 {
				t.Fatalf("no comparison of the in-source document: %+v", inv.ContractComparisons)
			}
		})
	}
}
