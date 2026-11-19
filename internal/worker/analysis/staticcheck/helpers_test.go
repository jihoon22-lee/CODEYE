package staticcheck

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// testGo returns the go command that built this test binary (no PATH search).
func testGo(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("CODEYE_TEST_GO"); p != "" {
		return p
	}
	exe := "go"
	if runtime.GOOS == "windows" {
		exe = "go.exe"
	}
	//lint:ignore SA1019 the test needs the toolchain that built it, without PATH search
	p := filepath.Join(runtime.GOROOT(), "bin", exe)
	if _, err := os.Stat(p); err != nil {
		t.Skipf("go toolchain not found at GOROOT/bin (set CODEYE_TEST_GO): %v", err)
	}
	return p
}

// testStaticcheck returns the operator-installed staticcheck binary named by
// CODEYE_TEST_STATICCHECK, or skips: CI has no staticcheck, and the adapter
// never searches PATH or downloads a tool.
func testStaticcheck(t *testing.T) string {
	t.Helper()
	p := os.Getenv("CODEYE_TEST_STATICCHECK")
	if p == "" {
		t.Skip("set CODEYE_TEST_STATICCHECK to the staticcheck binary (2026.2.x) to run real-tool tests")
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("staticcheck not found: %v", err)
	}
	return p
}

// Fixture module sources stay strings so the repository's gofmt, go vet and
// staticcheck runs never see the intentionally defective code.
const (
	fixGoMod = "module example.test/sc\n\ngo 1.22\n"
	// findings: SA1000 (bad regexp), SA1012 (nil context), SA4006 (value never
	// read), SA4023 (impossible nil comparison with a related location), U1000.
	fixFind = "package find\n\nimport (\n\t\"context\"\n\t\"regexp\"\n\t\"strconv\"\n)\n\nfunc Re() *regexp.Regexp { return regexp.MustCompile(\"[\") }\n\nfunc Ctx() (context.Context, context.CancelFunc) { return context.WithCancel(nil) }\n\nfunc Parse(s string) int {\n\tn, err := strconv.Atoi(s)\n\tn, err = strconv.Atoi(s + \"0\")\n\t_ = err\n\treturn n\n}\n\ntype E struct{}\n\nfunc (*E) Error() string { return \"e\" }\n\nfunc get() *E { return nil }\n\nfunc Check() bool {\n\tvar err error = get()\n\treturn err == nil\n}\n\nfunc unused() {}\n"
	// concurrency in a test file: SA2001 empty critical section.
	fixFindT = "package find\n\nimport (\n\t\"sync\"\n\t\"testing\"\n)\n\nfunc TestLock(t *testing.T) {\n\tvar mu sync.Mutex\n\tmu.Lock()\n\tmu.Unlock()\n}\n"
	fixClean = "package clean\n\n// Add returns a+b.\nfunc Add(a, b int) int { return a + b }\n"
	fixIgn   = "//go:build ignore\n\npackage clean\n"
	fixTag   = "//go:build special\n\npackage tagged\n"
	fixType  = "package typeerr\n\nfunc F() int { return \"s\" }\n"
	fixDep   = "package dep\n\nimport _ \"golang.org/x/text/language\"\n"
	// overlap: constructs both go vet and staticcheck report (docs/ANALYZERS.md#탐색-probe).
	fixOverlap  = "package overlap\n\nimport (\n\t\"encoding/json\"\n\t\"fmt\"\n\t\"os\"\n\t\"os/signal\"\n\t\"sync\"\n)\n\nfunc Printf() { fmt.Printf(\"%d\", \"x\") }\n\nfunc Sig() {\n\tc := make(chan os.Signal)\n\tsignal.Notify(c, os.Interrupt)\n}\n\nfunc WG() {\n\tvar wg sync.WaitGroup\n\tgo func() {\n\t\twg.Add(1)\n\t\tdefer wg.Done()\n\t}()\n\twg.Wait()\n}\n\nfunc Appends(x []int) []int {\n\tx = append(x)\n\treturn x\n}\n\nfunc Unmarshal(b []byte) {\n\tvar v struct{ A int }\n\t_ = json.Unmarshal(b, v)\n}\n\nfunc Bools(a int) bool { return a == 1 || a == 1 }\n"
	fixOverlapT = "package overlap\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {\n\tgo func() { t.Fatal(\"x\") }()\n}\n"
	// suppressions (docs/ANALYZERS.md#규칙-문맥과-소스-안-억제): one active SA1000, one hidden by
	// //lint:ignore, one by //lint:file-ignore in another file.
	fixSup     = "package sup\n\nimport \"regexp\"\n\nfunc Active() *regexp.Regexp { return regexp.MustCompile(\"[\") }\n\nfunc Hidden() *regexp.Regexp {\n\t//lint:ignore SA1000 kept for the suppression fixture\n\treturn regexp.MustCompile(\"(\")\n}\n"
	fixSupFile = "//lint:file-ignore SA1000 whole file kept for the suppression fixture\n\npackage sup\n\nimport \"regexp\"\n\nfunc FileHidden() *regexp.Regexp { return regexp.MustCompile(\"(\") }\n"
	// U1000 (measured, staticcheck 2026.2.2): //lint:ignore U1000 hides the
	// unused function without any record, even with -show-ignored; the
	// visible one shows the check fires on the same construct.
	fixSupUnused       = "package sup\n\nfunc unusedVisible() int { return 1 }\n\n//lint:ignore U1000 kept for the suppression fixture\nfunc unusedHidden() int { return 2 }\n"
	fixSupUnusedHidden = "package sup\n\n//lint:ignore U1000 kept for the suppression fixture\nfunc unusedHidden() int { return 2 }\n"
	// the same package with the active problems suppressed too.
	fixSupAll = "package sup\n\nimport \"regexp\"\n\nfunc Active() *regexp.Regexp {\n\t//lint:ignore SA1000 suppressed after review\n\treturn regexp.MustCompile(\"[\")\n}\n\nfunc Hidden() *regexp.Regexp {\n\t//lint:ignore SA1000 kept for the suppression fixture\n\treturn regexp.MustCompile(\"(\")\n}\n"
)

func suppressionFiles(all bool) map[string]string {
	sup, unused := fixSup, fixSupUnused
	if all {
		sup, unused = fixSupAll, fixSupUnusedHidden
	}
	return map[string]string{"go.mod": fixGoMod, "sup/sup.go": sup, "sup/file.go": fixSupFile, "sup/unused.go": unused}
}

func findingFiles() map[string]string {
	return map[string]string{"go.mod": fixGoMod, "find/find.go": fixFind, "find/find_test.go": fixFindT, "clean/c.go": fixClean, "clean/ign.go": fixIgn, "README.md": "# sc\n"}
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func treeHash(t *testing.T, root string) string {
	t.Helper()
	h := sha256.New()
	var lines []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		info, err := d.Info()
		if err != nil {
			return err
		}
		line := filepath.ToSlash(rel) + "|" + info.Mode().String()
		if d.Type().IsRegular() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(b)
			line += "|" + hex.EncodeToString(sum[:])
		}
		lines = append(lines, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	for _, l := range lines {
		h.Write([]byte(l + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

type fixture struct {
	SourceRoot string
	Job        analysis.Job
}

// newFixture acquires a snapshot of files and clones an attempt
// workspace like the worker.
func newFixture(t *testing.T, base string, files map[string]string, scope domain.Scope) fixture {
	t.Helper()
	if base == "" {
		base = t.TempDir()
	}
	src, data, scratch := filepath.Join(base, "source"), filepath.Join(base, "data"), filepath.Join(base, "scratch")
	writeTree(t, src, files)
	for _, d := range []string{data, scratch} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	snap, err := source.AcquireLocal(ctx, src, ".", data, source.DefaultLimits())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	loaded, err := source.Load(ctx, snap.Root, snap.Digest)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	ws := filepath.Join(data, "attempt-workspace")
	if err := source.CloneWorkspace(ctx, loaded, ws); err != nil {
		t.Fatalf("clone: %v", err)
	}
	loaded.WorkspaceDir = ws
	if scope.Kind == "" {
		scope.Kind = "full"
	}
	return fixture{SourceRoot: src, Job: analysis.Job{RunID: "run-1", AttemptID: "attempt-1", Epoch: 1, Profile: "deep-review", Scope: scope, Snapshot: loaded, Scratch: scratch}}
}

// mutatingRunner wraps the real executor and lets a test alter one result.
type mutatingRunner struct {
	inner  executor.Runner
	mutate func(spec executor.Spec, r *executor.Result)
	specs  []executor.Spec
}

func (m *mutatingRunner) Run(ctx context.Context, spec executor.Spec) executor.Result {
	m.specs = append(m.specs, spec)
	r := m.inner.Run(ctx, spec)
	if m.mutate != nil {
		m.mutate(spec, &r)
	}
	return r
}

func newAnalyzer(t *testing.T, modcache string) *Analyzer {
	t.Helper()
	goh, err := govet.New(govet.Config{GoExecutable: testGo(t), GoModCache: modcache})
	if err != nil {
		t.Fatalf("go: %v", err)
	}
	a, err := New(Config{Executable: testStaticcheck(t), Go: goh})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

// readEnvelope reads and validates the task's observations artifact.
func readEnvelope(t *testing.T, job analysis.Job, res analysis.TaskResult) (obsdoc.Document, []byte) {
	t.Helper()
	if len(res.Artifacts) != 1 || res.Artifacts[0].Role != analysis.RoleObservations || res.Artifacts[0].MediaType != analysis.MediaObservations {
		t.Fatalf("observations artifact missing: %+v", res)
	}
	raw, err := os.ReadFile(res.Artifacts[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	key := strings.TrimSuffix(filepath.Base(filepath.Dir(res.Artifacts[0].Path)), "")
	doc, err := obsdoc.Decode(raw, pack.Expect(job, key))
	if err != nil {
		t.Fatalf("envelope invalid: %v\n%s", err, raw)
	}
	if dump := os.Getenv("CODEYE_STATICCHECK_DUMP"); dump != "" {
		name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()) + "-" + key + ".json"
		if err := os.WriteFile(filepath.Join(dump, name), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return doc, raw
}

func runTask(t *testing.T, a *Analyzer, job analysis.Job, key string, runner executor.Runner) analysis.TaskResult {
	t.Helper()
	tasks, err := a.Plan(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.Spec.Key == key {
			return a.Run(context.Background(), job, task.Spec, runner)
		}
	}
	t.Fatalf("task %s not planned: %+v", key, tasks)
	return analysis.TaskResult{}
}

func assertNoNodePaths(t *testing.T, raw []byte, paths ...string) {
	t.Helper()
	s := string(raw)
	for _, p := range paths {
		if p == "" {
			continue
		}
		for _, f := range []string{p, filepath.ToSlash(p)} {
			if strings.Contains(s, f) {
				t.Fatalf("structured output contains node path %q", f)
			}
		}
	}
}
