package discovery

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
)

func TestDiscoveryKeepsProductGuidanceWithinDocumentBudgets(t *testing.T) {
	const linked = "docs/panels.md"
	const start = "site/routes/getting-started/+page.md"
	const configuration = "site/content/configuration/index.md"
	const region = "plugins/console/docs/reference.md"
	const policy = "docs/governance/overview.md"
	const translation = "README.fr.md"
	const body = "Selecting a panel keeps the previous selection.\n"
	files := map[string]string{
		"README.md": "# Panels\n\n[Available panels](./docs/panels.md#grid)\n",
		"AGENTS.md": "Use the local fixture for examples.\n",
		linked:      body, start: body, configuration: body, region: body,
		policy: body, translation: body, "CONTRIBUTING.md": body,
		"plugins/console/go.mod": "module example.test/console\n\ngo 1.27\n",
	}
	for i := range 20 {
		files[fmt.Sprintf("packages/component%d/README.md", i)] = body
	}
	j := jobOf(t, files, domain.Scope{Kind: "full"})
	want := []string{"README.md", "AGENTS.md", linked, start, configuration, region}
	var bytes int64
	for _, p := range want {
		bytes += int64(len(files[p]))
	}
	for _, tc := range []struct {
		name    string
		budget  Budget
		limited string
	}{
		{"bytes", Budget{TotalBytes: bytes, PerDocumentBytes: 128, MaxDocuments: 100}, LimDocumentBudget},
		{"inventory entries", Budget{TotalBytes: 8192, PerDocumentBytes: 128, MaxDocuments: len(want)}, LimDocumentLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := runDocs(t, j, tc.budget)
			if !slices.Contains(d.Limitations, tc.limited) || d.Documents.Budget.ReadBytes != bytes {
				t.Fatalf("read budget is unaccounted for: %+v %v", d.Documents.Budget, d.Limitations)
			}
			for _, p := range want {
				if status, reason := statusOf(t, d, p); status != envdoc.DocSelected || reason != "" {
					t.Fatalf("product guidance %s: %s %s", p, status, reason)
				}
			}
			if tc.limited == LimDocumentBudget {
				if len(d.Documents.Items) != len(files)-1 {
					t.Fatalf("entries missing from the inventory: %+v", d.Documents.Items)
				}
				for _, p := range []string{policy, translation, "CONTRIBUTING.md", "packages/component0/README.md"} {
					if status, reason := statusOf(t, d, p); status != envdoc.DocBudgetExceeded || reason != envdoc.ReasonBudget {
						t.Fatalf("omitted document %s: %s %s", p, status, reason)
					}
				}
			}
			if !slices.ContainsFunc(d.Documents.Modules, func(m envdoc.ModuleDocs) bool {
				return m.Module == "go:plugins/console" && slices.Contains(m.Own, region) && slices.Contains(m.Inherited, "README.md")
			}) {
				t.Fatalf("module guidance or inheritance lost: %+v", d.Documents.Modules)
			}
		})
	}
}

func TestDiscoveryKeepsScopedGuideWhenPrimaryReadmeIsUnavailable(t *testing.T) {
	const link = "modules/dashboard/docs/panels.md"
	const start = "modules/dashboard/docs/getting-started/index.md"
	files := map[string]string{
		"README.md": "[Panels](" + link + ")\n",
		link:        strings.Repeat("panel details\n", 4),
		start:       strings.Repeat("start the dashboard\n", 5),
	}
	j := jobOf(t, files, domain.Scope{Kind: "paths", Paths: []string{"modules/dashboard"}})
	d := runDocs(t, j, Budget{TotalBytes: 100, PerDocumentBytes: 100, MaxDocuments: 3})
	for p, want := range map[string]string{"README.md": envdoc.ReasonOutOfScope, link: envdoc.ReasonBudget, start: ""} {
		if _, reason := statusOf(t, d, p); reason != want {
			t.Fatalf("document %s: %s, want %s", p, reason, want)
		}
	}
}
