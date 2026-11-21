package review

import (
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

func TestDocumentedCDefinitionUsesAnExecutableBody(t *testing.T) {
	const name = "load_profile"
	const usage = "Use `load_profile` to read the profile.\n"
	const definition = "PUBLIC(const profile *) load_profile(\n    const char *name)\n{\n    return dispatch_profile(name);\n}\n"
	for _, tc := range []struct {
		name  string
		extra string
		want  bool
	}{
		{name: "unique definition", want: true},
		{name: "ambiguous within scope", extra: "const profile *load_profile(const char *name) { return 0; }\n"},
		{name: "a private function does not define the API", extra: "static const profile *load_profile(const char *name) { return 0; }\n", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"app/docs/usage.md": usage,
				"app/public.h":      "PUBLIC(const profile *) load_profile(const char *name);\n",
				"app/client.c":      "/* int load_profile(void) { return 0; } */\nvoid run(void) { load_profile(\"sample\"); }\n",
				"app/entry.c":       strings.Repeat("/* Preamble. */\n", 70) + definition,
				"app/macro.c":       "#define MOCK_PROFILE \\\nint load_profile(void) { return 0; }\n",
				"other/entry.c":     definition,
			}
			if tc.extra != "" {
				files["app/other.c"] = tc.extra
			}
			j := treeJob(t, files)
			s := &roleStage{ctx: t.Context(), j: j}
			entry := s.entries()["app/docs/usage.md"]
			doc, _, ok, err := s.excerpt(entry, "document", "", 1, 1, roleMaxDocBytes)
			if err != nil || !ok {
				t.Fatalf("document excerpt: %v", err)
			}
			docs := record.Record{Evidence: []record.Excerpt{doc}, Contracts: []record.Contract{
				{Statement: "`" + name + "`는 프로필을 읽는다.", AppliesTo: "app", EvidenceIDs: []string{doc.ID}},
			}}
			groups := planCodeGroupsFor(j.Snapshot.Manifest, j.Scope, s.h.secondary(), changedPaths(j))
			selected, err := s.contractWitnesses(groups, docs)
			if err != nil || (len(selected.cDefinitions) == 1) != tc.want {
				t.Fatalf("definition selection %+v: %v", selected.cDefinitions, err)
			}
			if tc.want {
				corpus, _, _, err := s.codeCorpus(docs)
				if err != nil || len(corpus) == 0 || corpus[0].Path != "app/entry.c" || corpus[0].Kind != "code" ||
					corpus[0].Start != 71 || corpus[0].End != 75 || corpus[0].Text != strings.TrimSuffix(definition, "\n") {
					t.Fatalf("first window should contain the definition and body: %+v (%v)", corpus, err)
				}
			}
		})
	}
}
