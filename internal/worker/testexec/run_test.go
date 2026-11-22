package testexec

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

func TestGoTestsRunOnlyInScopedPackages(t *testing.T) {
	goName := "go"
	if runtime.GOOS == "windows" {
		goName += ".exe"
	}
	h := New(nil, Config{Go: filepath.Join(runtime.GOROOT(), "bin", goName)})
	scope := domain.Scope{Kind: "paths", Paths: []string{"picked"}}
	fx := packtest.NewFixture(t, "", map[string]string{
		"go.mod": "module example.test/scoped\n\ngo 1.22\n",
		"picked/picked_test.go": `package picked
import ("io"; "net/http"; "net/http/httptest"; "testing")
func TestSelected(t *testing.T) {
  server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("local")) }))
  defer server.Close()
  response, err := http.Get(server.URL)
  if err != nil { t.Fatal(err) }
  defer response.Body.Close()
  body, err := io.ReadAll(response.Body)
  if err != nil || string(body) != "local" { t.Fatalf("body %q, err %v", body, err) }
}`,
		"outside/outside_test.go": "package outside\nimport \"testing\"\nfunc TestOutside(t *testing.T) { t.Fatal(\"outside the scope\") }\n",
	}, scope)
	job := fx.Job
	job.RunID, job.AttemptID = domain.NewID(), domain.NewID()
	job.Profile = "with-tests"
	p, err := plans(job)
	if err != nil || len(p) != 1 || p[0].spec.Key != goKey || p[0].spec.Stage != domain.StageTest {
		t.Fatalf("plan %+v, %v", p, err)
	}
	ctx := context.Background()
	before := packtest.TreeHash(t, job.Snapshot.SnapshotDir)
	blocked := h.Run(ctx, job, p[0].spec, &executor.Executor{})
	if blocked.Reason != "repository_code_not_allowed" || len(blocked.Executions) != 0 {
		t.Fatalf("without grant: %+v", blocked)
	}
	job.AllowRepositoryCode = true
	got := h.Run(ctx, job, p[0].spec, &executor.Executor{})
	if got.Status == domain.TaskUnsupported && got.Reason == isolationUnavailable {
		assertNoTestRun(t, got)
		t.Skip("isolated loopback namespace unavailable")
	}
	if got.Status != domain.TaskSucceeded || len(got.Executions) != 3 || got.Executions[2].Name != runName {
		t.Fatalf("test execution: %+v", got)
	}
	result := got.Executions[2].Result
	if !strings.Contains(string(result.Stdout.Data), "TestSelected") || strings.Contains(string(result.Stdout.Data), "TestOutside") || strings.Contains(strings.Join(result.Command.Argv, " "), "./...") {
		t.Fatalf("outside test package was considered: %v\n%s", result.Command.Argv, result.Stdout.Data)
	}
	if before != packtest.TreeHash(t, job.Snapshot.SnapshotDir) {
		t.Fatal("tests modified the acquired snapshot")
	}
}

func TestNativeNpmSuites(t *testing.T) {
	node, npm := os.Getenv("CODEYE_TEST_NODE"), os.Getenv("CODEYE_TEST_NPM")
	if node == "" || npm == "" {
		t.Skip("set CODEYE_TEST_NODE and CODEYE_TEST_NPM to the node binary and the npm entry script")
	}
	h := New(nil, Config{Node: node, Npm: npm})
	parentNetwork, _ := os.Readlink("/proc/self/ns/net")
	networkJSON, _ := json.Marshal(parentNetwork)
	fx := packtest.NewFixture(t, "", map[string]string{
		"package.json": `{"name":"test-fixture","scripts":{"test:unit":"node --test --test-reporter=tap unit.cjs","test:module":"node --test --test-reporter=tap module.cjs","test:e2e":"node --test --test-reporter=tap e2e.cjs"}}`,
		"unit.cjs":     `require('node:test').test('wrong value', () => require('node:assert/strict').equal(1, 2))`,
		"module.cjs":   `require('node:test').test('module value', () => require('node:assert/strict').equal(require('./value.cjs'), 2))`,
		"value.cjs":    `module.exports=2`,
		"e2e.cjs": `
const assert = require('node:assert/strict');
const fs = require('node:fs');
const http = require('node:http');
require('node:test').test('private loopback', async () => {
assert.notEqual(fs.readlinkSync('/proc/self/ns/net'), ` + string(networkJSON) + `);
const server = http.createServer((req, res) => res.end('ok'));
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  try {
    const response = await fetch('http://127.0.0.1:' + server.address().port);
    assert.equal(await response.text(), 'ok');
    console.log('private loopback response checked');
  } finally { server.close(); }
});`,
	}, domain.Scope{})
	job := fx.Job
	job.RunID, job.AttemptID = domain.NewID(), domain.NewID()
	job.AllowRepositoryCode = true
	p, err := plans(job)
	if err != nil || len(p) != 3 {
		t.Fatalf("plan %+v, %v", p, err)
	}
	scoped := job
	scoped.Scope = domain.Scope{Kind: "paths", Paths: []string{"module.cjs"}}
	s := h.Run(context.Background(), scoped, p[1].spec, &executor.Executor{})
	if s.Status != domain.TaskUnsupported || s.Reason != "test_scope_unsupported" || len(s.Executions) > 0 {
		t.Fatalf("file-scoped npm scripts were permitted: %+v", s)
	}
	for _, tc := range []struct {
		key, reason string
		status      domain.TaskStatus
	}{
		{jsUnit, "test_command_failed", domain.TaskFailed},
		{jsModule, "", domain.TaskSucceeded},
		{jsE2E, "", domain.TaskSucceeded},
	} {
		t.Run(tc.key, func(t *testing.T) {
			at := slices.IndexFunc(p, func(p testPlan) bool { return p.spec.Key == tc.key })
			if at < 0 {
				t.Fatalf("not planned: %s", tc.key)
			}
			got := h.Run(context.Background(), job, p[at].spec, &executor.Executor{})
			if got.Status == domain.TaskUnsupported && got.Reason == isolationUnavailable {
				assertNoTestRun(t, got)
				t.Skip("isolated loopback namespace unavailable")
			}
			if got.Status != tc.status || got.Reason != tc.reason || len(got.Executions) == 0 {
				t.Fatalf("test execution: %+v", got)
			}
			last := got.Executions[len(got.Executions)-1]
			if last.Name != runName || tc.key == jsE2E && !strings.Contains(string(last.Result.Stdout.Data), "private loopback response checked") {
				t.Fatalf("test run evidence missing: %+v", last.Result)
			}
		})
	}
}

func TestNativePythonAndRustSuites(t *testing.T) {
	for _, tc := range []struct {
		key, env, success string
		files             map[string]string
	}{
		{pythonKey, "CODEYE_TEST_PYTHON", "Ran 1 test", map[string]string{
			"pyproject.toml":      "[project]\nname = 'example'\nversion = '1.0'\n",
			"tests/test_local.py": "import unittest\n\nclass TestLocal(unittest.TestCase):\n    def test_value(self):\n        self.assertEqual(sum([1, 2]), 3)\n",
		}},
		{rustKey, "CODEYE_TEST_CARGO", "1 passed", map[string]string{
			"Cargo.toml": "[package]\nname = 'example'\nversion = '1.0.0'\nedition = '2021'\n",
			"src/lib.rs": "#[test]\nfn test_value() { assert_eq!(1 + 2, 3); }\n",
		}},
	} {
		t.Run(tc.key, func(t *testing.T) {
			tool := os.Getenv(tc.env)
			if tool == "" {
				t.Skipf("set %s to the operator-installed runtime", tc.env)
			}
			cfg := Config{ToolPath: os.Getenv("CODEYE_TEST_TOOLPATH")}
			if tc.key == pythonKey {
				cfg.Python = tool
			} else {
				cfg.Cargo = tool
			}
			fx := packtest.NewFixture(t, "", tc.files, domain.Scope{})
			job := fx.Job
			job.RunID, job.AttemptID = domain.NewID(), domain.NewID()
			job.AllowRepositoryCode = true
			got := New(nil, cfg).Run(context.Background(), job, analysis.TaskSpec{Key: tc.key}, &executor.Executor{})
			if got.Status == domain.TaskUnsupported && got.Reason == isolationUnavailable {
				assertNoTestRun(t, got)
				t.Skip("isolated loopback namespace unavailable")
			}
			if got.Status != domain.TaskSucceeded || len(got.Executions) != 2 {
				t.Fatalf("test execution: %+v", got)
			}
			r := got.Executions[1].Result
			if !strings.Contains(string(r.Stdout.Data)+string(r.Stderr.Data), tc.success) {
				t.Fatalf("successful test output missing: %s\n%s", r.Stdout.Data, r.Stderr.Data)
			}
		})
	}
}

func assertNoTestRun(t *testing.T, got analysis.TaskResult) {
	t.Helper()
	for _, ex := range got.Executions {
		if ex.Name == runName {
			t.Fatal("suite ran after isolation failed")
		}
	}
}

type refuseIsolation struct{ calls int }

func (r *refuseIsolation) Run(_ context.Context, spec executor.Spec) executor.Result {
	r.calls++
	code := 1
	return executor.Result{Status: executor.StatusExited, Cleanup: executor.CleanupConfirmed, ExitCode: &code}
}

func TestIsolationFailurePreventsRepositoryCommands(t *testing.T) {
	name := "go"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	h := New(nil, Config{Go: filepath.Join(runtime.GOROOT(), "bin", name)})
	fx := packtest.NewFixture(t, "", map[string]string{
		"go.mod":        "module example.test/isolated\n\ngo 1.22\n",
		"local_test.go": "package x\nimport \"testing\"\nfunc TestLocal(t *testing.T) {}\n",
	}, domain.Scope{})
	job := fx.Job
	job.AllowRepositoryCode = true
	r := &refuseIsolation{}
	got := h.Run(context.Background(), job, analysis.TaskSpec{Key: goKey}, r)
	if got.Status != domain.TaskUnsupported || got.Reason != isolationUnavailable || r.calls > 1 {
		t.Fatalf("incomplete isolation allowed repository command: %+v, %d calls", got, r.calls)
	}
	assertNoTestRun(t, got)
}

func TestIsolatedCancellationAndTimeoutCleanDetachedChild(t *testing.T) {
	h := New(nil, Config{})
	dirs := h.toolPath()
	shell, setsid, sleep := findTool(dirs, "sh"), findTool(dirs, "setsid"), findTool(dirs, "sleep")
	if shell == "" || setsid == "" || sleep == "" {
		t.Skip("native detached-child fixture tools unavailable")
	}
	for _, state := range []executor.Status{executor.StatusCancelled, executor.StatusTimedOut} {
		t.Run(string(state), func(t *testing.T) {
			work := t.TempDir()
			ready, leaked := filepath.Join(work, "child-ready"), filepath.Join(work, "child-leaked")
			launch := `"$1" "$2" -c ': > "$1"; "$2" 1.3; : > "$3"' child "$4" "$3" "$5" &
exec "$3" 60`
			spec := executor.Spec{Executable: shell, WorkspaceRoot: work, Dir: work, TempParent: work,
				Env: nativeEnv(joinPath(nil, dirs)), Args: []string{"-c", launch, "fixture", setsid, shell, sleep, ready, leaked},
				Timeout: 500 * time.Millisecond, Grace: 50 * time.Millisecond}
			probe, isolated, ok := h.isolate(spec, filepath.Join(work, "started"))
			runner := &executor.Executor{}
			if !ok {
				t.Skip("namespace tools unavailable")
			}
			if res := runner.Run(context.Background(), probe); res.ExitCode == nil || *res.ExitCode != 0 {
				t.Skipf("namespace unavailable: %s", res.Stderr.Data)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			defer cancel()
			if state == executor.StatusCancelled {
				isolated.Timeout = 5 * time.Second
				go func() {
					ticker := time.NewTicker(20 * time.Millisecond)
					defer ticker.Stop()
					for {
						select {
						case <-ctx.Done():
							return
						case <-ticker.C:
							if _, err := os.Stat(ready); err == nil {
								cancel()
								return
							}
						}
					}
				}()
			}
			result := runner.Run(ctx, isolated)
			if result.Status != state || result.Cleanup != executor.CleanupConfirmed {
				t.Fatalf("namespace cleanup: %+v", result)
			}
			if _, err := os.Stat(ready); err != nil {
				t.Fatalf("detached child did not reach the fixture checkpoint: %v", err)
			}
			time.Sleep(1400 * time.Millisecond)
			if _, err := os.Stat(leaked); !os.IsNotExist(err) {
				t.Fatal("detached test child kept running after cleanup")
			}
		})
	}
}

func TestMissingTestsAndZeroRunResults(t *testing.T) {
	fx := packtest.NewFixture(t, "", map[string]string{"README.md": "# Example\n"}, domain.Scope{})
	p, err := plans(fx.Job)
	if err != nil || len(p) != 1 || p[0].spec.Key != noEntries {
		t.Fatalf("missing test plan %+v %v", p, err)
	}
	result := New(nil, Config{}).Run(context.Background(), fx.Job, analysis.TaskSpec{Key: noEntries}, &executor.Executor{})
	if result.Reason != "test_entries_unavailable" || len(result.Executions) != 0 {
		t.Fatalf("missing tests %+v", result)
	}
	if goResults([]byte(`{"Action":"pass","Package":"example.test"}`)).counts.Passed != 0 ||
		unittestResults([]byte("Ran 2 tests in 0.0s\n\nOK (skipped=2)\n")).counts.Passed != 0 ||
		cargoResults([]byte("test result: ok. 0 passed; 0 failed; 2 ignored; 0 filtered out\n")).counts.Passed != 0 {
		t.Fatal("zero passed tests treated as success")
	}
}
