package deps

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/deps/depdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
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

// jobOf snapshots files into a real local snapshot with a workspace clone.
func jobOf(t *testing.T, files map[string]string) (analysis.Job, string) {
	t.Helper()
	base := t.TempDir()
	src := filepath.Join(base, "source")
	writeTree(t, src, files)
	data, scratch := filepath.Join(base, "data"), filepath.Join(base, "scratch")
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
	return analysis.Job{AllowDependencyFetch: true, RunID: domain.NewID(), AttemptID: domain.NewID(), Epoch: 1, Profile: "deep-review", Snapshot: loaded, Scratch: scratch}, src
}

type innerHandler struct{ planned []analysis.TaskSpec }

func (h innerHandler) Info() analysis.HandlerInfo {
	return analysis.HandlerInfo{Name: "analyzers", Version: "1", Capability: "analysis:deep-review"}
}
func (h innerHandler) Plan(context.Context, analysis.Job) ([]analysis.TaskSpec, error) {
	return append([]analysis.TaskSpec(nil), h.planned...), nil
}
func (h innerHandler) Run(context.Context, analysis.Job, analysis.TaskSpec, executor.Runner) analysis.TaskResult {
	return analysis.TaskResult{Status: domain.TaskSucceeded}
}

type fakeRunner struct {
	specs []executor.Spec
	fn    func(context.Context, executor.Spec) executor.Result
}

func (f *fakeRunner) Run(ctx context.Context, s executor.Spec) executor.Result {
	f.specs = append(f.specs, s)
	return f.fn(ctx, s)
}

func exited(code int, stdout, stderr string) executor.Result {
	return executor.Result{Status: executor.StatusExited, ExitCode: &code, Cleanup: executor.CleanupConfirmed,
		Stdout: executor.Stream{Data: []byte(stdout), TotalBytes: int64(len(stdout))}, Stderr: executor.Stream{Data: []byte(stderr), TotalBytes: int64(len(stderr))}}
}

const goMod = "module example.com/m\n\ngo 1.22\n\nrequire github.com/spf13/pflag v1.0.5\n"
const goSum = "github.com/spf13/pflag v1.0.5 h1:iy+VFUOCP1a+8yFto/drg2CJ5u0yRoB7fZw3DKv/JXA=\ngithub.com/spf13/pflag v1.0.5/go.mod h1:McXfInJRrz4CZXVZOBLb0bTZqETkiAhM9Iw0y3An2Bg=\n"
const modEditJSON = `{"Module":{"Path":"example.com/m"},"Go":"1.22","Require":[{"Path":"github.com/spf13/pflag","Version":"v1.0.5"}],"Replace":null}`
const downloadJSON = "{\n\t\"Path\": \"github.com/spf13/pflag\",\n\t\"Version\": \"v1.0.5\"\n}\n"

// goScript answers go version, go mod edit and go mod download.
func goScript(download func(context.Context, executor.Spec) executor.Result) func(context.Context, executor.Spec) executor.Result {
	return func(ctx context.Context, s executor.Spec) executor.Result {
		switch {
		case slices.Equal(s.Args, []string{"version"}):
			return exited(0, "go version go1.27.4 linux/amd64\n", "")
		case len(s.Args) > 2 && s.Args[0] == "mod" && s.Args[1] == "edit":
			return exited(0, modEditJSON, "")
		case len(s.Args) > 2 && s.Args[0] == "mod" && s.Args[1] == "download":
			return download(ctx, s)
		}
		return exited(2, "", "unexpected")
	}
}

func handler(t *testing.T, cfg Config) *Handler {
	t.Helper()
	if cfg.CacheRoot == "" {
		cfg.CacheRoot = filepath.Join(t.TempDir(), "cache", "deps")
	}
	if cfg.GoExecutable == "" {
		cfg.GoExecutable = filepath.Join(t.TempDir(), "go", "bin", "go")
	}
	inner := innerHandler{planned: []analysis.TaskSpec{{Key: "go-vet", Required: true, Stage: domain.StageStaticAnalysis}}}
	h, err := Wrap(inner, cfg, Fetchers()...)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func readRecord(t *testing.T, job analysis.Job, key string) depdoc.Record {
	t.Helper()
	b, err := os.ReadFile(depdoc.Path(job.Scratch, key))
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	r, err := depdoc.Decode(b, expect(job, key))
	if err != nil {
		t.Fatalf("record does not decode: %v", err)
	}
	return r
}

func TestPlanWithoutFetchIsTheInnerPlan(t *testing.T) {
	job, _ := jobOf(t, map[string]string{"go.mod": goMod, "go.sum": goSum, "main.go": "package main\n"})
	h := handler(t, Config{})
	inner, _ := h.inner.Plan(context.Background(), job)
	job.AllowDependencyFetch = false
	got, err := h.Plan(context.Background(), job)
	if err != nil || !slices.EqualFunc(got, inner, func(a, b analysis.TaskSpec) bool { return a == b }) {
		t.Fatalf("plan without fetch = %+v, %v; want the inner plan", got, err)
	}
	// The operator switch off (no cache root) plans nothing either.
	job.AllowDependencyFetch = true
	off, _ := Wrap(h.inner, Config{}, Fetchers()...)
	if got, _ := off.Plan(context.Background(), job); len(got) != len(inner) {
		t.Fatalf("plan without a cache root = %+v", got)
	}
}

func TestPlanOneTaskPerGoModuleBeforeTheAnalyzers(t *testing.T) {
	job, _ := jobOf(t, map[string]string{"go.mod": goMod, "main.go": "package main\n", "svc/a/go.mod": "module example.com/a\n", "svc/a/a.go": "package a\n"})
	got, err := handler(t, Config{}).Plan(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, s := range got {
		keys = append(keys, s.Key)
		if strings.HasPrefix(s.Key, "deps.") && (s.Required || s.Stage != domain.StageEnvironment || s.Weight != 0) {
			t.Fatalf("dependency task %+v must be optional, environment, unweighted", s)
		}
	}
	if want := []string{"deps.go", "deps.go.m01", "go-vet"}; !slices.Equal(keys, want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
}

func TestGoFetchArgvEnvAndRecord(t *testing.T) {
	job, src := jobOf(t, map[string]string{"go.mod": goMod, "go.sum": goSum, "main.go": "package main\n"})
	operator := t.TempDir()
	writeTree(t, operator, map[string]string{"cache/download/github.com/spf13/pflag/@v/v1.0.5.mod": "module github.com/spf13/pflag\n"})
	h := handler(t, Config{GoModCache: operator})
	r := &fakeRunner{fn: goScript(func(context.Context, executor.Spec) executor.Result { return exited(0, downloadJSON, "") })}
	res := h.Run(context.Background(), job, analysis.TaskSpec{Key: "deps.go"}, r)
	if res.Status != domain.TaskSucceeded || res.Reason != "" || len(res.Artifacts) != 1 || res.Artifacts[0].MediaType != depdoc.MediaType {
		t.Fatalf("result = %+v", res)
	}
	if len(r.specs) != 3 {
		t.Fatalf("executions = %d", len(r.specs))
	}
	dl := r.specs[2]
	modfile := filepath.Join(job.Scratch, "deps", "deps.go", "modfile", "go.mod")
	if want := []string{"mod", "download", "-json", "-modfile=" + modfile}; !slices.Equal(dl.Args, want) {
		t.Fatalf("argv = %v, want %v", dl.Args, want)
	}
	for _, s := range r.specs {
		if s.Dir != job.Snapshot.WorkspaceDir || s.WorkspaceRoot != job.Snapshot.WorkspaceDir || strings.HasPrefix(s.Dir, src) || strings.HasPrefix(s.Dir, job.Snapshot.SnapshotDir) {
			t.Fatalf("cwd %s must be the workspace module dir, never the source or snapshot", s.Dir)
		}
		if !slices.Contains(s.ProtectedRoots, job.Snapshot.SnapshotDir) || !slices.Contains(s.ProtectedRoots, operator) {
			t.Fatalf("protected roots = %v", s.ProtectedRoots)
		}
	}
	env := dl.Env
	wantEnv := map[string]string{
		"GOENV": "off", "GOWORK": "off", "GOTOOLCHAIN": "local", "GOFLAGS": "-mod=readonly -modcacherw", "CGO_ENABLED": "0",
		"GOSUMDB": "sum.golang.org", "GOVCS": "*:off", "GOMODCACHE": filepath.Join(h.cfg.CacheRoot, "go", "mod"),
		"GOPROXY": fileProxyURL(filepath.Join(operator, "cache", "download")) + "," + DefaultGoProxy,
	}
	for k, v := range wantEnv {
		if env[k] != v {
			t.Fatalf("env %s = %q, want %q", k, env[k], v)
		}
	}
	rec := readRecord(t, job, "deps.go")
	if rec.State != depdoc.StatePrepared || rec.Cache.Kind != depdoc.CacheShared || *rec.Cache.Key != GoCacheKey || rec.Lockfile == nil || rec.Lockfile.Path != "go.sum" ||
		*rec.Counts.Declared != 1 || rec.Counts.Prepared != 1 || *rec.Manager.Version != "go1.27.4" || !slices.Contains(rec.Limitations, depdoc.LimSeededFromOperatorCache) {
		t.Fatalf("record = %+v", rec)
	}
	raw, _ := os.ReadFile(depdoc.Path(job.Scratch, "deps.go"))
	if strings.Contains(string(raw), job.Scratch) || strings.Contains(string(raw), h.cfg.CacheRoot) {
		t.Fatalf("record holds an absolute path: %s", raw)
	}
	// The workspace and the user's source are untouched.
	for _, dir := range []string{src, job.Snapshot.WorkspaceDir} {
		if b, _ := os.ReadFile(filepath.Join(dir, "go.sum")); string(b) != goSum {
			t.Fatalf("%s/go.sum changed", dir)
		}
	}
}

func TestGoFetchNothingToFetch(t *testing.T) {
	for name, tc := range map[string]struct {
		files   map[string]string
		modEdit string
		status  domain.TaskStatus
		reason  string
	}{
		"no requires":     {map[string]string{"go.mod": "module example.com/m\n", "m.go": "package m\n"}, `{"Module":{"Path":"example.com/m"}}`, domain.TaskSkippedPolicy, depdoc.ReasonNoDependencies},
		"vendored":        {map[string]string{"go.mod": goMod, "vendor/modules.txt": "# x\n", "m.go": "package m\n"}, modEditJSON, domain.TaskSkippedPolicy, depdoc.ReasonVendoredDependencies},
		"replace outside": {map[string]string{"go.mod": goMod, "m.go": "package m\n"}, `{"Module":{"Path":"example.com/m"},"Require":[{"Path":"a.b/c","Version":"v1.0.0"}],"Replace":[{"Old":{"Path":"a.b/c"},"New":{"Path":"../../outside"}}]}`, domain.TaskSkippedPolicy, ReasonGoReplaceOutside},
	} {
		t.Run(name, func(t *testing.T) {
			job, _ := jobOf(t, tc.files)
			r := &fakeRunner{fn: func(ctx context.Context, s executor.Spec) executor.Result {
				if s.Args[0] == "version" {
					return exited(0, "go version go1.27.4 linux/amd64\n", "")
				}
				if s.Args[1] == "edit" {
					return exited(0, tc.modEdit, "")
				}
				t.Fatalf("unexpected download: %v", s.Args)
				return executor.Result{}
			}}
			res := handler(t, Config{}).Run(context.Background(), job, analysis.TaskSpec{Key: "deps.go"}, r)
			if res.Status != tc.status || res.Reason != tc.reason {
				t.Fatalf("result = %s/%s, want %s/%s", res.Status, res.Reason, tc.status, tc.reason)
			}
			if rec := readRecord(t, job, "deps.go"); rec.State != depdoc.StateSkipped || !domain.NothingToFetch(tc.reason) && tc.reason != ReasonGoReplaceOutside {
				t.Fatalf("record = %+v", rec)
			}
		})
	}
}

func TestGoFetchFailures(t *testing.T) {
	for name, tc := range map[string]struct {
		download func(context.Context, executor.Spec) executor.Result
		status   domain.TaskStatus
		reason   string
		state    string
	}{
		"registry unreachable": {func(context.Context, executor.Spec) executor.Result {
			return exited(1, "", `go: github.com/spf13/pflag@v1.0.5: Get "http://127.0.0.1:9/x": dial tcp 127.0.0.1:9: connect: connection refused`)
		}, domain.TaskFailed, depdoc.ReasonRegistryUnreachable, depdoc.StateFailed},
		"not on proxy": {func(context.Context, executor.Spec) executor.Result {
			return exited(1, "{\"Path\":\"a.b/c\",\"Version\":\"v1.0.0\",\"Error\":\"a.b/c@v1.0.0: reading https://proxy.golang.org/a.b/c/@v/v1.0.0.mod: 404 Not Found\"}\n"+downloadJSON, "")
		}, domain.TaskFailed, ReasonGoModuleNotOnProxy, depdoc.StatePartial},
		"checksum": {func(context.Context, executor.Spec) executor.Result {
			return exited(1, "", "verifying github.com/spf13/pflag@v1.0.5: checksum mismatch\nSECURITY ERROR")
		}, domain.TaskFailed, ReasonGoChecksumMismatch, depdoc.StateFailed},
		"toolchain": {func(context.Context, executor.Spec) executor.Result {
			return exited(1, "", "go: go.mod requires go >= 1.99 (running go 1.27.4; GOTOOLCHAIN=local)")
		}, domain.TaskFailed, depdoc.ReasonToolchainUnavailable, depdoc.StateFailed},
		"timeout": {func(context.Context, executor.Spec) executor.Result {
			return executor.Result{Status: executor.StatusTimedOut, Cleanup: executor.CleanupConfirmed}
		}, domain.TaskTimedOut, depdoc.ReasonTimeout, depdoc.StateFailed},
		"cleanup uncertain": {func(context.Context, executor.Spec) executor.Result {
			return executor.Result{Status: executor.StatusTimedOut, Cleanup: executor.CleanupUncertain}
		}, domain.TaskFailed, depdoc.ReasonCleanupUncertain, depdoc.StateFailed},
		"go.sum incomplete": {func(_ context.Context, s executor.Spec) executor.Result {
			mod := strings.TrimPrefix(s.Args[3], "-modfile=")
			sum := strings.TrimSuffix(mod, ".mod") + ".sum"
			f, _ := os.OpenFile(sum, os.O_APPEND|os.O_WRONLY, 0o600)
			_, _ = f.WriteString("example.org/extra v1.0.0/go.mod h1:x=\n")
			_ = f.Close()
			return exited(0, downloadJSON, "")
		}, domain.TaskFailed, ReasonGoSumIncomplete, depdoc.StatePartial},
	} {
		t.Run(name, func(t *testing.T) {
			job, _ := jobOf(t, map[string]string{"go.mod": goMod, "go.sum": goSum, "main.go": "package main\n"})
			res := handler(t, Config{}).Run(context.Background(), job, analysis.TaskSpec{Key: "deps.go"}, &fakeRunner{fn: goScript(tc.download)})
			if res.Status != tc.status || res.Reason != tc.reason {
				t.Fatalf("result = %s/%s, want %s/%s", res.Status, res.Reason, tc.status, tc.reason)
			}
			if rec := readRecord(t, job, "deps.go"); rec.State != tc.state || rec.TaskStatus != string(tc.status) {
				t.Fatalf("record = %+v", rec)
			}
		})
	}
}

func TestBudgetExhaustedBeforeTheFetch(t *testing.T) {
	job, _ := jobOf(t, map[string]string{"go.mod": goMod, "go.sum": goSum, "main.go": "package main\n"})
	h := handler(t, Config{TotalTimeout: time.Minute})
	if err := os.MkdirAll(filepath.Join(job.Scratch, "deps"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := addBudget(job.Scratch, time.Minute); err != nil {
		t.Fatal(err)
	}
	r := &fakeRunner{fn: func(context.Context, executor.Spec) executor.Result {
		t.Fatal("no process may start")
		return executor.Result{}
	}}
	res := h.Run(context.Background(), job, analysis.TaskSpec{Key: "deps.go"}, r)
	if res.Status != domain.TaskBudgetExhausted || res.Reason != depdoc.ReasonBudgetExhausted {
		t.Fatalf("result = %+v", res)
	}
}

func TestBudgetIsSharedByTheAttemptsTasks(t *testing.T) {
	job, _ := jobOf(t, map[string]string{"go.mod": goMod, "go.sum": goSum, "main.go": "package main\n"})
	h := handler(t, Config{TotalTimeout: time.Hour, ModuleTimeout: time.Hour})
	if err := os.MkdirAll(filepath.Join(job.Scratch, "deps"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := addBudget(job.Scratch, 59*time.Minute); err != nil {
		t.Fatal(err)
	}
	var timeout time.Duration
	r := &fakeRunner{fn: goScript(func(_ context.Context, s executor.Spec) executor.Result {
		timeout = s.Timeout
		return executor.Result{Status: executor.StatusTimedOut, Cleanup: executor.CleanupConfirmed}
	})}
	res := h.Run(context.Background(), job, analysis.TaskSpec{Key: "deps.go"}, r)
	if timeout > time.Minute || res.Status != domain.TaskBudgetExhausted || res.Reason != depdoc.ReasonBudgetExhausted {
		t.Fatalf("timeout %s, result %s/%s: the remaining attempt budget bounds the task", timeout, res.Status, res.Reason)
	}
}

func TestDownloadLimitStopsTheFetch(t *testing.T) {
	pollEvery = 20 * time.Millisecond
	t.Cleanup(func() { pollEvery = 2 * time.Second })
	job, _ := jobOf(t, map[string]string{"go.mod": goMod, "go.sum": goSum, "main.go": "package main\n"})
	h := handler(t, Config{MaxDownloadBytes: 1024})
	r := &fakeRunner{fn: goScript(func(ctx context.Context, s executor.Spec) executor.Result {
		dir := filepath.Join(s.Env["GOMODCACHE"], "cache", "download")
		_ = os.MkdirAll(dir, 0o700)
		_ = os.WriteFile(filepath.Join(dir, "big.zip"), make([]byte, 4096), 0o600)
		<-ctx.Done()
		return executor.Result{Status: executor.StatusCancelled, Cleanup: executor.CleanupConfirmed}
	})}
	res := h.Run(context.Background(), job, analysis.TaskSpec{Key: "deps.go"}, r)
	if res.Status != domain.TaskBudgetExhausted || res.Reason != depdoc.ReasonDownloadLimitExceeded {
		t.Fatalf("result = %s/%s", res.Status, res.Reason)
	}
}

// What an installer unpacks into the process temp directory counts against
// the same allowance as the cache.
func TestDownloadLimitCountsTheTempDirectory(t *testing.T) {
	pollEvery = 20 * time.Millisecond
	t.Cleanup(func() { pollEvery = 2 * time.Second })
	job, _ := jobOf(t, map[string]string{"go.mod": goMod, "go.sum": goSum, "main.go": "package main\n"})
	h := handler(t, Config{MaxDownloadBytes: 1024})
	r := &fakeRunner{fn: goScript(func(ctx context.Context, s executor.Spec) executor.Result {
		_ = os.WriteFile(filepath.Join(s.TempParent, "unpacked.bin"), make([]byte, 4096), 0o600)
		<-ctx.Done()
		return executor.Result{Status: executor.StatusCancelled, Cleanup: executor.CleanupConfirmed}
	})}
	res := h.Run(context.Background(), job, analysis.TaskSpec{Key: "deps.go"}, r)
	if res.Status != domain.TaskBudgetExhausted || res.Reason != depdoc.ReasonDownloadLimitExceeded {
		t.Fatalf("result = %s/%s", res.Status, res.Reason)
	}
}

func TestAttemptCancelDuringFetch(t *testing.T) {
	job, _ := jobOf(t, map[string]string{"go.mod": goMod, "go.sum": goSum, "main.go": "package main\n"})
	ctx, cancel := context.WithCancel(context.Background())
	r := &fakeRunner{fn: goScript(func(c context.Context, s executor.Spec) executor.Result {
		cancel()
		<-c.Done()
		return executor.Result{Status: executor.StatusCancelled, Cleanup: executor.CleanupConfirmed}
	})}
	res := handler(t, Config{}).Run(ctx, job, analysis.TaskSpec{Key: "deps.go"}, r)
	if res.Status != domain.TaskCancelled || res.Reason != depdoc.ReasonCancelled {
		t.Fatalf("result = %+v", res)
	}
	if rec := readRecord(t, job, "deps.go"); rec.TaskStatus != string(domain.TaskCancelled) {
		t.Fatalf("record = %+v", rec)
	}
}

func TestCacheQuotaRefusesTheFetch(t *testing.T) {
	job, _ := jobOf(t, map[string]string{"go.mod": goMod, "go.sum": goSum, "main.go": "package main\n"})
	root := filepath.Join(t.TempDir(), "deps")
	writeTree(t, root, map[string]string{"go/mod/cache/download/x.zip": strings.Repeat("x", 2048)})
	h := handler(t, Config{CacheRoot: root, CacheQuotaBytes: 1024})
	r := &fakeRunner{fn: func(context.Context, executor.Spec) executor.Result {
		t.Fatal("no process may start")
		return executor.Result{}
	}}
	res := h.Run(context.Background(), job, analysis.TaskSpec{Key: "deps.go"}, r)
	if res.Status != domain.TaskBudgetExhausted || res.Reason != depdoc.ReasonCacheQuotaExceeded {
		t.Fatalf("result = %+v", res)
	}
}

func TestNoGoConfigured(t *testing.T) {
	job, _ := jobOf(t, map[string]string{"go.mod": goMod, "main.go": "package main\n"})
	inner := innerHandler{}
	h, _ := Wrap(inner, Config{CacheRoot: filepath.Join(t.TempDir(), "deps")}, Fetchers()...)
	res := h.Run(context.Background(), job, analysis.TaskSpec{Key: "deps.go"}, &fakeRunner{})
	if res.Status != domain.TaskUnsupported || res.Reason != depdoc.ReasonPackageManagerNotConfigured {
		t.Fatalf("result = %+v", res)
	}
}

func TestInnerMayNotPlanDependencyKeys(t *testing.T) {
	h, _ := Wrap(innerHandler{planned: []analysis.TaskSpec{{Key: "deps.go"}}}, Config{}, Fetchers()...)
	if _, err := h.Plan(context.Background(), analysis.Job{}); err == nil {
		t.Fatal("an inner deps.* key must be refused")
	}
}

func TestClassifyGo(t *testing.T) {
	for text, want := range map[string]string{
		"dial tcp: lookup proxy.golang.org: no such host":        depdoc.ReasonRegistryUnreachable,
		"reading https://proxy.golang.org/x/@v/v1.mod: 410 Gone": ReasonGoModuleNotOnProxy,
		"verifying x@v1: checksum mismatch":                      ReasonGoChecksumMismatch,
		"missing go.sum entry for module providing package x":    ReasonGoSumIncomplete,
		"go: go.mod requires go >= 1.99 (running go 1.27.4)":     depdoc.ReasonToolchainUnavailable,
		"something else": depdoc.ReasonFetchFailed,
	} {
		if got := classifyGo(text); got != want {
			t.Errorf("classifyGo(%q) = %s, want %s", text, got, want)
		}
	}
}

func TestFileProxyURL(t *testing.T) {
	if got := fileProxyURL(filepath.FromSlash("/home/u/go/pkg/mod/cache/download")); !strings.HasPrefix(got, "file:///") || !strings.HasSuffix(got, "/cache/download") {
		t.Fatalf("file proxy URL = %s", got)
	}
}

func TestRecordValidation(t *testing.T) {
	key := "deps.go"
	want := depdoc.Expect{Snapshot: "s", RunID: "r", AttemptID: "a", TaskKey: key}
	good := func() depdoc.Record {
		k := GoCacheKey
		return depdoc.Record{SchemaVersion: depdoc.SchemaVersion, Kind: depdoc.Kind, Snapshot: "s", RunID: "r", AttemptID: "a", TaskKey: key,
			Module: depdoc.Module{ID: "go:.", Ecosystem: "go", Path: "."}, Manager: depdoc.Manager{Name: "go"}, Pinned: true, State: depdoc.StatePrepared,
			TaskStatus: "succeeded", Cache: depdoc.Cache{Kind: depdoc.CacheShared, Key: &k}, DroppedInputs: []depdoc.Dropped{}, FailedItems: []depdoc.Failed{}, Limitations: []string{}}
	}
	if _, err := depdoc.Marshal(good(), want); err != nil {
		t.Fatalf("good record: %v", err)
	}
	reason := "registry_unreachable"
	for name, edit := range map[string]func(*depdoc.Record){
		"succeeded but failed state":  func(r *depdoc.Record) { r.State = depdoc.StateFailed },
		"failed without reason":       func(r *depdoc.Record) { r.TaskStatus = "failed"; r.State = depdoc.StateFailed },
		"prepared but failed task":    func(r *depdoc.Record) { r.TaskStatus = "failed"; r.Reason = &reason },
		"other attempt":               func(r *depdoc.Record) { r.AttemptID = "b" },
		"module of another ecosystem": func(r *depdoc.Record) { r.Module = depdoc.Module{ID: "rust:.", Ecosystem: "rust", Path: "."} },
		"unsorted limitations":        func(r *depdoc.Record) { r.Limitations = []string{"b", "a"} },
		"absolute cache key":          func(r *depdoc.Record) { k := "/abs"; r.Cache.Key = &k },
		"skipped_policy not skipped": func(r *depdoc.Record) {
			r.TaskStatus, r.Reason, r.State = "skipped_policy", &reason, depdoc.StateFailed
		},
	} {
		r := good()
		edit(&r)
		if _, err := depdoc.Marshal(r, want); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	b, _ := json.Marshal(good())
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	m["extra"] = true
	b, _ = json.Marshal(m)
	if _, err := depdoc.Decode(b, want); err == nil {
		t.Error("unknown field accepted")
	}
}

// examples/dependencies.json is a record the decoder accepts, in the
// canonical form Marshal writes.
func TestRecordExampleDecodes(t *testing.T) {
	b, err := os.ReadFile("../../../examples/dependencies.json")
	if err != nil {
		t.Fatal(err)
	}
	var head struct {
		Snapshot  string `json:"snapshot_manifest_digest"`
		RunID     string `json:"run_id"`
		AttemptID string `json:"attempt_id"`
		TaskKey   string `json:"task_key"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		t.Fatal(err)
	}
	want := depdoc.Expect{Snapshot: head.Snapshot, RunID: head.RunID, AttemptID: head.AttemptID, TaskKey: head.TaskKey}
	r, err := depdoc.Decode(b, want)
	if err != nil {
		t.Fatalf("example: %v", err)
	}
	canon, err := depdoc.Marshal(r, want)
	if err != nil || strings.TrimSpace(string(b)) != string(canon) {
		t.Fatalf("example is not the canonical record: %v\n%s", err, canon)
	}
}
