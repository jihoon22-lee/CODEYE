package toolfind

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/jsts"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
)

// Package managers of the dependency fetch (docs/ANALYZERS.md#의존성-준비):
// npm, pnpm and yarn (JS entry scripts run with node, found like the JS
// analyzer tools) and uv and the Python interpreter whose pip installs when
// there is no uv. They are no analyzer: each is one agent flag
// (--npm ... --python, deps.ManagerFlags) and agent.tools entry, and they
// travel in the worker hello's dependencies.package_managers.

// jsManagerNames and pythonManagerNames are the package manager tools in
// detection order: after node, and python after uv, since an interpreter
// without pip is still usable when uv is found.
var (
	jsManagerNames     = []string{"npm", "pnpm", "yarn"}
	pythonManagerNames = []string{"uv", "python"}
)

// isManager reports a package manager tool (not an analyzer tool).
func isManager(name string) bool {
	return slices.Contains(jsManagerNames, name) || slices.Contains(pythonManagerNames, name)
}

// Tested series of the Python package managers: uv 0.12; Python 3.11 and
// later read pyproject.toml with the standard library (tomllib). The JS
// managers have no tested series: any recognized version.
const (
	uvTestedSeries     = "0.12"
	pythonTestedSeries = "3.11+"
)

func pythonBin() string {
	if runtime.GOOS == "windows" {
		return "python"
	}
	return "python3"
}

// managerSpecs: the JS managers are entry scripts below node_modules, the
// Python ones executables on PATH and in the user directories.
var managerSpecs = map[string]spec{
	"npm":    {kindJS, "npm", "npm/bin/npm-cli.js", "npm comes with Node.js (https://nodejs.org); or set `agent.tools.npm`"},
	"pnpm":   {kindJS, "pnpm", "pnpm/bin/pnpm.cjs", npmHint("pnpm", "pnpm")},
	"yarn":   {kindJS, "yarn", "yarn/bin/yarn.js", npmHint("yarn", "yarn@1")},
	"uv":     {kindExec, "uv", "", "`curl -LsSf https://astral.sh/uv/install.sh | sh` (installs to ~/.local/bin) or set `agent.tools.uv`"},
	"python": {kindExec, pythonBin(), "", "install Python 3 with pip (Ubuntu: `sudo apt install python3 python3-pip`) or uv, or set `agent.tools.python`"},
}

var (
	jsManagerVersion  = regexp.MustCompile(`^v?([0-9]{1,4}\.[0-9]{1,4}\.[0-9]{1,6})(?:-[0-9A-Za-z.]+)?$`)
	uvVersionLine     = regexp.MustCompile(`^uv ([0-9]+\.[0-9]+\.[0-9]+)`)
	pythonVersionLine = regexp.MustCompile(`^Python (3)\.([0-9]+)\.([0-9]+)$`)
	pipVersionLine    = regexp.MustCompile(`^pip [0-9][0-9A-Za-z.+-]* from `)
)

// validateManager validates a package manager candidate the way its
// dependency task runs it; handled is false for any other tool.
func (d *detector) validateManager(name, path string) (outcome, bool) {
	switch name {
	case "npm", "pnpm", "yarn":
		return d.validateJSManager(name, path), true
	case "uv":
		v, err := d.version(path, "--version")
		if err != "" {
			return outcome{reason: err, series: uvTestedSeries}, true
		}
		m := uvVersionLine.FindStringSubmatch(v)
		if m == nil {
			return outcome{reason: "version output not recognized", series: uvTestedSeries}, true
		}
		return outcome{ok: true, version: m[1], tested: seriesMatch(m[1], uvTestedSeries), series: uvTestedSeries}, true
	case "python":
		v, err := d.version(path, "--version")
		if err != "" {
			return outcome{reason: err, series: pythonTestedSeries}, true
		}
		m := pythonVersionLine.FindStringSubmatch(v)
		if m == nil {
			return outcome{reason: "version output not recognized", series: pythonTestedSeries}, true
		}
		minor, _ := strconv.Atoi(m[2])
		if minor < 8 {
			return outcome{reason: "Python 3.8 or later is needed", series: pythonTestedSeries}, true
		}
		// pip installs when there is no uv; an interpreter without pip is
		// usable only with uv.
		if pv, perr := d.version(path, "-I", "-m", "pip", "--version"); perr != "" || !pipVersionLine.MatchString(pv) {
			if t := d.get("uv"); t.Status != Found && d.ex.Tools["uv"] == "" {
				return outcome{reason: "pip is not installed for this interpreter and uv was not found", series: pythonTestedSeries}, true
			}
		}
		return outcome{ok: true, version: m[1] + "." + m[2] + "." + m[3], tested: minor >= 11, series: pythonTestedSeries}, true
	}
	return outcome{}, false
}

// validateJSManager checks a JS package manager as the deps.javascript task
// runs it: the node toolchain first, then the entry script inside its npm
// package (no shim), then `<node> <script> --version`.
func (d *detector) validateJSManager(name, path string) outcome {
	t := d.get("node")
	if t.Status != Found {
		return outcome{reason: "needs node (not found or unusable)"}
	}
	node := t.Path
	resolved, err := filepath.EvalSymlinks(node)
	if err != nil {
		return outcome{reason: "needs node (not found or unusable)"}
	}
	s, err := jsts.NewScript(path, managerSpecs[name].bin, pack.Toolchain{Name: "node", Executable: node, Dir: filepath.Dir(resolved)})
	if err != nil {
		return outcome{reason: err.Error()}
	}
	out, err := s.Probe(d.ctx, d.runner, d.probeDir, name, "--version")
	if err != nil {
		return outcome{reason: "version probe did not succeed"}
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	m := jsManagerVersion.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return outcome{reason: "version output not recognized"}
	}
	return outcome{ok: true, version: m[1], tested: true}
}

// seriesMatch reports v in the major.minor series s.
func seriesMatch(v, s string) bool {
	return len(v) > len(s) && v[:len(s)] == s && v[len(s)] == '.'
}

// defaultNpmRegistry is npm's own default; a value equal to it is not a
// user setting.
const defaultNpmRegistry = "https://registry.npmjs.org/"

// npmRegistry asks the found npm for the registry of the user's real
// configuration (`npm config get registry`, their ~/.npmrc and variables).
// HOME, the npm cache and the logs are in the probe directory (npmProbeEnv),
// so the probe writes nothing in the user's home. Without a probe directory
// it does not run and npm's default registry applies.
func npmRegistry(ctx context.Context, node, npm, dir, home string) string {
	if dir == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, npm, "config", "get", "registry")
	cmd.Dir = dir
	isolated := probePath(dir, "npm-config")
	defer os.RemoveAll(isolated)
	cmd.Env = npmProbeEnv(os.Environ(), home, isolated)
	cmd.WaitDelay = time.Second
	var out bytes.Buffer
	cmd.Stdout = &out
	if cmd.Run() != nil {
		return ""
	}
	return registryOf(out.String())
}

// registryOf keeps a registry that differs from npm's default and carries
// no credential (a credential would land in tools.json, the agent's
// arguments and the worker hello).
func registryOf(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v == defaultNpmRegistry || strings.TrimSuffix(v, "/")+"/" == defaultNpmRegistry || !domain.ValidRegistryURL(v) {
		return ""
	}
	return v
}
