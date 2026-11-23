package review

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// A citation of a line range inside a sent excerpt narrows to the evidence
// ID of that range on the same blob (docs/REVIEW.md#인용); a range outside
// the excerpt, a malformed range, or a long excerpt cited without a range is
// an output error; a comparison keeps the excerpt's own ID.
func TestCitationNarrowsToSnapshotRange(t *testing.T) {
	snapshot := strings.Repeat("5", 64)
	sha := strings.Repeat("a", 64)
	var b strings.Builder
	for i := 1; i <= 120; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	text := strings.TrimSuffix(b.String(), "\n")
	whole := record.Excerpt{ID: record.ExcerptID(snapshot, "pkg/a.go", sha, 1, 120), Path: "pkg/a.go", SHA256: sha, Start: 1, End: 120,
		Kind: "code", Origin: "context", Text: text}
	short := record.Excerpt{ID: record.ExcerptID(snapshot, "pkg/b.go", sha, 1, 10), Path: "pkg/b.go", SHA256: sha, Start: 1, End: 10,
		Kind: "code", Origin: "context", Text: "x"}

	c := newCiter(snapshot, []record.Excerpt{whole, short})
	ids, base, errs := c.resolve([]string{whole.ID + ":40-44", short.ID}, citeNarrow)
	want := record.ExcerptID(snapshot, "pkg/a.go", sha, 40, 44)
	if len(errs) != 0 || ids[0] != want || ids[1] != short.ID || base[0] != whole.ID {
		t.Fatalf("ids %v base %v errs %v", ids, base, errs)
	}
	n := c.catalog()[want]
	if n.Origin != record.OriginCited || n.Start != 40 || n.End != 44 || n.SHA256 != sha || n.Text != "line 40\nline 41\nline 42\nline 43\nline 44" {
		t.Fatalf("narrowed %+v", n)
	}
	if used := c.used(ids); len(used) != 1 || used[0].ID != want {
		t.Fatalf("used %v", used)
	}

	for raw, code := range map[string]string{
		whole.ID:              errCitationMissing, // 120 lines, no range
		whole.ID + ":0-3":     errCitationInvalid, // malformed
		whole.ID + ":9-3":     errCitationInvalid, // reversed
		whole.ID + ":100-130": errCitationInvalid, // past the excerpt
		short.ID + ":11":      errCitationInvalid, // past the excerpt
		whole.ID + ":1-81":    errCitationInvalid, // longer than one citation
	} {
		if _, _, errs := newCiter(snapshot, []record.Excerpt{whole, short}).resolve([]string{raw}, citeNarrow); !slices.Equal(errs, []string{code}) {
			t.Errorf("%s: errs %v, want %s", raw, errs, code)
		}
	}
	if ids, _, errs := newCiter(snapshot, []record.Excerpt{whole}).resolve([]string{whole.ID + ":40-44"}, citeStrip); len(errs) != 0 || ids[0] != whole.ID {
		t.Fatalf("strip %v %v", ids, errs)
	}
	if ids, _, errs := newCiter(snapshot, []record.Excerpt{whole}).resolve([]string{whole.ID}, citeOptional); len(errs) != 0 || ids[0] != whole.ID {
		t.Fatalf("optional %v %v", ids, errs)
	}
	// An unknown ID is kept for the caller's evidence_id_unknown check.
	unknown := "ev_" + strings.Repeat("0", 24)
	if ids, _, errs := newCiter(snapshot, []record.Excerpt{whole}).resolve([]string{unknown + ":1-2"}, citeNarrow); len(errs) != 0 || ids[0] != unknown {
		t.Fatalf("unknown %v %v", ids, errs)
	}
}

// A window inside another excerpt of the same blob travels as a reference
// and reads back with its own lines.
func TestRequestExcerptsReferenceContainedWindows(t *testing.T) {
	sha := strings.Repeat("b", 64)
	whole := record.Excerpt{ID: "ev_" + strings.Repeat("1", 24), Path: "a.go", SHA256: sha, Start: 1, End: 4, Kind: "code", Text: "a\nb\nc\nd"}
	window := record.Excerpt{ID: "ev_" + strings.Repeat("2", 24), Path: "a.go", SHA256: sha, Start: 2, End: 3, Kind: "code", Text: "b\nc"}
	other := record.Excerpt{ID: "ev_" + strings.Repeat("3", 24), Path: "a.go", SHA256: strings.Repeat("c", 64), Start: 2, End: 3, Kind: "code", Text: "x\ny"}
	raw, err := json.Marshal(excerptList{window, whole, other})
	if err != nil {
		t.Fatal(err)
	}
	var sent []map[string]any
	if err := json.Unmarshal(raw, &sent); err != nil {
		t.Fatal(err)
	}
	// The window inside the whole file carries no text; the other blob's
	// window with the same lines is not inside it.
	if _, has := sent[0]["text"]; has || sent[0]["within"] != whole.ID || sent[1]["text"] != "1| a\n2| b\n3| c\n4| d" || sent[2]["text"] != "2| x\n3| y" {
		t.Fatalf("request excerpts %s", raw)
	}
	var back excerptList
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if len(back) != 3 || back[0].Text != "b\nc" || back[0].Within != "" || back[1].Text != whole.Text || back[2].Text != "x\ny" {
		t.Fatalf("read back %+v", back)
	}
}

// One rule derives every cap from the request budget. At the floor the
// caps are those of the earlier fixed request; larger budgets only raise
// them, and the output budget bounds prose and candidates.
func TestBudgetLimits(t *testing.T) {
	floor := smallBudget.limits()
	if floor.request != 40000 || floor.reserve != 3072 || floor.source != 12000 || floor.unit != 2400 || floor.excerpts != 8 ||
		floor.verifyExcerpts != 40 || floor.expansion != 6 || floor.expansionBytes != 8000 || floor.text != 400 || floor.listText != 600 ||
		floor.mechanism != 800 || floor.candidates != 4 {
		t.Fatalf("floor %+v", floor)
	}
	def := Budget{}.limits()
	if def.request != DefaultRequestBytes || def.source != DefaultRequestBytes-fixedRequestBytes-def.reserve || def.excerpts != 64 ||
		def.unit != def.source/5 || def.text != record.MaxProseText || def.mechanism != record.MaxMechanismText || def.candidates != 8 {
		t.Fatalf("default %+v", def)
	}
	top := Budget{RequestBytes: MaxRequestBytes, OutputTokens: MaxOutputTokens}.limits()
	if top.candidates != 12 || top.candidates > record.MaxCandidates || top.verifyExcerpts != 128 || top.expansion != 24 {
		t.Fatalf("max %+v", top)
	}
	prev := floor
	for _, req := range []int{40000, 50000, 65536, 131072, 262144, 524288, 1 << 20} {
		l := Budget{RequestBytes: req, OutputTokens: MinOutputTokens}.limits()
		if l.source < prev.source || l.unit < prev.unit || l.excerpts < prev.excerpts || l.source+l.reserve > l.request ||
			l.unit > l.source || l.text != 400 {
			t.Fatalf("%d: %+v after %+v", req, l, prev)
		}
		prev = l
	}
	if got := def.candidateLimit(1); got != 4 {
		t.Fatalf("candidates for 1 byte %d", got)
	}
	if got := def.candidateLimit(64 << 10); got != 5 {
		t.Fatalf("candidates for 64 KiB %d", got)
	}
	if got := def.candidateLimit(1 << 20); got != def.candidates {
		t.Fatalf("candidates for 1 MiB %d", got)
	}
}

// The plan does not depend on the request budget: the same snapshot and
// scope give the same slot keys at every budget (A37).
func TestPlanKeysDoNotDependOnBudget(t *testing.T) {
	j := worstCaseJob(t, nil)
	var first []string
	for _, b := range []Budget{smallBudget, {}, {RequestBytes: MaxRequestBytes, OutputTokens: MaxOutputTokens}} {
		h, err := discovery.Wrap(Handler{Static: vetStub{obs: worstCaseObservations}, Caller: &scriptedCaller{}, Budget: b},
			discovery.Config{Observer: discovery.StaticObserver{Versions: map[string]string{"go": "1.27.4"}}})
		if err != nil {
			t.Fatal(err)
		}
		plan, err := h.Plan(WithAttemptSourceIndex(t.Context(), j), j)
		if err != nil {
			t.Fatal(err)
		}
		var keys []string
		for _, p := range plan {
			keys = append(keys, p.Key)
		}
		if first == nil {
			first = keys
		} else if !slices.Equal(keys, first) {
			t.Fatalf("budget %+v plan %v, want %v", b, keys, first)
		}
	}
	if !slices.ContainsFunc(first, func(k string) bool { return strings.HasPrefix(k, "review.investigate.s") }) {
		t.Fatalf("no slots planned: %v", first)
	}
}
