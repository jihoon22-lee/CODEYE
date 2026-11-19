package builtin

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/jsts"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// JS/TS package managers (npm, pnpm, yarn): the tools the deps.javascript
// task installs a project's dependencies with (docs/ANALYZERS.md#jsts-의존성-받기).
// They are no analyzer and no shared toolchain: each is the entry script
// of its npm package, run as <node> <script> like the JS analyzer tools,
// with one agent flag (--npm, --pnpm, --yarn) that travels in the hello's
// analyzer_tools map.

// jsPackageManager is one JS package manager tool.
type jsPackageManager struct {
	// name is the flag and tool name, pkg the npm package that holds the
	// entry script.
	name, pkg string
}

func jsPackageManagers() []jsPackageManager {
	return []jsPackageManager{{"npm", "npm"}, {"pnpm", "pnpm"}, {"yarn", "yarn"}}
}

// JSPackageManagerFlags are the agent flag names of the JS package
// managers, sorted.
func JSPackageManagerFlags() []string {
	var out []string
	for _, p := range jsPackageManagers() {
		out = append(out, p.name)
	}
	return out
}

func jsPackageManagerOf(tool string) (jsPackageManager, bool) {
	for _, p := range jsPackageManagers() {
		if p.name == tool {
			return p, true
		}
	}
	return jsPackageManager{}, false
}

var pmVersion = regexp.MustCompile(`^v?([0-9]{1,4}\.[0-9]{1,4}\.[0-9]{1,6})(?:-[0-9A-Za-z.]+)?$`)

// parsePackageManagerVersion reads `<manager> --version` (one version line).
func parsePackageManagerVersion(stdout []byte) (string, bool) {
	line, _, _ := strings.Cut(strings.TrimSpace(string(stdout)), "\n")
	m := pmVersion.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// checkJSPackageManager validates a package manager the way the
// deps.javascript task uses it: the node toolchain first, then the entry
// script inside its npm package (no shim), then `<node> <script>
// --version`.
func checkJSPackageManager(ctx context.Context, cfg Config, runner executor.Runner, p jsPackageManager) built {
	exe := cfg.Tools[p.name]
	if exe == "" {
		return unavailable(p.name, "not_configured", "", "")
	}
	states, _ := validateToolchains(ctx, cfg, runner)
	node := states["node"]
	switch {
	case !node.configured:
		return unavailable(p.name, "toolchain_not_configured", p.name+" needs --node", "")
	case !node.ok:
		return unavailable(p.name, "toolchain_unavailable", "node toolchain is unavailable", "")
	}
	s, err := jsts.NewScript(exe, p.pkg, node.handle)
	if err != nil {
		return unavailable(p.name, "invalid_config", err.Error(), "")
	}
	if runner == nil || cfg.ProbeDir == "" {
		return unavailable(p.name, "probe_failed", "no runner or probe dir", "")
	}
	out, err := s.Probe(ctx, runner, cfg.ProbeDir, p.name, "--version")
	if err != nil {
		return unavailable(p.name, "probe_failed", err.Error(), "")
	}
	v, ok := parsePackageManagerVersion(out)
	if !ok {
		return unavailable(p.name, "probe_failed", errors.New(p.name+" version output not recognized").Error(), "")
	}
	return built{version: v}
}
