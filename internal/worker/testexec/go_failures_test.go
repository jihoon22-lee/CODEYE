package testexec

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/testexec/faildoc"
)

func TestGoFailureReceiptFromScopedWorkerExecution(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the with-tests isolation launcher is Linux-only")
	}
	tools := t.TempDir()
	for _, name := range []string{"unshare", "ip", "sh"} {
		if err := os.WriteFile(filepath.Join(tools, name), []byte("fixture"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	h := New(nil, Config{Go: filepath.Join(runtime.GOROOT(), "bin", "go"), ToolPath: tools})
	const module, pkg = "example.test/selected", "example.test/selected/picked"
	const declaration = "package picked_test\nimport std \"testing\"\n//line fake.go:999\nfunc TestSelected(t *std.T) {}\n"
	const logs = `{"Action":"output","Package":"` + pkg + `","Test":"TestSelected/private-subtest","Output":"unpublished-test-message"}` + "\n"
	const alternate = "package picked\nimport \"testing\"\nfunc TestSelected(t *testing.T) {}\n"
	fail := goFailureOutput(pkg, "TestSelected")
	dot := "package picked_test\nimport . \"testing\"\n//line fake.go:999\nfunc TestSelected(t *T) {}\n"
	missing := `{"Action":"build-output","ImportPath":"example.test/selected/missing","Output":"module lookup disabled by GOPROXY=off"}` + "\n"
	compile := `{"Action":"build-fail","ImportPath":"` + pkg + `"}` + "\n" + fmt.Sprintf("{\"Action\":\"fail\",\"Package\":%q}\n", pkg)
	for _, tc := range []struct {
		name, stdout, primary, additionalName, additionalBody, reason string
		passed, truncated, unfinished, drift                          bool
		want                                                          bool
	}{
		{name: "named failure with unselected and subtest events", stdout: logs + fail + goFailureOutput("example.test/selected/outside", "TestSelected") + goFailureOutput("example.test/selected-other/picked", "TestSelected"), want: true},
		{name: "compile failure", stdout: compile},
		{name: "successful test", stdout: strings.ReplaceAll(fail, `"fail"`, `"pass"`), passed: true},
		{name: "truncated stream", stdout: fail, truncated: true},
		{name: "unconfirmed cleanup", stdout: fail, unfinished: true},
		{name: "changed test source", stdout: fail, drift: true},
		{name: "platform duplicate", stdout: fail, additionalName: "picked/alternate_windows_test.go", additionalBody: alternate},
		{name: "conditional duplicate", stdout: fail, additionalName: "picked/alternate_test.go", additionalBody: "//go:build other\n\n" + alternate},
		{name: "internal external duplicate", stdout: fail, additionalName: "picked/alternate_test.go", additionalBody: alternate},
		{name: "method with test name", stdout: fail, additionalName: "picked/alternate_test.go", additionalBody: "package picked_test\nimport \"testing\"\ntype only struct{}\nfunc (only) TestSelected(t *testing.T) {}\n", want: true},
		{name: "non-testing parameter", stdout: goFailureOutput(pkg, "TestDecoy"), additionalName: "picked/alternate_test.go", additionalBody: "package picked_test\ntype T struct{}\nfunc TestDecoy(t *T) {}\n"},
		{name: "only a tagged declaration", stdout: goFailureOutput(pkg, "TestTagged"), additionalName: "picked/tagged_test.go", additionalBody: "//go:build hosttag\n\npackage picked\nimport \"testing\"\nfunc TestTagged(t *testing.T) {}\n"},
		{name: "dot testing import", stdout: fail, primary: dot, want: true},
		{name: "another dot import", stdout: fail, primary: strings.Replace(dot, `import . "testing"`, `import (. "testing"; . "example.test/other")`, 1)},
		{name: "local T in the declaration file", stdout: fail, primary: dot + "type T struct{}\n"},
		{name: "local T in the same test package", stdout: fail, primary: dot, additionalName: "picked/shadow_test.go", additionalBody: "package picked_test\ntype T struct{}\n"},
		{name: "runtime failure beside offline build", stdout: fail + missing, reason: dependenciesUnavailable, want: true},
		{name: "offline build only", stdout: missing + compile, reason: dependenciesUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			primary := tc.primary
			if primary == "" {
				primary = declaration
			}
			files := map[string]string{
				"go.mod": "module " + module + "\n\ngo 1.22\n", "picked/picked_test.go": primary,
				"outside/outside_test.go": alternate,
			}
			if tc.additionalName != "" {
				files[tc.additionalName] = tc.additionalBody
			}
			fx := packtest.NewFixture(t, "", files, domain.Scope{Kind: "paths", Paths: []string{"picked"}})
			job := fx.Job
			job.RunID, job.AttemptID, job.AllowRepositoryCode = domain.NewID(), domain.NewID(), true
			file := filepath.Join(job.Snapshot.WorkspaceDir, "picked", "picked_test.go")
			if tc.drift {
				if err := os.WriteFile(file, []byte(declaration+"// changed after snapshot\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			result := h.Run(t.Context(), job, analysis.TaskSpec{Key: goKey}, fakeGoTestRunner(func(_ context.Context, _ executor.Spec) executor.Result {
				calls++
				code := 0
				r := executor.Result{Status: executor.StatusExited, Cleanup: executor.CleanupConfirmed, ExitCode: &code}
				switch calls {
				case 1:
					return r
				case 2:
					r.Stdout.Data = fmt.Appendf(nil, `{"Module":{"Path":%q}}`, module)
				case 3:
					marker := filepath.Join(job.Scratch, "tests", goKey, "started")
					if err := os.WriteFile(marker, nil, 0o600); err != nil {
						t.Fatal(err)
					}
					if !tc.passed {
						code = 1
					}
					r.Stdout = executor.Stream{Data: []byte(tc.stdout), Truncated: tc.truncated}
					r.Stderr.Data = []byte("unpublished-stderr")
					if tc.unfinished {
						r.Cleanup = executor.CleanupUncertain
					}
				default:
					t.Fatalf("unexpected extra repository command: %d", calls)
				}
				return r
			}))
			wantStatus := domain.TaskFailed
			if tc.passed {
				wantStatus = domain.TaskSucceeded
			}
			var failures []analysis.ArtifactFile
			for _, a := range result.Artifacts {
				if a.MediaType == faildoc.MediaType {
					failures = append(failures, a)
				}
			}
			if result.Status != wantStatus || calls != 3 || (len(failures) == 1) != tc.want {
				t.Fatalf("status %s, calls %d, artifacts %+v", result.Status, calls, result.Artifacts)
			}
			if tc.reason != "" && result.Reason != tc.reason {
				t.Fatalf("lost task reason: got %q, want %q", result.Reason, tc.reason)
			}
			if !tc.want {
				return
			}
			art := failures[0]
			body, err := os.ReadFile(art.Path)
			if err != nil || art.Role != "other" || art.MediaType != faildoc.MediaType || bytes.Contains(body, []byte("unpublished")) || bytes.Contains(body, []byte("private-subtest")) {
				t.Fatalf("unsafe or missing artifact: %+v, error %v", art, err)
			}
			sha := domain.Digest([]byte(primary))
			doc, err := faildoc.Decode(body, faildoc.Expect{RunID: job.RunID, AttemptID: job.AttemptID,
				Snapshot: job.Snapshot.Digest, TaskKey: goKey, ModuleDir: ".", StdoutSHA256: domain.Digest([]byte(tc.stdout)),
				Lookup: func(p string) (string, bool) { return sha, p == "picked/picked_test.go" }})
			if err != nil || faildoc.ValidateEvents(doc, []byte(tc.stdout)) != nil || len(doc.Failures) != 1 ||
				doc.Failures[0].Line != 4 || doc.Failures[0].Test != "TestSelected" || doc.Failures[0].Package != pkg {
				t.Fatalf("wrong declaration/event binding: %+v, %v", doc, err)
			}
		})
	}
}

type fakeGoTestRunner func(context.Context, executor.Spec) executor.Result

func (f fakeGoTestRunner) Run(ctx context.Context, spec executor.Spec) executor.Result {
	return f(ctx, spec)
}

func goFailureOutput(pkg, name string) string {
	return fmt.Sprintf("{\"Action\":\"run\",\"Package\":%[1]q,\"Test\":%[2]q}\n{\"Action\":\"fail\",\"Package\":%[1]q,\"Test\":%[2]q}\n{\"Action\":\"fail\",\"Package\":%[1]q}\n", pkg, name)
}
