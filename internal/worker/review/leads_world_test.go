package review

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// The closed world reads a pull request's unchanged sources too: a method
// binds when no unchanged source declares another member of its name, and
// a C prototype's one definition outside the change is found and refused
// with that file's reason (the prototype is sent).
func TestLeadWorldReadsAPullRequestsUnchangedSources(t *testing.T) {
	j := treeJob(t, map[string]string{"app/job.ts": tsJob, "app/report.ts": "export class Report {\n  show() {\n    return 1;\n  }\n}\n"})
	j.PullRequest = &domain.PullRequestJob{Changed: []string{"app/job.ts"}}
	s := leadStage(t, j)
	window, anchor := leadWindow(t, s, "app/job.ts", "return this.helper(x)", 0)
	if l := resolveOne(t, s, record.LeadNeedDefinition, "helper", window, anchor); !l.Resolved() || len(l.Targets) != 1 || l.Targets[0].Path != "app/job.ts" ||
		l.Targets[0].Start != 2 || l.Targets[0].End != 4 {
		t.Fatalf("lead %+v", l)
	}

	j = treeJob(t, prototypeCase{extra: map[string]string{"native/rates.c": ratesSource}}.tree())
	j.PullRequest = &domain.PullRequestJob{Changed: []string{"native/app.c", "native/rates.h"}}
	s = leadStage(t, j)
	window, anchor = leadWindow(t, s, "native/app.c", "return rate(", 0)
	if l := resolveOne(t, s, record.LeadNeedDefinition, "rate", window, anchor, sentLines(t, s, "native/rates.h", 4, 4)); l.Resolved() || l.Reason != record.LeadOutsideChange {
		t.Fatalf("lead %+v, want the definition outside the change", l)
	}
}

// A question the world would answer by tokenizing more than maxWorldFiles
// sources (those holding the name) is unanswered: lead_scan_limit, the
// method not chosen.
func TestLeadWorldPastItsFileBoundIsUnanswered(t *testing.T) {
	tree := map[string]string{"app/job.ts": tsJob}
	for k := range maxWorldFiles {
		tree[fmt.Sprintf("app/notes/n%03d.ts", k)] = fmt.Sprintf("// helper notes %d\nexport const n%d = %d;\n", k, k, k)
	}
	s := leadStage(t, treeJob(t, tree))
	window, anchor := leadWindow(t, s, "app/job.ts", "return this.helper(x)", 0)
	if l := resolveOne(t, s, record.LeadNeedDefinition, "helper", window, anchor); l.Resolved() || l.Reason != record.LeadScanLimit {
		t.Fatalf("lead %+v, want lead_scan_limit", l)
	}
}

// The leads of one investigate task share the world: it reads each source
// once, and every lead's result is the one it gets alone.
func TestLeadWorldIsSharedByATasksLeads(t *testing.T) {
	tree := map[string]string{"app/job.ts": tsJob + "\nexport class Other {\n  shown() {\n    return 2;\n  }\n\n  show() {\n    return this.shown();\n  }\n}\n",
		"app/report.ts": "export class Report {\n  helpers() {\n    return 1;\n  }\n}\n", "app/util.ts": "export const one = 1;\n"}
	s := leadStage(t, treeJob(t, tree))
	window, helper := leadWindow(t, s, "app/job.ts", "return this.helper(x)", 0)
	whole, shown := leadWindow(t, s, "app/job.ts", "return this.shown()", 0)
	leads := []record.Lead{
		{ID: "lead_1", Need: record.LeadNeedDefinition, Symbol: "helper", EvidenceID: helper.ID, Suspicion: "호출한 함수가 오류를 돌려주는지 확인해야 한다."},
		{ID: "lead_2", Need: record.LeadNeedDefinition, Symbol: "shown", EvidenceID: shown.ID, Suspicion: "호출한 함수가 오류를 돌려주는지 확인해야 한다."},
		{ID: "lead_3", Need: record.LeadNeedDefinition, Symbol: "helper", EvidenceID: helper.ID, Suspicion: "호출한 함수가 오류를 돌려주는지 확인해야 한다."},
	}
	anchors := map[string]record.Excerpt{helper.ID: helper, shown.ID: shown}
	sent := []record.Excerpt{window, whole}
	r := s.newLeadResolver(sent)
	reads := map[string]int{}
	r.worldRead = func(e source.Entry) (string, bool, error) {
		reads[e.Path]++
		return s.worldBytes(e)
	}
	var together []record.Lead
	for _, l := range leads {
		out, err := r.resolve(l, anchors[l.EvidenceID])
		if err != nil {
			t.Fatal(err)
		}
		together = append(together, out)
	}
	for i, l := range leads {
		alone, err := s.newLeadResolver(sent).resolve(l, anchors[l.EvidenceID])
		if err != nil {
			t.Fatal(err)
		}
		if !together[i].Resolved() || together[i].Reason != alone.Reason || !slices.Equal(together[i].Targets, alone.Targets) {
			t.Fatalf("lead %d with the others %+v, alone %+v", i, together[i], alone)
		}
	}
	if len(reads) != 3 || reads["app/job.ts"] != 1 || reads["app/report.ts"] != 1 || reads["app/util.ts"] != 1 {
		t.Fatalf("world reads %v, want each source once", reads)
	}
}

// The world searches a source larger than a review sends (maxFileBytes):
// one that does not hold the name leaves the method bound, and one holding
// it, too large to tokenize, leaves it unresolved.
func TestLeadWorldSearchesSourcesLargerThanAReviewSends(t *testing.T) {
	var b strings.Builder
	for k := 0; b.Len() <= maxFileBytes; k++ {
		fmt.Fprintf(&b, "export const table%d = [%d, %d, %d];\n", k, k, k+1, k+2)
	}
	for _, c := range []struct {
		name, big, reason string
	}{
		{"without the name", b.String(), ""},
		{"holding the name", b.String() + "export const helper = 1;\n", record.LeadDefinitionMissing},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := leadStage(t, treeJob(t, map[string]string{"app/job.ts": tsJob, "app/tables.js": c.big}))
			window, anchor := leadWindow(t, s, "app/job.ts", "return this.helper(x)", 0)
			l := resolveOne(t, s, record.LeadNeedDefinition, "helper", window, anchor)
			if c.reason == "" && (!l.Resolved() || len(l.Targets) != 1 || l.Targets[0].Path != "app/job.ts") || c.reason != "" && (l.Resolved() || l.Reason != c.reason) {
				t.Fatalf("lead %+v, want %q", l, c.reason)
			}
		})
	}
}

// The world cannot answer for a language whose sources pass maxWorldBytes,
// or when the snapshot holds no bytes of a source it may need: a link of
// the language or a submodule (any language's sources may be there). A
// method is left unanswered (lead_scan_limit, lead_read_failed) and a C
// prototype sent alone; nothing is read past the bound.
func TestLeadWorldNeedsEverySourceAcquired(t *testing.T) {
	huge := int64(maxWorldBytes)
	for _, c := range []struct {
		name   string
		extra  source.Entry
		reason string
	}{
		{"past the byte bound", source.Entry{Path: "app/huge.js", Kind: "file", Size: &huge, SHA256: strings.Repeat("0", 64)}, record.LeadScanLimit},
		{"a link", source.Entry{Path: "app/linked.ts", Kind: "symlink", ExcludedReason: "link_not_followed"}, record.LeadReadFailed},
		{"a submodule", source.Entry{Path: "third_party/lib", Kind: "submodule", ExcludedReason: "submodule_not_acquired"}, record.LeadReadFailed},
	} {
		t.Run(c.name, func(t *testing.T) {
			j := treeJob(t, map[string]string{"app/job.ts": tsJob})
			j.Snapshot.Manifest.Entries = append(j.Snapshot.Manifest.Entries, c.extra)
			s := leadStage(t, j)
			window, anchor := leadWindow(t, s, "app/job.ts", "return this.helper(x)", 0)
			read := 0
			r := s.newLeadResolver([]record.Excerpt{window})
			r.worldRead = func(e source.Entry) (string, bool, error) {
				read++
				return s.worldBytes(e)
			}
			l := record.Lead{ID: "lead_1", Need: record.LeadNeedDefinition, Symbol: "helper", EvidenceID: anchor.ID, Suspicion: "호출한 함수가 오류를 돌려주는지 확인해야 한다."}
			out, err := r.resolve(l, anchor)
			if err != nil || out.Resolved() || out.Reason != c.reason || read != 0 {
				t.Fatalf("lead %+v (%v) after %d reads, want %s", out, err, read, c.reason)
			}
		})
	}
	j := treeJob(t, prototypeCase{extra: map[string]string{"native/rates.c": ratesSource}}.tree())
	j.Snapshot.Manifest.Entries = append(j.Snapshot.Manifest.Entries, source.Entry{Path: "third_party/zlib", Kind: "submodule", ExcludedReason: "submodule_not_acquired"})
	s := leadStage(t, j)
	window, anchor := leadWindow(t, s, "native/app.c", "return rate(", 0)
	if l := resolveOne(t, s, record.LeadNeedDefinition, "rate", window, anchor); !l.Resolved() || len(l.Targets) != 1 || l.Targets[0].Path != "native/rates.h" {
		t.Fatalf("lead %+v, want the prototype alone beside a submodule", l)
	}
}

// The world's word search finds a name as a whole word only, but wherever
// a token of it may stand: after a number (0x1A, a token after 0x1), a
// string's quote, a sign or a non-ASCII byte; not inside a longer name.
func TestLeadWorldWordSearch(t *testing.T) {
	for _, c := range []struct {
		text, word string
		holds      bool
	}{
		{"return rate(2);", "rate", true},
		{"generate(2); rates; rate_limit; _rate", "rate", false},
		{"0rate", "rate", true},
		{"x0rate", "rate", true},
		{"$rate é rateé", "rate", true},
		{"obj['helper'] = f", "helper", true},
		{"this[k] = v", "this[", true},
		{"isthis[k]", "this[", false},
		{"Object.assignment", "Object.assign", false},
		{"Reflect.set(o, k, v)", "Reflect.", true},
		{"", "rate", false},
	} {
		if got := holdsWord(c.text, c.word); got != c.holds {
			t.Errorf("holdsWord(%q, %q) = %v, want %v", c.text, c.word, got, c.holds)
		}
	}
}
