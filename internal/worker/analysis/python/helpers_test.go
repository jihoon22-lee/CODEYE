package python

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

// Real-tool tests need the operator-installed tools named by the
// environment; CI has none and the adapters never search PATH or download.
func testTool(t *testing.T, env string) string {
	t.Helper()
	p := os.Getenv(env)
	if p == "" {
		t.Skipf("set %s to run real-tool tests", env)
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("%s not found: %v", env, err)
	}
	return p
}

// testNode is the validated node toolchain handle for pyright.
func testNode(t *testing.T) *pack.Toolchain {
	t.Helper()
	p := testTool(t, "CODEYE_TEST_NODE")
	return &pack.Toolchain{Name: "node", Executable: p, Dir: filepath.Dir(p), Version: "test"}
}

func newRuffT(t *testing.T) *Ruff {
	t.Helper()
	a, err := NewRuff(Config{Executable: testTool(t, "CODEYE_TEST_RUFF")})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func newMypyT(t *testing.T) *Mypy {
	t.Helper()
	a, err := NewMypy(Config{Executable: testTool(t, "CODEYE_TEST_MYPY")})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func newPyrightT(t *testing.T) *Pyright {
	t.Helper()
	exe := testTool(t, "CODEYE_TEST_PYRIGHT")
	a, err := NewPyright(Config{Executable: exe, Node: testNode(t)})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func newBanditT(t *testing.T) *Bandit {
	t.Helper()
	a, err := NewBandit(Config{Executable: testTool(t, "CODEYE_TEST_BANDIT")})
	if err != nil {
		t.Fatal(err)
	}
	return a
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
	h := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(h[:])
}

type fixture struct {
	SourceRoot string
	Job        analysis.Job
}

// newFixture acquires a snapshot of files and clones an attempt
// workspace like the worker.
func newFixture(t *testing.T, files map[string]string, scope domain.Scope) fixture {
	t.Helper()
	base := t.TempDir()
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

// recordingRunner wraps a runner, records every spec and lets a test alter
// one result.
type recordingRunner struct {
	inner   executor.Runner
	mutate  func(spec executor.Spec, r *executor.Result)
	specs   []executor.Spec
	results []executor.Result
}

func (m *recordingRunner) Run(ctx context.Context, spec executor.Spec) executor.Result {
	m.specs = append(m.specs, spec)
	r := m.inner.Run(ctx, spec)
	if m.mutate != nil {
		m.mutate(spec, &r)
	}
	m.results = append(m.results, r)
	return r
}

type planner interface {
	pack.Analyzer
}

// runTask plans and runs one task of a.
func runTask(t *testing.T, a planner, job analysis.Job, key string, runner executor.Runner) analysis.TaskResult {
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
	if dump := os.Getenv("CODEYE_PYTHON_DUMP"); dump != "" {
		name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()) + "-" + key + ".json"
		if err := os.WriteFile(filepath.Join(dump, name), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return doc, raw
}

// assertNoNodePaths fails when the structured output names a node path.
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

func rulesOf(d obsdoc.Document, suppressed bool) map[string]int {
	out := map[string]int{}
	for _, o := range d.Observations {
		if o.Suppressed == suppressed {
			out[o.RuleID]++
		}
	}
	return out
}

func hasLimit(d obsdoc.Document, code string) bool {
	for _, l := range d.Limitations {
		if l == code {
			return true
		}
	}
	return false
}
