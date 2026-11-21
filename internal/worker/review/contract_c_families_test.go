package review

import (
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

const parcelC = `static int resolve_parcel(int slot, int mode)
{
    if (mode)
    {
        return rack_exact(slot);
    }
    else
    {
        return rack_nearby(slot);
    }
}

int parcel_lookup(int slot)
{
    return resolve_parcel(slot, 0);
}

int parcel_lookupMode(int slot)
{
    return resolve_parcel(slot, 2);
}
`

const parcelGuide = "# Parcel guide\n\nUse the `Mode` variant to limit a lookup.\n\n" +
	"```c\n\nint parcel = parcel_lookupMode(5);\n```\n"

func TestDocumentedCApiFamilySuppliesBothWrappersAndModeBranch(t *testing.T) {
	const sourcePath = "app/parcel.c"
	for _, tc := range []struct {
		name, replace, with string
		guide               string
		other               string
		setup               func(*roleStage)
		want                bool
	}{
		{name: "call in same guide and mode in same file", want: true},
		{name: "prose only", guide: strings.ReplaceAll(parcelGuide, "```c", "```text")},
		{name: "prototype only", guide: strings.ReplaceAll(parcelGuide, "int parcel = parcel_lookupMode(5);", "int parcel_lookupMode(int slot);")},
		{name: "comment only", guide: strings.ReplaceAll(parcelGuide, "int parcel =", "// int parcel =")},
		{name: "ambiguous plain suffix", guide: strings.ReplaceAll(parcelGuide, "`Mode`", "Mode")},
		{name: "declaration not the same shape", replace: "int parcel_lookupMode(int slot)", with: "long parcel_lookupMode(int slot)"},
		{name: "nonliteral mode", replace: "resolve_parcel(slot, 2)", with: "resolve_parcel(slot, selected_mode)"},
		{name: "different private call", replace: "resolve_parcel(slot, 2)", with: "resolve_other(slot, 2)"},
		{name: "same interpreted mode", replace: "resolve_parcel(slot, 2)", with: "resolve_parcel(slot, false)"},
		{name: "mode changed before branch", replace: "    if (mode)", with: "    mode = 1;\n    if (mode)"},
		{name: "conditional wrapper", replace: "int parcel_lookupMode(int slot)\n{\n    return resolve_parcel(slot, 2);\n}",
			with: "#if(OPTIONAL)\nint parcel_lookupMode(int slot)\n{\n    return resolve_parcel(slot, 2);\n}\n#endif"},
		{name: "ambiguous wrapper across source", other: "int parcel_lookupMode(int slot)\n{\n    return extras(slot);\n}\n"},
		{name: "branch exceeds the window", replace: "return rack_exact(slot);", with: strings.Repeat("update_parcel(slot);\n        ", 25) + "return rack_exact(slot);"},
		{name: "withheld implementation", setup: func(s *roleStage) { s.screen.withheld = map[string]bool{sourcePath: true} }},
		{name: "unchanged PR source", setup: func(s *roleStage) {
			s.j.PullRequest = &domain.PullRequestJob{Changed: []string{"app/README.md"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			guide, source := parcelGuide, parcelC
			if tc.guide != "" {
				guide = tc.guide
			}
			if tc.replace != "" {
				source = strings.Replace(source, tc.replace, tc.with, 1)
			}
			files := map[string]string{"app/README.md": guide, sourcePath: strings.Repeat("/* prelude */\n", 60) + source}
			if tc.other != "" {
				files["app/extra.c"] = tc.other
			}
			j := treeJob(t, files)
			s := roleStage{ctx: t.Context(), j: j}
			if tc.setup != nil {
				tc.setup(&s)
			}
			doc, _, ok, err := s.excerpt(s.entries()["app/README.md"], "document", "", 1, 3, witnessMaxExcerpt)
			if err != nil || !ok {
				t.Fatalf("guide: %+v, %v", doc, err)
			}
			docs := record.Record{Contracts: []record.Contract{{Statement: "제한된 조회에는 Mode 변형을 사용한다.", AppliesTo: "app", EvidenceIDs: []string{doc.ID}}},
				Evidence: []record.Excerpt{doc}}
			groups := planCodeGroupsFor(s.j.Snapshot.Manifest, s.j.Scope, s.h.secondary(), changedPaths(s.j))
			witnesses, err := s.contractWitnesses(groups, docs)
			if err != nil || (len(witnesses.cFamily) == 2) != tc.want {
				t.Fatalf("unexpected C family selection: %+v, %v", witnesses.cFamily, err)
			}
			if !tc.want {
				return
			}
			windows, _, _, err := s.codeCorpus(docs)
			if err != nil {
				t.Fatal(err)
			}
			wrappers, decision := false, false
			for _, window := range windows {
				if window.Path != sourcePath || window.Start <= 60 {
					continue
				}
				wrappers = wrappers || strings.Contains(window.Text, "int parcel_lookup(") && strings.Contains(window.Text, "int parcel_lookupMode(")
				decision = decision || strings.Contains(window.Text, "if (mode)") && strings.Contains(window.Text, "return rack_exact(") &&
					strings.Contains(window.Text, "return rack_nearby(")
			}
			if !wrappers || !decision {
				t.Fatalf("unexpected C family evidence: wrappers %v, decision %v, windows %+v", wrappers, decision, windows)
			}
		})
	}
}
