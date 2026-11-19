package govet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// testGo returns the go command used to build this test binary, or skips.
// CODEYE_TEST_GO overrides it. No PATH search and no os/exec.
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

// Fixture module sources are kept as strings (not testdata .go files) so the
// repository's gofmt/go vet runs never see the intentionally broken code.
const (
	fixGoMod = "module example.test/probe\n\ngo 1.27\n"
	fixBad   = "package bad\n\nimport (\n\t\"fmt\"\n\t\"sync\"\n)\n\ntype T struct{ mu sync.Mutex }\n\nfunc Copy(t T) T { return t }\n\nfunc P() { fmt.Printf(\"%d\\n\", \"x\") }\n"
	fixBadT  = "package bad\n\nimport (\n\t\"fmt\"\n\t\"testing\"\n)\n\nfunc TestX(t *testing.T) { fmt.Printf(\"%s\\n\", 1) }\n"
	fixBadX  = "package bad_test\n\nimport (\n\t\"fmt\"\n\t\"testing\"\n)\n\nfunc TestY(t *testing.T) { fmt.Printf(\"%s\\n\", 2) }\n"
	fixClean = "package clean\n\nfunc Add(a, b int) int { return a + b }\n"
	fixIgn   = "//go:build ignore\n\npackage clean\n"
	fixTag   = "//go:build special\n\npackage tagged\n"
	fixCgo   = "package cg\n\nimport \"C\"\n\nfunc X() {}\n"
	fixType  = "package typeerr\n\nfunc F() int { return \"s\" }\n"
)

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
	var paths []string
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
		paths = append(paths, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	for _, p := range paths {
		h.Write([]byte(p + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

type fixture struct {
	SourceRoot string
	DataRoot   string
	Job        analysis.Job
}

// newFixture writes files into a source root, acquires a snapshot and
// clones a fresh attempt workspace, mimicking the worker.
func newFixture(t *testing.T, base string, files map[string]string, scope domain.Scope) fixture {
	t.Helper()
	if base == "" {
		base = t.TempDir()
	}
	src := filepath.Join(base, "source")
	data := filepath.Join(base, "data")
	scratch := filepath.Join(base, "scratch")
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
	return fixture{SourceRoot: src, DataRoot: data, Job: analysis.Job{RunID: "run-1", AttemptID: "attempt-1", Epoch: 1, Profile: "deep-review", Scope: scope, Snapshot: loaded, Scratch: scratch}}
}

// mutatingRunner wraps the real executor and lets a test alter one result,
// e.g. to simulate truncated or garbled output on real tool bytes.
type mutatingRunner struct {
	inner  executor.Runner
	mutate func(sub string, r *executor.Result)
	specs  []executor.Spec
}

func (m *mutatingRunner) Run(ctx context.Context, spec executor.Spec) executor.Result {
	m.specs = append(m.specs, spec)
	r := m.inner.Run(ctx, spec)
	if m.mutate != nil && len(spec.Args) > 0 {
		m.mutate(spec.Args[0], &r)
	}
	return r
}

func readDoc(t *testing.T, res analysis.TaskResult) (Document, []byte) {
	t.Helper()
	if len(res.Artifacts) != 1 || res.Artifacts[0].Role != analysis.RoleObservations || res.Artifacts[0].MediaType != analysis.MediaObservations {
		t.Fatalf("observations artifact missing: %+v", res.Artifacts)
	}
	raw, err := os.ReadFile(res.Artifacts[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	var doc Document
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("decode doc: %v", err)
	}
	if dump := os.Getenv("CODEYE_W08_DUMP"); dump != "" {
		name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()) + "-" + filepath.Base(filepath.Dir(res.Artifacts[0].Path)) + ".json"
		if err := os.WriteFile(filepath.Join(dump, name), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	canon, err := domain.CanonicalJSON(raw)
	if err != nil || string(canon) != string(raw) {
		t.Fatalf("observations document is not canonical JSON: %v", err)
	}
	return doc, raw
}

func newHandler(t *testing.T, cfg Config) *Handler {
	t.Helper()
	if cfg.GoExecutable == "" {
		cfg.GoExecutable = testGo(t)
	}
	h, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return h
}

func runAll(t *testing.T, h *Handler, job analysis.Job, runner executor.Runner) map[string]analysis.TaskResult {
	t.Helper()
	tasks, err := h.Plan(context.Background(), job)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	out := map[string]analysis.TaskResult{}
	for _, task := range tasks {
		out[task.Key] = h.Run(context.Background(), job, task, runner)
	}
	return out
}

// assertNoNodePaths checks that structured output carries none of the
// node-absolute directories used by the run.
func assertNoNodePaths(t *testing.T, raw []byte, paths ...string) {
	t.Helper()
	s := string(raw)
	for _, p := range paths {
		if p == "" {
			continue
		}
		forms := []string{p, filepath.ToSlash(p)}
		if r, err := filepath.EvalSymlinks(p); err == nil {
			forms = append(forms, r, filepath.ToSlash(r))
		}
		if b, err := json.Marshal(p); err == nil {
			forms = append(forms, strings.Trim(string(b), `"`))
		}
		for _, f := range forms {
			if strings.Contains(s, f) {
				t.Fatalf("structured output contains node path %q", f)
			}
		}
	}
}
