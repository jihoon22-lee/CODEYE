package rustpack

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
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

// newFixture acquires a snapshot of files and clones an attempt workspace
// like the worker. The job carries the repository-code grant when allow is set.
func newFixture(t *testing.T, base string, files map[string]string, scope domain.Scope, allow bool) fixture {
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
	return fixture{SourceRoot: src, Job: analysis.Job{RunID: "run-1", AttemptID: "attempt-1", Epoch: 1, Profile: "deep-review", Scope: scope, Snapshot: loaded, Scratch: scratch, AllowRepositoryCode: allow}}
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
	key := filepath.Base(filepath.Dir(res.Artifacts[0].Path))
	doc, err := obsdoc.Decode(raw, pack.Expect(job, key))
	if err != nil {
		t.Fatalf("envelope invalid: %v\n%s", err, raw)
	}
	if dump := os.Getenv("CODEYE_RUST_DUMP"); dump != "" {
		name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()) + "-" + key + ".json"
		if err := os.WriteFile(filepath.Join(dump, name), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return doc, raw
}

func runTask(t *testing.T, a pack.Analyzer, job analysis.Job, key string, runner executor.Runner) analysis.TaskResult {
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
