package review

import (
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// ledger is a plan in review order: the overview prefix, then the slots.
func ledger(p slicePlan) []string {
	var out []string
	for _, f := range p.Overview {
		out = append(out, fmt.Sprintf("overview %-26s %-8s %s", f.Path, f.Change, f.Rank))
	}
	for _, s := range p.Slices {
		for _, f := range s.Files {
			out = append(out, fmt.Sprintf("%s     %-26s %-8s %s", s.Key, f.Path, f.Change, f.Rank))
		}
	}
	return out
}

// Review priority of a pull-request run (docs/REVIEW.md#검토-우선순위): the
// changed files (a source file deep in a large module, a test and a
// changed fixture) are the overview's first files, then the files beside
// them, then the usual order; slots never mix tiers; a run without a pull
// request plans exactly as before.
func TestPullRequestChangedFilesFirst(t *testing.T) {
	files := map[string]int64{"go.mod": 30, "web/package.json": 30, "cmd/tool/main.go": 1500}
	for f := range 30 {
		files[fmt.Sprintf("internal/a/a%02d.go", f)] = 2000
		files[fmt.Sprintf("internal/b/b%02d.go", f)] = 2000
		files[fmt.Sprintf("internal/b/b%02d_test.go", f)] = 2000
	}
	for f := range 10 {
		files[fmt.Sprintf("web/src/w%02d.ts", f)] = 1500
		files[fmt.Sprintf("fixtures/m%d/go.mod", f)] = 30
		files[fmt.Sprintf("fixtures/m%d/main.go", f)] = 300
	}
	m := syntheticManifest(files)
	scope := domain.Scope{Kind: "full"}
	before := planSlices(m, scope, record.DefaultSecondary)
	if !reflect.DeepEqual(before, planSlicesFor(m, scope, record.DefaultSecondary, nil)) {
		t.Fatal("a run without a pull request plans differently")
	}
	changed := []string{"fixtures/m3/main.go", "internal/b/b27.go", "internal/b/b27_test.go"}
	after := planSlicesFor(m, scope, record.DefaultSecondary, changed)
	checkPlanFor(t, m, scope, changed, after)
	t.Logf("before (branch run):\n%s", strings.Join(ledger(before)[:12], "\n"))
	t.Logf("after (pull request changing %v):\n%s", changed, strings.Join(ledger(after)[:12], "\n"))
	got := map[string]bool{}
	for _, f := range after.Overview[:len(changed)] {
		got[f.Path] = true
		if f.Change != changeChanged {
			t.Fatalf("overview head %+v", f)
		}
	}
	for _, p := range changed {
		if !got[p] {
			t.Fatalf("changed %s is not among the overview's first files: %v", p, ledger(after)[:6])
		}
	}
	// After the changed files come their neighbors (internal/b, fixtures/m3),
	// before any other file.
	seenOther := false
	all := append(append([]plannedFile{}, after.Overview...), after.Rest...)
	for _, f := range after.Overview[len(changed):] {
		switch f.Change {
		case changeNeighbor:
			if seenOther {
				t.Fatalf("neighbor %s after another file", f.Path)
			}
		case "":
			seenOther = true
		default:
			t.Fatalf("changed file %s after the first", f.Path)
		}
	}
	for _, f := range all {
		want := ""
		switch {
		case f.Path == changed[0] || f.Path == changed[1] || f.Path == changed[2]:
			want = changeChanged
		case strings.HasPrefix(f.Path, "internal/b/") || strings.HasPrefix(f.Path, "fixtures/m3/"):
			want = changeNeighbor
		}
		if f.Change != want {
			t.Fatalf("%s labelled %q, want %q", f.Path, f.Change, want)
		}
	}
	for _, s := range after.Slices {
		for _, f := range s.Files {
			if f.Change != s.Change {
				t.Fatalf("slot %s mixes tiers", s.Key)
			}
		}
	}
	if before.Overview[0].Path == after.Overview[0].Path && before.Overview[1].Path == after.Overview[1].Path {
		t.Fatal("the pull request did not change the order")
	}
	// Packing at review.scope time keeps the tiers above the diagnostics: a
	// diagnosed file outside the changes comes after the changed and
	// neighbor files.
	rest := append([]plannedFile{}, after.Rest...)
	for i := range rest {
		if rest[i].Path == "internal/a/a29.go" {
			rest[i].Rank, rest[i].Observations, rest[i].Severity = record.RankToolObservations, 5, 3
		}
	}
	slots := packSlots(rest)
	checkSlots(t, slots)
	tierSeen := 0
	for _, s := range slots {
		tier := map[string]int{changeChanged: 0, changeNeighbor: 1, "": 2}[s.Change]
		if tier < tierSeen {
			t.Fatalf("slot %s (%q) after a lower tier", s.Key, s.Change)
		}
		tierSeen = tier
	}
}

// A pull-request run end to end through the role pack: the overview
// investigate request offers the changed files first, review.scope records
// the pull-request order and labels (and the decoder accepts them), and no
// file is sent twice.
func TestPullRequestRunReviewsChangedFilesFirst(t *testing.T) {
	j := coverageJob(t)
	base := planSlices(j.Snapshot.Manifest, j.Scope, record.DefaultSecondary)
	last := base.Rest[len(base.Rest)-1].Path
	changed := []string{last}
	j.PullRequest = &domain.PullRequestJob{Number: 7, BaseSHA: strings.Repeat("1", 40), HeadSHA: strings.Repeat("2", 40), Changed: changed}
	f := &roleCaller{script: coverageScript(t)}
	runRoleWith(t, j, RoleHandler{Static: secretsStub{}, Caller: f, MaxCalls: record.MaxMaxCalls}, "1.27.4")
	var first []string
	seen := map[string]bool{}
	for _, q := range f.requests {
		if q.Role != "investigate" {
			continue
		}
		files := investigateFiles(t, q)
		if first == nil {
			first = files
		}
		for _, p := range files {
			if seen[p] {
				t.Fatalf("%s sent twice", p)
			}
			seen[p] = true
		}
	}
	if len(first) == 0 || !slices.Contains(first, last) {
		t.Fatalf("the overview did not offer the changed %s: %v", last, first)
	}
	b, err := os.ReadFile(scopePath(j))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := record.DecodeScope(b, scopeExpect(j))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Priority.Order != record.PriorityPullRequestFirst {
		t.Fatalf("order %s", doc.Priority.Order)
	}
	labelled := 0
	for _, f := range doc.Overview.Files {
		if f.Path == last && f.PullRequest != record.PullRequestChanged {
			t.Fatalf("overview file %+v", f)
		}
		if f.PullRequest != "" {
			labelled++
		}
	}
	for _, sl := range doc.Slices {
		for _, f := range sl.Files {
			if f.PullRequest != sl.PullRequest {
				t.Fatalf("slot %s mixes tiers", sl.Key)
			}
		}
	}
	if labelled == 0 {
		t.Fatal("no labelled file")
	}
	// The same artifact under the branch order is refused.
	doc.Priority.Order = record.PriorityOrder
	if _, err := record.MarshalScope(doc, scopeExpect(j)); err == nil {
		t.Fatal("pull-request labels accepted under the branch order")
	}
}
