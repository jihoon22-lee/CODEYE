package rustpack

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// fixtureDir is fixtures/rust/<name> of the repository.
func fixtureDir(name string) string {
	return filepath.Join("..", "..", "..", "..", "fixtures", "rust", name)
}

// fixtureFiles reads a fixture project into snapshot-relative files.
func fixtureFiles(t *testing.T, name string) map[string]string {
	t.Helper()
	out := map[string]string{}
	root := fixtureDir(name)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatalf("fixture %s is empty", name)
	}
	return out
}

func withFile(files map[string]string, rel, body string) map[string]string {
	out := map[string]string{}
	for k, v := range files {
		out[k] = v
	}
	out[rel] = body
	return out
}

// testToolchain returns the operator-installed cargo (CODEYE_TEST_CARGO) and
// cargo-clippy (CODEYE_TEST_CLIPPY), or skips: CI has no Rust toolchain and the
// adapter never searches PATH or downloads a tool.
func testToolchain(t *testing.T) (cargo, clippy string) {
	t.Helper()
	cargo = os.Getenv("CODEYE_TEST_CARGO")
	if cargo == "" {
		t.Skip("set CODEYE_TEST_CARGO to the toolchain's cargo (1.99.x, not the rustup proxy) to run real-tool tests")
	}
	if _, err := os.Stat(cargo); err != nil {
		t.Skipf("cargo not found: %v", err)
	}
	return cargo, os.Getenv("CODEYE_TEST_CLIPPY")
}

func testClippy(t *testing.T) (cargo, clippy string) {
	t.Helper()
	cargo, clippy = testToolchain(t)
	if clippy == "" {
		t.Skip("set CODEYE_TEST_CLIPPY to the toolchain's cargo-clippy (0.1.99) to run real clippy tests")
	}
	return cargo, clippy
}

// testToolPath is the operator tool path the real tests give the linker
// lookup; "" skips tests that link build scripts when no cc is found.
func testToolPath() string {
	if p, ok := os.LookupEnv("CODEYE_TEST_TOOLPATH"); ok {
		return p
	}
	return "/usr/bin:/bin"
}

func requireLinker(t *testing.T) {
	t.Helper()
	if FindLinker(testToolPath()) == "" {
		t.Skip("no cc in the test tool path (CODEYE_TEST_TOOLPATH): build scripts cannot be linked")
	}
}

type fixture = packtest.Fixture

// newFixture acquires a snapshot of files and clones an attempt workspace
// like the worker. The job carries the repository-code grant when allow is set.
func newFixture(t *testing.T, base string, files map[string]string, scope domain.Scope, allow bool) fixture {
	t.Helper()
	fx := packtest.NewFixture(t, base, files, scope)
	fx.Job.AllowRepositoryCode = allow
	return fx
}

// readEnvelope reads and validates the task's observations artifact.
func readEnvelope(t *testing.T, job analysis.Job, res analysis.TaskResult) (obsdoc.Document, []byte) {
	t.Helper()
	return packtest.ReadEnvelope(t, job, res, "CODEYE_RUST_DUMP")
}

func ruleCount(doc obsdoc.Document) map[string]int {
	out := map[string]int{}
	for _, o := range doc.Observations {
		out[o.RuleID]++
	}
	return out
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

func newReal(t *testing.T, kind Kind) *Analyzer {
	t.Helper()
	var cargo, clippy string
	if kind == KindClippy {
		cargo, clippy = testClippy(t)
	} else {
		cargo, _ = testToolchain(t)
	}
	a, err := New(kind, Config{Cargo: cargo, Clippy: clippy, ToolPath: testToolPath()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}
