package review

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
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
	g := planCodeGroups(m, scope)
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
	if len(plan.Slices) > MaxSlices || (len(plan.NotPlanned) > 0 && len(plan.Slices) != MaxSlices) {
		t.Fatalf("%d slices, %d files not planned", len(plan.Slices), len(plan.NotPlanned))
	}
	for i, s := range plan.Slices {
		add(s.Key, s.Files)
		bytes := 0
		modules := []string{}
		for _, f := range s.Files {
			bytes += f.Bound
			if !slices.Contains(modules, f.Module) {
				modules = append(modules, f.Module)
			}
		}
		slices.Sort(modules)
		if s.Key != sliceKey(i) || len(s.Files) == 0 || len(s.Files) > sliceMaxFiles || bytes > sliceMaxBytes || !slices.Equal(modules, s.Modules) {
			t.Fatalf("slice %+v (%d bytes)", s, bytes)
		}
	}
	add("not planned", plan.NotPlanned)
	if len(seen) != g.eligible {
		t.Fatalf("%d of %d eligible files planned", len(seen), g.eligible)
	}
	// Every group keeps its rank order across overview, slices and the rest.
	position := map[string]int{}
	order := append(append([]plannedFile{}, plan.Overview...), plan.NotPlanned...)
	order = slices.Insert(order, len(plan.Overview), func() []plannedFile {
		var all []plannedFile
		for _, s := range plan.Slices {
			all = append(all, s.Files...)
		}
		return all
	}()...)
	for i, f := range order {
		position[f.Path] = i
	}
	for _, k := range g.order {
		var inP []string
		last := -1
		for _, e := range g.files[k] {
			if seen[e.Path] == "overview" {
				inP = append(inP, e.Path)
				continue
			}
			if position[e.Path] < last {
				t.Fatalf("group %q out of rank order at %s", k, e.Path)
			}
			last = position[e.Path]
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
	for round := 0; ; round++ {
		progressed := false
		for _, k := range g.order {
			if round < len(g.files[k]) {
				rr = append(rr, g.files[k][round].Path)
				progressed = true
			}
		}
		if !progressed {
			break
		}
	}
	for i, f := range plan.Overview {
		if rr[i] != f.Path {
			t.Fatalf("overview is not the round-robin prefix at %d: %s vs %s", i, f.Path, rr[i])
		}
	}
}

// TC-161: the plan partitions the eligible files exactly once into the
// overview prefix, at most 32 slices under the overview's caps and the
// files beyond the slice limit; it is a pure function of manifest and
// scope (entry order does not matter).
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
		plan := planSlices(m, scope)
		checkPlan(t, m, scope, plan)
		shuffled := m
		shuffled.Entries = append([]source.Entry{}, m.Entries...)
		r.Shuffle(len(shuffled.Entries), func(a, b int) { shuffled.Entries[a], shuffled.Entries[b] = shuffled.Entries[b], shuffled.Entries[a] })
		if !reflect.DeepEqual(plan, planSlices(shuffled, scope)) {
			t.Fatal("plan depends on manifest entry order")
		}
		sawSlices = sawSlices || len(plan.Slices) > 0
	}
	// A large tree reaches the slice limit.
	big := map[string]int64{"go.mod": 30}
	for f := range 400 {
		big[fmt.Sprintf("pkg/f%03d.go", f)] = 5000
	}
	plan := planSlices(syntheticManifest(big), domain.Scope{Kind: "full"})
	checkPlan(t, syntheticManifest(big), domain.Scope{Kind: "full"}, plan)
	if len(plan.Slices) != MaxSlices || len(plan.NotPlanned) != 400-5-MaxSlices*5 {
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
	plan := planSlices(syntheticManifest(files), domain.Scope{Kind: "full"})
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

// TC-163: a repository whose eligible files all fit the overview gets no
// slice: today's plan, request bytes and records (the fixtures pinned by
// TestGoReviewInputsUnchanged, the language fixtures and the regression
// inputs, one metrics.go each).
func TestSmallRepositoriesPlanNoSlices(t *testing.T) {
	jobs := goPinJobs(t)
	jobs["metrics-regression"] = treeJob(t, map[string]string{"go.mod": "module sample.test/metrics\n\ngo 1.23\n", "metrics.go": "package metrics\n\nfunc Mean() {}\n", "README.md": "# Metrics\n"})
	for _, c := range langCases {
		jobs["lang-"+c.eco] = langJob(t, c.eco)
	}
	for name, j := range jobs {
		plan := planSlices(j.Snapshot.Manifest, j.Scope)
		if len(plan.Slices) != 0 || len(plan.NotPlanned) != 0 {
			t.Errorf("%s: %d slices", name, len(plan.Slices))
		}
		specs, err := RoleHandler{Static: stubStatic{}}.Plan(t.Context(), j)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range specs {
			if strings.HasPrefix(s.Key, "review.") && !slices.Contains([]string{"review.environment", "review.documents", "review.investigate", "review.interpret", "review.verify"}, s.Key) {
				t.Errorf("%s plans %s", name, s.Key)
			}
		}
	}
}

// coveragePlanFile is the slice plan of the coverage fixture, kept outside
// its input.
const coveragePlanFile = "../../../fixtures/modules/coverage/expected-plan.json"

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
	j := dirJob(t, "../../../fixtures/modules/coverage/input")
	plan := planSlices(j.Snapshot.Manifest, j.Scope)
	checkPlan(t, j.Snapshot.Manifest, j.Scope, plan)
	paths := func(list []plannedFile) []string {
		out := []string{}
		for _, f := range list {
			out = append(out, f.Path)
		}
		return out
	}
	got := expectedPlan{Description: "Slice plan of fixtures/modules/coverage/input (docs/TESTING.md, TC-161): the overview prefix the overview investigate is certain to offer, then the slices that hold the other eligible files under the same caps.",
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
