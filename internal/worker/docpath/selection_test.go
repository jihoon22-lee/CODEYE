package docpath

import "testing"

func TestReadmeLinksPromoteOnlyLocalUnambiguousGuidance(t *testing.T) {
	s := NewSelection([]string{"workspace/README.md", "workspace/README.fr.md", "workspace/docs/panels.md",
		"workspace/docs/start/+page.md", "workspace/docs/inline.md", "workspace/docs/fenced.md", "workspace/docs/remote.md",
		"workspace/docs/ambiguous.md", "workspace/docs/ambiguous/index.md", "workspace/docs/issue_template/guide.md"})
	const readme = "# Dashboard\n" +
		"[Overview](./docs/panels.md#grid) [Start](docs/start/)\n" +
		"`[Inline code](docs/inline.md)`\n```markdown\n[Fenced example](docs/fenced.md)\n```\n" +
		"[Remote](https://example.test/workspace/docs/remote.md) [Website](/workspace/docs/remote.md)\n" +
		"[Ambiguous](docs/ambiguous) [Translation](README.fr.md) [Template](docs/issue_template/guide.md)\n"
	if !s.WithReadmeLinks("workspace/README.md", readme) {
		t.Fatal("the only regional primary README did not yield local links")
	}
	for _, p := range []string{"workspace/docs/panels.md", "workspace/docs/start/+page.md"} {
		if !s.Product(p, Classify(p)) {
			t.Errorf("linked guidance was skipped: %s", p)
		}
	}
	for _, p := range []string{"workspace/docs/inline.md", "workspace/docs/fenced.md", "workspace/docs/remote.md", "workspace/docs/ambiguous.md",
		"workspace/docs/ambiguous/index.md", "workspace/README.fr.md", "workspace/docs/issue_template/guide.md"} {
		if s.Product(p, Classify(p)) {
			t.Errorf("candidate should not be treated as product guidance: %s", p)
		}
	}
}
