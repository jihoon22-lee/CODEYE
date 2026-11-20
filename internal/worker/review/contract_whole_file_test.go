package review

import (
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

func TestContractFollowupUsesWholeShortFileWithOriginalEvidence(t *testing.T) {
	t.Run("selected location", func(t *testing.T) { testContractFollowupWholeShortFile(t, false) })
	t.Run("found through selected literal", func(t *testing.T) { testContractFollowupWholeShortFile(t, true) })
}

func testContractFollowupWholeShortFile(t *testing.T, literal bool) {
	t.Helper()
	const name = "svc/b/display.js"
	const other = "svc/b/readiness.js"
	const definition = `const missing = "unset";
function valueKind(value) {
  if (value === undefined) {
    return missing;
  }
  return typeof value;
}
`
	const caller = `export function describeValue(input) {
  const result = valueKind(input);
  return "value:" + result;
}
`
	source := definition + strings.Repeat("// Shared runtime source.\n", 20) + caller
	j := contractExampleJob(t, func(root string) {
		writeFile(t, root, name, source)
		writeFile(t, root, other, "export function readiness() { return 'ready'; }\n")
	})
	contractExampleInput(t, j)
	docs, inv := readRecord(t, j, record.StageDocuments), readRecord(t, j, record.StageInvestigate)
	docs.Conflicts = nil
	if _, err := persistRecord(j, docs); err != nil {
		t.Fatal(err)
	}
	for i, up := range inv.Upstream {
		if up.Source == "review.documents" {
			inv.Upstream[i].SHA256 = digestOf(mustRead(t, recordPath(j, record.StageDocuments)))
		}
	}
	files := (&roleStage{j: j}).entries()
	e := files[name]
	makeID := func(start, end int) string { return record.ExcerptID(j.Snapshot.Digest, name, e.SHA256, start, end) }
	last := strings.Count(source, "\n")
	priorCode := record.Excerpt{ID: makeID(last-3, last), Path: name, SHA256: e.SHA256, Start: last - 3, End: last, Kind: "code", Origin: "context"}
	inv.Evidence = append(inv.Evidence, priorCode)
	inv.ContractComparisons[0].CodeEvidenceIDs = []string{priorCode.ID}
	window := func(start, end int) record.ContractLocation {
		return record.ContractLocation{ID: record.ContractLocationID(j.Snapshot.Digest, name, e.SHA256, start, end),
			Path: name, SHA256: e.SHA256, Start: start, End: end, Kind: "code"}
	}
	inv.ContractProbe.Candidates = []record.ContractLocation{window(3, 5), window(last-2, last)}
	inv.ContractProbe.Literals = nil
	branch := makeID(3, 5)
	if literal {
		// The selected catalog location is a different file. Only resolving
		// the exact saved literal can reach the first part of this source.
		blob := files[other].SHA256
		inv.ContractProbe.Candidates = []record.ContractLocation{{
			ID: record.ContractLocationID(j.Snapshot.Digest, other, blob, 1, 1), Path: other, SHA256: blob, Start: 1, End: 1, Kind: "code",
		}}
		inv.ContractProbe.Literals = []string{"valueKind"}
		branch = makeID(1, record.MaxContractLocationLines)
	}
	if _, err := persistRecord(j, inv); err != nil {
		t.Fatal(err)
	}
	whole := makeID(1, last)
	f := &scriptedCaller{script: map[string]func(int, opencode.Request) opencode.Reply{
		record.StageContract: func(_ int, q opencode.Request) opencode.Reply {
			in := decodeAs[contractReviewInput](t, q)
			if !slices.Equal(in.Prior.CodeEvidenceIDs, []string{priorCode.ID}) {
				t.Fatalf("comparison lost prior citation: %+v", in.Prior)
			}
			var code []string
			for _, x := range in.Scope.Excerpts {
				if strings.HasPrefix(x.ID, "loc_") {
					t.Fatalf("location alias was exposed as evidence: %+v", x)
				}
				if x.Path == name {
					code = append(code, x.ID)
					if x.ID == whole && x.Text != strings.TrimSuffix(source, "\n") {
						t.Fatalf("whole file didn't contain exact snapshot text: %q", x.Text)
					}
				}
			}
			if !slices.Equal(code, []string{whole, priorCode.ID, branch}) {
				t.Fatalf("whole source, the original citation or the independent branch was lost: %v", code)
			}
			comparison := in.Prior
			comparison.Reason = "도움 함수와 호출부의 반환을 확인했다. 나머지 문서 조건은 미확인이다."
			comparison.CodeEvidenceIDs = []string{whole, priorCode.ID}
			return asJSON(map[string]any{"contract_comparisons": []record.ContractComparison{comparison}})
		},
	}}
	h := Handler{Static: vetStub{}, Caller: f}
	res := h.Run(t.Context(), j, analysis.TaskSpec{Key: record.TaskContract}, nil)
	if res.Status != domain.TaskSucceeded || f.calls(record.StageContract) != 1 {
		t.Fatalf("result %+v, %d calls", res, f.calls(record.StageContract))
	}
	receipt := readRecord(t, j, record.StageContract)
	if !record.ValidateContractFollowup(receipt, docs, inv) || !slices.Equal(receipt.ContractComparisons[0].CodeEvidenceIDs, []string{whole, priorCode.ID}) ||
		receipt.Calls[0].RequestDigest == "" || len(receipt.Upstream) != 2 {
		t.Fatalf("whole source and its original citation were not recorded: %+v", receipt)
	}
}

func TestContractFollowupSkipsIneligibleWholeFile(t *testing.T) {
	const source = "// Read the marker.\nfunction display(value) {\n  if (value) return 'ok';\n  return 'unset';\n}\n"
	for _, c := range []struct {
		name, path, content, scope string
		withheld, stale, comment   bool
	}{
		{name: "source outside contract scope", scope: "other"},
		{name: "test source", path: "app/display.test.js"},
		{name: "type declarations", path: "app/display.d.ts"},
		{name: "generated source", content: "// Code generated by tools. DO NOT EDIT.\n" + source},
		{name: "withheld", withheld: true},
		{name: "already exposed", stale: true},
		{name: "selected range is comment only", comment: true},
		{name: "over line limit", content: source + strings.Repeat("// Documentation\n", roleMaxCodeLines)},
		{name: "over byte limit", content: source + "// " + strings.Repeat("x", roleMaxWindowBytes) + "\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			name, content, scope := c.path, c.content, c.scope
			if name == "" {
				name = "app/display.js"
			}
			if content == "" {
				content = source
			}
			if scope == "" {
				scope = "app"
			}
			j := treeJob(t, map[string]string{name: content})
			s := roleStage{ctx: t.Context(), j: j}
			files := s.entries()
			e := files[name]
			if c.withheld {
				s.screen.withheld = map[string]bool{name: true}
			}
			makeWindow := func(start, end int) record.Excerpt {
				return record.Excerpt{ID: record.ExcerptID(j.Snapshot.Digest, name, e.SHA256, start, end), Path: name,
					SHA256: e.SHA256, Kind: record.SourceKind(name), Start: start, End: end}
			}
			x, old := makeWindow(3, 4), makeWindow(2, 2)
			if c.stale {
				old = makeWindow(1, strings.Count(content, "\n"))
			}
			if c.comment {
				x = makeWindow(1, 1)
			}
			got, err := s.contractWholeFiles(scope, []record.Excerpt{x}, []record.Excerpt{old}, nil, files)
			if err != nil || len(got) != 0 {
				t.Fatalf("ineligible full source was returned: %+v, %v", got, err)
			}
		})
	}
}

func TestContractFollowupWholeFileRetainsExactSliceLineage(t *testing.T) {
	full := record.Excerpt{ID: "full", Path: "app/display.js", SHA256: "hash", Start: 1, End: 10, Kind: "code", Origin: "context"}
	partial := record.Excerpt{ID: "partial", Path: full.Path, SHA256: full.SHA256, Start: 2, End: 5, Kind: "code", Origin: "context"}
	lineage := []record.Upstream{{Source: "review.scope", State: record.UpstreamAccepted}, {Source: "review.investigate.s001", State: record.UpstreamAccepted}}
	pool := []contractExcerpt{{excerpt: full, sources: lineage}, {excerpt: partial, sources: lineage}}
	got := preferWholeContractFiles([]contractExcerpt{{excerpt: full}}, pool, nil)
	if len(got) != 2 || got[0].excerpt != full || got[1].excerpt != partial || !slices.Equal(got[0].sources, lineage) || !slices.Equal(got[1].sources, lineage) {
		t.Fatalf("lost bound evidence while preferring the full file: %+v", got)
	}
}
