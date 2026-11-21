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
		return executor.Spec{Executable: h.tools.Node, Args: []string{h.tools.Npm, "--offline", "run", p.args[0]}, Env: env, Redact: []string{h.tools.Npm}}, ""
	case key == "python":
		if h.tools.Python == "" {
			return executor.Spec{}, toolUnavailable
		}
		env := nativeEnv(joinPath([]string{filepath.Dir(h.tools.Python)}, toolPath))
		env["PYTHONNOUSERSITE"], env["PYTHONDONTWRITEBYTECODE"], env["PYTHONIOENCODING"] = "1", "1", "utf-8"
		if site := python.PreparedSite(job, h.tools.DependencyCacheRoot, p.spec.Key, pythonKey, p.dir); site != "" {
			env["PYTHONPATH"] = site
		}
		return executor.Spec{Executable: h.tools.Python, Args: []string{"-B", "-m", "unittest", "discover", "-s", p.args[0]}, Env: env}, ""
	case key == "rust":
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
		return executor.Spec{Executable: h.rustTool.Cargo(), Args: args, Env: env}, ""
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
