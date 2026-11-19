package staticcheck

import (
	"context"
	"os"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

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

type fixture = packtest.Fixture

// newFixture acquires a snapshot of files and clones an attempt
// workspace like the worker.
func newFixture(t *testing.T, base string, files map[string]string, scope domain.Scope) fixture {
	t.Helper()
	return packtest.NewFixture(t, base, files, scope)
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
	goh, err := govet.New(govet.Config{GoExecutable: packtest.GoExecutable(t), GoModCache: modcache})
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
	return packtest.ReadEnvelope(t, job, res, "CODEYE_STATICCHECK_DUMP")
}
