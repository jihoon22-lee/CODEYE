package cpp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Real-tool tests (TC-105..TC-107). They skip unless CODEYE_TEST_CPPCHECK
// (cppcheck 2.22.x) or CODEYE_TEST_CLANG_TIDY (clang-tidy 23.1.x) names an
// operator-installed binary. With CODEYE_W16_CAPTURE=<dir> they write the
// check streams the replay tests read (docs/41 §9):
//
//	CODEYE_TEST_CPPCHECK=$HOME/.local/share/codeye-tools/cpp/bin/cppcheck \
//	CODEYE_TEST_CLANG_TIDY=$HOME/.local/share/codeye-tools/cpp/bin/clang-tidy \
//	CODEYE_W16_CAPTURE=$PWD/internal/worker/analysis/cpp/testdata \
//	go test -count=1 -run 'TestReal' ./internal/worker/analysis/cpp/

func optionalTool(env string) string {
	p := os.Getenv(env)
	if p == "" {
		return ""
	}
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

type realRun struct {
	res    analysis.TaskResult
	doc    obsdoc.Document
	raw    []byte
	specs  []executor.Spec
	fx     fixture
	stored [][]byte // the stored stream files of every execution
}

func runReal(t *testing.T, a planner, files map[string]string, scope domain.Scope) realRun {
	t.Helper()
	fx := newFixture(t, files, scope)
	srcBefore, snapBefore := treeHash(t, fx.SourceRoot), treeHash(t, fx.Job.Snapshot.SnapshotDir)
	runner := &recordingRunner{inner: &executor.Executor{}}
	res := runTask(t, a, fx.Job, runner)
	doc, raw := readEnvelope(t, fx.Job, res)
	if treeHash(t, fx.SourceRoot) != srcBefore || treeHash(t, fx.Job.Snapshot.SnapshotDir) != snapBefore {
		t.Fatal("the original source or the snapshot changed")
	}
	home, _ := os.UserHomeDir()
	assertNoNodePaths(t, raw, fx.Job.Scratch, fx.Job.Snapshot.WorkspaceDir, fx.Job.Snapshot.Root, fx.SourceRoot, home, filepath.Dir(fx.Job.Scratch))
	out := realRun{res: res, doc: doc, raw: raw, specs: runner.specs, fx: fx}
	for _, e := range res.Executions {
		for _, p := range []string{e.StdoutPath, e.StderrPath} {
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			out.stored = append(out.stored, b)
		}
	}
	for _, s := range runner.specs {
		// The cwd is the empty task scratch directory, never the workspace;
		// the environment is PATH and LANG only (the executor adds HOME and
		// TMP); every argument after the executable is an option or an
		// absolute path.
		keys := make([]string, 0, len(s.Env))
		for k := range s.Env {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		if s.Dir != s.WorkspaceRoot || !strings.HasPrefix(s.Dir, fx.Job.Scratch) || !slices.Equal(keys, []string{"LANG", "PATH"}) || s.Timeout <= 0 {
			t.Fatalf("spec %+v", s)
		}
		for _, arg := range s.Args {
			if !strings.HasPrefix(arg, "-") && !filepath.IsAbs(arg) {
				t.Fatalf("argument %q of %v is neither an option nor absolute", arg, s.Args)
			}
		}
	}
	for _, e := range doc.Executions {
		if e.Command.Dir != analysis.RedactedMarker {
			t.Fatalf("execution %s cwd recorded as %q", e.Name, e.Command.Dir)
		}
	}
	return out
}

func (r realRun) capture(t *testing.T, name string) {
	t.Helper()
	for _, e := range r.res.Executions {
		switch {
		case e.Name == execCppcheckCheck:
			captureStreams(t, name+".cppcheck", r.res, e.Name)
		case strings.HasPrefix(e.Name, execClangTidyCheck+"."):
			captureStreams(t, name+".clang-tidy."+strings.TrimPrefix(e.Name, execClangTidyCheck+"."), r.res, e.Name)
		case e.Name == execCppcheckVersion:
			captureStreams(t, "version.cppcheck", r.res, e.Name)
		case e.Name == execClangTidyVersion:
			captureStreams(t, "version.clang-tidy", r.res, e.Name)
		}
	}
}

// Every case of fixtures/w16-cpp/expected.json on the real tools.
func TestRealFixtureCases(t *testing.T) {
	ccPath, ctPath := optionalTool("CODEYE_TEST_CPPCHECK"), optionalTool("CODEYE_TEST_CLANG_TIDY")
	if ccPath == "" && ctPath == "" {
		t.Skip("set CODEYE_TEST_CPPCHECK and/or CODEYE_TEST_CLANG_TIDY to run real-tool tests")
	}
	e := loadExpected(t)
	for _, name := range caseNames(e) {
		c := e.Cases[name]
		t.Run(name, func(t *testing.T) {
			files := caseFiles(t, c)
			if c.Cppcheck != nil && ccPath != "" {
				a, err := NewCppcheck(Config{Executable: ccPath})
				if err != nil {
					t.Fatal(err)
				}
				r := runReal(t, a, files, caseScope(c))
				compareTool(t, name+" cppcheck", r.doc, *c.Cppcheck)
				if !c.RealOnly {
					r.capture(t, name)
				}
			}
			if c.ClangTidy != nil && ctPath != "" {
				a, err := NewClangTidy(Config{Executable: ctPath, BatchSize: c.BatchSize})
				if err != nil {
					t.Fatal(err)
				}
				r := runReal(t, a, files, caseScope(c))
				compareTool(t, name+" clang-tidy", r.doc, *c.ClangTidy)
				// No source line is stored (sanitize.go).
				for _, b := range r.stored {
					for _, l := range strings.Split(string(b), "\n") {
						if ctSnippet.MatchString(l) {
							t.Fatalf("stored stream holds a source snippet line %q", l)
						}
					}
				}
				if !c.RealOnly {
					r.capture(t, name)
				}
			}
		})
	}
}

// Security: a repository source that includes files outside the workspace
// copy by absolute path makes the tools read them (the node account can
// read them), but nothing of their content is stored: not in the stored
// streams, not in the envelope, not in the task result.
func TestRealHostFileContentIsNotStored(t *testing.T) {
	ccPath, ctPath := optionalTool("CODEYE_TEST_CPPCHECK"), optionalTool("CODEYE_TEST_CLANG_TIDY")
	if ccPath == "" && ctPath == "" {
		t.Skip("set CODEYE_TEST_CPPCHECK and/or CODEYE_TEST_CLANG_TIDY to run real-tool tests")
	}
	host := t.TempDir()
	const bad, ok = "W16CANARY_BAD_c41f9e", "W16CANARY_OK_8d27b0"
	writeTree(t, host, map[string]string{
		"bad.txt": bad + " secret line\n",
		"ok.h":    "static int canary_fn(void) { int *p = 0; return *p; } /* " + ok + " */\n",
	})
	files := map[string]string{
		"src/a.c": "#include \"" + filepath.ToSlash(filepath.Join(host, "bad.txt")) + "\"\nint a(void) { return 1; }\n",
		"src/b.c": "#include \"" + filepath.ToSlash(filepath.Join(host, "ok.h")) + "\"\nint b(void) { return canary_fn(); }\n",
		"src/c.c": "int c(void) { int z = 0; return 1 / z; }\n",
	}
	db, _ := json.Marshal([]map[string]any{
		{"directory": "/build/leak", "file": "src/a.c", "arguments": []string{"cc", "-c", "src/a.c"}},
		{"directory": "/build/leak", "file": "src/b.c", "arguments": []string{"cc", "-c", "src/b.c"}},
		{"directory": "/build/leak", "file": "src/c.c", "arguments": []string{"cc", "-c", "src/c.c"}},
	})
	check := func(t *testing.T, r realRun) {
		t.Helper()
		blobs := append([][]byte{r.raw}, r.stored...)
		res, _ := json.Marshal(r.res)
		blobs = append(blobs, res)
		for _, b := range blobs {
			for _, canary := range []string{bad, ok, "secret line", "canary_fn(void)"} {
				if strings.Contains(string(b), canary) {
					t.Fatalf("host file content %q stored:\n%s", canary, b)
				}
			}
		}
		if !slices.Contains(r.doc.Limitations, LimOutsideDropped) {
			t.Fatalf("outcome %+v limitations %v", r.doc.Outcome, r.doc.Limitations)
		}
	}
	for _, mode := range []string{"compile_db", "reduced"} {
		f := map[string]string{}
		for k, v := range files {
			f[k] = v
		}
		if mode == "compile_db" {
			f[CompileDBName] = string(db)
		}
		t.Run(mode, func(t *testing.T) {
			if ccPath != "" {
				a, err := NewCppcheck(Config{Executable: ccPath})
				if err != nil {
					t.Fatal(err)
				}
				check(t, runReal(t, a, f, domain.Scope{}))
			}
			if ctPath != "" && mode == "compile_db" {
				a, err := NewClangTidy(Config{Executable: ctPath})
				if err != nil {
					t.Fatal(err)
				}
				r := runReal(t, a, f, domain.Scope{})
				check(t, r)
				// The errors in bad.txt fail a.c without their text; the
				// warning in ok.h and its notes are gone; c.c is analyzed.
				if r.doc.Coverage.Units[TUFailedCompile] != 1 || !slices.ContainsFunc(r.doc.Observations, func(o obsdoc.Observation) bool {
					return o.RuleID == "clang-analyzer-core.DivideZero"
				}) {
					t.Fatalf("units %v observations %+v", r.doc.Coverage.Units, r.doc.Observations)
				}
			}
		})
	}
}

// The startup probe of both tools on the real binaries.
func TestRealProbes(t *testing.T) {
	ccPath, ctPath := optionalTool("CODEYE_TEST_CPPCHECK"), optionalTool("CODEYE_TEST_CLANG_TIDY")
	if ccPath == "" && ctPath == "" {
		t.Skip("set CODEYE_TEST_CPPCHECK and/or CODEYE_TEST_CLANG_TIDY to run real-tool tests")
	}
	dir := t.TempDir()
	if ccPath != "" {
		a, _ := NewCppcheck(Config{Executable: ccPath})
		if v, err := a.Probe(t.Context(), &executor.Executor{}, dir); err != nil || !CppcheckVersionTested(v) {
			t.Fatalf("cppcheck probe %q %v", v, err)
		}
	}
	if ctPath != "" {
		a, _ := NewClangTidy(Config{Executable: ctPath})
		if v, err := a.Probe(t.Context(), &executor.Executor{}, dir); err != nil || !ClangTidyVersionTested(v) {
			t.Fatalf("clang-tidy probe %q %v", v, err)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("probe left %v", entries)
	}
}

// Engine overlap (docs/41 §7), measured on fixtures/w16-cpp/overlap: the
// baseline cppcheck and clang-tidy are different engines and both run. A
// clang-tidy check that reported only constructs cppcheck also reported on
// the same lines is not selected; a kept check that overlaps has a variant
// that only clang-tidy reports. Inside clang-tidy, an Analyzer check that
// reported only what a bugprone check reported is not selected, and no alias
// family appears. The line table is logged for the document.
func TestRealEngineOverlap(t *testing.T) {
	ccPath, ctPath := optionalTool("CODEYE_TEST_CPPCHECK"), optionalTool("CODEYE_TEST_CLANG_TIDY")
	if ccPath == "" || ctPath == "" {
		t.Skip("set CODEYE_TEST_CPPCHECK and CODEYE_TEST_CLANG_TIDY to measure the engine overlap")
	}
	src, err := os.ReadFile("../../../../fixtures/w16-cpp/overlap/overlap.cpp")
	if err != nil {
		t.Fatal(err)
	}
	lineOf := func(marker string) int {
		i := strings.Index(string(src), marker)
		if i < 0 || strings.Count(string(src), marker) != 1 {
			t.Fatalf("marker %q", marker)
		}
		return strings.Count(string(src[:i]), "\n") + 1
	}
	db, _ := json.Marshal([]map[string]any{{"directory": "/build/ov", "file": "overlap.cpp", "arguments": []string{"c++", "-std=c++17", "-c", "overlap.cpp"}}})
	files := map[string]string{"overlap.cpp": string(src), CompileDBName: string(db)}
	cc, _ := NewCppcheck(Config{Executable: ccPath})
	ct, _ := NewClangTidy(Config{Executable: ctPath})
	lines := map[int][2][]string{}
	for i, a := range []planner{cc, ct} {
		r := runReal(t, a, files, domain.Scope{})
		if r.doc.Outcome.TaskStatus != domain.TaskSucceeded {
			t.Fatalf("%s outcome %+v", r.doc.Analyzer.ID, r.doc.Outcome)
		}
		for _, o := range r.doc.Observations {
			if o.Location != nil {
				l := lines[o.Location.StartLine]
				l[i] = append(l[i], o.RuleID)
				lines[o.Location.StartLine] = l
			}
		}
	}
	excluded := []string{"bugprone-assert-side-effect", "bugprone-suspicious-memset-usage", "clang-analyzer-core.BitwiseShift", "clang-analyzer-core.StackAddressEscape",
		"clang-analyzer-cplusplus.InnerPointer", "clang-analyzer-cplusplus.Move", "clang-analyzer-security.FloatLoopCounter"}
	for _, l := range lines {
		for _, rule := range l[1] {
			if slices.Contains(excluded, rule) || strings.HasPrefix(rule, "cert-") || strings.HasPrefix(rule, "hicpp-") || strings.HasPrefix(rule, "cppcoreguidelines-") {
				t.Fatalf("excluded rule reported: %s", rule)
			}
		}
	}
	// The kept overlapping checks: a variant only clang-tidy reports.
	for marker, rule := range map[string]string{
		"std::error_code make_error(void);\nvoid unused_error_code": "bugprone-unused-return-value",
		"int *p = make_array(); delete p;":                          "clang-analyzer-unix.MismatchedDeallocator",
		"static int at(const int *a, int i) { return a[i]; }":       "clang-analyzer-security.ArrayBound",
		"return 10 / q->d;":                  "clang-analyzer-core.DivideZero",
		"return *q->p;":                      "clang-analyzer-core.NullDereference",
		"release(p); free(p);":               "clang-analyzer-unix.Malloc",
		"Owner &operator=(const Owner &o) {": "bugprone-unhandled-self-assignment",
		"b.empty(); }":                       "bugprone-standalone-empty",
		"if (a) x = 1; else x = 1;":          "bugprone-branch-clone",
		"return a.v + b.v;":                  "bugprone-use-after-move",
		"for (float f = 0.0f;":               "bugprone-float-loop-counter",
	} {
		n := lineOf(marker) + strings.Count(marker, "\n")
		if l := lines[n]; !slices.Contains(l[1], rule) || len(l[0]) > 0 {
			t.Fatalf("line %d (%s): cppcheck %v clang-tidy %v", n, rule, l[0], l[1])
		}
	}
	// What only the baseline reports.
	for _, marker := range []string{"memset(buf, 0xff, 0);", "return *null_source();"} {
		if l := lines[lineOf(marker)]; len(l[0]) == 0 || len(l[1]) > 0 {
			t.Fatalf("%q: cppcheck %v clang-tidy %v", marker, l[0], l[1])
		}
	}
	var keys []int
	for k := range lines {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		t.Logf("%3d | %s | %s", k, strings.Join(lines[k][0], ","), strings.Join(lines[k][1], ","))
	}
}
