package analysis

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

type stubHandler struct{ info HandlerInfo }

func (s stubHandler) Info() HandlerInfo                             { return s.info }
func (s stubHandler) Plan(context.Context, Job) ([]TaskSpec, error) { return nil, nil }
func (s stubHandler) Run(context.Context, Job, TaskSpec, executor.Runner) TaskResult {
	return TaskResult{Status: domain.TaskUnsupported, Reason: ReasonAnalysisUnsupported}
}

func TestRegistry(t *testing.T) {
	a := stubHandler{HandlerInfo{"go-vet", "1", "analysis:deep-review"}}
	b := stubHandler{HandlerInfo{"fixture", "1", "analysis:focused"}}
	r, err := NewRegistry(a, b)
	if err != nil {
		t.Fatal(err)
	}
	infos := r.Infos()
	if len(infos) != 2 || infos[0].Capability != "analysis:deep-review" || infos[1].Capability != "analysis:focused" {
		t.Fatalf("infos = %+v", infos)
	}
	if h, ok := r.Lookup("analysis:deep-review"); !ok || h.Info().Name != "go-vet" {
		t.Fatal("lookup failed")
	}
	if _, ok := r.Lookup("analysis:with-tests"); ok {
		t.Fatal("unknown capability found")
	}
	empty, err := NewRegistry()
	if err != nil || len(empty.Infos()) != 0 {
		t.Fatal("empty registry must advertise nothing")
	}
	bad := []struct {
		name string
		hs   []Handler
	}{
		{"nil", []Handler{nil}},
		{"dup capability", []Handler{a, stubHandler{HandlerInfo{"other", "1", "analysis:deep-review"}}}},
		{"dup name", []Handler{a, stubHandler{HandlerInfo{"go-vet", "1", "analysis:focused"}}}},
		{"bad capability", []Handler{stubHandler{HandlerInfo{"x", "1", "deep-review"}}}},
		{"bad name", []Handler{stubHandler{HandlerInfo{"X Y", "1", "analysis:x"}}}},
		{"no version", []Handler{stubHandler{HandlerInfo{"x", "", "analysis:x"}}}},
	}
	for _, c := range bad {
		if _, err := NewRegistry(c.hs...); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: err = %v", c.name, err)
		}
	}
}

func TestWriteExecution(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "exec")
	r := executor.Result{Stdout: executor.Stream{Data: []byte("out")}, Stderr: executor.Stream{Data: []byte("err")}}
	e, err := WriteExecution(dir, "go.vet", r)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{e.StdoutPath: "out", e.StderrPath: "err"} {
		b, err := os.ReadFile(path)
		if err != nil || string(b) != want || filepath.Dir(path) != dir {
			t.Fatalf("%s = %q %v", path, b, err)
		}
		if info, _ := os.Stat(path); info.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
			t.Fatalf("stream file mode %v", info.Mode())
		}
	}
	if _, err := WriteExecution(dir, "go.vet", r); err == nil {
		t.Fatal("overwrite must fail")
	}
	for _, name := range []string{"../x", "A", ""} {
		if _, err := WriteExecution(dir, name, r); !errors.Is(err, ErrInvalid) {
			t.Fatalf("name %q accepted", name)
		}
	}
	if _, err := WriteExecution("rel", "x", r); !errors.Is(err, ErrInvalid) {
		t.Fatal("relative dir accepted")
	}
}

func TestCommandForStripsNodePaths(t *testing.T) {
	c := CommandFor(executor.CommandRecord{Argv: []string{"/opt/go/bin/go", "vet", "-json", "/abs/x", `C:\w\y`, "./..."}, Dir: "[REDACTED]/sub/pkg", EnvKeys: []string{"GOPROXY", "PATH"}})
	if strings.Join(c.Argv, " ") != "go vet -json [REDACTED] [REDACTED] ./..." || c.Dir != "sub/pkg" {
		t.Fatalf("command = %+v", c)
	}
	for in, want := range map[string]string{"[REDACTED]": ".", "/home/u/ws": "[REDACTED]", "[REDACTED]/../x": "[REDACTED]", "": "[REDACTED]", `[REDACTED]\a`: "[REDACTED]"} {
		if got := RelativeDir(in); got != want && !(os.PathSeparator == '\\' && in == `[REDACTED]\a`) {
			t.Fatalf("RelativeDir(%q) = %q want %q", in, got, want)
		}
	}
}

func validManifest() ResultManifest {
	code := 0
	reason := "package_errors"
	return ResultManifest{
		SchemaVersion: ResultManifestSchemaVersion, RunID: "r", AttemptID: "a", LeaseEpoch: 1,
		Snapshot: ManifestSnapshot{ID: "s", ManifestDigest: strings.Repeat("a", 64)},
		Handlers: []ManifestHandler{{Name: "go-vet", Version: "1", Capability: "analysis:deep-review"}},
		Tasks: []ManifestTask{{TaskID: "t", Key: "go-vet", Required: true, Status: domain.TaskFailed, Reason: &reason,
			Executions: []ManifestExecution{{Name: "go.vet", Status: executor.StatusExited, ExitCode: &code, Cleanup: executor.CleanupConfirmed,
				Command: ManifestCommand{Argv: []string{"go", "vet"}, Dir: ".", EnvKeys: []string{"PATH"}}}},
			Artifacts:   []ManifestArtifact{{ArtifactID: "x", Role: RoleObservations, MediaType: MediaObservations, SHA256: strings.Repeat("c", 64), SizeBytes: 3}},
			Limitations: []string{}}},
		CleanupState: "confirmed", Limitations: []string{},
	}
}

func TestResultManifestValidate(t *testing.T) {
	m := validManifest()
	raw, err := m.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if canon, err := domain.CanonicalJSON(raw); err != nil || string(canon) != string(raw) || !strings.Contains(string(raw), `"name":"go.vet"`) {
		t.Fatalf("manifest JSON = %s", raw)
	}
	mutations := map[string]func(*ResultManifest){
		"abs dir":          func(m *ResultManifest) { m.Tasks[0].Executions[0].Command.Dir = "/home/u/ws" },
		"abs argv0":        func(m *ResultManifest) { m.Tasks[0].Executions[0].Command.Argv[0] = "/usr/bin/go" },
		"env value":        func(m *ResultManifest) { m.Tasks[0].Executions[0].Command.EnvKeys = []string{"PATH=/x"} },
		"uncertain hidden": func(m *ResultManifest) { m.Tasks[0].Executions[0].Cleanup = executor.CleanupUncertain },
		"success uncertain": func(m *ResultManifest) {
			m.Tasks[0].Status = domain.TaskSucceeded
			m.Tasks[0].Executions[0].Cleanup = executor.CleanupUncertain
			m.CleanupState = "uncertain"
		},
		"success truncated": func(m *ResultManifest) {
			m.Tasks[0].Status = domain.TaskSucceeded
			m.Tasks[0].Executions[0].StdoutTruncated = true
		},
		"success not exited": func(m *ResultManifest) {
			m.Tasks[0].Status = domain.TaskSucceeded
			m.Tasks[0].Executions[0].Status = executor.StatusTimedOut
		},
		"running task":      func(m *ResultManifest) { m.Tasks[0].Status = domain.TaskRunning },
		"bad digest":        func(m *ResultManifest) { m.Snapshot.ManifestDigest = "x" },
		"dup key":           func(m *ResultManifest) { m.Tasks = append(m.Tasks, m.Tasks[0]) },
		"null limitations":  func(m *ResultManifest) { m.Limitations = nil },
		"bad artifact hash": func(m *ResultManifest) { m.Tasks[0].Artifacts[0].SHA256 = "x" },
	}
	for name, mut := range mutations {
		m := validManifest()
		mut(&m)
		if err := m.Validate(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: accepted", name)
		}
	}
}

// Handlers must start processes only through executor.Runner and must not
// reach the network: no os/exec, net or net/http under analysis/... (tests included).
func TestNoProcessOrNetworkImports(t *testing.T) {
	forbidden := map[string]bool{"os/exec": true, "net": true, "net/http": true, "syscall": true}
	count := 0
	err := filepath.WalkDir(".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") {
			return err
		}
		count++
		f, err := parser.ParseFile(token.NewFileSet(), p, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if forbidden[path] {
				t.Errorf("%s imports %s", p, path)
			}
		}
		return nil
	})
	if err != nil || count < 8 {
		t.Fatalf("walk: %v (files=%d)", err, count)
	}
}
