package review

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

func TestAuthoredPublicAPIEvidenceReachesInvestigation(t *testing.T) {
	const rustGuide = "# Measurement\n\n```rust\nuse survey_kit::measure;\nlet value = measure(\"wide\");\n```\n"
	const rustModules = "mod reading;\npub use crate::reading::{decode as measure};\n"
	const rustBody = `// implementation
/// Measure the specified kind.
/// A blank kind returns no reading.
pub fn decode(kind: &str) -> Option<u8> {
    if kind.is_empty() {
        return None;
    }
    Some(kind.len() as u8)
}

#[cfg(test)]
mod checks {
    #[test]
    fn keeps_blank_empty() {
        assert_eq!(super::decode(""), None);
    }
}
`
	const pythonBody = `def measure(kind: str, compact: bool = False) -> str:
    """Return the given kind unless the compact option is requested.

    Return "short" when compact is True.
    """
    if compact:
        return "short"
    return kind
`
	cases := []struct {
		name, guide, implementation, testFile, class, statement, code, assertion string
		tree                                                                     map[string]string
	}{
		{name: "Sphinx function with local reexport",
			guide: "docs/reference.rst", implementation: "src/gauge/measurements.py", testFile: "tests/test_measurements.py",
			class: "python_docstring", statement: "`measure`는 compact 인자가 참이면 short를 반환한다.",
			code: "return \"short\"", assertion: "assert api.measure(\"wide\", compact=True) == \"short\"",
			tree: map[string]string{
				"docs/reference.rst":         "Measurement\n===========\n\n" + strings.Repeat("Usage notes.\n\n", 30) + ".. currentmodule:: gauge\n\n.. autofunction:: measure\n",
				"pyproject.toml":             "[project]\nname = 'gauge'\n",
				"src/gauge/__init__.py":      "from .measurements import measure\n",
				"src/gauge/measurements.py":  strings.Repeat("# library declarations\n", 60) + pythonBody,
				"tests/test_measurements.py": "import gauge as api\n" + strings.Repeat("# test setup\n", 60) + "\ndef test_compact():\n    assert api.measure(\"wide\", compact=True) == \"short\"\n",
			}},
		{name: "Rust public reexport and in-file unit test",
			guide: "README.md", implementation: "src/reading.rs", testFile: "src/reading.rs",
			class: "rustdoc", statement: "빈 kind에서 measure는 None을 반환한다.",
			code: "return None;", assertion: "assert_eq!(super::decode(\"\"), None);",
			tree: map[string]string{
				"README.md":      rustGuide,
				"Cargo.toml":     "[package]\nname = \"survey-kit\"\nversion = \"0.1.0\"\n",
				"src/lib.rs":     rustModules,
				"src/reading.rs": strings.Repeat("// library declarations\n", 60) + rustBody,
			}},
		{name: "Rust documented reexport without a README example",
			guide: "README.md", implementation: "src/reading.rs", testFile: "src/reading.rs",
			class: "rustdoc", statement: "빈 kind에서 measure는 None을 반환한다.",
			code: "return None;", assertion: "assert_eq!(super::decode(\"\"), None);",
			tree: map[string]string{
				"README.md":      "# Survey\n\nCollects and formats measurements.\n",
				"Cargo.toml":     "[package]\nname = \"survey-kit\"\nversion = \"0.1.0\"\n",
				"src/lib.rs":     rustModules,
				"src/reading.rs": strings.Repeat("// library declarations\n", 60) + rustBody,
			}},
		{name: "Rust public reexport and integration assertion",
			guide: "README.md", implementation: "src/reading.rs", testFile: "tests/reading.rs",
			class: "rustdoc", statement: "빈 kind에서 measure는 None을 반환한다.",
			code: "return None;", assertion: "assert_eq!(measure(\"\"), None);",
			tree: map[string]string{
				"README.md":      rustGuide,
				"Cargo.toml":     "[package]\nname = \"survey-kit\"\nversion = \"0.1.0\"\n",
				"src/lib.rs":     rustModules,
				"src/reading.rs": strings.Repeat("// library declarations\n", 60) + strings.Split(rustBody, "\n#[cfg")[0],
				"tests/reading.rs": "use survey_kit::measure;\n" +
					strings.Repeat("// tests setup\n", 60) + "#[test]\nfn checks_blank() {\n    assert_eq!(measure(\"\"), None);\n}\n",
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			j := treeJob(t, tc.tree)
			script := maxScript(t)
			var canonical string
			script["documents"] = func(_ int, q opencode.Request) opencode.Reply {
				input := decodeAs[docsInput](t, q)
				for _, e := range input.Documents {
					if e.Path != tc.implementation || e.DirectivePath != tc.guide {
						continue
					}
					if e.Class != tc.class || e.StartLine <= 55 || strings.Contains(e.Text, tc.code) || strings.Contains(e.Text, tc.assertion) {
						t.Fatalf("expected source documentation only: %+v", e)
					}
					blob := includedFiles(j.Snapshot.Manifest)[e.Path]
					canonical = record.ExcerptID(j.Snapshot.Digest, e.Path, blob.SHA256, e.StartLine, e.EndLine)
					return asJSON(docsOutput{Contracts: []record.Contract{{Statement: tc.statement, AppliesTo: ".", EvidenceIDs: []string{e.EvidenceID}}},
						Conflicts: []record.Conflict{}, Questions: []record.Question{}, InstructionNotes: []record.InstructionNote{}})
				}
				t.Fatalf("no API documentation for %s from %s: %+v", tc.implementation, tc.guide, input.Documents)
				return opencode.Reply{}
			}
			script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
				input := decodeAs[investigateInput](t, q)
				if !input.Scope.WholeRun {
					return asJSON(map[string]any{"candidates": []any{}})
				}
				var implementation, assertion string
				for _, e := range input.Scope.Excerpts {
					// The narrowest windows (the selectors') come first.
					if implementation == "" && e.Path == tc.implementation && e.Kind == "code" && strings.Contains(e.Text, tc.code) {
						implementation = e.ID
					}
					if assertion == "" && e.Path == tc.testFile && strings.Contains(e.Text, tc.assertion) {
						assertion = e.ID
					}
				}
				if implementation == "" || assertion == "" || implementation == assertion {
					t.Fatalf("expected distinct implementation and static test windows: %+v", input.Scope.Excerpts)
				}
				return asJSON(map[string]any{"candidates": []any{}, "contract_comparisons": []record.ContractComparison{{
					ContractIndex: 1, AppliesTo: ".", Status: "match", Reason: "문서 조건에 해당하는 분기와 그 결과를 확인하는 테스트 단언이 있다.",
					DocumentEvidenceIDs: []string{canonical}, CodeEvidenceIDs: []string{implementation, assertion},
				}}})
			}
			h := Handler{Budget: smallBudget, Static: vetStub{}, Caller: &scriptedCaller{script: script}}
			plan, results := runRoleWith(t, j, h, "1.27.4")
			if results["review.investigate"].Status != domain.TaskSucceeded {
				t.Fatalf("review.investigate: %+v", results["review.investigate"])
			}
			r, err := report.Build(exampleInput(t, j, h, plan, results, &exampleIDs{n: 2}), report.DefaultRegistry())
			if err != nil {
				t.Fatal(err)
			}
			for _, includeCode := range []bool{true, false} {
				raw, err := report.Marshal(report.Project(r, includeCode))
				if err != nil {
					t.Fatal(err)
				}
				reread, err := report.Unmarshal(raw)
				if err != nil || len(reread.Documents.Comparisons) != 1 ||
					!slices.ContainsFunc(reread.Documents.Evidence, func(e report.SectionEvidence) bool {
						return e.ID == canonical && e.Path == tc.implementation && e.DirectivePath == tc.guide
					}) {
					t.Fatalf("report lost published source provenance: %+v %v", reread.Documents, err)
				}
			}
		})
	}
}

func TestAuthoredPublicAPIBindingRejectsAmbiguityAndScope(t *testing.T) {
	const p = "docs/api.rst"
	const rust = "README.md"
	const rustText = "~~~rust\nuse garden::weight;\nlet value = weight(3);\n~~~\n"
	const declaration = "/// Report weight.\npub fn weight(value: i32) -> i32 { value }\n"
	cases := []struct {
		name, guide string
		tree        map[string]string
		scope       domain.Scope
	}{
		{"Sphinx duplicate modules", p, map[string]string{p: ".. autofunction:: garden.values.weight\n",
			"garden/values.py": "def weight(x):\n    \"Weight.\"\n    return x\n", "src/garden/values.py": "def weight(x):\n    \"Another weight.\"\n    return -x\n",
		}, domain.Scope{}},
		{"Sphinx source outside paths", p, map[string]string{p: ".. autofunction:: garden.weight\n",
			"garden.py": "def weight(x):\n    \"Weight.\"\n    return x\n",
		}, domain.Scope{Kind: "paths", Paths: []string{"docs"}}},
		{"Rust conditional module", rust, map[string]string{rust: rustText, "Cargo.toml": "[package]\nname = 'garden'\n",
			"src/lib.rs": "#[cfg(feature = \"platform\")]\nmod implementation;\npub use implementation::weight;\n", "src/implementation.rs": declaration,
		}, domain.Scope{}},
		{"Rust ambiguous module files", rust, map[string]string{rust: rustText, "Cargo.toml": "[package]\nname = 'garden'\n",
			"src/lib.rs": "mod implementation;\npub use implementation::weight;\n", "src/implementation.rs": declaration, "src/implementation/mod.rs": declaration,
		}, domain.Scope{}},
		{"Rust source outside paths", rust, map[string]string{rust: rustText, "Cargo.toml": "[package]\nname = 'garden'\n",
			"src/lib.rs": declaration,
		}, domain.Scope{Kind: "paths", Paths: []string{"README.md", "Cargo.toml"}}},
		{"Rust fence in HTML comment", rust, map[string]string{rust: "<!--\n" + rustText + "-->\n", "Cargo.toml": "[package]\nname = 'garden'\n",
			"src/lib.rs": declaration,
		}, domain.Scope{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			j := treeJob(t, tc.tree)
			j.Scope = tc.scope
			page := includedFiles(j.Snapshot.Manifest)[tc.guide]
			n := len(strings.Split(strings.TrimSuffix(tc.tree[tc.guide], "\n"), "\n"))
			class := envdoc.ClassDesign
			if tc.guide == rust {
				class = envdoc.ClassReadme
			}
			s := &roleStage{j: j, ctx: context.Background(), h: Handler{Budget: smallBudget}}
			out, err := s.documentExcerpts([]envdoc.Doc{{Path: tc.guide, Class: class, AppliesTo: ".", SHA256: &page.SHA256, LineCount: &n}})
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range out.excerpts {
				if e.DirectivePath != "" {
					t.Fatalf("unsafe API binding: %+v", e)
				}
			}
		})
	}
}

func TestRustPublicReexportWithoutExampleUsesBoundedSample(t *testing.T) {
	const page = "README.md"
	const first = "src/mappings.rs"
	files := map[string]string{
		page:         "# Palette\n\nTransforms color components.\n",
		"Cargo.toml": "[package]\nname = 'palette-map'\n",
		"src/lib.rs": `mod mappings;
mod alternate;
pub(crate) use mappings::show_preview;
#[cfg(feature = "convert")]
pub use mappings::show_preview;
pub use mappings::same_name as check;
pub use alternate::same_name as check;
pub use mappings::{brighten, dim, blend};
`,
		first: `/// Return the brighter representation.
pub fn brighten(value: u8) -> u8 { value.saturating_add(10) }
/// Return the dimmer representation.
pub fn dim(value: u8) -> u8 { value.saturating_sub(10) }
/// Return an average representation.
pub fn blend(left: u8, right: u8) -> u8 { left / 2 + right / 2 }
/// Format a preview.
pub fn show_preview(value: u8) -> String { value.to_string() }
/// Return an unchanged number.
pub fn same_name(value: u8) -> u8 { value }
`,
		"src/alternate.rs": "/// Return a constant.\npub fn same_name(_: u8) -> u8 { 0 }\n",
	}
	j := treeJob(t, files)
	entry := includedFiles(j.Snapshot.Manifest)[page]
	lines := 3
	stage := &roleStage{j: j, ctx: context.Background(), h: Handler{Budget: smallBudget}}
	found, err := stage.documentExcerpts([]envdoc.Doc{{Path: page, Class: envdoc.ClassReadme, AppliesTo: ".", SHA256: &entry.SHA256, LineCount: &lines}})
	if err != nil {
		t.Fatal(err)
	}
	var rustdoc []record.Excerpt
	for _, excerpt := range found.excerpts {
		if excerpt.IsRustAPIDocument() {
			rustdoc = append(rustdoc, excerpt)
		}
	}
	if len(rustdoc) != 2 || rustdoc[0].Path != first || rustdoc[0].Start != 1 ||
		rustdoc[1].Path != first || rustdoc[1].Start != 3 || !slices.Contains(found.limitations, rustAPISampled) {
		t.Fatalf("expected a bounded sample of unambiguous public Rust functions: %+v, %v", rustdoc, found.limitations)
	}
}

func TestRustContractEvidenceKeepsDistinctImplementations(t *testing.T) {
	for _, tight := range []bool{false, true} {
		name := "keeps both implementations before the second assertion"
		if tight {
			name = "exposes the omitted implementation at the file limit"
		}
		t.Run(name, func(t *testing.T) {
			const filename = "src/conversion.rs"
			var pad string
			if tight {
				pad = strings.Repeat("    let _padding = \""+strings.Repeat("x", 128)+"\";\n", 8)
			}
			body := `/// A negative reading returns no value.
pub fn calibrated(reading: i32) -> Option<i32> {
` + pad + `    if reading < 0 { return None; }
    Some(reading)
}

/// Clamp a level at the upper limit.
pub fn limited(level: u16) -> u16 {
` + pad + `    if level > 90 { return 90; }
    level
}

#[cfg(test)]
mod checks {
    #[test]
    fn preserves_the_limit() {
        assert_eq!(super::limited(120), 90);
    }
}
`
			j := treeJob(t, map[string]string{
				"README.md":  "# Readings\n\nConverts sensor levels.\n",
				"Cargo.toml": "[package]\nname = 'sensor-readings'\n",
				"src/lib.rs": "mod conversion;\npub use self::conversion::{calibrated, limited};\n",
				filename:     body,
			})
			script := maxScript(t)
			var ids [2]string
			script["documents"] = func(_ int, q opencode.Request) opencode.Reply {
				in := decodeAs[docsInput](t, q)
				contracts := make([]record.Contract, len(ids))
				for _, ex := range in.Documents {
					if ex.Path != filename || ex.Class != "rustdoc" {
						continue
					}
					index, statement := 0, "`calibrated`는 음수 입력에서 None을 반환한다."
					if strings.Contains(ex.Text, "Clamp a level") {
						index, statement = 1, "`limited`는 90을 넘는 값을 90으로 제한한다."
					}
					entry := includedFiles(j.Snapshot.Manifest)[ex.Path]
					ids[index] = record.ExcerptID(j.Snapshot.Digest, ex.Path, entry.SHA256, ex.StartLine, ex.EndLine)
					contracts[index] = record.Contract{Statement: statement, AppliesTo: ".", EvidenceIDs: []string{ex.EvidenceID}}
				}
				if ids[0] == "" || ids[1] == "" {
					t.Fatalf("expected both public API documents: %+v", in.Documents)
				}
				return asJSON(docsOutput{Contracts: contracts,
					Conflicts: []record.Conflict{}, Questions: []record.Question{}, InstructionNotes: []record.InstructionNote{}})
			}
			var rejectedSwap bool
			script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
				in := decodeAs[investigateInput](t, q)
				if !in.Scope.WholeRun {
					return asJSON(map[string]any{"candidates": []any{}})
				}
				want := 2
				if tight {
					want = 1
				}
				if len(in.Scope.RustAPIs) != want || slices.Contains(in.Scope.Limitations, rustAPIContextOmitted) != tight {
					t.Fatalf("wrong delivered Rust API coverage: %+v %v", in.Scope.RustAPIs, in.Scope.Limitations)
				}
				catalog := catalogOf(in.Scope.Excerpts)
				a := in.Scope.RustAPIs[0]
				if a.ContractIndex != 1 || len(a.DocumentIDs) != 1 || a.DocumentIDs[0] != ids[0] || a.Symbol != "calibrated" ||
					!strings.Contains(catalog[a.ImplementationID].Text, "if reading < 0 { return None; }") || a.TestID != "" {
					t.Fatalf("first contract lost its own implementation: %+v", a)
				}
				claim := func(link rustAPILink, codeID string) record.ContractComparison {
					return record.ContractComparison{ContractIndex: link.ContractIndex, AppliesTo: ".", Status: "match",
						Reason: "인용한 구현에서 각 조건의 반환값을 확인했다.", DocumentEvidenceIDs: link.DocumentIDs, CodeEvidenceIDs: []string{codeID}}
				}
				comparisons := []record.ContractComparison{claim(a, a.ImplementationID)}
				if !tight {
					b := in.Scope.RustAPIs[1]
					if b.ContractIndex != 2 || len(b.DocumentIDs) != 1 || b.DocumentIDs[0] != ids[1] || b.Symbol != "limited" ||
						!strings.Contains(catalog[b.ImplementationID].Text, "if level > 90 { return 90; }") ||
						!strings.Contains(catalog[b.TestID].Text, "assert_eq!(super::limited(120), 90)") ||
						a.ImplementationID == b.ImplementationID {
						t.Fatalf("second contract uses unrelated or absent evidence: %+v", b)
					}
					if in.Repair == nil {
						comparisons[0] = claim(a, b.ImplementationID)
					} else {
						// The repair names the comparison, its field and the
						// rule: code of the other documented function.
						rejectedSwap = slices.Equal(in.Repair.OutputErrors, []string{record.CompareCodeOutsideAPI}) &&
							slices.Equal(in.Repair.Items, []itemIssue{cmpIssue(1, "code_evidence_ids", record.CompareCodeOutsideAPI)})
					}
					comparisons = append(comparisons, claim(b, b.ImplementationID))
				}
				return asJSON(map[string]any{"candidates": []any{}, "contract_comparisons": comparisons})
			}
			h := Handler{Budget: smallBudget, Static: vetStub{}, Caller: &scriptedCaller{script: script}}
			_, results := runRoleWith(t, j, h, "1.27.4")
			if got := results["review.investigate"]; got.Status != domain.TaskSucceeded || !tight && !rejectedSwap {
				t.Fatalf("unexpected investigation or wrong-function comparison accepted: %+v, %v", got, rejectedSwap)
			}
			stored := readRecord(t, j, "investigate")
			if slices.Contains(stored.Limitations, rustAPIContextOmitted) != tight {
				t.Fatalf("record omitted its Rust API coverage: %+v", stored.Limitations)
			}
		})
	}
}
