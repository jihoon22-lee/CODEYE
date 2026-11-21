package review

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// syntheticManifest builds a manifest of entries with the given sizes; no
// file exists, which is all the planner needs (it reads no file).
func syntheticManifest(files map[string]int64) source.Manifest {
	m := source.Manifest{SchemaVersion: "0.1", Kind: "local"}
	for _, p := range sortedKeys(files) {
		n := files[p]
		m.Entries = append(m.Entries, source.Entry{Path: p, Kind: "file", Size: &n, SHA256: strings.Repeat("a", 64)})
	}
	return m
}

// randomTree is a repository of Go, Python, JS/TS, Rust and C/C++ modules
// with files of random size, data and excluded directories included.
func randomTree(r *rand.Rand) map[string]int64 {
	files := map[string]int64{}
	size := func() int64 {
		switch r.IntN(4) {
		case 0:
			return int64(r.IntN(60))
		case 1:
			return int64(r.IntN(400))
		}
		return int64(r.IntN(6000))
	}
	type eco struct{ manifest, ext, entry, test string }
	ecos := []eco{{"go.mod", ".go", "main.go", "x_test.go"}, {"pyproject.toml", ".py", "app.py", "test_x.py"}, {"package.json", ".ts", "index.ts", "x.test.ts"},
		{"Cargo.toml", ".rs", "main.rs", "tests.rs"}, {"CMakeLists.txt", ".cc", "main.cc", "x_test.cc"}}
	for m := range 1 + r.IntN(7) {
		e := ecos[r.IntN(len(ecos))]
		dir := fmt.Sprintf("m%d", m)
		if r.IntN(4) == 0 {
			dir += "/nested"
		}
		files[dir+"/"+e.manifest] = 40
		if r.IntN(2) == 0 {
			files[dir+"/"+e.entry] = size()
		}
		if r.IntN(2) == 0 {
			files[dir+"/"+e.test] = size()
		}
		for f := range r.IntN(40) {
			sub := ""
			if r.IntN(3) == 0 {
				sub = fmt.Sprintf("pkg%d/", r.IntN(3))
			}
			files[fmt.Sprintf("%s/%sf%02d%s", dir, sub, f, e.ext)] = size()
		}
	}
	for f := range r.IntN(10) {
		files[fmt.Sprintf("root%d.go", f)] = size()
	}
	files["vendor/dep/dep.go"] = 100
	files["node_modules/x/index.js"] = 100
	files["build/gen.py"] = 100
	files["huge.go"] = maxFileBytes + 1
	files["README.md"] = 300
	return files
}

// checkPlan asserts the partition and cap invariants of a slice plan.
func checkPlan(t *testing.T, m source.Manifest, scope domain.Scope, plan slicePlan) {
	t.Helper()
	checkPlanFor(t, m, scope, nil, plan)
}

// checkPlanFor is checkPlan of a pull-request run's plan (changed paths).
func checkPlanFor(t *testing.T, m source.Manifest, scope domain.Scope, changed []string, plan slicePlan) {
	t.Helper()
	g := planCodeGroupsFor(m, scope, record.DefaultSecondary, changed)
	seen := map[string]string{}
	add := func(where string, files []plannedFile) {
		for _, f := range files {
			if prev, dup := seen[f.Path]; dup {
				t.Fatalf("%s planned twice (%s, %s)", f.Path, prev, where)
			}
			seen[f.Path] = where
			if f.Bound != excerptBound(f.Size) {
				t.Fatalf("%s bound %d", f.Path, f.Bound)
			}
		}
	}
	add("overview", plan.Overview)
	bytes := 0
	for _, f := range plan.Overview {
		bytes += f.Bound
	}
	if len(plan.Overview) > roleMaxCodeFiles || bytes > roleMaxCodeBytes {
		t.Fatalf("overview %d files %d bytes", len(plan.Overview), bytes)
	}
	if plan.Slots > MaxSlices || len(plan.Slices) > plan.Slots || (len(plan.NotPlanned) > 0 && len(plan.Slices) != plan.Slots) ||
		(plan.Slots > 0) != (len(plan.Rest) > 0) || (plan.Slots > 0 && plan.Slots < MaxSlices && plan.Slots != len(plan.Slices)+slotSlack(len(plan.Slices))) {
		t.Fatalf("%d slots, %d slices, %d files not planned", plan.Slots, len(plan.Slices), len(plan.NotPlanned))
	}
	checkSlots(t, plan.Slices)
	for _, s := range plan.Slices {
		add(s.Key, s.Files)
	}
	add("not planned", plan.NotPlanned)
	if len(seen) != g.eligible || len(plan.Rest)+len(plan.Overview) != g.eligible {
		t.Fatalf("%d of %d eligible files planned", len(seen), g.eligible)
	}
	// The overview prefix holds the first files of its groups.
	for _, k := range g.order {
		var inP []string
		for _, e := range g.files[k] {
			if seen[e.Path] == "overview" {
				inP = append(inP, e.Path)
			}
		}
		for i, p := range inP {
			if g.files[k][i].Path != p {
				t.Fatalf("overview files of %q are not its first files", k)
			}
		}
	}
	// The overview prefix is codeCorpus's round-robin order with every file
	// at its bound.
	var rr []string
	for _, ph := range g.phases() {
		for round := 0; ; round++ {
			progressed := false
			for _, k := range ph.order {
				if round < len(ph.files[k]) {
					rr = append(rr, ph.files[k][round].Path)
					progressed = true
				}
			}
			if !progressed {
				break
			}
		}
	}
	for i, f := range plan.Overview {
		if rr[i] != f.Path {
			t.Fatalf("overview is not the round-robin prefix at %d: %s vs %s", i, f.Path, rr[i])
		}
	}
}

// checkSlots asserts what every packing keeps: slot keys in order, the caps,
// one class and pull-request tier per slot, tiers in order and, within a
// tier, primary slots before secondary ones and, within a class, slots in
// rank order.
func checkSlots(t *testing.T, list []reviewSlice) {
	t.Helper()
	for i, s := range list {
		bytes := 0
		modules := []string{}
		for _, f := range s.Files {
			bytes += f.Bound
			if !slices.Contains(modules, f.Module) {
				modules = append(modules, f.Module)
			}
			if f.Class != s.Class {
				t.Fatalf("slot %s mixes classes", s.Key)
			}
		}
		slices.Sort(modules)
		if s.Key != sliceKey(i) || len(s.Files) == 0 || len(s.Files) > sliceMaxFiles || bytes > sliceMaxBytes || !slices.Equal(modules, s.Modules) ||
			len(modules) > record.MaxSliceModules || s.Rank != s.Files[0].Rank {
			t.Fatalf("slice %+v (%d bytes)", s, bytes)
		}
		for _, f := range s.Files {
			if f.Tier != s.Files[0].Tier {
				t.Fatalf("slot %s mixes pull-request tiers", s.Key)
			}
		}
		if i > 0 {
			p := list[i-1]
			if p.Files[0].Tier > s.Files[0].Tier {
				t.Fatalf("slot %s (%q) after %s (%q)", s.Key, s.Change, p.Key, p.Change)
			}
			// Class and rank order hold within a pull-request tier.
			if p.Files[0].Tier != s.Files[0].Tier {
				continue
			}
			if p.Class == record.ClassSecondary && s.Class == record.ClassPrimary || p.Class == s.Class && record.RankIndex(p.Rank) > record.RankIndex(s.Rank) {
				t.Fatalf("slot %s (%s %s) after %s (%s %s)", s.Key, s.Class, s.Rank, p.Key, p.Class, p.Rank)
			}
		}
	}
}

// TC-161: the plan partitions the eligible files exactly once into the
// overview prefix, the slots under the overview's caps and the files beyond
// the slot limit; it is a pure function of manifest and scope (entry order
// does not matter).
func TestSlicePlanPartitionsEligibleFiles(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	sawSlices := false
	for i := range 400 {
		files := randomTree(r)
		m := syntheticManifest(files)
		scope := domain.Scope{Kind: "full"}
		if i%5 == 4 {
			scope = domain.Scope{Kind: "paths", Paths: []string{"m0", "m1"}}
		}
		plan := planSlices(m, scope, record.DefaultSecondary)
		checkPlan(t, m, scope, plan)
		shuffled := m
		shuffled.Entries = append([]source.Entry{}, m.Entries...)
		r.Shuffle(len(shuffled.Entries), func(a, b int) { shuffled.Entries[a], shuffled.Entries[b] = shuffled.Entries[b], shuffled.Entries[a] })
		if !reflect.DeepEqual(plan, planSlices(shuffled, scope, record.DefaultSecondary)) {
			t.Fatal("plan depends on manifest entry order")
		}
		sawSlices = sawSlices || len(plan.Slices) > 0
	}
	// A large tree reaches the slot limit.
	big := map[string]int64{"go.mod": 30}
	for f := range 600 {
		big[fmt.Sprintf("pkg/f%03d.go", f)] = 5000
	}
	plan := planSlices(syntheticManifest(big), domain.Scope{Kind: "full"}, record.DefaultSecondary)
	checkPlan(t, syntheticManifest(big), domain.Scope{Kind: "full"}, plan)
	if plan.Slots != MaxSlices || len(plan.Slices) != MaxSlices || len(plan.NotPlanned) != 600-5-MaxSlices*5 {
		t.Fatalf("slices %d not planned %d", len(plan.Slices), len(plan.NotPlanned))
	}
	if !sawSlices {
		t.Fatal("generator never produced slices")
	}
}

// Slices come round by round: the first chunk of every module before the
// second chunk of any, and small modules share a slice.
func TestSlicePlanBreadthBeforeDepth(t *testing.T) {
	files := map[string]int64{"a/go.mod": 30, "b/go.mod": 30, "c/pyproject.toml": 30, "d/package.json": 30}
	for f := range 20 {
		files[fmt.Sprintf("a/f%02d.go", f)] = 3000 // bound 2400: five per slice
	}
	for f := range 6 {
		files[fmt.Sprintf("b/b%d.go", f)] = 100
		files[fmt.Sprintf("c/c%d.py", f)] = 100
	}
	files["d/index.ts"] = 100
	plan := planSlices(syntheticManifest(files), domain.Scope{Kind: "full"}, record.DefaultSecondary)
	checkPlan(t, syntheticManifest(files), domain.Scope{Kind: "full"}, plan)
	var got []string
	for _, s := range plan.Slices {
		got = append(got, strings.Join(s.Modules, "+")+fmt.Sprintf("/%d", len(s.Files)))
	}
	// Group order a, d, c, b. The overview takes a0 d0 c0 b0, a1 c1 b1, a2
	// (eight files). Module a's first remaining chunk leads, the rests of
	// c and b (four files each) share the second slice, then a's depth.
	want := []string{"go:a/5", "go:b+python:c/8", "go:a/5", "go:a/5", "go:a/2"}
	if !slices.Equal(got, want) {
		t.Fatalf("slices %v, overview %d", got, len(plan.Overview))
	}
}

// TC-162: the planner's bound never falls below the JSON bytes of the real
// excerpt the overview and the slices take (first 50 lines, at most 2,400
// bytes), on content full of characters JSON escapes, control bytes,
// multi-byte text and long lines.
func TestExcerptBoundNeverBelowRealBytes(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	alphabet := []string{"a", "<", ">", "&", "\"", "\\", "\t", "\x01", "\x1f", "가", "é", " ", "\r", " ", "\n", "\n", "\n"}
	tree := map[string]string{"go.mod": "module x\n\ngo 1.23\n"}
	for f := range 120 {
		var b strings.Builder
		n := r.IntN(4000)
		if f%10 == 0 {
			n = r.IntN(40)
		}
		for b.Len() < n {
			b.WriteString(alphabet[r.IntN(len(alphabet))])
		}
		body := b.String()
		if strings.TrimSpace(body) == "" {
			body += "x"
		}
		tree[fmt.Sprintf("f%03d.go", f)] = body
	}
	j := treeJob(t, tree)
	s := &roleStage{j: j, screen: secretScreen{withheld: map[string]bool{}}}
	files := s.entries()
	checked := 0
	for p, e := range files {
		if !strings.HasSuffix(p, ".go") || p == "go.mod" {
			continue
		}
		x, _, ok, err := s.excerpt(e, "code", "context", 1, 50, roleMaxCodeFile)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			continue
		}
		perLine := 0
		for _, line := range strings.Split(x.Text, "\n") {
			perLine += record.JSONLen(line)
		}
		if bound := excerptBound(*e.Size); record.JSONLen(x.Text) > bound || perLine > bound {
			t.Fatalf("%s: excerpt %d (per line %d) bytes > bound %d", p, record.JSONLen(x.Text), perLine, bound)
		}
		checked++
	}
	if checked < 100 {
		t.Fatalf("only %d excerpts checked", checked)
	}
}

// TC-163: even when every file fits the ordinary overview estimate, a
// contract witness or a long file can leave lines for follow-up.
func TestSmallRepositoriesPlanCoverageForDisplacedLines(t *testing.T) {
	jobs := goPinJobs(t)
	jobs["metrics-regression"] = treeJob(t, map[string]string{"go.mod": "module sample.test/metrics\n\ngo 1.23\n", "metrics.go": "package metrics\n\nfunc Mean() {}\n", "README.md": "# Metrics\n"})
	for _, c := range langCases {
		jobs["lang-"+c.eco] = langJob(t, c.eco)
	}
	for name, j := range jobs {
		plan := planSlices(j.Snapshot.Manifest, j.Scope, record.DefaultSecondary)
		if plan.Slots != 0 || len(plan.Slices) != 0 || len(plan.NotPlanned) != 0 {
			t.Errorf("%s: %d slices", name, len(plan.Slices))
		}
		specs, err := Handler{Static: stubStatic{}}.Plan(t.Context(), j)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.ContainsFunc(specs, func(s analysis.TaskSpec) bool { return s.Key == record.TaskScope }) || len(sliceKeys(specs)) != coverageSlots(plan) {
			t.Errorf("%s plan does not allow for displaced lines (%d slots)", name, len(sliceKeys(specs)))
		}
	}
}

// coveragePlanFile is the slice plan of the coverage fixture, kept outside
// its input.
const coveragePlanFile = "../../../fixtures/modules/slices/expected-plan.json"

type expectedPlan struct {
	Description string              `json:"description"`
	Modules     []expectedModule    `json:"modules"`
	Overview    []string            `json:"overview"`
	Slices      []expectedPlanSlice `json:"slices"`
	NotPlanned  []string            `json:"not_planned"`
}

type expectedModule struct {
	Module   string `json:"module"`
	Eligible int    `json:"files_eligible"`
}

type expectedPlanSlice struct {
	Key     string   `json:"key"`
	Modules []string `json:"modules"`
	Files   []string `json:"files"`
}

// TC-161: the coverage fixture (Go, Python and JS/TS modules and one Go
// module of 30 files) plans the overview prefix, then slices round by round.
func TestCoverageFixturePlan(t *testing.T) {
	j := dirJob(t, "../../../fixtures/modules/slices/input")
	plan := planSlices(j.Snapshot.Manifest, j.Scope, record.DefaultSecondary)
	checkPlan(t, j.Snapshot.Manifest, j.Scope, plan)
	paths := func(list []plannedFile) []string {
		out := []string{}
		for _, f := range list {
			out = append(out, f.Path)
		}
		return out
	}
	got := expectedPlan{Description: "Slice plan of fixtures/modules/slices/input (docs/TESTING.md, TC-161): the overview prefix the overview investigate is certain to offer, then the slices that hold the other eligible files under the same caps.",
		Overview: paths(plan.Overview), Slices: []expectedPlanSlice{}, NotPlanned: paths(plan.NotPlanned)}
	for _, g := range plan.Groups {
		got.Modules = append(got.Modules, expectedModule{Module: g.Module, Eligible: g.Eligible})
	}
	for _, s := range plan.Slices {
		got.Slices = append(got.Slices, expectedPlanSlice{Key: s.Key, Modules: s.Modules, Files: paths(s.Files)})
	}
	b, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	if os.Getenv("CODEYE_UPDATE_EXAMPLES") == "1" {
		if err := os.WriteFile(coveragePlanFile, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(coveragePlanFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(b) {
		t.Fatalf("coverage plan differs from %s; rerun with CODEYE_UPDATE_EXAMPLES=1 and review the diff:\n%s", coveragePlanFile, b)
	}
	if len(plan.Slices) < 6 || len(plan.NotPlanned) != 0 {
		t.Fatalf("coverage fixture slices %d", len(plan.Slices))
	}
}

// repoLikePlan is a repository shaped like this one: a large main Go module
// (cmd, internal/a, internal/b), a TypeScript app in web/, Python scripts,
// and ten tiny modules under fixtures/ that are test inputs.
func repoLikePlan(t *testing.T) slicePlan {
	t.Helper()
	files := map[string]int64{"go.mod": 30, "web/package.json": 30, "scripts/pyproject.toml": 30, "cmd/tool/main.go": 1500}
	for f := range 30 {
		files[fmt.Sprintf("internal/a/a%02d.go", f)] = 2000
		files[fmt.Sprintf("internal/b/b%02d.go", f)] = 2000
		files[fmt.Sprintf("internal/b/b%02d_test.go", f)] = 2000
	}
	for f := range 10 {
		files[fmt.Sprintf("web/src/page%02d.ts", f)] = 1500
		files[fmt.Sprintf("scripts/s%02d.py", f)] = 1500
		files[fmt.Sprintf("fixtures/m%d/go.mod", f)] = 30
		files[fmt.Sprintf("fixtures/m%d/main.go", f)] = 300
	}
	plan := planSlices(syntheticManifest(files), domain.Scope{Kind: "full"}, record.DefaultSecondary)
	checkPlan(t, syntheticManifest(files), domain.Scope{Kind: "full"}, plan)
	return plan
}

// TC-161 (review priority, docs/REVIEW.md#검토-우선순위): files with tool
// diagnostics fill the first slots (higher severity first), then every
// primary module gets its first slot before any gets a second, the large
// module takes most of the rest, tests come after source, and the
// secondary material (fixtures) comes last, its diagnosed file first. The
// fixtures do not take the overview either.
func TestPackSlotsPriority(t *testing.T) {
	plan := repoLikePlan(t)
	for _, f := range plan.Overview {
		if strings.HasPrefix(f.Path, "fixtures/") {
			t.Fatalf("overview takes secondary %s", f.Path)
		}
	}
	rest := append([]plannedFile{}, plan.Rest...)
	for i := range rest {
		switch rest[i].Path {
		case "internal/b/b17.go":
			rest[i].Rank, rest[i].Observations, rest[i].Severity = record.RankToolObservations, 2, 3
		case "web/src/page07.ts":
			rest[i].Rank, rest[i].Observations, rest[i].Severity = record.RankToolObservations, 9, 1
		case "fixtures/m4/main.go":
			rest[i].Rank, rest[i].Observations, rest[i].Severity = record.RankToolObservations, 40, 3
		}
	}
	slots := packSlots(rest)
	checkSlots(t, slots)
	if slots[0].Files[0].Path != "internal/b/b17.go" || slots[0].Rank != record.RankToolObservations {
		t.Fatalf("first slot %+v", slots[0].Files[0])
	}
	firstOf := map[string]int{}
	secondaryFrom := -1
	for i, s := range slots {
		for _, m := range s.Modules {
			if _, ok := firstOf[m]; !ok {
				firstOf[m] = i
			}
		}
		if s.Class == record.ClassSecondary && secondaryFrom < 0 {
			secondaryFrom = i
			if s.Files[0].Path != "fixtures/m4/main.go" {
				t.Fatalf("first secondary slot %+v", s.Files)
			}
		}
		if secondaryFrom >= 0 && s.Class != record.ClassSecondary {
			t.Fatalf("primary slot %s after secondary ones", s.Key)
		}
	}
	// The two diagnosed files share the first slot (the higher severity
	// first); the other primary modules each get a slot right after it.
	if len(slots[0].Files) != 2 || slots[0].Files[1].Path != "web/src/page07.ts" {
		t.Fatalf("first slot %+v", slots[0].Files)
	}
	if firstOf["python:scripts"] > 3 || firstOf["go:."] > 0 {
		t.Fatalf("first slots %v", firstOf)
	}
	// Secondary material is last and its ten tiny modules share two slots.
	if secondaryFrom < 0 || len(slots)-secondaryFrom > 2 {
		t.Fatalf("secondary from %d of %d", secondaryFrom, len(slots))
	}
	// Tests of the large module come after its source, and a slot holds one
	// directory where it can: no slot mixes internal/a with internal/b.
	sourceDone := false
	for _, s := range slots[:secondaryFrom] {
		dirs := map[string]bool{}
		for _, f := range s.Files {
			dirs[path.Dir(f.Path)] = true
			if strings.HasSuffix(f.Path, "_test.go") {
				sourceDone = true
			} else if sourceDone && f.Module == "go:." && f.Rank != record.RankToolObservations {
				t.Fatalf("source %s after a test slot", f.Path)
			}
		}
		if dirs["internal/a"] && dirs["internal/b"] {
			t.Fatalf("slot %s mixes directories %v", s.Key, dirs)
		}
	}
}
