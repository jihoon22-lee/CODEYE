package review

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/sourceexcerpt"
)

func TestDocumentContinuationReachesReport(t *testing.T) {
	const readme = "README.md"
	const behavior = "The call returns queued records only when the caller supplies a matching filter."
	lead := "# Record cache\n\n" + strings.Repeat("The introduction explains the project and supported platforms.\n", 46)
	sponsor := "\n## Sponsors\n\n" + strings.Repeat("A link returns sponsor profiles.\n", 8)
	api := "\n## API\n\n### countRecords()\n\nReturns the number of records before filtering.\n\n### fetchRecords(filter)\n\n" + behavior + "\n\n" +
		"The default filter returns an empty result.\n\n" +
		"### addRecord(value)\n\nThe optional value is validated before storage.\n"
	changelog := "\n## Changelog\n\nThe current version returns release information.\n"
	body := lead + sponsor + api + changelog
	j := treeJob(t, map[string]string{
		readme: body, "AGENTS.md": "Keep the cache in memory.\n",
		"go.mod":   "module test.example/cache\n\ngo 1.27\n",
		"cache.go": "package cache\n\nfunc fetchRecords(filter string) []string {\n if filter != \"open\" { return nil }; return []string{\"queued\"}\n}\n",
	})
	var later docInput
	script := maxScript(t)
	script["documents"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[docsInput](t, q)
		if len(in.Documents) != 5 || in.Documents[0].Path != "AGENTS.md" ||
			in.Documents[1].Path != readme || in.Documents[1].StartLine != 1 || in.Documents[2].Path != readme {
			t.Fatalf("did not preserve the first windows: %+v", in.Documents)
		}
		first := in.Documents[1]
		later = in.Documents[3]
		lines := strings.Split(body, "\n")
		if later.StartLine <= first.EndLine || later.EndLine-later.StartLine >= roleMaxDocLines ||
			later.Text != strings.Join(lines[later.StartLine-1:later.EndLine], "\n") ||
			later.EvidenceID == first.EvidenceID || !strings.HasPrefix(later.Text, "### fetchRecords(filter)") ||
			!strings.Contains(later.Text, behavior) || strings.Contains(later.Text, "countRecords") ||
			strings.Contains(later.Text, "Sponsors") || strings.Contains(later.Text, "Changelog") {
			t.Fatalf("later evidence did not represent an independent API window: %+v", later)
		}
		used := 0
		for _, d := range in.Documents {
			used += record.JSONLen(d.Text)
		}
		if used > roleMaxDocsBytes {
			t.Fatalf("document budget exceeded: %d", used)
		}
		return asJSON(docsOutput{
			Contracts: []record.Contract{{Statement: "조회는 필터에 맞는 대기 레코드를 반환한다.", AppliesTo: ".", EvidenceIDs: []string{later.EvidenceID}}},
			Conflicts: []record.Conflict{}, Questions: []record.Question{}, InstructionNotes: []record.InstructionNote{},
		})
	}
	script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		return asJSON(map[string]any{"candidates": []any{}, "contract_comparisons": []record.ContractComparison{{
			ContractIndex: 1, AppliesTo: ".", Status: "match", Reason: "조회는 열린 필터일 때 대기 레코드를 반환한다.",
			DocumentEvidenceIDs: []string{later.EvidenceID}, CodeEvidenceIDs: []string{excerptID(in.Scope.Excerpts, "cache.go")},
		}}})
	}
	h := Handler{Static: vetStub{}, Caller: &scriptedCaller{script: script}}
	plan, results := runRoleWith(t, j, h, "1.27.4")
	for _, key := range []string{"review.documents", "review.investigate"} {
		if results[key].Status != domain.TaskSucceeded {
			t.Fatalf("%s: %+v", key, results[key])
		}
	}
	rec := readRecord(t, j, "documents")
	if !slices.Contains(rec.Limitations, "document_excerpt_only") {
		t.Fatalf("partial document coverage was unmarked: %+v", rec.Limitations)
	}
	e := includedFiles(j.Snapshot.Manifest)[readme]
	if later.EvidenceID != record.ExcerptID(j.Snapshot.Digest, readme, e.SHA256, later.StartLine, later.EndLine) {
		t.Fatalf("late document evidence is not bound to its blob and line range: %+v", later)
	}

	root, err := os.OpenRoot(j.Snapshot.SnapshotDir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	source, ok := sourceexcerpt.ReadRange(root, e, later.StartLine, later.EndLine)
	if !ok || source.StartLine != later.StartLine || source.EndLine != later.EndLine || source.Text != later.Text {
		t.Fatalf("source excerpt does not match the cited API lines: %+v", source)
	}
	b, err := sourceexcerpt.Encode(sourceexcerpt.Document{
		SchemaVersion: sourceexcerpt.Version, RunID: j.RunID, AttemptID: j.AttemptID, Snapshot: j.Snapshot.Digest,
		Items: []sourceexcerpt.Item{{Path: e.Path, SHA256: e.SHA256, Anchor: later.StartLine, Excerpt: source}},
	})
	if err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(j.Scratch, "doc-continuation-excerpts.json")
	if err := os.WriteFile(artifact, b, 0600); err != nil {
		t.Fatal(err)
	}
	first := results[plan[0].Key]
	first.Artifacts = append(first.Artifacts, analysis.ArtifactFile{Path: artifact, Role: nodeproto.RoleOther, MediaType: sourceexcerpt.MediaType})
	results[plan[0].Key] = first
	r, err := report.Build(exampleInput(t, j, h, plan, results, &exampleIDs{n: 2}), report.DefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Documents.Comparisons) != 1 || r.Documents.Comparisons[0].Status != "match" ||
		!slices.Equal(r.Documents.Comparisons[0].DocumentEvidenceIDs, []string{later.EvidenceID}) ||
		!slices.ContainsFunc(r.Documents.Evidence, func(ev report.SectionEvidence) bool {
			return ev.ID == later.EvidenceID && ev.Path == readme && ev.StartLine == later.StartLine &&
				ev.EndLine == later.EndLine && ev.Excerpt != nil && strings.Contains(ev.Excerpt.Text, behavior)
		}) {
		t.Fatalf("report did not retain the late document and comparison: %+v", r.Documents)
	}
}

func TestDocumentationContinuationUsesSectionStructure(t *testing.T) {
	for _, tc := range []struct {
		name, ext, text, heading string
	}{
		{"only setup and navigation", ".md", "## Table of contents\n\n- [Usage](#usage)\n\n## Installation\n\nThe default installer returns 0.\nIt can return an error.\n", ""},
		{"boilerplate owns its subsections", ".md", "## Sponsor\n\n### API\n\nThis returns a link.\nAn optional filter applies.\n\n## Usage\n\nThe optional count takes an integer.\nAn error is returned for zero.\n", "## Usage"},
		{"reStructuredText", ".rst", "API reference\n=============\n\nThe optional filter takes a string.\nIt returns matching records.\n", "API reference"},
		{"example heading inside a fence", ".md", "```md\n## API\n\nThis returns text.\nAn optional filter applies.\n```\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lines := strings.Split("# Project\n\n"+tc.text, "\n")
			start, end := documentationContinuation(lines, 2, tc.ext, 500)
			if tc.heading == "" {
				if start != -1 {
					t.Fatalf("boilerplate selected: %q", lines[start:end])
				}
			} else if start < 0 || lines[start] != tc.heading {
				t.Fatalf("expected %s, got %d..%d", tc.heading, start, end)
			}
		})
	}
}

func TestDocumentEvidenceRepairReportsTheInvalidField(t *testing.T) {
	files := map[string]string{
		"README.md": "The project stores values.\n",
		"app.go":    "package values\nfunc store() {}\n",
	}
	for _, name := range []string{"readme-a.md", "readme-b.md", "readme-c.md", "readme-d.md", "readme-e.md"} {
		files[name] = "Reference details.\n"
	}
	j := treeJob(t, files)
	script := maxScript(t)
	script["documents"] = func(attempt int, q opencode.Request) opencode.Reply {
		in := decodeAs[docsInput](t, q)
		if len(in.Documents) < 5 || !strings.Contains(q.Instructions, "1 to 4 distinct IDs") ||
			!strings.Contains(q.Instructions, "1 to 3 distinct IDs") || !strings.Contains(q.Instructions, "single most direct excerpt") {
			t.Fatalf("role request did not describe the established field limits")
		}
		var ids []string
		for _, d := range in.Documents {
			ids = append(ids, d.EvidenceID)
		}
		result := docsOutput{Contracts: []record.Contract{}, Conflicts: []record.Conflict{}, Questions: []record.Question{}, InstructionNotes: []record.InstructionNote{}}
		quality := record.DocumentQuality{Aspect: "clarity", Assessment: "strength", Statement: "첫 문장에 저장 동작을 안내한다."}
		if in.Repair == nil {
			quality.EvidenceIDs = ids[:5]
			result.Contracts = []record.Contract{{Statement: "값을 저장한다.", AppliesTo: ".", EvidenceIDs: ids[:5]}}
			result.Conflicts = []record.Conflict{{
				Scope: ".", Summary: "동작 설명이 겹친다.", Impact: "호출 조건을 확인해야 한다.",
				FirstEvidence: ids[:4], SecondEvidence: ids[4:5],
			}}
		} else {
			for _, code := range []string{"contracts_evidence_ids_invalid", "conflicts_first_evidence_ids_invalid",
				"document_quality_evidence_ids_invalid", "evidence_list_too_long"} {
				if !slices.Contains(in.Repair.OutputErrors, code) {
					t.Fatalf("repair did not identify %s: %+v", code, in.Repair.OutputErrors)
				}
			}
			quality.EvidenceIDs = ids[:1]
			result.Contracts = []record.Contract{{Statement: "값을 저장한다.", AppliesTo: ".", EvidenceIDs: ids[:1]}}
		}
		result.DocumentQuality = []record.DocumentQuality{quality}
		return asJSON(result)
	}
	_, results := runRole(t, j, vetStub{}, &scriptedCaller{script: script}, "1.27.4")
	if results["review.documents"].Status != domain.TaskSucceeded {
		t.Fatalf("document role: %+v", results["review.documents"])
	}
	r := readRecord(t, j, "documents")
	if len(r.Calls) != 2 || len(r.DocumentQuality) != 1 || len(r.DocumentQuality[0].EvidenceIDs) != 1 {
		t.Fatalf("repair was not recorded and accepted: %+v", r)
	}
}

func TestDocumentSectionSplitsPreserveRangeAndBudget(t *testing.T) {
	text := "## API\n\n### client.read()\n\nReturns a record.\n\n" +
		"### client.status\n\nCurrent status.\n\n" +
		"### client.finish()\n\nReturns the final state when ready.\nIt can raise an error if invalid.\n"
	lines := strings.Split(text, "\n")
	for _, slots := range []int{1, 2, 3} {
		ranges := documentationRanges(lines, ".md", 0, len(lines), slots)
		if len(ranges) != slots || ranges[0].start != 0 || ranges[len(ranges)-1].end != len(lines) {
			t.Fatalf("wrong ranges for %d slots: %+v", slots, ranges)
		}
		if slots > 1 && lines[ranges[len(ranges)-1].start] != "### client.finish()" {
			t.Fatalf("specific behavior heading lost under %d slots: %+v", slots, ranges)
		}
		var windows []string
		used, end := 0, 0
		for _, r := range ranges {
			if r.start != end {
				t.Fatalf("gap or overlap between windows: %+v", ranges)
			}
			joined := strings.Join(lines[r.start:r.end], "\n")
			windows = append(windows, joined)
			used += record.JSONLen(joined)
			end = r.end
		}
		if strings.Join(windows, "\n") != text || used != record.JSONLen(text) {
			t.Fatalf("partition changed coverage or input byte cost for %d slots", slots)
		}
	}
}
