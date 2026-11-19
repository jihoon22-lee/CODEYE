// Package jststest holds the test fixtures the tsc and ESLint adapters
// share: operator tool paths from CODEYE_TEST_* (real-tool tests skip
// without them; CI has no Node tools), a snapshot with a cloned attempt
// workspace, tree hashes, a result-mutating runner, envelope decoding and
// the capture of real tool streams into testdata. It is imported by tests
// only.
package jststest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nativepath"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Env names the operator tool paths of the real-tool tests.
const (
	EnvNode   = "CODEYE_TEST_NODE"
	EnvTsc    = "CODEYE_TEST_TSC"
	EnvESLint = "CODEYE_TEST_ESLINT"
	// EnvCapture names a directory: real-tool tests then write the check
	// streams and exit code of each case there (how testdata was captured).
	EnvCapture = "CODEYE_JSTS_CAPTURE"
)

func toolPath(t *testing.T, env, what string) string {
	t.Helper()
	p := os.Getenv(env)
	if p == "" {
		t.Skipf("set %s to %s to run real-tool tests", env, what)
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("%s not found: %v", what, err)
	}
	return p
}

var (
	nodeOnce sync.Once
	nodeTC   pack.Toolchain
	nodeErr  string
)

// Node is the shared node toolchain from CODEYE_TEST_NODE, its version read
// through the executor like the agent's startup probe; it skips without it.
func Node(t *testing.T) pack.Toolchain {
	t.Helper()
	p := toolPath(t, EnvNode, "the node executable (v24)")
	nodeOnce.Do(func() {
		resolved, err := nativepath.Resolve(p)
		if err != nil {
			nodeErr = err.Error()
			return
		}
		dir, err := os.MkdirTemp("", "jststest-node-")
		if err != nil {
			nodeErr = err.Error()
			return
		}
		defer os.RemoveAll(dir)
		res := (&executor.Executor{}).Run(context.Background(), executor.Spec{Executable: p, Args: []string{"--version"}, WorkspaceRoot: dir, Dir: dir, TempParent: dir,
			Env: map[string]string{"PATH": filepath.Dir(resolved)}, Timeout: 30e9, StdoutLimit: 1 << 10, StderrLimit: 1 << 10})
		m := regexp.MustCompile(`^v([0-9]+\.[0-9]+\.[0-9]+)$`).FindStringSubmatch(strings.TrimSpace(string(res.Stdout.Data)))
		if m == nil {
			nodeErr = "node --version: " + string(res.Status) + " " + string(res.Stdout.Data)
			return
		}
		nodeTC = pack.Toolchain{Name: "node", Executable: p, Dir: filepath.Dir(resolved), Version: m[1]}
	})
	if nodeErr != "" {
		t.Fatalf("node toolchain: %s", nodeErr)
	}
	return nodeTC
}

// Tsc is the operator's typescript/bin/tsc from CODEYE_TEST_TSC.
func Tsc(t *testing.T) string { return toolPath(t, EnvTsc, "typescript/bin/tsc (6.0.x)") }

// ESLint is the operator's eslint/bin/eslint.js from CODEYE_TEST_ESLINT.
func ESLint(t *testing.T) string { return toolPath(t, EnvESLint, "eslint/bin/eslint.js (10.15.x)") }

// WriteTree writes files (slash paths) below root.
func WriteTree(t *testing.T, root string, files map[string]string) {
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

// ReadTree reads every regular file below dir as slash path -> content.
func ReadTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// MonorepoFiles is fixtures/js-ts/input, read from a package directory
// depth levels below the repository root.
func MonorepoFiles(t *testing.T, depth int) map[string]string {
	t.Helper()
	return ReadTree(t, filepath.Join(strings.Repeat("../", depth)+"fixtures", "js-ts", "input"))
}

// TreeHash digests every path, mode and regular file content below root.
func TreeHash(t *testing.T, root string) string {
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
	h := sha256.New()
	for _, l := range lines {
		h.Write([]byte(l + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Fixture is a snapshot of files with a cloned attempt workspace.
type Fixture struct {
	Base, SourceRoot string
	Job              analysis.Job
}

// NewFixture acquires a snapshot of files under base (a temp dir when
// empty) and clones an attempt workspace like the worker.
func NewFixture(t *testing.T, base string, files map[string]string, scope domain.Scope) Fixture {
	t.Helper()
	if base == "" {
		base = t.TempDir()
	}
	src, data, scratch := filepath.Join(base, "source"), filepath.Join(base, "data"), filepath.Join(base, "scratch")
	WriteTree(t, src, files)
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
	return Fixture{Base: base, SourceRoot: src, Job: analysis.Job{RunID: "run-1", AttemptID: "attempt-1", Epoch: 1, Profile: "deep-review", Scope: scope, Snapshot: loaded, Scratch: scratch}}
}

// MutatingRunner wraps a runner, records the specs and lets a test alter a
// result.
type MutatingRunner struct {
	Inner  executor.Runner
	Mutate func(spec executor.Spec, r *executor.Result)
	Specs  []executor.Spec
}

// Run implements executor.Runner.
func (m *MutatingRunner) Run(ctx context.Context, spec executor.Spec) executor.Result {
	m.Specs = append(m.Specs, spec)
	r := m.Inner.Run(ctx, spec)
	if m.Mutate != nil {
		m.Mutate(spec, &r)
	}
	return r
}

// ReadEnvelope reads the task's observations artifact and decodes it with
// the contract checks.
func ReadEnvelope(t *testing.T, job analysis.Job, res analysis.TaskResult) (obsdoc.Document, []byte) {
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
	if dump := os.Getenv("CODEYE_JSTS_DUMP"); dump != "" {
		name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()) + "-" + key + ".json"
		if err := os.WriteFile(filepath.Join(dump, name), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return doc, raw
}

// RunTask plans with plan and runs the task keyed key.
func RunTask(t *testing.T, a pack.Analyzer, job analysis.Job, key string, runner executor.Runner) analysis.TaskResult {
	t.Helper()
	return RunTaskCtx(t, context.Background(), a, job, key, runner)
}

// RunTaskCtx is RunTask with a run context.
func RunTaskCtx(t *testing.T, ctx context.Context, a pack.Analyzer, job analysis.Job, key string, runner executor.Runner) analysis.TaskResult {
	t.Helper()
	tasks, err := a.Plan(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.Spec.Key == key {
			return a.Run(ctx, job, task.Spec, runner)
		}
	}
	t.Fatalf("task %s not planned: %+v", key, tasks)
	return analysis.TaskResult{}
}

// AssertNoNodePaths fails when raw holds any of the node paths.
func AssertNoNodePaths(t *testing.T, raw []byte, paths ...string) {
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

// Capture writes every execution of a task run (with CODEYE_JSTS_CAPTURE
// set) as <name>.<execution>.stdout|stderr|exit and the execution order as
// <name>.order, the input of NewReplay. Captures must hold no node path.
func Capture(t *testing.T, name string, res analysis.TaskResult) {
	t.Helper()
	dir := os.Getenv(EnvCapture)
	if dir == "" {
		return
	}
	var order []string
	for _, e := range res.Executions {
		order = append(order, e.Name)
		code := "none"
		if e.Result.ExitCode != nil {
			code = strconv.Itoa(*e.Result.ExitCode)
		}
		for suffix, data := range map[string][]byte{".stdout": e.Result.Stdout.Data, ".stderr": e.Result.Stderr.Data, ".exit": []byte(code + "\n")} {
			if bytes.Contains(data, []byte("/home/")) || bytes.Contains(data, []byte("/tmp/")) || bytes.Contains(data, []byte(`\Users\`)) {
				t.Fatalf("capture %s.%s%s holds a node path:\n%s", name, e.Name, suffix, data)
			}
			if err := os.WriteFile(filepath.Join(dir, name+"."+e.Name+suffix), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(dir, name+".order"), []byte(strings.Join(order, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Recorded is one captured execution.
type Recorded struct {
	Name           string
	Stdout, Stderr []byte
	Exit           int
}

// LoadCapture reads testdata/<name>.order and its executions.
func LoadCapture(t *testing.T, name string) []Recorded {
	t.Helper()
	read := func(file string) []byte {
		b, err := os.ReadFile(filepath.Join("testdata", file))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	var out []Recorded
	for _, exec := range strings.Fields(string(read(name + ".order"))) {
		code, err := strconv.Atoi(strings.TrimSpace(string(read(name + "." + exec + ".exit"))))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, Recorded{Name: exec, Stdout: read(name + "." + exec + ".stdout"), Stderr: read(name + "." + exec + ".stderr"), Exit: code})
	}
	return out
}

// Replay answers the executions of one task run from a capture, in order,
// and records every spec so tests can check argv, cwd, environment and
// limits. Match (optional) checks each spec against its recorded step;
// Mutate alters a result.
type Replay struct {
	T      *testing.T
	Steps  []Recorded
	Match  func(step Recorded, spec executor.Spec) bool
	Mutate func(step Recorded, r *executor.Result)
	Specs  []executor.Spec
}

// Run implements executor.Runner.
func (r *Replay) Run(_ context.Context, spec executor.Spec) executor.Result {
	r.Specs = append(r.Specs, spec)
	i := len(r.Specs) - 1
	if i >= len(r.Steps) {
		r.T.Errorf("replay: unexpected execution %d %v", i, spec.Args)
		return executor.Result{Status: executor.StatusStartFailed, Cleanup: executor.CleanupNotStarted}
	}
	step := r.Steps[i]
	if r.Match != nil && !r.Match(step, spec) {
		r.T.Errorf("replay: execution %d does not match %s: %v", i, step.Name, spec.Args)
	}
	keys := make([]string, 0, len(spec.Env))
	for k := range spec.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	code := step.Exit
	res := executor.Result{Status: executor.StatusExited, Cleanup: executor.CleanupConfirmed, Containment: executor.ContainmentNone, ExitCode: &code,
		Command: executor.CommandRecord{Executable: spec.Executable, Argv: append([]string{spec.Executable}, spec.Args...), Dir: analysis.RedactedMarker, EnvKeys: keys},
		Stdout:  executor.Stream{Data: append([]byte{}, step.Stdout...), TotalBytes: int64(len(step.Stdout))},
		Stderr:  executor.Stream{Data: append([]byte{}, step.Stderr...), TotalBytes: int64(len(step.Stderr))}}
	if r.Mutate != nil {
		r.Mutate(step, &res)
	}
	return res
}

// FakeTools lays out an operator tool directory without any code that runs
// (replay tests never start a process): node_modules with eslint 10.15.0,
// @eslint/js 10.0.1, typescript-eslint 8.73.0 and typescript 6.0.3, as
// package.json files and empty entry scripts, plus a node toolchain handle.
// It returns the eslint and tsc entry scripts.
func FakeTools(t *testing.T) (eslintJS, tscJS string, node pack.Toolchain) {
	t.Helper()
	root := t.TempDir()
	nm := filepath.Join(root, "node_modules")
	WriteTree(t, nm, map[string]string{
		"eslint/package.json":             "{\"name\":\"eslint\",\"version\":\"10.15.0\",\"main\":\"./lib/api.js\"}\n",
		"eslint/lib/api.js":               "",
		"eslint/bin/eslint.js":            "#!/usr/bin/env node\n",
		"@eslint/js/package.json":         "{\"name\":\"@eslint/js\",\"version\":\"10.0.1\",\"main\":\"./src/index.js\"}\n",
		"@eslint/js/src/index.js":         "",
		"typescript-eslint/package.json":  "{\"name\":\"typescript-eslint\",\"version\":\"8.73.0\",\"main\":\"dist/index.js\"}\n",
		"typescript-eslint/dist/index.js": "",
		"typescript/package.json":         "{\"name\":\"typescript\",\"version\":\"6.0.3\",\"main\":\"./lib/typescript.js\"}\n",
		"typescript/lib/typescript.js":    "",
		"typescript/bin/tsc":              "#!/usr/bin/env node\n",
	})
	bin := filepath.Join(root, "node", "bin")
	WriteTree(t, bin, map[string]string{"node": ""})
	return filepath.Join(nm, "eslint", "bin", "eslint.js"), filepath.Join(nm, "typescript", "bin", "tsc"),
		pack.Toolchain{Name: "node", Executable: filepath.Join(bin, "node"), Dir: bin, Version: "24.18.0"}
}

// EnvAllowlisted reports exactly the script environment allowlist: PATH and
// LANG, plus SystemRoot on Windows.
func EnvAllowlisted(env map[string]string) bool {
	want := 2
	if runtime.GOOS == "windows" {
		want = 3
		if _, ok := env["SystemRoot"]; !ok && os.Getenv("SystemRoot") != "" {
			return false
		}
	}
	_, path := env["PATH"]
	_, lang := env["LANG"]
	return path && lang && len(env) <= want
}

// Rules counts observations per rule id.
func Rules(doc obsdoc.Document) map[string]int {
	out := map[string]int{}
	for _, o := range doc.Observations {
		out[o.RuleID]++
	}
	return out
}
