package builtin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/nativepath"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/javapack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Shared toolchains (docs/ANALYZERS.md#도구-경로) are interpreters or runtimes that
// analyzer tools of several packs need (node for pyright, tsc, eslint,
// jscpd). Each has one agent flag (--<name> /abs/path), one version probe
// and one validated handle; packs declare the need in
// pack.Descriptor.Toolchains and never configure the interpreter
// themselves. go is not in the table: --go keeps its own setting.

// toolchainSpec is one entry of the toolchain table.
type toolchainSpec struct {
	name        string
	versionArgs []string
	parse       func(stdout []byte) (version string, ok bool)
}

// toolchainTable lists the shared toolchains of this build. Their names are
// reserved: no analyzer tool may use one (TestToolchainNamesAreReserved).
func toolchainTable() []toolchainSpec {
	return []toolchainSpec{
		{name: "java", versionArgs: []string{"--version"}, parse: javapack.ParseJavaVersion},
		{name: "node", versionArgs: []string{"--version"}, parse: parseNodeVersion},
	}
}

// ToolchainFlags are the agent flag names (--<name>) of the shared
// toolchains, sorted. Their paths travel in the hello's analyzer_tools map
// under the same reserved names.
func ToolchainFlags() []string {
	var out []string
	for _, tc := range toolchainTable() {
		out = append(out, tc.name)
	}
	sort.Strings(out)
	return out
}

// nodeVersion is `node --version` output: v<major>.<minor>.<patch>.
var nodeVersion = regexp.MustCompile(`^v([0-9]{1,4})\.([0-9]{1,4})\.([0-9]{1,4})$`)

// parseNodeVersion reads "v24.18.0\n" and returns "24.18.0" (the runtime
// observation form). Anything else, pre-release tags included, is rejected.
func parseNodeVersion(stdout []byte) (string, bool) {
	m := nodeVersion.FindStringSubmatch(strings.TrimSpace(string(stdout)))
	if m == nil {
		return "", false
	}
	return m[1] + "." + m[2] + "." + m[3], true
}

// toolchainProbeTimeout matches the env.discover tool probe.
const toolchainProbeTimeout = 30 * time.Second

// toolchainState is the startup outcome of one toolchain.
type toolchainState struct {
	configured bool
	ok         bool
	handle     pack.Toolchain
}

// validateToolchains probes every configured toolchain once. A configured
// toolchain that is invalid or fails its probe is reported unavailable; an
// unconfigured one is not reported (the analyzers that need it are).
func validateToolchains(ctx context.Context, cfg Config, runner executor.Runner) (map[string]toolchainState, []Unavailable) {
	states := map[string]toolchainState{}
	var missing []Unavailable
	for _, tc := range toolchainTable() {
		exe := cfg.Tools[tc.name]
		if exe == "" {
			states[tc.name] = toolchainState{}
			continue
		}
		h, reason, err := probeToolchain(ctx, runner, cfg.ProbeDir, tc, exe)
		if err != nil {
			states[tc.name] = toolchainState{configured: true}
			missing = append(missing, Unavailable{Name: tc.name, Reason: reason, Detail: err.Error()})
			continue
		}
		states[tc.name] = toolchainState{configured: true, ok: true, handle: h}
	}
	return states, missing
}

// probeToolchain checks the operator path and runs the version argv through
// the executor with the tool-probe rules: a fresh directory under probeDir,
// PATH set to the resolved executable's directory only, LANG=C.UTF-8
// (Windows: SystemRoot), 30 s, 64 KiB of output. Errors name no path.
func probeToolchain(ctx context.Context, runner executor.Runner, probeDir string, tc toolchainSpec, exe string) (pack.Toolchain, string, error) {
	if !filepath.IsAbs(exe) {
		return pack.Toolchain{}, "invalid_config", errors.New(tc.name + " toolchain path must be absolute")
	}
	resolved, err := nativepath.Resolve(exe)
	if err != nil {
		return pack.Toolchain{}, "invalid_config", errors.New(tc.name + " toolchain not found")
	}
	st, err := os.Stat(resolved)
	if err != nil || !st.Mode().IsRegular() {
		return pack.Toolchain{}, "invalid_config", errors.New(tc.name + " toolchain is not a file")
	}
	if runtime.GOOS != "windows" && st.Mode().Perm()&0o111 == 0 {
		return pack.Toolchain{}, "invalid_config", errors.New(tc.name + " toolchain is not executable")
	}
	if runner == nil || probeDir == "" || !filepath.IsAbs(probeDir) {
		return pack.Toolchain{}, "probe_failed", errors.New("no runner or probe dir")
	}
	work, err := os.MkdirTemp(probeDir, tc.name+"-toolchain-probe-")
	if err != nil {
		return pack.Toolchain{}, "probe_failed", errors.New("probe dir is not writable")
	}
	defer os.RemoveAll(work)
	ws := filepath.Join(work, "ws")
	if err := os.Mkdir(ws, 0o700); err != nil {
		return pack.Toolchain{}, "probe_failed", errors.New("probe dir is not writable")
	}
	env := map[string]string{"PATH": filepath.Dir(resolved), "LANG": "C.UTF-8"}
	if runtime.GOOS == "windows" {
		if v := os.Getenv("SystemRoot"); v != "" {
			env["SystemRoot"] = v
		}
	}
	res := runner.Run(ctx, executor.Spec{Executable: exe, Args: append([]string{}, tc.versionArgs...), WorkspaceRoot: ws, Dir: ws, TempParent: work,
		Env: env, Timeout: toolchainProbeTimeout, StdoutLimit: 64 << 10, StderrLimit: 64 << 10})
	switch {
	case res.Cleanup == executor.CleanupUncertain:
		return pack.Toolchain{}, "probe_failed", errors.New(tc.name + " version probe cleanup uncertain")
	case res.Status != executor.StatusExited || res.ExitCode == nil || *res.ExitCode != 0:
		return pack.Toolchain{}, "probe_failed", errors.New(tc.name + " version probe did not succeed: " + string(res.Status) + " " + res.Reason)
	case res.Stdout.Truncated || res.Stdout.Incomplete:
		return pack.Toolchain{}, "probe_failed", errors.New(tc.name + " version output incomplete")
	}
	v, ok := tc.parse(res.Stdout.Data)
	if !ok {
		return pack.Toolchain{}, "probe_failed", errors.New(tc.name + " version output not recognized")
	}
	return pack.Toolchain{Name: tc.name, Executable: exe, Dir: filepath.Dir(resolved), Version: v}, "", nil
}

// gateToolchains decides an analyzer that needs shared toolchains before it
// is built. Precedence: the tool itself not configured
// (tool_not_configured), then a needed toolchain not configured
// (toolchain_not_configured), then one that is invalid or failed its probe
// (tool_unavailable). nil means build, with the validated handles. The
// repository-code grant is checked later, per task, in Composite.Run and
// precedes all of these.
func gateToolchains(d pack.Descriptor, cfg Config, states map[string]toolchainState) (*built, map[string]pack.Toolchain) {
	if len(d.Toolchains) == 0 {
		return nil, map[string]pack.Toolchain{}
	}
	if d.Tool != "go" && cfg.Tools[d.Tool] == "" {
		b := unavailable(d.Name, "not_configured", "", pack.ReasonToolNotConfigured)
		return &b, nil
	}
	for _, n := range d.Toolchains {
		if st, known := states[n]; !known || !st.configured {
			b := unavailable(d.Name, "toolchain_not_configured", d.Name+" needs --"+n, pack.ReasonToolchainNotConfigured)
			return &b, nil
		}
	}
	handles := map[string]pack.Toolchain{}
	for _, n := range d.Toolchains {
		st := states[n]
		if !st.ok {
			b := unavailable(d.Name, "toolchain_unavailable", n+" toolchain is unavailable", pack.ReasonToolUnavailable)
			return &b, nil
		}
		handles[n] = st.handle
	}
	return nil, handles
}

// ToolchainProbes are the env.discover probes of the shared toolchains, one
// per table entry (not_probed when unconfigured). Analyzers and Ecosystems
// name the analyzers of this build that need the toolchain: env.discover
// observes it as a runtime when one of those ecosystems is in scope, in
// addition to the modules that declare the runtime themselves.
func ToolchainProbes(cfg Config) []pack.ToolProbe { return toolchainProbesOf(cfg, catalog()) }

func toolchainProbesOf(cfg Config, entries []entry) []pack.ToolProbe {
	var out []pack.ToolProbe
	for _, tc := range toolchainTable() {
		p := pack.ToolProbe{Tool: tc.name, Executable: cfg.Tools[tc.name], Args: append([]string{}, tc.versionArgs...), ParseVersion: tc.parse,
			Analyzers: []string{}, Ecosystems: []string{}}
		for _, e := range entries {
			if slices.Contains(e.desc.Toolchains, tc.name) {
				p.Analyzers = append(p.Analyzers, e.desc.ID)
				if !slices.Contains(p.Ecosystems, e.desc.Ecosystem) {
					p.Ecosystems = append(p.Ecosystems, e.desc.Ecosystem)
				}
			}
		}
		sort.Strings(p.Analyzers)
		sort.Strings(p.Ecosystems)
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tool < out[j].Tool })
	return out
}
