package review

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

func TestReviewPrioritizesProductGuidesUnderInputLimits(t *testing.T) {
	const linked = "docs/panels.md"
	const configuration = "docs/configuration.md"
	const start = "docs/pages/getting-started/+page.md"
	const region = "plugins/console/docs/reference/index.md"
	const guide = "docs/dashboard-guide.md"
	const body = "The dashboard keeps its most recent panel after reload.\n"
	for _, tc := range []struct {
		name  string
		count int
		text  string
	}{
		{"excerpt count", 12, body},
		{"excerpt bytes", 0, strings.Repeat(body, 34)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"README.md": "# Panels\n\n[See available panels](./docs/panels.md#grid)\n",
				"AGENTS.md": "Keep examples small.\n",
				linked:      tc.text, configuration: tc.text, start: tc.text, region: tc.text,
				"README.fr.md": tc.text, "docs/business-policy/guide.md": tc.text,
			}
			for i := range tc.count {
				files[fmt.Sprintf("packages/component%d/README.md", i)] = body
			}
			if tc.count > 0 {
				files[guide] = body
				files["docs/templates/getting-started.md"] = body
			}
			in, stored, inventory := collectDocumentSelection(t, files)
			if len(in.Documents) < 6 || len(in.Documents) > roleMaxDocs || in.Documents[0].Path != "README.md" ||
				in.Documents[1].Path != "AGENTS.md" || in.Documents[2].Path != linked {
				t.Fatalf("primary guidance lost: %+v", in.Documents)
			}
			for p, scope := range map[string]string{configuration: ".", start: ".", region: "plugins/console"} {
				if !slices.ContainsFunc(in.Documents, func(d docInput) bool { return d.Path == p && d.AppliesTo == scope }) {
					t.Fatalf("missing guidance %s for %s", p, scope)
				}
			}
			if tc.count > 0 && !slices.ContainsFunc(in.Documents, func(d docInput) bool { return d.Path == guide }) {
				t.Fatalf("the named product guide is missing: %+v", in.Documents)
			}
			used := 0
			for _, d := range in.Documents {
				used += record.JSONLen(d.Text)
			}
			limited := "documents_outside_review_input"
			if len(in.Documents) == len(files) {
				limited = "document_excerpt_only"
			}
			if used > roleMaxDocsBytes || !slices.Contains(stored.Limitations, limited) || len(inventory.Items) != len(files) {
				t.Fatalf("selection accounting changed: %d bytes, %v limitations, %d inventory entries", used, stored.Limitations, len(inventory.Items))
			}
			if slices.ContainsFunc(in.Documents[:6], func(d docInput) bool {
				return d.Path == "docs/business-policy/guide.md" || d.Path == "README.fr.md" || d.Path == "docs/templates/getting-started.md"
			}) {
				t.Fatal("administrative materials displaced product guidance")
			}
		})
	}
}

func TestReviewStillUsesAdministrativeOnlyGuidance(t *testing.T) {
	in, stored, inventory := collectDocumentSelection(t, map[string]string{
		"CONTRIBUTING.md": "Use the maintenance checklist before submitting changes.\n",
		"docs/roadmap.md": "The governance group meets once a month.\n",
	})
	if len(in.Documents) != 2 || in.Documents[0].Path != "CONTRIBUTING.md" || in.Documents[1].Path != "docs/roadmap.md" ||
		len(stored.Evidence) != 2 || len(inventory.Items) != 2 || slices.Contains(stored.Limitations, "documents_outside_review_input") {
		t.Fatalf("administrative-only guidance is still reviewed: %+v %v", in.Documents, stored.Limitations)
	}
}

func TestDocumentCoverageReadsPlainProseWithinSpareCapacity(t *testing.T) {
	const prose = "The panel stays active.\n"
	for _, tc := range []struct {
		name     string
		path     string
		body     string
		ends     []int
		partial  bool
		withDocs bool
	}{
		{name: "already complete", body: strings.Repeat(prose, 17), ends: []int{17}},
		{name: "complete after filling", body: strings.Repeat(prose, 74), ends: []int{40, 74}},
		{name: "long guide fits within existing budgets", body: strings.Repeat(prose, 169) + "The final setting persists after a reload.\n", ends: []int{40, 80, 120, 160, 170}},
		{name: "product guide without a README", path: "docs/usage.md", body: strings.Repeat(prose, 55), ends: []int{40, 55}},
		{name: "global window cap", body: strings.Repeat(prose, 350), ends: []int{40, 80, 120, 160, 200, 240, 280, 320}, partial: true},
		{name: "administrative sections skipped", body: strings.Repeat(prose, 40) + "## Sponsors\n\n### Contributors\nPlan a sponsor campaign.\n", ends: []int{40}, partial: true},
		{name: "all slots reserved for distinct files", body: strings.Repeat(prose, 74), ends: []int{40}, partial: true, withDocs: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := tc.path
			if target == "" {
				target = "README.md"
			}
			files := map[string]string{target: tc.body}
			if tc.withDocs {
				for i := 1; i < roleMaxDocs; i++ {
					files[fmt.Sprintf("component%d/README.md", i)] = fmt.Sprintf("Component %d applies a default value.\n", i)
				}
			}
			in, stored, _ := collectDocumentSelection(t, files)
			if want := len(tc.ends) + len(files) - 1; len(in.Documents) != want {
				t.Fatalf("got %d windows, want %d", len(in.Documents), want)
			}
			lines := strings.Split(tc.body, "\n")
			start, i := 1, 0
			for _, d := range in.Documents {
				if d.Path != target {
					continue
				}
				if d.StartLine != start || i >= len(tc.ends) || d.EndLine != tc.ends[i] || d.Text != strings.Join(lines[start-1:d.EndLine], "\n") ||
					record.JSONLen(d.Text) > roleMaxDocBytes {
					t.Fatalf("unexpected document window: %+v", d)
				}
				start, i = d.EndLine+1, i+1
			}
			if i != len(tc.ends) || slices.Contains(stored.Limitations, "document_excerpt_only") != tc.partial {
				t.Fatalf("incorrect source coverage: %d windows, %v", i, stored.Limitations)
			}
		})
	}
}

func TestDocumentCoverageSharesSpareSlotsAcrossGuides(t *testing.T) {
	const readme = "README.md"
	const guide = "docs/usage.md"
	in, stored, _ := collectDocumentSelection(t, map[string]string{
		readme: strings.Repeat("A saved panel opens again.\n", 200),
		guide:  strings.Repeat("The list shows active panels.\n", 200),
	})
	if len(in.Documents) != roleMaxDocs || !slices.Contains(stored.Limitations, "document_excerpt_only") {
		t.Fatalf("incorrect shared coverage: %d windows, %v", len(in.Documents), stored.Limitations)
	}
	for i, d := range in.Documents {
		path := guide
		if i%2 == 0 {
			path = readme
		}
		start := (i/2)*roleMaxDocLines + 1
		if d.Path != path || d.StartLine != start || d.EndLine != start+roleMaxDocLines-1 {
			t.Fatalf("window %d did not continue %s at line %d: %+v", i, path, start, d)
		}
	}
}

func TestDocumentCoverageStopsAtEncodedByteBudget(t *testing.T) {
	const raw = "A label may include & < > or quotes."
	const totalLines = 230
	text := strings.Repeat(raw+"\n", totalLines)
	in, stored, _ := collectDocumentSelection(t, map[string]string{"README.md": text})
	bytes, next := 0, 1
	for _, d := range in.Documents {
		lines := d.EndLine - d.StartLine + 1
		used := record.JSONLen(d.Text)
		if d.StartLine != next || lines > roleMaxDocLines || used > roleMaxDocBytes ||
			d.Text != strings.TrimSuffix(strings.Repeat(raw+"\n", lines), "\n") {
			t.Fatalf("did not clip at a full source line: %+v", d)
		}
		bytes += used
		next = d.EndLine + 1
	}
	if next > totalLines || len(in.Documents) >= roleMaxDocs || bytes > roleMaxDocsBytes || roleMaxDocsBytes-bytes >= record.JSONLen(raw) ||
		!slices.Contains(stored.Limitations, "document_excerpt_only") {
		t.Fatalf("incorrect encoded byte accounting: %d windows, %d bytes, last line %d, limits %v", len(in.Documents), bytes, next-1, stored.Limitations)
	}
}

func TestDocumentCoverageResumesAfterOversizedLine(t *testing.T) {
	const prefix = "Record label is kept.\n"
	const body = "The newest item is kept."
	files := map[string]string{"README.md": strings.Repeat(prefix, 40) + strings.Repeat("<", roleMaxDocBytes) + "\n" + body + "\n"}
	in, stored, _ := collectDocumentSelection(t, files)
	if len(in.Documents) != 2 || in.Documents[0].EndLine != 40 || in.Documents[1].StartLine != 42 || in.Documents[1].EndLine != 42 ||
		in.Documents[1].Text != body || !slices.Contains(stored.Limitations, "document_excerpt_only") {
		t.Fatalf("complete lines past byte limit were unavailable: %+v (%v)", in.Documents, stored.Limitations)
	}
}

func collectDocumentSelection(t *testing.T, files map[string]string) (docsInput, record.Record, *envdoc.Documents) {
	t.Helper()
	j := treeJob(t, files)
	var in docsInput
	caller := &scriptedCaller{script: map[string]func(int, opencode.Request) opencode.Reply{
		"documents": func(_ int, q opencode.Request) opencode.Reply {
			in = decodeAs[docsInput](t, q)
			return asJSON(docsOutput{Contracts: []record.Contract{}, Conflicts: []record.Conflict{},
				Questions: []record.Question{}, InstructionNotes: []record.InstructionNote{}})
		},
	}}
	h, err := discovery.Wrap(Handler{Static: vetStub{}, Caller: caller}, discovery.Config{})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := h.Plan(t.Context(), j)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range plan {
		if task.Key == envdoc.TaskDocuments || task.Key == "review.documents" {
			if result := h.Run(t.Context(), j, task, nil); result.Status != domain.TaskSucceeded {
				t.Fatalf("%s: %+v", task.Key, result)
			}
		}
	}
	if caller.calls("documents") != 1 {
		t.Fatalf("document role call count: %d", caller.calls("documents"))
	}
	discovered, upstream := (&roleStage{j: j}).discoveryDoc(envdoc.KindDocuments)
	if discovered.Documents == nil || upstream.State != record.UpstreamAccepted {
		t.Fatalf("invalid discovery artifact: %+v", upstream)
	}
	return in, readRecord(t, j, record.StageDocuments), discovered.Documents
}
