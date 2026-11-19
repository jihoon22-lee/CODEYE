package deps

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/python"
	"github.com/jihoon22-lee/CODEYE/internal/worker/deps/depdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// pyScript answers the interpreter, uv and pip probes, the pyproject reader
// (facts as JSON) and the install, which install handles.
type pyScript struct {
	facts   string
	install func(n int, s executor.Spec) executor.Result
	export  func(s executor.Spec) executor.Result
	noPip   bool
	calls   int
}

func (p *pyScript) run(_ context.Context, s executor.Spec) executor.Result {
	a := s.Args
	switch {
	case len(a) == 4 && a[0] == "-I" && a[1] == "-S" && a[3] == pyVersionScript:
		return exited(0, "3.14.4\n", "")
	case len(a) == 4 && a[0] == "-I" && a[1] == "-S" && a[3] == pyprojectScript:
		return exited(0, p.facts, "")
	case slices.Equal(a, []string{"--version"}):
		return exited(0, "uv 0.12.5 (x86_64-unknown-linux-gnu)\n", "")
	case slices.Equal(a, []string{"-I", "-m", "pip", "--version"}):
		if p.noPip {
			return exited(1, "", "No module named pip\n")
		}
		return exited(0, "pip 25.1.1 from /usr/lib/python3/dist-packages/pip (python 3.14)\n", "")
	case len(a) > 0 && a[0] == "export":
		return p.export(s)
	case slices.Contains(a, "install"):
		p.calls++
		return p.install(p.calls, s)
	}
	return exited(2, "", "unexpected "+strings.Join(a, " "))
}

// installInto writes a package, a .pth and a sitecustomize into --target.
func installInto(s executor.Spec) executor.Result {
	i := slices.Index(s.Args, "--target")
	site := s.Args[i+1]
	for name, body := range map[string]string{"idna/__init__.py": "", "idna/py.typed": "", "evil.pth": "import os\n", "sitecustomize.py": "raise SystemExit\n"} {
		p := filepath.Join(site, filepath.FromSlash(name))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte(body), 0o644)
	}
	return exited(0, "", " + idna==3.10\n")
}

func pyHandler(t *testing.T, cfg Config) *Handler {
	t.Helper()
	if cfg.CacheRoot == "" {
		cfg.CacheRoot = filepath.Join(t.TempDir(), "cache", "deps")
	}
	if cfg.Python == "" {
		cfg.Python = filepath.Join(t.TempDir(), "bin", "python3")
	}
	cfg.PythonConsumers = true
	inner := innerHandler{planned: []analysis.TaskSpec{{Key: "mypy", Stage: domain.StageStaticAnalysis}}}
	h, err := Wrap(inner, cfg, PythonFetcher{})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func factsJSON(reqs ...string) string {
	b, _ := json.Marshal(map[string]any{"project": true, "dynamic": []string{}, "requirements": reqs, "invalid": 0, "poetry": false})
	return string(b)
}

// One task per module the type checkers plan, with their key suffix; a
// directory with Python sources but nothing analyzable plans none.
func TestPythonPlanFollowsTheTypeCheckerModules(t *testing.T) {
	job, _ := jobOf(t, map[string]string{"pyproject.toml": "[project]\nname='a'\n", "a.py": "x = 1\n", "svc/requirements.txt": "idna\n", "svc/b.py": "y = 2\n"})
	got, err := pyHandler(t, Config{}).Plan(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, s := range got {
		keys = append(keys, s.Key)
	}
	if want := []string{"deps.python", "deps.python.m01", "mypy"}; !slices.Equal(keys, want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
	mods, _ := python.DependencyModules(job)
	if len(mods) != 2 || mods[1].Dir != "svc" || mods[1].Suffix != ".m01" {
		t.Fatalf("modules %+v", mods)
	}
	only, _ := jobOf(t, map[string]string{"venv/lib/x.py": "x = 1\n"})
	if mods, _ := python.DependencyModules(only); len(mods) != 0 {
		t.Fatalf("placeholder module planned: %+v", mods)
	}
}

// The uv install: every process starts in an empty scratch directory, the
// requirements file holds only allowed lines, the tree is the attempt's
// and the startup hooks the packages brought are removed.
func TestPythonFetchWithUVArgvRequirementsAndRecord(t *testing.T) {
	files := map[string]string{
		"pyproject.toml":        "[project]\nname='a'\n",
		"requirements.txt":      "-r requirements/base.txt\n-e .[cli]\n--index-url https://mirror.example/simple\ngit+https://example.com/x.git#egg=x\n./local/pkg\nrich>=10,<15  # comment\n",
		"requirements/base.txt": "idna==3.10 --hash=sha256:" + strings.Repeat("a", 64) + "\n",
		"a.py":                  "import idna\n",
	}
	job, src := jobOf(t, files)
	uv := filepath.Join(t.TempDir(), "bin", "uv")
	h := pyHandler(t, Config{UV: uv, PipIndexURL: "https://pypi.example/simple"})
	ps := &pyScript{facts: factsJSON("certifi", "brotli; platform_python_implementation == 'CPython'", "click==8.*"), install: func(_ int, s executor.Spec) executor.Result { return installInto(s) }}
	r := &fakeRunner{fn: ps.run}
	res := h.Run(context.Background(), job, analysis.TaskSpec{Key: "deps.python"}, r)
	if res.Status != domain.TaskSucceeded || len(res.Artifacts) != 1 {
		t.Fatalf("result %+v", res)
	}
	taskDir := filepath.Join(job.Scratch, "deps", "deps.python")
	for _, s := range r.specs {
		if s.Dir != filepath.Join(taskDir, "work") || s.WorkspaceRoot != s.Dir || strings.HasPrefix(s.Dir, src) || strings.HasPrefix(s.Dir, job.Snapshot.WorkspaceDir) {
			t.Fatalf("cwd %s must be the task's empty work directory", s.Dir)
		}
		if !slices.Contains(s.ProtectedRoots, job.Snapshot.SnapshotDir) {
			t.Fatalf("protected roots %v", s.ProtectedRoots)
		}
	}
	install := r.specs[len(r.specs)-1]
	tree := depdoc.AttemptTree(job.Scratch, "deps.python")
	site := filepath.Join(tree, python.SitePackagesRel("3.14"))
	for _, want := range [][]string{{"pip", "install", "--target", site, "--python", h.cfg.Python, "--no-build", "--no-config"}, {"--index-url", "https://pypi.example/simple"}} {
		if !containsRun(install.Args, want) {
			t.Fatalf("argv %v lacks %v", install.Args, want)
		}
	}
	if install.Executable != uv || install.Env["UV_PYTHON_DOWNLOADS"] != "never" {
		t.Fatalf("install %s env %v", install.Executable, install.Env)
	}
	list := install.Args[slices.Index(install.Args, "-r")+1]
	got, _ := os.ReadFile(list)
	// The hashed line loses its hash: the other lines have none.
	want := "brotli ; platform_python_implementation == 'CPython'\ncertifi\nclick ==8.*\nidna ==3.10\nrich >=10,<15\n"
	if string(got) != want {
		t.Fatalf("requirements file\n%s\nwant\n%s", got, want)
	}
	rec := readRecord(t, job, "deps.python")
	if rec.Manager.Name != "uv" || rec.Manager.Version == nil || *rec.Manager.Version != "0.12.5" || rec.Pinned || rec.Cache.Kind != depdoc.CacheAttempt || rec.Lockfile != nil {
		t.Fatalf("record %+v", rec)
	}
	if *rec.Counts.Declared != 5 || rec.Counts.Prepared != 5 || rec.Counts.Failed != 0 {
		t.Fatalf("counts %+v", rec.Counts)
	}
	var reasons []string
	for _, d := range rec.DroppedInputs {
		if d.Path != "requirements.txt" {
			t.Fatalf("dropped %+v", d)
		}
		reasons = append(reasons, d.Reason)
	}
	if want := []string{DropPyEditable, DropPyOption, DropPyVCS, DropPyLocal}; !slices.Equal(reasons, want) {
		t.Fatalf("dropped reasons %v, want %v", reasons, want)
	}
	for _, l := range []string{depdoc.LimVersionsNotPinned, LimPyHashesNotChecked, LimPyStartupHooksRemoved} {
		if !slices.Contains(rec.Limitations, l) {
			t.Fatalf("limitations %v lack %s", rec.Limitations, l)
		}
	}
	for _, gone := range []string{"evil.pth", "sitecustomize.py"} {
		if _, err := os.Lstat(filepath.Join(site, gone)); err == nil {
			t.Fatalf("%s left in the tree", gone)
		}
	}
	if _, err := os.Stat(filepath.Join(site, "idna", "py.typed")); err != nil {
		t.Fatalf("package missing: %v", err)
	}
	for rel, body := range files {
		if b, err := os.ReadFile(filepath.Join(src, rel)); err != nil || string(b) != body {
			t.Fatalf("source %s changed", rel)
		}
	}
}

func containsRun(args, want []string) bool {
	for i := range args {
		if i+len(want) <= len(args) && slices.Equal(args[i:i+len(want)], want) {
			return true
		}
	}
	return false
}

// pip of the interpreter installs when no uv is configured: wheels only,
// isolated from the user's pip configuration.
func TestPythonFetchWithPip(t *testing.T) {
	job, _ := jobOf(t, map[string]string{"requirements.txt": "idna==3.10\n", "a.py": "import idna\n"})
	h := pyHandler(t, Config{})
	ps := &pyScript{install: func(_ int, s executor.Spec) executor.Result { return installInto(s) }}
	r := &fakeRunner{fn: ps.run}
	if res := h.Run(context.Background(), job, analysis.TaskSpec{Key: "deps.python"}, r); res.Status != domain.TaskSucceeded {
		t.Fatalf("result %+v", res)
	}
	install := r.specs[len(r.specs)-1]
	if install.Executable != h.cfg.Python || !containsRun(install.Args, []string{"-I", "-m", "pip", "install", "--target"}) ||
		!slices.Contains(install.Args, "--only-binary=:all:") || !slices.Contains(install.Args, "--isolated") || install.Env["PYTHONNOUSERSITE"] != "1" {
		t.Fatalf("pip install %s %v %v", install.Executable, install.Args, install.Env)
	}
	if rec := readRecord(t, job, "deps.python"); rec.Manager.Name != "pip" || *rec.Manager.Version != "25.1.1" || !rec.Pinned || rec.Cache.Kind != depdoc.CacheShared {
		t.Fatalf("record %+v", rec)
	}
	// An interpreter without pip and no uv: nothing can install.
	job2, _ := jobOf(t, map[string]string{"requirements.txt": "idna==3.10\n", "a.py": "import idna\n"})
	ps2 := &pyScript{noPip: true}
	res := pyHandler(t, Config{}).Run(context.Background(), job2, analysis.TaskSpec{Key: "deps.python"}, &fakeRunner{fn: ps2.run})
	if res.Status != domain.TaskUnsupported || res.Reason != depdoc.ReasonPackageManagerNotConfigured {
		t.Fatalf("no pip: %+v", res)
	}
}

// Fully pinned requirements build a shared tree once; the next attempt
// reuses it without running an installer.
func TestPythonPinnedTreeIsSharedAndReused(t *testing.T) {
	files := map[string]string{"requirements.txt": "idna==3.10\nattrs==24.2.0\n", "a.py": "import idna\n"}
	cache := filepath.Join(t.TempDir(), "cache", "deps")
	h := pyHandler(t, Config{CacheRoot: cache})
	job, _ := jobOf(t, files)
	ps := &pyScript{install: func(_ int, s executor.Spec) executor.Result { return installInto(s) }}
	if res := h.Run(context.Background(), job, analysis.TaskSpec{Key: "deps.python"}, &fakeRunner{fn: ps.run}); res.Status != domain.TaskSucceeded {
		t.Fatalf("first %+v", res)
	}
	rec := readRecord(t, job, "deps.python")
	if rec.Cache.Kind != depdoc.CacheShared || rec.Cache.Key == nil || !strings.HasPrefix(*rec.Cache.Key, "python/trees/") {
		t.Fatalf("record %+v", rec)
	}
	if _, err := os.Stat(filepath.Join(cache, filepath.FromSlash(*rec.Cache.Key), python.SitePackagesRel("3.14"), "idna")); err != nil {
		t.Fatalf("shared tree: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(cache, "python", "staging")); len(entries) != 0 {
		t.Fatalf("staging left: %v", entries)
	}
	job2, _ := jobOf(t, files)
	ps2 := &pyScript{install: func(int, executor.Spec) executor.Result {
		t.Fatal("installer run for a reused tree")
		return executor.Result{}
	}}
	if res := h.Run(context.Background(), job2, analysis.TaskSpec{Key: "deps.python"}, &fakeRunner{fn: ps2.run}); res.Status != domain.TaskSucceeded {
		t.Fatalf("second %+v", res)
	}
	again := readRecord(t, job2, "deps.python")
	if *again.Cache.Key != *rec.Cache.Key || !slices.Contains(again.Limitations, LimPyTreeReused) || again.Counts.Prepared != 2 {
		t.Fatalf("reused record %+v", again)
	}
}

// A requirement without a wheel is left out and the install tried again;
// one pulled in by another requirement leaves out that requirement.
func TestPythonDropsRequirementsWithoutWheels(t *testing.T) {
	uvDirect := "  × No solution found when resolving dependencies:\n  ╰─▶ Because mkautodoc==0.2.0 has no usable wheels and you require\n      mkautodoc==0.2.0, we can conclude that your requirements are\n      unsatisfiable.\n\nhint: Wheels are required for `mkautodoc` because building from source is disabled for all packages (i.e., with `--no-build`)\n"
	uvTransitive := "  × No solution found when resolving dependencies:\n  ╰─▶ Because mkautodoc==0.2.0 has no usable wheels and all versions of\n      codeyetest depend on mkautodoc==0.2.0, we can conclude that all versions\n      of codeyetest cannot be used.\n      And because you require codeyetest==1.0, we can conclude that your\n      requirements are unsatisfiable.\n"
	for name, tc := range map[string]struct {
		stderr string
		uv     bool
		failed string
		reason string
	}{
		"uv direct":     {uvDirect, true, "mkautodoc", ReasonPyNoBinaryDistribution},
		"uv transitive": {uvTransitive, true, "codeyetest", ReasonPyNoBinaryDistribution},
		"pip":           {"ERROR: Could not find a version that satisfies the requirement mkautodoc==0.2.0 (from versions: none)\nERROR: No matching distribution found for mkautodoc==0.2.0\n", false, "mkautodoc", ReasonPyNoMatchingDistibution},
		"pip transitive": {"ERROR: Could not find a version that satisfies the requirement mkautodoc==0.2.0 (from codeyetest) (from versions: none)\nERROR: No matching distribution found for mkautodoc==0.2.0\n",
			false, "codeyetest", ReasonPyNoMatchingDistibution},
	} {
		t.Run(name, func(t *testing.T) {
			job, _ := jobOf(t, map[string]string{"requirements.txt": "idna==3.10\nattrs==24.2.0\n" + tc.failed + "==1.0\n", "a.py": "import idna\n"})
			cfg := Config{}
			if tc.uv {
				cfg.UV = filepath.Join(t.TempDir(), "uv")
			}
			ps := &pyScript{install: func(n int, s executor.Spec) executor.Result {
				if n == 1 {
					return exited(1, "", tc.stderr)
				}
				list, _ := os.ReadFile(s.Args[slices.Index(s.Args, "-r")+1])
				if strings.Contains(string(list), tc.failed) {
					t.Errorf("retry still asks for %s:\n%s", tc.failed, list)
				}
				return installInto(s)
			}}
			res := pyHandler(t, cfg).Run(context.Background(), job, analysis.TaskSpec{Key: "deps.python"}, &fakeRunner{fn: ps.run})
			rec := readRecord(t, job, "deps.python")
			if res.Status != domain.TaskFailed || res.Reason != tc.reason || rec.State != depdoc.StatePartial || ps.calls != 2 {
				t.Fatalf("result %+v state %s calls %d", res, rec.State, ps.calls)
			}
			if rec.Counts.Prepared != 2 || rec.Counts.Failed != 1 || len(rec.FailedItems) != 1 || rec.FailedItems[0] != (depdoc.Failed{Name: tc.failed, Reason: tc.reason}) {
				t.Fatalf("record %+v", rec)
			}
		})
	}
}

// Failures that end the task: each with its reason and a record.
func TestPythonFetchFailures(t *testing.T) {
	cases := map[string]struct {
		stderr string
		reason string
	}{
		"uv unreachable": {"error: Request failed after 3 retries in 7.0s\n  Caused by: Failed to fetch: `http://127.0.0.1:9/simple/idna/`\n  Caused by: error sending request for url (http://127.0.0.1:9/simple/idna/)\n  Caused by: tcp connect error\n  Caused by: Connection refused (os error 111)\n", depdoc.ReasonRegistryUnreachable},
		"pip unreachable": {"WARNING: Retrying (Retry(total=0)) after connection broken by 'NewConnectionError(...: Failed to establish a new connection: [Errno 111] Connection refused')': /simple/idna/\nERROR: Could not find a version that satisfies the requirement idna==3.10 (from versions: none)\nERROR: No matching distribution found for idna==3.10\n",
			depdoc.ReasonRegistryUnreachable},
		"auth":     {"error: Failed to fetch: `https://pypi.example/simple/idna/`\n  Caused by: HTTP status client error (401 Unauthorized) for url (https://pypi.example/simple/idna/)\n", ReasonPyRegistryAuthRequired},
		"conflict": {"  × No solution found when resolving dependencies:\n  ╰─▶ Because you require idna==3.10 and idna==3.9, we can conclude that your requirements are unsatisfiable.\n", ReasonPyResolutionConflict},
		"other":    {"error: something else\n", depdoc.ReasonFetchFailed},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			job, _ := jobOf(t, map[string]string{"requirements.txt": "idna==3.10\n", "a.py": "import idna\n"})
			cache := filepath.Join(t.TempDir(), "deps")
			ps := &pyScript{install: func(int, executor.Spec) executor.Result { return exited(1, "", tc.stderr) }}
			res := pyHandler(t, Config{CacheRoot: cache}).Run(context.Background(), job, analysis.TaskSpec{Key: "deps.python"}, &fakeRunner{fn: ps.run})
			rec := readRecord(t, job, "deps.python")
			if res.Status != domain.TaskFailed || res.Reason != tc.reason || rec.State != depdoc.StateFailed || ps.calls != 1 {
				t.Fatalf("result %+v state %s calls %d", res, rec.State, ps.calls)
			}
			// A failed shared build leaves no tree behind.
			if entries, _ := os.ReadDir(filepath.Join(cache, "python", "trees")); len(entries) != 0 {
				t.Fatalf("tree published: %v", entries)
			}
			if entries, _ := os.ReadDir(filepath.Join(cache, "python", "staging")); len(entries) != 0 {
				t.Fatalf("staging left: %v", entries)
			}
		})
	}
}

// uv.lock with uv: uv export (frozen, offline) reads the workspace copy and
// pins the versions; without uv the static inputs are read and the lock is
// a dropped input.
func TestPythonUVLock(t *testing.T) {
	files := map[string]string{"pyproject.toml": "[project]\nname='a'\ndependencies=['idna']\n", "uv.lock": "version = 1\n", "a.py": "import idna\n"}
	job, _ := jobOf(t, files)
	exported := "idna==3.10 \\\n    --hash=sha256:" + strings.Repeat("b", 64) + "\nanyio==4.13.0 ; python_full_version < '3.11' \\\n    --hash=sha256:" + strings.Repeat("c", 64) + "\n-e ./packages/member\n"
	var export executor.Spec
	ps := &pyScript{export: func(s executor.Spec) executor.Result { export = s; return exited(0, exported, "") }, install: func(_ int, s executor.Spec) executor.Result { return installInto(s) }}
	uv := filepath.Join(t.TempDir(), "uv")
	res := pyHandler(t, Config{UV: uv}).Run(context.Background(), job, analysis.TaskSpec{Key: "deps.python"}, &fakeRunner{fn: ps.run})
	if res.Status != domain.TaskSucceeded {
		t.Fatalf("result %+v", res)
	}
	if !containsRun(export.Args, []string{"export", "--frozen", "--offline", "--no-config", "--project", job.Snapshot.WorkspaceDir}) {
		t.Fatalf("export argv %v", export.Args)
	}
	rec := readRecord(t, job, "deps.python")
	if !rec.Pinned || rec.Lockfile == nil || rec.Lockfile.Path != "uv.lock" || *rec.Counts.Declared != 2 || rec.Cache.Kind != depdoc.CacheShared {
		t.Fatalf("record %+v", rec)
	}
	if len(rec.DroppedInputs) != 1 || rec.DroppedInputs[0] != (depdoc.Dropped{Path: "uv.lock", Reason: DropPyEditable}) {
		t.Fatalf("dropped %+v", rec.DroppedInputs)
	}

	job2, _ := jobOf(t, files)
	ps2 := &pyScript{facts: factsJSON("idna"), install: func(_ int, s executor.Spec) executor.Result { return installInto(s) }}
	if res := pyHandler(t, Config{}).Run(context.Background(), job2, analysis.TaskSpec{Key: "deps.python"}, &fakeRunner{fn: ps2.run}); res.Status != domain.TaskSucceeded {
		t.Fatalf("without uv %+v", res)
	}
	rec2 := readRecord(t, job2, "deps.python")
	if rec2.Pinned || rec2.Lockfile != nil || !slices.Contains(rec2.DroppedInputs, depdoc.Dropped{Path: "uv.lock", Reason: DropPyUVMissing}) {
		t.Fatalf("record without uv %+v", rec2)
	}
}

// Nothing to fetch, nothing to install with, or nothing readable.
func TestPythonSkipsAndUnsupported(t *testing.T) {
	cases := map[string]struct {
		files  map[string]string
		facts  string
		cfg    Config
		status domain.TaskStatus
		reason string
	}{
		"no consumer":      {map[string]string{"requirements.txt": "idna\n", "a.py": ""}, "", Config{}, domain.TaskSkippedPolicy, depdoc.ReasonNoDependencyConsumer},
		"no requirements":  {map[string]string{"pyproject.toml": "[project]\nname='a'\n", "a.py": ""}, factsJSON(), Config{}, domain.TaskSkippedPolicy, depdoc.ReasonNoDependencies},
		"poetry lock only": {map[string]string{"pyproject.toml": "[tool.poetry]\n", "poetry.lock": "", "a.py": ""}, `{"project":false,"dynamic":[],"requirements":[],"invalid":0,"poetry":true}`, Config{}, domain.TaskUnsupported, ReasonPyLockfileUnsupported},
		"dynamic":          {map[string]string{"pyproject.toml": "[project]\nname='a'\ndynamic=['dependencies']\n", "a.py": ""}, `{"project":true,"dynamic":["dependencies"],"requirements":[],"invalid":0,"poetry":false}`, Config{}, domain.TaskUnsupported, ReasonPyDynamicDependencies},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			job, _ := jobOf(t, tc.files)
			h := pyHandler(t, tc.cfg)
			if name == "no consumer" {
				h.cfg.PythonConsumers = false
			}
			ps := &pyScript{facts: tc.facts}
			res := h.Run(context.Background(), job, analysis.TaskSpec{Key: "deps.python"}, &fakeRunner{fn: ps.run})
			if res.Status != tc.status || res.Reason != tc.reason || ps.calls != 0 {
				t.Fatalf("result %+v", res)
			}
			if rec := readRecord(t, job, "deps.python"); rec.State != depdoc.StateSkipped {
				t.Fatalf("record %+v", rec)
			}
		})
	}
	job, _ := jobOf(t, map[string]string{"requirements.txt": "idna\n", "a.py": ""})
	h := pyHandler(t, Config{})
	h.cfg.Python = ""
	if res := h.Run(context.Background(), job, analysis.TaskSpec{Key: "deps.python"}, &fakeRunner{fn: (&pyScript{}).run}); res.Status != domain.TaskUnsupported || res.Reason != depdoc.ReasonPackageManagerNotConfigured {
		t.Fatalf("no interpreter %+v", res)
	}
}

func TestParseRequirementAllowlist(t *testing.T) {
	hash := "sha256:" + strings.Repeat("d", 64)
	for line, want := range map[string]struct {
		out    string
		reason string
		pinned bool
	}{
		"idna==3.10": {"idna ==3.10", "", true},
		"Foo_Bar[x, y] >= 1.0 ; python_version < '3.12'": {"Foo_Bar[x,y] >= 1.0 ; python_version < '3.12'", "", false},
		"click==8.*":                       {"click ==8.*", "", false},
		"pkg (>=1,<2)":                     {"pkg >=1,<2", "", false},
		"idna==3.10 --hash=" + hash:        {"idna ==3.10", "", true},
		"idna==3.10 --config-settings=x=1": {"", DropPyOption, false},
		"pkg @ https://example.com/pkg-1.0-py3-none-any.whl": {"pkg @ https://example.com/pkg-1.0-py3-none-any.whl", "", false},
		"pkg @ git+https://example.com/p.git":                {"", DropPyVCS, false},
		"git+https://example.com/p.git":                      {"", DropPyVCS, false},
		"pkg @ file:///tmp/p":                                {"", DropPyLocal, false},
		"./vendor/pkg":                                       {"", DropPyLocal, false},
		"/abs/pkg.whl":                                       {"", DropPyLocal, false},
		"C:\\pkg":                                            {"", DropPyLocal, false},
		"pkg; os.system('x')\"":                              {"pkg ; os.system('x')\"", "", false},
		"pkg >= $(rm -rf)":                                   {"", DropPyNotUnderstood, false},
		"%%%":                                                {"", DropPyNotUnderstood, false},
	} {
		r, reason := parseRequirement(line)
		if reason != want.reason || r.line != want.out || r.pinned != want.pinned {
			t.Errorf("%q: line %q reason %q pinned %v, want %+v", line, r.line, reason, r.pinned, want)
		}
	}
}

// The pyproject reader runs on the operator's interpreter with the
// standard library only; skipped where no python3 with tomllib exists.
func TestPyprojectScriptReadsStaticRequirements(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found")
	}
	toml := `[project]
name = "a"
dynamic = ["version"]
dependencies = [
  "certifi",  # comment
  'httpcore==1.*',
]
[project.optional-dependencies]
http2 = ["h2>=3,<5"]
[dependency-groups]
test = ["pytest", {include-group = "lint"}]
lint = ["ruff==0.12.11"]
[tool.poetry.dependencies]
python = "^3.10"
`
	cmd := exec.Command(py, "-I", "-S", "-c", pyprojectScript)
	cmd.Stdin = strings.NewReader(toml)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var facts pyprojectFacts
	if err := json.Unmarshal(out, &facts); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	if facts.Error == "tomllib_unavailable" {
		t.Skip("python3 older than 3.11")
	}
	want := []string{"certifi", "httpcore==1.*", "h2>=3,<5", "ruff==0.12.11", "pytest", "ruff==0.12.11"}
	if !facts.Project || !slices.Equal(facts.Requirements, want) || !slices.Equal(facts.Dynamic, []string{"version"}) || !facts.Poetry || facts.Invalid != 0 {
		t.Fatalf("facts %+v", facts)
	}
}
