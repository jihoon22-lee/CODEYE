package review

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// W24 phase 2c: review input selection for C/C++, Python, JS/TS and Rust.
// The fixtures are fixtures/w24-review-inputs/<ecosystem>, written for these
// tests (no evaluation case).

const w24Fixtures = "../../../fixtures/w24-review-inputs"

type sel struct {
	path, kind string
}

// w24Case is one language fixture: the review-v1 corpus it must select (in
// order; every file whole from line 1), the review-r1 code excerpts in
// order, and the source line of the synthetic analyzer observation.
type w24Case struct {
	eco         string
	v1          []sel
	r1          []sel
	obsPath     string
	obsLine     int
	modulesWant []string
}

var w24Cases = []w24Case{
	{eco: "cpp",
		v1: []sel{{"src/main.cc", "code"}, {"README.md", "document"}, {"include/ring.h", "code"}, {"src/ring.cc", "code"}, {"CMakeLists.txt", "config"}, {"tests/ring_test.cc", "test"}},
		r1: []sel{{"src/main.cc", "code"}, {"include/ring.h", "code"}, {"src/ring.cc", "code"}, {"tests/ring_test.cc", "test"}}, obsPath: "src/ring.cc", obsLine: 8},
	{eco: "python",
		v1: []sel{{"app/__main__.py", "code"}, {"README.md", "document"}, {"app/__init__.py", "code"}, {"app/store.py", "code"}, {"pyproject.toml", "config"}, {"tests/test_store.py", "test"}},
		r1: []sel{{"app/__main__.py", "code"}, {"app/__init__.py", "code"}, {"app/store.py", "code"}, {"tests/test_store.py", "test"}}, obsPath: "app/store.py", obsLine: 16},
	{eco: "javascript",
		v1: []sel{{"src/index.ts", "code"}, {"README.md", "document"}, {"src/queue.ts", "code"}, {"package.json", "config"}, {"tsconfig.json", "config"}, {"src/queue.test.ts", "test"}},
		r1: []sel{{"src/index.ts", "code"}, {"src/queue.ts", "code"}, {"src/queue.test.ts", "test"}}, obsPath: "src/queue.ts", obsLine: 29},
	{eco: "rust",
		v1: []sel{{"src/lib.rs", "code"}, {"src/main.rs", "code"}, {"README.md", "document"}, {"src/parse.rs", "code"}, {"Cargo.toml", "config"}, {"tests/parse.rs", "test"}},
		r1: []sel{{"src/lib.rs", "code"}, {"src/main.rs", "code"}, {"src/parse.rs", "code"}, {"tests/parse.rs", "test"}}, obsPath: "src/parse.rs", obsLine: 11},
}

func w24Job(t *testing.T, eco string) analysis.Job {
	t.Helper()
	return dirJob(t, filepath.Join(w24Fixtures, eco))
}

// lineCount is the number of lines of a snapshot file.
func lineCount(t *testing.T, j analysis.Job, p string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(j.Snapshot.SnapshotDir, filepath.FromSlash(p)))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(strings.TrimSuffix(string(b), "\n"), "\n") + 1
}

// langStub is a static composite with one analyzer of the fixture's
// ecosystem ("lint": an observation at path:line and one on the module's
// manifest) and, when secrets is set, the W21 secrets task: "hit" reports
// secretPath, "failed" and "missing" are unusable scans (screen.go).
type langStub struct {
	eco, path       string
	line            int
	manifest        string
	secrets         string
	secretPath      string
	withoutManifest bool
}

const w24SecretAnalyzer = "any.gitl" + "eaks"

func (s langStub) Descriptors() []pack.Descriptor {
	return []pack.Descriptor{{ID: s.eco + ".lint", Name: "lint", Ecosystem: s.eco}, {ID: w24SecretAnalyzer, Name: "secrets", Ecosystem: "any"}}
}

func (langStub) Info() analysis.HandlerInfo {
	return analysis.HandlerInfo{Name: pack.CompositeName, Version: "stub", Capability: pack.Capability}
}

func (s langStub) Plan(context.Context, analysis.Job) ([]analysis.TaskSpec, error) {
	plan := []analysis.TaskSpec{{Key: "lint", Title: "lint", Stage: domain.StageStaticAnalysis}}
	if s.secrets != "" {
		plan = append(plan, analysis.TaskSpec{Key: "secrets", Title: "secrets", Stage: domain.StageStaticAnalysis})
	}
	return plan, nil
}

func (s langStub) Run(_ context.Context, j analysis.Job, t analysis.TaskSpec, _ executor.Runner) analysis.TaskResult {
	if t.Key == "secrets" && s.secrets == "missing" {
		return analysis.TaskResult{Status: domain.TaskFailed, Reason: "handler_error"}
	}
	sha := map[string]string{}
	for _, e := range j.Snapshot.Manifest.Entries {
		sha[e.Path] = e.SHA256
	}
	id, eco := s.eco+".lint", s.eco
	if t.Key == "secrets" {
		id, eco = w24SecretAnalyzer, "any"
	}
	doc := obsdoc.New(obsdoc.Analyzer{ID: id, Ecosystem: eco}, obsdoc.Component{ID: t.Key, Version: "1"}, obsdoc.Component{ID: t.Key + "-stub", Version: "1"}, t.Key, j.Snapshot.Digest)
	doc.Module.Dir, doc.Tool.Name = ".", t.Key
	version := "1.0.0"
	doc.Tool.Version, doc.Tool.VersionTested = &version, true
	doc.RulePack = obsdoc.RulePack{ID: "stub", Version: "1", Selection: []string{}, RepositoryConfig: obsdoc.RepoConfigOverridden, SourceSuppressions: obsdoc.SuppressHonored,
		ConfigFiles: []obsdoc.ConfigFile{}, UnreportedSuppressionRules: []string{}}
	stdout := []byte("record\n")
	code := 0
	doc.Executions = []obsdoc.Execution{{Name: t.Key + ".run", Status: executor.StatusExited, ExitCode: &code, Cleanup: executor.CleanupConfirmed,
		Command: analysis.ManifestCommand{Argv: []string{t.Key}, Dir: ".", EnvKeys: []string{}},
		Stdout:  obsdoc.StreamRef{SHA256: obsdoc.SHA256Hex(stdout), CapturedBytes: int64(len(stdout)), TotalBytes: int64(len(stdout))}, Stderr: obsdoc.StreamRef{SHA256: obsdoc.SHA256Hex(nil)}}}
	observe := func(p string, line int, rule, msg, category string) obsdoc.Observation {
		at := fmt.Sprintf("%s:%d", p, line)
		return obsdoc.Observation{ID: obsdoc.ObservationID(id, ".", p, rule, at, at, msg, 0), RuleID: rule, Category: category, RawMessage: msg,
			Path: &p, Location: &obsdoc.Location{Path: p, StartLine: line, EndLine: line, ColumnUnit: "byte", BlobSHA256: sha[p]}, LocationState: obsdoc.LocationResolved,
			Related: []obsdoc.Related{}, RawRef: obsdoc.RawRef{Execution: t.Key + ".run", Stream: "stdout", StreamSHA256: obsdoc.SHA256Hex(stdout), ByteEnd: int64(len(stdout))}, Unit: p}
	}
	doc.Observations = []obsdoc.Observation{observe(s.path, s.line, "possible-defect", "possible defect at this line (synthetic fixture)", "correctness")}
	if !s.withoutManifest && s.manifest != "" {
		doc.Observations = append(doc.Observations, observe(s.manifest, 1, "manifest-field", "manifest field check (synthetic fixture)", "maintainability"))
	}
	doc.Outcome = obsdoc.Outcome{TaskStatus: domain.TaskSucceeded, ExitClass: obsdoc.ExitSuccessWithFindings}
	if t.Key == "secrets" {
		doc.Observations = []obsdoc.Observation{observe(s.secretPath, 1, "generic-api-key", "secret detected (value withheld)", "security")}
		if s.secrets == "failed" {
			reason := "scan_incomplete"
			doc.Observations = []obsdoc.Observation{}
			doc.Outcome = obsdoc.Outcome{TaskStatus: domain.TaskFailed, Reason: &reason, ExitClass: obsdoc.ExitExecutionError}
		}
	}
	processed := []string{}
	for _, o := range doc.Observations {
		if !slices.Contains(processed, *o.Path) {
			processed = append(processed, *o.Path)
		}
	}
	slices.Sort(processed)
	doc.Coverage.ProcessedPaths = processed
	doc.Parse.OK = s.secrets != "failed" || t.Key != "secrets"
	art, err := pack.WriteEnvelope(j, doc)
	if err != nil {
		panic(err)
	}
	return analysis.TaskResult{Status: domain.TaskSucceeded, Artifacts: []analysis.ArtifactFile{art}}
}

func (c w24Case) stub() langStub {
	manifest := map[string]string{"cpp": "CMakeLists.txt", "python": "pyproject.toml", "javascript": "package.json", "rust": "Cargo.toml"}[c.eco]
	return langStub{eco: c.eco, path: c.obsPath, line: c.obsLine, manifest: manifest}
}

// review-v1 corpus on each language fixture: entry points, the README,
// sources, manifests, tests, each whole from line 1 within the unchanged
// budgets; deterministic across acquisitions. The three roles then run and
// every record decodes against the snapshot (evidence IDs, kinds by path).
func TestW24V1CorpusPerLanguage(t *testing.T) {
	for _, c := range w24Cases {
		t.Run(c.eco, func(t *testing.T) {
			j := w24Job(t, c.eco)
			corpus, err := buildCorpus(j.Snapshot, j.Scope)
			if err != nil {
				t.Fatal(err)
			}
			var got []sel
			for _, e := range corpus.Excerpts {
				got = append(got, sel{e.Path, e.Kind})
				if e.Start != 1 || e.End != lineCount(t, j, e.Path) || e.ID != record.ExcerptID(j.Snapshot.Digest, e.Path, e.SHA256, 1, e.End) {
					t.Fatalf("excerpt %+v", e)
				}
			}
			if !slices.Equal(got, c.v1) || corpus.Eligible != len(c.v1) || corpus.Documents != 1 || len(corpus.Limitations) != 0 {
				t.Fatalf("v1 corpus %v eligible=%d docs=%d lims=%v", got, corpus.Eligible, corpus.Documents, corpus.Limitations)
			}
			// Deterministic: two acquisitions with the same identity send the
			// same bytes.
			first, second := v1Requests(t, fixIdentity(w24Job(t, c.eco))), v1Requests(t, fixIdentity(w24Job(t, c.eco)))
			if len(first) != 3 || !slices.Equal(first, second) {
				t.Fatalf("v1 requests %v / %v", first, second)
			}
			f := v1Generic(t)
			_, r := runStages(t, j, f)
			if r.Status != domain.TaskSucceeded {
				t.Fatalf("verify %+v", r)
			}
			for _, role := range []string{"context", "investigate"} {
				if _, err := readRecord(j, role); err != nil {
					t.Fatalf("%s record: %v", role, err)
				}
			}
			v := readVerify(t, j, r)
			if len(v.Candidates) != 1 || len(v.Verifications) != 1 || record.SourceEcosystem(evidencePath(v.Evidence, v.Candidates[0].Evidence[0])) != c.eco ||
				!slices.Contains(v.Limitations, limExpansionGoOnly) || !slices.Contains(v.Limitations, limNoSymbol) {
				t.Fatalf("v1 verify record %+v", v)
			}
		})
	}
}

func evidencePath(list []record.Excerpt, id string) string {
	for _, e := range list {
		if e.ID == id {
			return e.Path
		}
	}
	return ""
}

// review-r1 on each language-only snapshot: investigate gets code excerpts
// of that language (round-robin over its module, first lines of each file),
// interpret a line window around the analyzer observation and none on the
// manifest, and verify runs on the candidates without Go caller expansion.
// Records decode, the language guide is that language's alone, and the
// requests are the same bytes across acquisitions.
func TestW24R1InputsPerLanguage(t *testing.T) {
	for _, c := range w24Cases {
		t.Run(c.eco, func(t *testing.T) {
			j := w24Job(t, c.eco)
			f := &r1Caller{script: maxScript(t)}
			plan, res := runR1(t, j, c.stub(), f, "1.27.4")
			for _, p := range plan {
				if r := res[p.Key]; strings.HasPrefix(p.Key, "review.") && r.Status != domain.TaskSucceeded && !(p.Key == "review.environment" && r.Reason == ReasonNoEnvironmentGap) {
					t.Fatalf("%s %+v", p.Key, r)
				}
			}
			var inv investigateInput
			var interp interpretInput
			var ver r1VerifyInput
			for _, q := range f.requests {
				checkGuides(t, q.Role, q.Instructions, []string{c.eco})
				switch q.Role {
				case "investigate":
					inv = decodeAs[investigateInput](t, q)
				case "interpret":
					interp = decodeAs[interpretInput](t, q)
				case "verify":
					ver = decodeAs[r1VerifyInput](t, q)
				}
			}
			var got []sel
			for _, e := range inv.Scope.Excerpts {
				got = append(got, sel{e.Path, e.Kind})
				if e.Start != 1 || e.End != lineCount(t, j, e.Path) || e.Origin != "context" || e.Text == "" || e.ID != record.ExcerptID(j.Snapshot.Digest, e.Path, e.SHA256, 1, e.End) {
					t.Fatalf("investigate excerpt %+v", e)
				}
			}
			if !slices.Equal(got, c.r1) || !slices.Equal(inv.Scope.Modules, []string{"."}) || slices.Contains(inv.Scope.Limitations, "code_excerpts_go_only") {
				t.Fatalf("investigate excerpts %v modules %v lims %v", got, inv.Scope.Modules, inv.Scope.Limitations)
			}
			// interpret: a window around the source observation, none on the
			// manifest.
			total := lineCount(t, j, c.obsPath)
			var window record.Excerpt
			for _, o := range interp.Observations {
				switch o.Path {
				case c.obsPath:
					if len(o.EvidenceIDs) != 1 || len(interp.Scope.Excerpts) != 1 {
						t.Fatalf("observation %+v windows %d", o, len(interp.Scope.Excerpts))
					}
					window = interp.Scope.Excerpts[0]
				default:
					if len(o.EvidenceIDs) != 0 {
						t.Fatalf("window on a non-source file: %+v", o)
					}
				}
			}
			if len(interp.Observations) != 2 || window.Path != c.obsPath || window.Origin != "observation" || window.Kind != "code" ||
				window.Start != max(1, c.obsLine-r1WindowRadius) || window.End != min(total, window.Start+2*r1WindowRadius) {
				t.Fatalf("interpret observations %+v window %+v", interp.Observations, window)
			}
			// verify: the four investigate candidates and the interpret
			// hypothesis, with the excerpts they cite.
			if len(ver.Candidates) != 5 || len(ver.Scope.Excerpts) == 0 {
				t.Fatalf("verify input: %d candidates, %d excerpts", len(ver.Candidates), len(ver.Scope.Excerpts))
			}
			given := catalogOf(ver.Scope.Excerpts)
			for _, cand := range ver.Candidates {
				for _, id := range cand.Evidence {
					if _, ok := given[id]; !ok {
						t.Fatalf("candidate %s cites %s outside the verify input", cand.ID, id)
					}
				}
			}
			for _, stage := range record.R1Stages {
				if res["review."+stage].Status != domain.TaskSucceeded {
					continue // environment: no declared requirement to compare
				}
				rec := readR1(t, j, stage)
				if slices.Contains(rec.Limitations, "code_excerpts_go_only") {
					t.Fatalf("%s limitations %v", stage, rec.Limitations)
				}
				for _, call := range rec.Calls {
					if !slices.Equal(guideFiles(call.PackFiles), guidePaths([]string{c.eco})) {
						t.Fatalf("%s pack_files %v", stage, call.PackFiles)
					}
				}
			}
			v := readR1(t, j, "verify")
			if len(v.Verifications) != 5 || !slices.Contains(v.Limitations, limExpansionGoOnly) || !slices.Contains(v.Limitations, limNoSymbol) {
				t.Fatalf("verify record %d verifications, limitations %v", len(v.Verifications), v.Limitations)
			}
			for _, x := range v.Verifications {
				if x.Verdict == record.VerdictUnreviewed {
					t.Fatalf("unreviewed %+v", x)
				}
			}
			first := r1Requests(t, fixIdentity(w24Job(t, c.eco)), c.stub(), maxScript(t))
			second := r1Requests(t, fixIdentity(w24Job(t, c.eco)), c.stub(), maxScript(t))
			if !slices.Equal(first, second) {
				t.Fatalf("r1 requests differ across acquisitions:\n%v\n%v", first, second)
			}
		})
	}
}

// A polyglot snapshot: the review-r1 code corpus reaches every ecosystem in
// its first round (groupOrder), and each file is grouped under the module
// root of its own ecosystem.
func TestW24R1PolyglotRoundRobin(t *testing.T) {
	tree := map[string]string{}
	for _, c := range w24Cases {
		dir := filepath.Join(w24Fixtures, c.eco)
		err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(dir, p)
			b, err := os.ReadFile(p)
			tree[c.eco+"/"+filepath.ToSlash(rel)] = string(b)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	tree["go.mod"] = "module example.test/poly\n\ngo 1.23\n"
	tree["main.go"] = "package main\n\nfunc main() {}\n"
	j := treeJob(t, tree)
	s := &r1Stage{h: R1Handler{Caller: &r1Caller{}}, ctx: t.Context(), j: j, role: "investigate"}
	code, lims, err := s.codeCorpus()
	if err != nil {
		t.Fatal(err)
	}
	var first []string
	for _, e := range code[:5] {
		first = append(first, e.Path)
	}
	if !slices.Equal(first, []string{"cpp/src/main.cc", "main.go", "javascript/src/index.ts", "python/app/__main__.py", "rust/src/lib.rs"}) {
		t.Fatalf("first round %v", first)
	}
	if len(code) != r1MaxCodeFiles || !slices.Contains(lims, "files_outside_review_context") {
		t.Fatalf("corpus %d files, limitations %v", len(code), lims)
	}
	if got := s.otherModuleDirs(code); !slices.Equal(got, []string{"cpp", "javascript", "python", "rust"}) {
		t.Fatalf("module roots %v", got)
	}
}

// Exclusions: below the analyzer packs' data directories (hidden, testdata,
// vendor, node_modules) and below build output, dependency and third-party
// directories at any depth, no non-Go file is review input, in either pack;
// a manifest counts only for an ecosystem with source in scope. The paths
// are ones the snapshot keeps, so the review rule (not acquisition) decides.
// Go keeps its own rules: internal/build is Go source.
func TestW24ExclusionsHonoured(t *testing.T) {
	own := map[string]string{
		"README.md":           "# Mixed\n\nTools in several languages.\n",
		"go.mod":              "module example.test/mixed\n\ngo 1.23\n",
		"internal/build/b.go": "package build\n\nfunc B() int { return 1 }\n",
		"app/main.py":         "def main():\n    return 0\n",
		"app/util.py":         "def util():\n    return 1\n",
		"web/index.js":        "export const x = 1;\n",
		"web/package.json":    "{\"name\": \"web\"}\n",
		"crate/Cargo.toml":    "[package]\nname = \"c\"\nversion = \"0.1.0\"\n",
		"crate/src/lib.rs":    "pub fn f() {}\n",
		"native/main.cc":      "int main() { return 0; }\n",
	}
	excluded := []string{
		"vendor/lib/v.py", "vendor/lib/pyproject.toml", "testdata/t.js", ".tools/h.rs", "app/.cache/c.py",
		"web/dist/bundle.js", "web/dist/package.json", "web/build/out.js", "web/coverage/lcov-report/sorter.js",
		"crate/target/debug/build/gen.rs", "native/build/gen.cc", "native/third_party/z/z.cc",
		"app/build/lib/x.py", "app/venv/lib/x.py", "app/htmlcov/h.js", "pkg/site-packages/m.py",
	}
	tree := map[string]string{}
	for p, b := range own {
		tree[p] = b
	}
	for _, p := range excluded {
		tree[p] = "x = 1\n"
	}
	j := treeJob(t, tree)
	included := map[string]bool{}
	for _, e := range j.Snapshot.Manifest.Entries {
		included[e.Path] = e.Kind == "file" && e.ExcludedReason == ""
	}
	for _, p := range excluded {
		if !included[p] {
			t.Fatalf("the snapshot dropped %s; the test would not exercise the review rule", p)
		}
	}
	c, err := buildCorpus(j.Snapshot, j.Scope)
	if err != nil {
		t.Fatal(err)
	}
	if c.Eligible != len(own) || !slices.Contains(c.Limitations, limExcludedSource) {
		t.Fatalf("v1 eligible %d, want %d; limitations %v", c.Eligible, len(own), c.Limitations)
	}
	for _, e := range c.Excerpts {
		if _, ok := own[e.Path]; !ok {
			t.Fatalf("v1 excerpt of %s", e.Path)
		}
	}
	s := &r1Stage{h: R1Handler{Caller: &r1Caller{}}, ctx: t.Context(), j: j, role: "investigate"}
	code, lims, err := s.codeCorpus()
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, e := range code {
		paths = append(paths, e.Path)
	}
	slices.Sort(paths)
	if !slices.Equal(paths, []string{"app/main.py", "app/util.py", "crate/src/lib.rs", "internal/build/b.go", "native/main.cc", "web/index.js"}) || slices.Contains(lims, "files_outside_review_context") ||
		!slices.Contains(lims, limExcludedSource) {
		t.Fatalf("r1 code corpus %v limitations %v", paths, lims)
	}
	for _, p := range excluded {
		if r, _ := priority(p, newOtherScope(j.Snapshot.Manifest, j.Scope)); r != 5 {
			t.Fatalf("v1 priority of %s is %d", p, r)
		}
	}

	// Build files alone: on a Python-only snapshot, only pyproject.toml is
	// a manifest of the corpus.
	j = treeJob(t, map[string]string{"pyproject.toml": "[project]\nname = \"p\"\n", "p/m.py": "x = 1\n", "package.json": "{}\n", "tsconfig.json": "{}\n",
		"Cargo.toml": "[package]\nname = \"p\"\n", "CMakeLists.txt": "project(p)\n", "compile_commands.json": "[]\n", "setup.cfg": "[metadata]\nname = p\n"})
	c, err = buildCorpus(j.Snapshot, j.Scope)
	if err != nil {
		t.Fatal(err)
	}
	var configs []string
	for _, e := range c.Excerpts {
		if e.Kind == "config" {
			configs = append(configs, e.Path)
		}
	}
	if !slices.Equal(configs, []string{"pyproject.toml", "setup.cfg"}) || c.Eligible != 3 {
		t.Fatalf("configs %v eligible %d", configs, c.Eligible)
	}
}

// The W21 secrets screen covers the new inputs: on a Python-only snapshot a
// file the secrets task reports reaches no request of either pack (code
// context, observation window, verification), and an unusable scan sends
// nothing at all.
func TestW24SecretScreenOnPython(t *testing.T) {
	value := "deploy-token-" + strings.Repeat("p4", 10) // not a Sensitive shape
	planted := func(t *testing.T) analysis.Job {
		j := w24Job(t, "python")
		b, err := os.ReadFile(filepath.Join(w24Fixtures, "python", "app", "store.py"))
		if err != nil {
			t.Fatal(err)
		}
		tree := map[string]string{}
		for _, e := range j.Snapshot.Manifest.Entries {
			if e.Kind == "file" {
				body, err := os.ReadFile(filepath.Join(j.Snapshot.SnapshotDir, filepath.FromSlash(e.Path)))
				if err != nil {
					t.Fatal(err)
				}
				tree[e.Path] = string(body)
			}
		}
		tree["app/store.py"] = string(b) + "\nDEPLOY_TOKEN = \"" + value + "\"\n"
		return treeJob(t, tree)
	}
	stub := w24Cases[1].stub()
	for _, mode := range []string{"hit", "failed", "missing"} {
		t.Run(mode, func(t *testing.T) {
			stub.secrets, stub.secretPath = mode, "app/store.py"
			// review-r1
			j := planted(t)
			f := &r1Caller{script: maxScript(t)}
			plan, res := runR1(t, j, stub, f, "1.27.4")
			if mode != "hit" {
				if len(f.requests) != 0 {
					t.Fatalf("%d r1 requests after an unusable scan", len(f.requests))
				}
				for _, p := range plan {
					if strings.HasPrefix(p.Key, "review.") && (res[p.Key].Status != domain.TaskSkippedPolicy || res[p.Key].Reason != reasonSecretScreenFailed) {
						t.Fatalf("%s %+v", p.Key, res[p.Key])
					}
				}
			} else {
				if res["review.investigate"].Status != domain.TaskSucceeded || res["review.verify"].Status != domain.TaskSucceeded || len(f.requests) == 0 {
					t.Fatalf("investigate %+v verify %+v", res["review.investigate"], res["review.verify"])
				}
				for _, q := range f.requests {
					if strings.Contains(string(q.Input), value) {
						t.Fatalf("r1 %s request holds the planted value", q.Role)
					}
				}
				for _, stage := range []string{"investigate", "interpret", "verify"} {
					rec := readR1(t, j, stage)
					for _, e := range rec.Evidence {
						if e.Path == "app/store.py" {
							t.Fatalf("%s evidence on the withheld file", stage)
						}
					}
					if stage != "verify" && !slices.Contains(rec.Limitations, limSecretWithheld) {
						t.Fatalf("%s limitations %v", stage, rec.Limitations)
					}
				}
			}
			// review-v1
			j = planted(t)
			fv := v1Generic(t)
			h := Handler{Static: stub, Caller: fv}
			for _, task := range []string{"lint", "secrets"} {
				h.Run(t.Context(), j, analysis.TaskSpec{Key: task}, nil)
			}
			r := h.Run(t.Context(), j, analysis.TaskSpec{Key: "review.context"}, nil)
			if mode != "hit" {
				if r.Status != domain.TaskSkippedPolicy || r.Reason != reasonSecretScreenFailed || len(fv.requests) != 0 {
					t.Fatalf("v1 %+v requests %d", r, len(fv.requests))
				}
				return
			}
			rec, err := readRecord(j, "context")
			if r.Status != domain.TaskSucceeded || err != nil || !slices.Contains(rec.Limitations, limSecretWithheld) || strings.Contains(string(fv.requests[0].Input), value) {
				t.Fatalf("v1 %+v %v limitations %v", r, err, rec.Limitations)
			}
			for _, e := range rec.Evidence {
				if e.Path == "app/store.py" {
					t.Fatal("v1 evidence on the withheld file")
				}
			}
		})
	}
}

// record's source extensions are pack.SourceFiles' (the analyzer packs'
// definition of an ecosystem in scope), checked over a list of candidate
// extensions in both directions.
func TestW24SourceExtensionsMatchPack(t *testing.T) {
	exts := []string{".go", ".py", ".pyi", ".pyw", ".pyx", ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".mts", ".cts", ".vue", ".svelte", ".rs",
		".c", ".cc", ".cpp", ".cxx", ".c++", ".h", ".hh", ".hpp", ".hxx", ".h++", ".ipp", ".inl", ".m", ".mm", ".cu", ".java", ".kt", ".rb", ".php", ".cs", ".swift", ".scala",
		".GO", ".Py", ".CPP", ".md", ".toml", ".json", ""}
	for _, ext := range exts {
		name := "src/f" + ext
		size := int64(1)
		m := source.Manifest{Entries: []source.Entry{{Path: name, Kind: "file", SHA256: strings.Repeat("a", 64), Size: &size}}}
		want := ""
		for eco := range pack.SourceFiles(m, domain.Scope{Kind: "full"}) {
			want = eco
		}
		if got := record.SourceEcosystem(name); got != want {
			t.Errorf("%s: record %q, pack %q", name, got, want)
		}
	}
}

// Source the review rule leaves out is never silent: a Python project whose
// only source lies below build/ gives review-v1 a document-only corpus and
// review-r1 no code context, and both say why
// (source_in_excluded_directories). investigate fails, as before for any
// snapshot without reviewable code, instead of reviewing nothing.
func TestW24ExcludedSourceIsExplained(t *testing.T) {
	j := treeJob(t, map[string]string{
		"README.md":          "# Gen\n\nGenerated package.\n",
		"pyproject.toml":     "[project]\nname = \"gen\"\n",
		"pkg/build/gen/m.py": "def f():\n    return 1\n",
		"pkg/build/gen/n.py": "def g():\n    return 2\n",
	})
	if got := languagesInScope(j); !slices.Equal(got, []string{"python"}) {
		t.Fatalf("ecosystems %v", got)
	}
	c, err := buildCorpus(j.Snapshot, j.Scope)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range c.Excerpts {
		kinds = append(kinds, e.Path+":"+e.Kind)
	}
	if !slices.Equal(kinds, []string{"README.md:document", "pyproject.toml:config"}) || !slices.Contains(c.Limitations, limExcludedSource) {
		t.Fatalf("v1 corpus %v limitations %v", kinds, c.Limitations)
	}
	f := &r1Caller{script: maxScript(t)}
	_, res := runR1(t, j, vetStub{}, f, "1.27.4")
	if r := res["review.investigate"]; r.Status != domain.TaskFailed || r.Reason != "review_input_unavailable" {
		t.Fatalf("investigate %+v", r)
	}
	if rec := readR1(t, j, "investigate"); !slices.Contains(rec.Limitations, limExcludedSource) {
		t.Fatalf("investigate limitations %v", rec.Limitations)
	}
	for _, q := range f.requests {
		if q.Role == "investigate" || q.Role == "verify" {
			t.Fatalf("%s request without code", q.Role)
		}
	}
}

// Barrel files: a JS/TS index file is an entry point only in its module root
// or that root's src, so nested re-export index files rank as ordinary
// sources (deeper ones last) in both packs.
func TestW24BarrelIndexIsNotAnEntryPoint(t *testing.T) {
	tree := map[string]string{
		"package.json":        "{\"name\": \"barrels\"}\n",
		"src/index.ts":        "export * from \"./a/index.js\";\n",
		"src/real.ts":         "export function real(x: number): number {\n  return x + 1;\n}\n",
		"src/util.ts":         "export const util = 1;\n",
		"pkgs/p/package.json": "{\"name\": \"p\"}\n",
		"pkgs/p/index.js":     "module.exports = 1;\n",
	}
	for _, d := range []string{"a", "b", "c", "d", "e"} {
		tree["src/"+d+"/index.ts"] = "export * from \"./" + d + ".js\";\n"
	}
	j := treeJob(t, tree)
	o := newOtherScope(j.Snapshot.Manifest, j.Scope)
	for p, want := range map[string]int{"src/index.ts": 0, "pkgs/p/index.js": 0, "src/a/index.ts": 2, "src/real.ts": 2} {
		if r, _ := priority(p, o); r != want {
			t.Fatalf("v1 rank of %s is %d, want %d", p, r, want)
		}
	}
	c, err := buildCorpus(j.Snapshot, j.Scope)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range c.Excerpts {
		got = append(got, e.Path)
	}
	if !slices.Equal(got, []string{"src/index.ts", "pkgs/p/index.js", "src/real.ts", "src/util.ts", "src/a/index.ts", "src/b/index.ts"}) {
		t.Fatalf("v1 corpus %v", got)
	}
	s := &r1Stage{h: R1Handler{Caller: &r1Caller{}}, ctx: t.Context(), j: j, role: "investigate"}
	code, _, err := s.codeCorpus()
	if err != nil {
		t.Fatal(err)
	}
	got = nil
	for _, e := range code[:4] {
		got = append(got, e.Path)
	}
	// Groups "." and "pkgs/p" alternate; within "." the root index, then
	// src/real.ts and src/util.ts before any nested index file.
	if !slices.Equal(got, []string{"src/index.ts", "pkgs/p/index.js", "src/real.ts", "src/util.ts"}) {
		t.Fatalf("r1 corpus %v", got)
	}
}
