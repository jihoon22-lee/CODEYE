package review

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

func TestDocumentRequestPreservesListBoundary(t *testing.T) {
	const procedure = "The file is ready only after the final check."
	for _, tc := range []struct {
		name      string
		markers   []string
		noSlots   bool
		oversized bool
	}{
		{name: "ordered", markers: []string{"1.", "2.", "3.", "4."}},
		{name: "unordered", markers: []string{"-", "-", "-", "-"}},
		{name: "no free slot", markers: []string{"1)", "2)", "3)", "4)"}, noSlots: true},
		{name: "oversized final step", markers: []string{"*", "*", "*", "*"}, oversized: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			intro := strings.Repeat("The project handles files.\n", roleMaxDocLines-4)
			steps := []string{"Open the file.", "Check its name.", "Write changes.", "Verify the saved file."}
			if tc.oversized {
				steps[3] += strings.Repeat("<", roleMaxDocBytes)
			}
			var body strings.Builder
			body.WriteString(intro + "\n")
			for i, step := range steps {
				fmt.Fprintf(&body, "%s %s\n", tc.markers[i], step)
			}
			body.WriteString("\n" + procedure + "\n")
			files := map[string]string{"README.md": body.String()}
			if tc.noSlots {
				for i := 1; i < roleMaxDocs; i++ {
					files[fmt.Sprintf("component%d/README.md", i)] = "Component guidance is preserved.\n"
				}
			}
			in, r, _ := collectDocumentSelection(t, files)
			prefix := in.Documents[0]
			if prefix.Path != "README.md" || prefix.StartLine != 1 || prefix.EndLine != roleMaxDocLines ||
				!strings.HasSuffix(prefix.Text, steps[2]) || prefix.IncompleteList != (tc.noSlots || tc.oversized) {
				t.Fatalf("incorrect final request boundary: %+v", prefix)
			}
			if tc.noSlots {
				if len(in.Documents) != roleMaxDocs || slices.ContainsFunc(in.Documents[1:], func(d docInput) bool { return d.Path == "README.md" }) {
					t.Fatalf("the list displaced another file: %+v", in.Documents)
				}
				return
			}
			if len(in.Documents) != 2 || !strings.Contains(in.Documents[1].Text, procedure) || in.Documents[1].IncompleteList {
				t.Fatalf("expected a complete contiguous tail or the next complete line: %+v", in.Documents)
			}
			tail := in.Documents[1]
			lines := strings.Split(body.String(), "\n")
			if tail.Text != strings.Join(lines[tail.StartLine-1:tail.EndLine], "\n") || tail.EndLine != 43 {
				t.Fatalf("tail differs from the source range: %+v", tail)
			}
			if !tc.oversized && (tail.StartLine != 41 || !strings.HasPrefix(tail.Text, tc.markers[3]+" "+steps[3])) {
				t.Fatalf("the last item is absent: %+v", tail)
			}
			if slices.Contains(r.Limitations, "document_excerpt_only") != tc.oversized {
				t.Fatalf("unexpected coverage limitations: %v", r.Limitations)
			}
		})
	}
}

func TestDocumentListBoundaryReflectsFittedWindows(t *testing.T) {
	lines := []string{"```text", "1. code", "2. code", "```", "1. Start", "", "   - Detail", "     explanation", "2. Finish", "", "- New list"}
	lists := markdownListRanges(lines)
	if want := []documentRange{{4, 9}, {10, 11}}; !slices.Equal(lists, want) {
		t.Fatalf("wrong Markdown list ranges: %+v", lists)
	}
	first := record.Excerpt{Path: "README.md", Start: 1, End: 8}
	last := record.Excerpt{Path: "README.md", Start: 9, End: 11}
	sent := []record.Excerpt{first, last}
	if incompleteDocumentList(first, sent, lists) || incompleteDocumentList(last, sent, lists) || !incompleteDocumentList(first, sent[:1], lists) {
		t.Fatal("partial list marker did not follow the windows in the fitted request")
	}
}
