package review

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

func TestDocumentedGoDefaultsReadActualDecisions(t *testing.T) {
	const (
		fetchDoc   = "`fetch_manifest` is enabled by default when omitted."
		replayDoc  = "`allow_replay` is disabled by default when omitted."
		fetchCode  = "return state.FetchManifest == nil || *state.FetchManifest"
		replayCode = "if state.AllowReplay == nil {\n\t\treturn false\n\t}"
	)
	tree := map[string]string{
		"go.mod":    "module example.org/product\n\ngo 1.23\n",
		"README.md": "# Runtime behavior\n\n" + fetchDoc + "\n\n" + replayDoc + "\n",
		"service/types.go": "package service\n\ntype Settings struct {\n\tFetchManifest *bool `json:\"fetch_manifest\"`\n" +
			"\tAllowReplay *bool `json:\"allow_replay\"`\n}\n",
		"service/validation.go": "package service\n\nfunc Validate(state Settings) bool {\n" +
			"\tif state.FetchManifest == nil { return false }\n\tif state.AllowReplay == nil { return true }\n\treturn false\n}\n",
		"service/notes.go": "package service\n\n/*\n" + fetchCode + "\n" + replayCode + "\n*/\n",
		"service/alpha.go": "package service\n\nvar unrelated = struct{ SomethingElse bool }{SomethingElse: true}\n",
		"service/runtime.go": "package service\n" + strings.Repeat("// preceding source\n", 60) +
			"\nfunc manifestEnabled(state Settings) bool {\n\t" + fetchCode + "\n}\n\n" +
			"func replayEnabled(state Settings) bool {\n\t" + replayCode + "\n\treturn *state.AllowReplay\n}\n",
		"testdata/service/defaults.go": "package service\nvar cfg = Settings{FetchManifest: true, AllowReplay: false}\n",
	}
	for i := range 12 {
		tree[fmt.Sprintf("service/declaration%02d.go", i)] = "package service\n// unrelated source is read under the normal budget\n"
	}
	j := treeJob(t, tree)
	script := coverageScript(t)
	script["documents"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[docsInput](t, q)
		id := docID(in, "README.md")
		return asJSON(docsOutput{
			Contracts: []record.Contract{{Statement: fetchDoc, AppliesTo: ".", EvidenceIDs: []string{id}},
				{Statement: replayDoc, AppliesTo: ".", EvidenceIDs: []string{id}}},
			Conflicts: []record.Conflict{}, Questions: []record.Question{}, InstructionNotes: []record.InstructionNote{},
		})
	}
	var overview []record.Excerpt
	script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		if in.Scope.WholeRun {
			overview = in.Scope.Excerpts
			if len(opencode.Prompt(q)) > MinRequestBytes || len(overview) > roleMaxCodeFiles {
				t.Fatal("the default witnesses exceeded the ordinary request bounds")
			}
			for _, option := range in.NavigationCandidates {
				if slices.Contains([]string{"service/types.go", "service/notes.go", "service/validation.go", "service/alpha.go", "testdata/service/defaults.go"}, option.Path) {
					t.Errorf("navigation associated a decoy with a default: %+v", option)
				}
			}
		}
		return asJSON(map[string]any{"candidates": []any{}})
	}
	_, res := runRoleWith(t, j, Handler{Budget: smallBudget, Static: secretsStub{}, Caller: &scriptedCaller{script: script}}, "1.27.4")
	if res["review.investigate"].Status != domain.TaskSucceeded {
		t.Fatalf("overview: %+v", res["review.investigate"])
	}
	for _, marker := range []string{fetchCode, replayCode} {
		if !slices.ContainsFunc(overview, func(e record.Excerpt) bool {
			return e.Path == "service/runtime.go" && e.Start > 50 && strings.Contains(e.Text, marker)
		}) {
			t.Fatalf("missing default decision %q in overview excerpts: %+v", marker, overview)
		}
	}
	if !slices.ContainsFunc(overview, func(e record.Excerpt) bool { return e.Path != "service/runtime.go" }) {
		t.Fatal("the default evidence displaced every ordinary excerpt")
	}
}
