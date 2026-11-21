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

func TestReviewUsesLongPrimaryReadmeUnderDefaultBudget(t *testing.T) {
	const guide = "docs/options.md"
	const oversized = "docs/archive.md"
	const behavior = "The current notebook remains selected after the display is closed."
	readme := "# Field notebooks\n\n[Display options](docs/options.md)\n\n" +
		strings.Repeat("Each field notebook has a separate home view.\n", 1750) +
		"\n## User workflows\n\n" + behavior + "\nOpening another notebook changes which one is selected.\n"
	if len(readme) <= 64<<10 || len(readme) > 90<<10 {
		t.Fatalf("expected a README over 64 KiB, got %d bytes", len(readme))
	}
	help := "The display offers a compact view.\n"
	in, stored, inventory := collectDocumentSelection(t, map[string]string{
		"README.md": readme,
		guide:       help,
		oversized:   strings.Repeat(help, int(discovery.DefaultBudget.PerDocumentBytes)/len(help)+1),
	})
	if inventory.Budget.PerDocumentBytes != 512<<10 || inventory.Budget.TotalBytes != 4<<20 ||
		inventory.Budget.ReadBytes != int64(len(readme)+len(help)) {
		t.Fatalf("unexpected discovery budget: %+v", inventory.Budget)
	}
	for _, d := range inventory.Items {
		switch d.Path {
		case "README.md", guide:
			if d.Status != envdoc.DocSelected {
				t.Fatalf("product guidance not selected: %+v", d)
			}
		case oversized:
			if d.Status != envdoc.DocBudgetExceeded || d.Reason == nil || *d.Reason != envdoc.ReasonTooLarge {
				t.Fatalf("oversized document reason changed: %+v", d)
			}
		}
	}
	if len(in.Documents) == 0 || in.Documents[0].Path != "README.md" ||
		!slices.ContainsFunc(in.Documents, func(d docInput) bool { return d.Path == guide && strings.Contains(d.Text, strings.TrimSpace(help)) }) ||
		!slices.ContainsFunc(in.Documents, func(d docInput) bool { return d.Path == "README.md" && strings.Contains(d.Text, behavior) }) ||
		slices.ContainsFunc(in.Documents, func(d docInput) bool { return d.Path == oversized }) ||
		!slices.Contains(stored.Limitations, "document_excerpt_only") {
		t.Fatalf("primary README or guide lost from actual review request: %+v", in.Documents)
	}
	var sentBytes int
	for _, excerpt := range in.Documents {
		sentBytes += record.JSONLen(excerpt.Text)
	}
	if sentBytes > roleMaxDocsBytes || stored.Calls[0].RequestBytes > PromptBudget {
		t.Fatalf("local discovery budget enlarged model input: %d excerpt bytes, %d request bytes", sentBytes, stored.Calls[0].RequestBytes)
	}
}

func TestReviewUsesExplicitLongEngineeringDocument(t *testing.T) {
	const guide = "docs/EXECUTION.md"
	const selected = "The local queue starts approved jobs in a separate working copy."
	long := "# Execution\n\n" + selected + "\n\n" + strings.Repeat("The test runner reports its progress while the local copy remains available.\n", 960)
	if len(long) <= 64<<10 {
		t.Fatalf("long engineering document fixture is only %d bytes", len(long))
	}
	in, stored, inv := collectDocumentSelectionInScope(t, map[string]string{
		guide:                long,
		"docs/operations.md": "An unrelated overview describes build prerequisites.\n",
	}, domain.Scope{Kind: "paths", Paths: []string{guide}})
	if inv.Budget.ReadBytes != int64(len(long)) || len(in.Documents) == 0 ||
		!slices.ContainsFunc(in.Documents, func(d docInput) bool { return d.Path == guide && strings.Contains(d.Text, selected) }) ||
		slices.ContainsFunc(in.Documents, func(d docInput) bool { return d.Path != guide }) ||
		!slices.Contains(stored.Limitations, "document_excerpt_only") {
		t.Fatalf("scoped review did not receive the bounded engineering document: %+v, %v", inv.Budget, stored.Limitations)
	}
	if len(in.Documents) > roleMaxDocs || stored.Calls[0].RequestBytes > PromptBudget {
		t.Fatalf("scoped document exceeded request bounds: %d excerpts, %d bytes", len(in.Documents), stored.Calls[0].RequestBytes)
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

func TestDocumentRequestPreservesDistinctProductEvidenceUnderMetadataPressure(t *testing.T) {
	const requirements = "docs/product-requirements.md"
	const regionalGuide = "extensions/inbox/docs/usage.md"
	const oversized = "docs/dashboard-guide.md"
	const behavior = "A visitor can save a personal filter before choosing which team may see it."
	const prefix = "# Product requirements\n\n" + "The scope describes the original dashboard design.\n"
	const usage = "When a visitor opens a shared panel, the original view stays in the menu.\n"
	longSpec := prefix + strings.Repeat("The overview describes the record layout and initial browser view.\n", 45) +
		"\n## User workflows\n\n" + behavior + "\nOnly the creator sees a new filter before it is shared.\n" +
		"The selected team can see the filter after the creator shares it.\n"
	files := map[string]string{
		"README.md":                      "# Panels\n\nOpen a panel to see the saved filters.\n",
		"AGENTS.md":                      "Read the extension guidelines before changing an example.\n",
		"docs/usage.md":                  usage,
		"docs/templates/requirements.md": "The policy boilerplate describes a maintenance process.\n",
		"requirements.txt":               "pytest==8.4.1\n",
		requirements:                     longSpec,
		regionalGuide:                    "The inbox extension can display archived messages on demand.\n",
		oversized:                        strings.Repeat(usage, int(discovery.DefaultBudget.PerDocumentBytes)/len(usage)+1),
	}
	for i := range 49 {
		dir := fmt.Sprintf("modules/component%02d", i)
		files[dir+"/go.mod"] = "module example.test/" + dir + "\n\ngo 1.27\n"
		files[dir+"/main.go"] = "package main\nfunc main() {}\n"
	}
	for i := range 12 {
		files[fmt.Sprintf("README-%02d-%s.md", i, strings.Repeat("&", 64))] = "The dashboard opens a local region.\n"
	}
	in, stored, inventory := collectDocumentSelection(t, files)
	if len(in.Modules) >= 32 || len(in.Modules) >= len(inventory.Modules) || len(in.Overlapping) >= len(inventory.OverlappingGuidance) ||
		!slices.Contains(in.Limitations, "document_lists_shortened") || !slices.Contains(stored.Limitations, "document_lists_shortened") ||
		!slices.Contains(stored.Limitations, "document_excerpt_only") || !slices.Contains(stored.Limitations, "documents_outside_review_input") ||
		slices.Contains(stored.Limitations, limInputTrimmed) {
		t.Fatalf("evidence and optional list coverage: %d/%d modules, %d/%d overlap groups, %v, %v",
			len(in.Modules), len(inventory.Modules), len(in.Overlapping), len(inventory.OverlappingGuidance), in.Limitations, stored.Limitations)
	}
	if len(in.Documents) != len(stored.Evidence) || len(in.Documents) > roleMaxDocs || stored.Calls[0].RequestBytes > PromptBudget {
		t.Fatalf("request and record limits diverged: %d documents, %d references, %d bytes", len(in.Documents), len(stored.Evidence), stored.Calls[0].RequestBytes)
	}
	want := map[string]string{"README.md": ".", requirements: ".", "docs/usage.md": ".", regionalGuide: "extensions/inbox"}
	var bytes int
	var workflow bool
	for i, d := range in.Documents {
		cost := record.JSONLen(d.Text)
		bytes += cost
		if d.Path == oversized || d.Path == "requirements.txt" || cost > roleMaxDocBytes || d.EndLine-d.StartLine+1 > roleMaxDocLines ||
			stored.Evidence[i].Path != d.Path || stored.Evidence[i].Start != d.StartLine || stored.Evidence[i].End != d.EndLine {
			t.Fatalf("unexpected evidence %d: %+v", i, d)
		}
		if scope, ok := want[d.Path]; ok {
			if d.AppliesTo != scope {
				t.Fatalf("%s applies to %s, want %s", d.Path, d.AppliesTo, scope)
			}
			delete(want, d.Path)
		}
		if d.Path == requirements && strings.Contains(d.Text, behavior) {
			lines := strings.Split(strings.TrimSuffix(longSpec, "\n"), "\n")
			workflow = d.StartLine > roleMaxDocLines && d.Text == strings.Join(lines[d.StartLine-1:d.EndLine], "\n")
		}
	}
	if len(want) != 0 || !workflow || bytes > roleMaxDocsBytes {
		t.Fatalf("product evidence missing or oversized: %v, workflow=%t, bytes=%d", want, workflow, bytes)
	}
	if !slices.ContainsFunc(inventory.Items, func(d envdoc.Doc) bool {
		return d.Path == oversized && d.Status == envdoc.DocBudgetExceeded && d.Reason != nil && *d.Reason == envdoc.ReasonTooLarge
	}) {
		t.Fatal("the discovery budget did not account for the oversized guide")
	}
}

func TestDocumentSelectionFavorsProductTextOverUnlinkedFixtures(t *testing.T) {
	const readme = "README.md"
	const guide = "docs/usage.md"
	const linked = "examples/inbox/README.md"
	const extra = "The saved selection is also applied in the desktop client."
	files := map[string]string{}
	for i := range 20 {
		files[fmt.Sprintf("fixtures/panels%02d/README.md", i)] = "This sample panel opens the mock inbox.\n"
	}
	in, _, _ := collectDocumentSelection(t, files)
	if len(in.Documents) != roleMaxDocs || in.Documents[0].Path != "fixtures/panels00/README.md" || in.Documents[0].AppliesTo != "fixtures/panels00" {
		t.Fatalf("no representative guidance when all candidates are fixtures: %+v", in.Documents)
	}
	files[readme] = "# Panels\n\n[Runnable example](" + linked + ")\n" + strings.Repeat("The panel opens to the last selected item.\n", 37) + extra + "\n"
	files[guide] = "The dashboard supports a shared inbox.\n"
	files[linked] = "The example shows how to subscribe to the inbox.\n"
	in, _, _ = collectDocumentSelection(t, files)
	if len(in.Documents) < 5 || in.Documents[0].Path != readme || in.Documents[1].Path != linked || in.Documents[2].Path != guide ||
		in.Documents[3].Path != readme || in.Documents[3].StartLine != 41 || in.Documents[3].Text != extra ||
		!strings.HasPrefix(in.Documents[4].Path, "fixtures/") {
		t.Fatalf("representative product workflow and linked example should precede unlinked fixtures: %+v", in.Documents)
	}
}

func TestDocumentSelectionRepresentsBehaviorBeyondTheIntroduction(t *testing.T) {
	const meaning = "The service calculates the remaining quota from the accepted records. Archived records are excluded."
	const exception = "A client cannot resume a request when its lease has expired. It must start again with a new request."
	const readme = "README.md"
	body := "# Record service\n\n" + strings.Repeat("The introduction describes the history of the product.\n", 48) +
		"\n## Building\n\n" + strings.Repeat("When the build returns a result, the default installer writes its output.\n", 20) +
		"\n## Current release\n\n" + meaning + "\n\n" + strings.Repeat("This paragraph links to the build archive.\n\n", 45) +
		"## Caveats\n\n" + exception + "\n"
	files := map[string]string{readme: body}
	for i := range roleMaxDocs + 2 {
		files[fmt.Sprintf("tests/tool%d/README.md", i)] = "This tool builds and runs the bundled test suite.\n"
	}
	in, stored, _ := collectDocumentSelection(t, files)
	if len(in.Documents) != roleMaxDocs || in.Documents[0].Path != readme || in.Documents[0].StartLine != 1 {
		t.Fatalf("introductory and bounded evidence changed: %+v", in.Documents)
	}
	covered := map[int]bool{}
	for _, d := range in.Documents {
		if d.Path == readme {
			for i := d.StartLine; i <= d.EndLine; i++ {
				if covered[i] {
					t.Fatalf("README line %d was selected twice", i)
				}
				covered[i] = true
			}
		}
	}
	for _, clause := range []string{meaning, exception} {
		if !slices.ContainsFunc(in.Documents[1:3], func(d docInput) bool {
			lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
			return d.Path == readme && d.AppliesTo == "." && strings.Contains(d.Text, clause) &&
				d.Text == strings.Join(lines[d.StartLine-1:d.EndLine], "\n")
		}) {
			t.Fatalf("behavior missing before bundled fixtures: %q", clause)
		}
	}
	if !slices.Contains(stored.Limitations, "document_excerpt_only") || !slices.Contains(stored.Limitations, "documents_outside_review_input") {
		t.Fatalf("missing limitations: %v", stored.Limitations)
	}
}

func TestDocumentSelectionSpreadsAcrossShortSectionsBeforeNestedTestGuides(t *testing.T) {
	const readme = "README.md"
	const linked = "extensions/archive/docs/usage.md"
	const middle = "Each lamp indicates the condition of its own bay."
	const last = "The last open card reappears on the board after reloading."
	readmeText := "# Board\n\n[Archive extension](" + linked + ")\n\n" +
		strings.Repeat("The board was first designed for coordinating a group of stations.\n", 39) +
		"\n## Controls\n\n" + strings.Repeat("When the caller supplies a filter, the board only returns matching slots.\n", 18) +
		"\n## Storage\n\n" + strings.Repeat("The board must preserve each stored record unless the result is an error.\n", 18) +
		"\n## Queue\n\n" + strings.Repeat("The queue displays changes in priority order.\n", 30) +
		"\n## Bay indicators\n\n" + middle + "\n\n" +
		"## Inspection\n\n" + strings.Repeat("This section illustrates a station layout.\n", 35) +
		"\n## Reopening\n\n" + last + "\n"
	files := map[string]string{
		readme: readmeText, linked: "The archive extension keeps a separate record of open cards.\n",
		"tests/README.md": "From the test directory, run the product's integration checks.\n",
	}
	for i := range roleMaxDocs {
		files[fmt.Sprintf("tests/framework%d/README.md", i)] = "The bundled runner provides test assertions and output formatters.\n"
	}
	in, _, _ := collectDocumentSelection(t, files)
	if len(in.Documents) > roleMaxDocs || in.Documents[0].Path != readme || in.Documents[0].StartLine != 1 {
		t.Fatalf("README introduction or budget changed: %+v", in.Documents)
	}
	firstBundle := slices.IndexFunc(in.Documents, func(d docInput) bool { return strings.HasPrefix(d.Path, "tests/framework") })
	if firstBundle < 0 {
		firstBundle = len(in.Documents)
	}
	for _, p := range []string{linked, "tests/README.md"} {
		if !slices.ContainsFunc(in.Documents[:firstBundle], func(d docInput) bool { return d.Path == p }) {
			t.Fatalf("first-party guide %s displaced by nested test guides", p)
		}
	}
	for _, body := range []string{middle, last} {
		if !slices.ContainsFunc(in.Documents[:firstBundle], func(d docInput) bool {
			return d.Path == readme && d.EndLine-d.StartLine < compactDocLines && record.JSONLen(d.Text) <= compactDocBytes &&
				strings.Contains(d.Text, body)
		}) {
			t.Fatalf("short product section missing before test guides: %q", body)
		}
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
	return collectDocumentSelectionInScope(t, files, domain.Scope{Kind: "full"})
}

func collectDocumentSelectionInScope(t *testing.T, files map[string]string, scope domain.Scope) (docsInput, record.Record, *envdoc.Documents) {
	t.Helper()
	j := treeJob(t, files)
	j.Scope = scope
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
