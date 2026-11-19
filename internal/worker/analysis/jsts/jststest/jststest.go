// Package jststest holds the test fixtures the tsc and ESLint adapters
// share: operator tool paths from CODEYE_TEST_* (real-tool tests skip
// without them; CI has no Node tools), fake tool packages, a
// result-mutating runner, envelope decoding and the capture of real tool
// streams into testdata; snapshots, task runs and tree hashes come from
// packtest. It is imported by tests only.
package jststest

import (
	"bytes"
	"context"
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

	"github.com/jihoon22-lee/CODEYE/internal/nativepath"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
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
	return packtest.ReadEnvelope(t, job, res, "CODEYE_JSTS_DUMP")
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
	packtest.WriteTree(t, nm, map[string]string{
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
	packtest.WriteTree(t, bin, map[string]string{"node": ""})
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
