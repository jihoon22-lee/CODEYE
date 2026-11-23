package cpp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

func TestParseCMakeVersion(t *testing.T) {
	for in, want := range map[string]string{
		"cmake version 4.2.3\n\nCMake suite maintained and supported by Kitware (kitware.com/cmake).\n": "4.2.3",
		"cmake version 3.28.3-dirty\n": "3.28.3",
		"cmake 4.2.3\n":                "",
		"":                             "",
	} {
		got, ok := ParseCMakeVersion([]byte(in))
		if got != want || ok != (want != "") {
			t.Fatalf("%q: %q %v", in, got, ok)
		}
	}
}

func TestGenerationProjects(t *testing.T) {
	const c = "int f(void) { return 0; }\n"
	for _, tc := range []struct {
		name  string
		files map[string]string
		scope []string
		want  []string
	}{
		{"root project", map[string]string{"CMakeLists.txt": "project(a C)\n", "src/a.c": c, "src/CMakeLists.txt": "add_library(a a.c)\n"}, nil, []string{"."}},
		{"nested projects, shallowest first", map[string]string{"b/x/CMakeLists.txt": "project(b C)\n", "b/x/b.c": c, "a/CMakeLists.txt": "project(a C)\n", "a/a.c": c}, nil, []string{"a", "b/x"}},
		{"project without an in-scope source", map[string]string{"a/CMakeLists.txt": "project(a C)\n", "a/a.c": c, "b/CMakeLists.txt": "project(b C)\n", "b/b.c": c}, []string{"b"}, []string{"b"}},
		{"data directories are not projects", map[string]string{"vendor/z/CMakeLists.txt": "project(z C)\n", "vendor/z/z.c": c, "src/a.c": c}, nil, nil},
		{"snapshot compile DB wins", map[string]string{"CMakeLists.txt": "project(a C)\n", "a.c": c, CompileDBName: `[{"directory":"/b","file":"a.c","arguments":["cc","-c","a.c"]}]`}, nil, nil},
		{"no translation unit", map[string]string{"CMakeLists.txt": "project(a NONE)\n", "README.md": "x\n"}, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scope := domain.Scope{}
			if tc.scope != nil {
				scope = domain.Scope{Kind: "paths", Paths: tc.scope}
			}
			fx := newFixture(t, tc.files, scope)
			got, err := GenerationProjects(fx.Job)
			if err != nil || !slices.Equal(got, tc.want) {
				t.Fatalf("projects %v %v, want %v", got, err, tc.want)
			}
		})
	}
}

// staticPlanner stands for the analyzer composite.
type staticPlanner struct{ keys []string }

func (s staticPlanner) Info() analysis.HandlerInfo {
	return analysis.HandlerInfo{Name: "static", Version: "1", Capability: "analysis:deep-review"}
}

func (s staticPlanner) Plan(context.Context, analysis.Job) ([]analysis.TaskSpec, error) {
	var out []analysis.TaskSpec
	for _, k := range s.keys {
		out = append(out, analysis.TaskSpec{Key: k, Stage: domain.StageStaticAnalysis})
	}
	return out, nil
}

func (s staticPlanner) Run(context.Context, analysis.Job, analysis.TaskSpec, executor.Runner) analysis.TaskResult {
	return analysis.TaskResult{Status: domain.TaskSucceeded}
}

func generationFiles(t *testing.T) map[string]string {
	t.Helper()
	files := fixtureFiles(t)
	delete(files, CompileDBName)
	return files
}

// failRunner fails the test when anything is started.
type failRunner struct{ t *testing.T }

func (f failRunner) Run(context.Context, executor.Spec) executor.Result {
	f.t.Fatal("a process was started")
	return executor.Result{}
}

func TestGenerationHandlerPlanAndGrant(t *testing.T) {
	ctx := context.Background()
	files := generationFiles(t)
	for _, tc := range []struct {
		name     string
		keys     []string
		consumer bool
		want     bool
	}{
		{"planned before the analyzers", []string{CppcheckName, ClangTidyName}, true, true},
		{"no C/C++ analyzer task", []string{"go-vet"}, true, false},
		{"no usable C/C++ analyzer", []string{CppcheckName}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFixture(t, files, domain.Scope{})
			h, err := WrapGeneration(staticPlanner{tc.keys}, GenerationHandlerConfig{Consumer: tc.consumer})
			if err != nil {
				t.Fatal(err)
			}
			specs, err := h.Plan(ctx, fx.Job)
			if err != nil {
				t.Fatal(err)
			}
			if got := len(specs) > 0 && specs[0].Key == GenerateTaskKey; got != tc.want || len(specs) != len(tc.keys)+map[bool]int{true: 1}[tc.want] {
				t.Fatalf("plan %+v", specs)
			}
			if tc.want && (specs[0].Stage != domain.StageBuild || specs[0].Required) {
				t.Fatalf("generation spec %+v", specs[0])
			}
		})
	}
	t.Run("inner key collision", func(t *testing.T) {
		fx := newFixture(t, files, domain.Scope{})
		h, _ := WrapGeneration(staticPlanner{[]string{GenerateTaskKey, CppcheckName}}, GenerationHandlerConfig{Consumer: true})
		if _, err := h.Plan(ctx, fx.Job); err == nil {
			t.Fatal("collision accepted")
		}
	})
	for _, withheld := range []bool{false, true} {
		t.Run("no grant", func(t *testing.T) {
			fx := newFixture(t, files, domain.Scope{})
			fx.Job.RepositoryCodeWithheld = withheld
			h, _ := WrapGeneration(staticPlanner{[]string{CppcheckName}}, GenerationHandlerConfig{Consumer: true, Generate: GenerateConfig{ToolPath: "/usr/bin:/bin"}})
			res := h.Run(ctx, fx.Job, generationSpec(), failRunner{t})
			want := pack.ReasonRepositoryCodeNotAllowed
			if withheld {
				want = pack.ReasonRepositoryCodeNotOffered
			}
			if res.Status != domain.TaskUnsupported || res.Reason != want || len(res.Executions) != 0 || !slices.Contains(res.Limitations, LimGenerationNotAllowed) {
				t.Fatalf("result %+v", res)
			}
			l, err := layoutOf(fx.Job)
			if err != nil {
				t.Fatal(err)
			}
			src, broken := compileDBs(fx.Job, l)
			if broken != "" || len(src.DBs) != 0 || !slices.Equal(src.Limitations, []string{LimGenerationNotAllowed}) {
				t.Fatalf("analyzer input %+v %q", src, broken)
			}
		})
	}
	t.Run("delegates other keys", func(t *testing.T) {
		fx := newFixture(t, files, domain.Scope{})
		h, _ := WrapGeneration(staticPlanner{[]string{CppcheckName}}, GenerationHandlerConfig{Consumer: true})
		if res := h.Run(ctx, fx.Job, analysis.TaskSpec{Key: CppcheckName}, failRunner{t}); res.Status != domain.TaskSucceeded {
			t.Fatalf("result %+v", res)
		}
	})
}

func TestGenerateWithoutBuildTools(t *testing.T) {
	ctx := context.Background()
	bin := t.TempDir()
	fakeCMake := filepath.Join(bin, "cmake")
	if err := os.WriteFile(fakeCMake, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path, lim string
	}{
		{"no cmake", t.TempDir(), LimCMakeNotFound},
		{"no make or ninja", bin, LimGeneratorNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFixture(t, generationFiles(t), domain.Scope{})
			res := Generate(ctx, fx.Job, failRunner{t}, GenerateConfig{ToolPath: tc.path})
			if res.Status != domain.TaskUnsupported || res.Reason != ReasonBuildToolNotConfigured || !slices.Equal(res.Limitations, []string{tc.lim}) {
				t.Fatalf("result %+v", res)
			}
			l, _ := layoutOf(fx.Job)
			if src, broken := compileDBs(fx.Job, l); broken != "" || !slices.Equal(src.Limitations, []string{LimGenerationUnavailable}) {
				t.Fatalf("analyzer input %+v %q", src, broken)
			}
		})
	}
}

// realGeneration runs the generation on the fixture project without its
// compile DB, with the operator-style tool path CODEYE_TEST_BUILD_TOOL_PATH
// (cmake, make or ninja, a C and a C++ compiler).
func realGeneration(t *testing.T) (fixture, analysis.TaskResult, *recordingRunner) {
	t.Helper()
	toolPath := os.Getenv("CODEYE_TEST_BUILD_TOOL_PATH")
	if toolPath == "" || findIn(toolDirs(toolPath), "cmake") == "" {
		t.Skip("set CODEYE_TEST_BUILD_TOOL_PATH to a tool path with cmake, make and a C/C++ compiler")
	}
	fx := newFixture(t, generationFiles(t), domain.Scope{})
	fx.Job.AllowRepositoryCode = true
	runner := &recordingRunner{inner: &executor.Executor{}}
	h, err := WrapGeneration(staticPlanner{[]string{CppcheckName}}, GenerationHandlerConfig{Consumer: true, Generate: GenerateConfig{ToolPath: toolPath}})
	if err != nil {
		t.Fatal(err)
	}
	res := h.Run(context.Background(), fx.Job, generationSpec(), runner)
	return fx, res, runner
}

func TestRealCompileDBGeneration(t *testing.T) {
	fx, res, runner := realGeneration(t)
	if res.Status != domain.TaskSucceeded || !slices.Contains(res.Limitations, LimGenerated) || !slices.Contains(res.Limitations, pack.LimRepositoryCodeExecuted) {
		t.Fatalf("generation %+v", res)
	}
	ws := fx.Job.Snapshot.WorkspaceDir
	for _, s := range runner.specs {
		if s.WorkspaceRoot != ws && !strings.HasPrefix(s.WorkspaceRoot, fx.Job.Scratch) || !slices.Contains(s.ProtectedRoots, fx.Job.Snapshot.SnapshotDir) || s.Timeout <= 0 {
			t.Fatalf("spec %+v", s)
		}
		keys := []string{}
		for k := range s.Env {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		if !slices.Equal(keys, []string{"LANG", "PATH"}) {
			t.Fatalf("env %v", s.Env)
		}
	}
	var rec Generation
	b, err := os.ReadFile(generationPath(fx.Job.Scratch))
	if err != nil || json.Unmarshal(b, &rec) != nil || len(rec.Projects) != 1 || !rec.Projects[0].OK || rec.Projects[0].Entries == 0 || rec.Version == "" {
		t.Fatalf("record %s %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(ws, GeneratedBuildDir, "0", "gen", "config.h")); err != nil {
		t.Fatalf("configured header: %v", err)
	}
	// Nothing was built: no object file or library in the build directory.
	_ = filepath.WalkDir(filepath.Join(ws, GeneratedBuildDir), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && (strings.HasSuffix(p, ".o") || strings.HasSuffix(p, ".a") || strings.HasSuffix(p, ".so")) && !strings.Contains(p, "CMakeFiles/") {
			t.Fatalf("build output %s", p)
		}
		return nil
	})
	l, _ := layoutOf(fx.Job)
	src, broken := compileDBs(fx.Job, l)
	if broken != "" || !src.Generated || len(src.DBs) != 1 || !slices.Equal(src.Limitations, []string{LimGenerated, LimGeneratorCMake}) {
		t.Fatalf("analyzer input %+v %q", src, broken)
	}
	// A changed generated database is an integrity failure, not a silent
	// switch to the reduced mode.
	db := filepath.Join(ws, filepath.FromSlash(src.DBs[0]))
	if err := os.WriteFile(db, []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, broken := compileDBs(fx.Job, l); broken != ReasonSnapshotIntegrity {
		t.Fatalf("tampered database accepted: %q", broken)
	}
}

// The analyzers read the generated database: cppcheck runs in compile DB
// mode, the configured header in the build directory resolves, and the
// envelope says the database was generated.
func TestRealCppcheckReadsGeneratedCompileDB(t *testing.T) {
	a := newCppcheck(t)
	fx, res, _ := realGeneration(t)
	if res.Status != domain.TaskSucceeded {
		t.Fatalf("generation %+v", res)
	}
	r := runTask(t, a, fx.Job, &recordingRunner{inner: &executor.Executor{}})
	doc, raw := readEnvelope(t, fx.Job, r)
	packtest.AssertNoNodePaths(t, raw, fx.Job.Scratch, fx.Job.Snapshot.WorkspaceDir, fx.Job.Snapshot.Root, fx.SourceRoot)
	if doc.Target.Properties["mode"] != modeCompileDB || doc.Target.Properties["compile_db_source"] != "generated" ||
		!slices.Contains(doc.Limitations, LimGenerated) || !slices.Contains(doc.Limitations, LimGeneratedIncludes) ||
		slices.Contains(doc.Limitations, ReasonCompileDBMissing) {
		t.Fatalf("outcome %+v properties %v limitations %v", doc.Outcome, doc.Target.Properties, doc.Limitations)
	}
	if r.Status != domain.TaskSucceeded {
		t.Fatalf("cppcheck %+v", r)
	}
}

func TestRealClangTidyReadsGeneratedCompileDB(t *testing.T) {
	a := newClangTidy(t, 4)
	fx, res, _ := realGeneration(t)
	if res.Status != domain.TaskSucceeded {
		t.Fatalf("generation %+v", res)
	}
	r := runTask(t, a, fx.Job, &recordingRunner{inner: &executor.Executor{}})
	doc, _ := readEnvelope(t, fx.Job, r)
	if doc.Target.Properties["compile_db_source"] != "generated" || !slices.Contains(doc.Limitations, LimGenerated) || r.Status != domain.TaskSucceeded {
		t.Fatalf("outcome %+v properties %v limitations %v", doc.Outcome, doc.Target.Properties, doc.Limitations)
	}
}

func TestRealGCCAnalyzerReadsGeneratedCompileDB(t *testing.T) {
	exe := testTool(t, "CODEYE_TEST_GCC", "GCC 13 or newer")
	a, err := NewGCCAnalyzer(Config{Executable: exe})
	if err != nil {
		t.Fatal(err)
	}
	fx, res, _ := realGeneration(t)
	if res.Status != domain.TaskSucceeded {
		t.Fatalf("generation %+v", res)
	}
	runner := &recordingRunner{inner: &executor.Executor{}}
	if _, err := a.Probe(t.Context(), runner, fx.Job.Scratch); err != nil {
		t.Fatal(err)
	}
	r := runTask(t, a, fx.Job, runner)
	doc, _ := readEnvelope(t, fx.Job, r)
	// The C unit of the fixture's CMake targets (src/ring.c) is analyzed
	// with the generated flags and its configured header.
	if r.Status != domain.TaskSucceeded || doc.Target.Properties["compile_db_source"] != "generated" || !slices.Contains(doc.Limitations, LimGenerated) ||
		!slices.Contains(doc.Coverage.ProcessedPaths, "src/ring.c") {
		t.Fatalf("result %+v properties %v limitations %v coverage %+v", r, doc.Target.Properties, doc.Limitations, doc.Coverage)
	}
}
