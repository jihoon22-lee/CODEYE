package testexec

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/python"
	"github.com/jihoon22-lee/CODEYE/internal/worker/deps/depdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

func (h *Handler) command(job analysis.Job, p testPlan, key, scratch, dir string) (executor.Spec, string) {
	suffix := strings.TrimPrefix(p.spec.Key, "test."+key)
	toolPath := h.toolPath()
	switch {
	case key == "go":
		if h.goTool == nil {
			return executor.Spec{}, toolUnavailable
		}
		cache, _, _ := h.goTool.ModuleCache(job, p.spec.Key, goKey, p.dir)
		if cache == "" {
			cache = filepath.Join(scratch, "mod-cache")
		}
		env := h.goTool.Env(filepath.Join(scratch, "go-cache"), filepath.Join(scratch, "gopath"), cache)
		if err := govet.DisableTelemetry(env, filepath.Join(scratch, "config")); err != nil {
			return executor.Spec{}, "scratch_unavailable"
		}
		args := append([]string{"test", "-json", "-vet=off", "-count=1", "-timeout=9m"}, p.args...)
		if h.tools.GoTestRun != "" {
			args = append(args, "-run", h.tools.GoTestRun)
		}
		if h.tools.GoRace {
			cc := findTool(toolPath, "cc")
			if cc == "" {
				return executor.Spec{}, toolUnavailable
			}
			env["CGO_ENABLED"], env["CC"] = "1", cc
			env["PATH"] = joinPath([]string{filepath.Dir(h.goTool.Executable())}, toolPath)
			args = append(args, "-race")
		}
		return executor.Spec{Executable: h.goTool.Executable(), Args: args, Env: env}, ""
	case isJavaScript(key):
		if h.tools.Node == "" || h.tools.Npm == "" {
			return executor.Spec{}, toolUnavailable
		}
		if p.dependencies && !javascriptDependencies(job, p.dir) {
			return executor.Spec{}, dependenciesUnavailable
		}
		env := nativeEnv(joinPath([]string{filepath.Dir(h.tools.Node)}, toolPath))
		env["npm_config_offline"] = "true"
		env["npm_config_audit"] = "false"
		env["npm_config_fund"] = "false"
		env["CI"] = "true"
		args := []string{h.tools.Npm, "--offline", "run", p.args[0]}
		report := filepath.Join(scratch, "framework-report")
		switch p.runner {
		case "jest":
			args = append(args, "--", "--json", "--testLocationInResults", "--outputFile", report)
		case "vitest":
			args = append(args, "--", "--run", "--includeTaskLocation", "--reporter=json", "--outputFile="+report)
		case "playwright":
			args = append(args, "--", "--reporter=json", "--retries=0")
			env["PLAYWRIGHT_JSON_OUTPUT_FILE"] = report
		}
		return executor.Spec{Executable: h.tools.Node, Args: args, Env: env, Redact: []string{h.tools.Npm}}, ""
	case key == "python":
		if h.tools.Python == "" {
			return executor.Spec{}, toolUnavailable
		}
		env := nativeEnv(joinPath([]string{filepath.Dir(h.tools.Python)}, toolPath))
		env["PYTHONNOUSERSITE"], env["PYTHONDONTWRITEBYTECODE"], env["PYTHONIOENCODING"] = "1", "1", "utf-8"
		if site := python.PreparedSite(job, h.tools.DependencyCacheRoot, p.spec.Key, pythonKey, p.dir); site != "" {
			env["PYTHONPATH"] = site
		}
		var args []string
		if p.runner == "pytest" {
			args = append([]string{"-B", "-m", "pytest", "-o", "junit_family=xunit1",
				"--junitxml=" + filepath.Join(scratch, "framework-report")}, p.args...)
		} else {
			args = []string{"-B", "-m", "unittest", "discover", "--verbose", "-s", p.args[0]}
		}
		return executor.Spec{Executable: h.tools.Python, Args: args, Env: env}, ""
	case key == "rust" || key == "rust-nextest":
		if h.rustTool == nil {
			return executor.Spec{}, toolUnavailable
		}
		cache := filepath.Join(scratch, "cargo-home")
		depKey := depdoc.TaskKey("rust", suffix)
		if rec, ok := depdoc.ReadForModule(job.Scratch, depKey, "rust", p.dir, dependencyExpect(job)); ok && rec.Usable() && h.tools.DependencyCacheRoot != "" {
			cache = filepath.Join(h.tools.DependencyCacheRoot, filepath.FromSlash(*rec.Cache.Key))
		}
		env := h.rustTool.FetchEnv(cache, filepath.Join(scratch, "cargo-target"), "")
		env["CARGO_NET_OFFLINE"] = "true"
		env["PATH"] = joinPath([]string{h.rustTool.ToolchainDir()}, toolPath)
		args := []string{"test", "--workspace", "--offline"}
		if _, err := os.Stat(filepath.Join(dir, "Cargo.lock")); err == nil {
			args = append(args, "--locked")
		}
		args = append(args, p.args...)
		rust := executor.Spec{Executable: h.rustTool.Cargo(), Args: args, Env: env}
		if key == "rust-nextest" {
			return h.nextestCommand(job, p, scratch, rust)
		}
		return rust, ""
	case key == "maven" || key == "gradle":
		return h.javaCommand(p, scratch)
	default:
		return executor.Spec{}, toolUnavailable
	}
}

func javascriptDependencies(job analysis.Job, module string) bool {
	for _, relative := range depdoc.InstalledTrees(job.Scratch, dependencyExpect(job)) {
		dir := filepath.Dir(filepath.FromSlash(relative))
		if dir != "." && filepath.ToSlash(dir) != module && !strings.HasPrefix(module, filepath.ToSlash(dir)+"/") {
			continue
		}
		root := filepath.Join(job.Snapshot.WorkspaceDir, filepath.FromSlash(relative))
		if info, err := os.Lstat(root); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

func (h *Handler) toolPath() []string {
	if h.tools.ToolPath == "" && runtime.GOOS != "windows" {
		return []string{"/usr/bin", "/bin"}
	}
	var dirs []string
	for _, dir := range filepath.SplitList(h.tools.ToolPath) {
		if filepath.IsAbs(dir) {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

func joinPath(head, tail []string) string {
	return strings.Join(append(head, tail...), string(os.PathListSeparator))
}

func nativeEnv(path string) map[string]string {
	env := map[string]string{"LANG": "C.UTF-8", "PATH": path}
	if runtime.GOOS == "windows" {
		env["SystemRoot"] = os.Getenv("SystemRoot")
	}
	return env
}

func findTool(dirs []string, name string) string {
	for _, dir := range dirs {
		if p := regularPath(filepath.Join(dir, name), true); p != "" {
			return p
		}
	}
	return ""
}

func (h *Handler) tool(configured, name string) string {
	if configured != "" {
		return regularPath(configured, true)
	}
	return findTool(h.toolPath(), name)
}

// The launcher is fixed text. The validated tool paths, script name and
// arguments remain positional arguments throughout. The marker records that
// the private loopback is ready and the launcher reached the test command.
const loopbackLauncher = `"$1" link set dev lo up || exit 125
: > "$2" || exit 125
shift 2
exec "$@"`

func (h *Handler) isolate(spec executor.Spec, startedPath string) (executor.Spec, executor.Spec, bool) {
	if runtime.GOOS != "linux" {
		return executor.Spec{}, executor.Spec{}, false
	}
	dirs := h.toolPath()
	unshare, ip, shell := findTool(dirs, "unshare"), findTool(append(dirs, "/usr/sbin", "/sbin"), "ip"), findTool(dirs, "sh")
	if unshare == "" || ip == "" || shell == "" {
		return executor.Spec{}, executor.Spec{}, false
	}
	// The PID namespace prevents a test's detached children from outliving
	// the suite. The launcher is its init and is killed if unshare exits.
	prefix := []string{"--user", "--map-root-user", "--net", "--pid", "--fork", "--kill-child", "--mount-proc", "--"}
	probe := spec
	probe.Executable, probe.Args = unshare, append(prefix, ip, "link", "set", "dev", "lo", "up")
	probe.Timeout, probe.StdoutLimit, probe.StderrLimit = 10*time.Second, 64<<10, 64<<10
	probe.Redact = append(probe.Redact, unshare, ip)
	isolated := spec
	isolated.Executable = unshare
	isolated.Args = append(append(append([]string{}, prefix...), shell, "-c", loopbackLauncher, "codeye-test", ip, startedPath, spec.Executable), spec.Args...)
	isolated.Redact = append(isolated.Redact, unshare, ip, shell, spec.Executable)
	return probe, isolated, true
}
