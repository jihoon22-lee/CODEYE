// Package builtin assembles the analysis handlers a worker may advertise.
// The deep-review handler is a pack.Composite of every analyzer of this
// build in catalog order (docs/39). An analyzer whose tool is not
// configured, invalid or fails its startup probe is still a member: it plans
// its tasks and reports them unsupported. The capability is advertised only
// when at least one baseline (required) analyzer probed successfully.
package builtin

import (
	"context"
	"errors"
	"sort"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/rustpack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/staticcheck"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Config comes from the agent hello (go_executable, go_mod_cache,
// analyzer_tools) and the worker's own scratch root.
type Config struct {
	// GoExecutable is the absolute go command (--go); "" disables go vet.
	GoExecutable string
	// GoModCache is an optional absolute offline module cache.
	GoModCache string
	// Tools are absolute paths by name: analyzer tools (--<tool>) and the
	// shared toolchains of the toolchain table (--node), whose names are
	// reserved and never analyzer tool names (docs/39 §2.4).
	Tools map[string]string
	// ProbeDir is an absolute writable directory outside any source tree
	// (e.g. the worker scratch root) used for toolchain probes.
	ProbeDir string
	// ToolPath is the operator's --toolchain-path list (hello tool_path). It
	// is only searched, once, for the system linker `cc` of the Rust pack
	// (docs/44 §2); it is never given to an analyzer as PATH.
	ToolPath string
}

// Unavailable explains why a known analyzer, a configured toolchain (or the
// whole capability) is not usable on this node.
type Unavailable struct {
	Name   string
	Reason string // not_configured | invalid_config | probe_failed | toolchain_not_configured | toolchain_unavailable | unknown_tool
	Detail string // no secrets, no paths
}

// built is the outcome of configuring one catalog entry.
type built struct {
	analyzer pack.Analyzer // nil when unavailable
	missing  *Unavailable
	stub     string // pack reason when analyzer is nil
}

// buildFunc configures one analyzer. goh is the go toolchain (nil when --go
// is unset or invalid); toolchains are the validated handles of every
// toolchain the descriptor lists (builtin checked them before the call).
type buildFunc func(ctx context.Context, cfg Config, runner executor.Runner, goh *govet.Handler, toolchains map[string]pack.Toolchain) built

// entry is one analyzer of this build. Catalog order is plan order.
type entry struct {
	desc  pack.Descriptor
	plan  pack.PlanFunc
	build buildFunc
}

// catalog lists the analyzers of this build in plan order: per ecosystem the
// baseline analyzer first, then extra depth. Packs append their entries in
// their own section; nothing else in this package changes per pack.
func catalog() []entry {
	return []entry{
		// Go (W08 baseline, W19 extra depth).
		{desc: govet.Descriptor(), plan: govet.Plan, build: buildGoVet},
		{desc: staticcheck.Descriptor(), plan: staticcheck.Plan, build: buildStaticcheck},
		// JavaScript/TypeScript (W18 baseline ESLint, extra depth tsc and
		// type-aware ESLint; javascript.go, docs/43).
		eslintEntry(), tscEntry(), eslintTypedEntry(),
		// Rust (W20 baseline cargo check, extra depth clippy).
		{desc: rustpack.CheckDescriptor(), plan: rustpack.CheckPlan, build: buildCargoCheck},
		{desc: rustpack.ClippyDescriptor(), plan: rustpack.ClippyPlan, build: buildClippy},
	}
}

// ToolFlags are the agent flag names (--<tool>) of this build's analyzer
// tools, sorted; "go" is the separate --go toolchain flag and the shared
// toolchains are ToolchainFlags.
func ToolFlags() []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range catalog() {
		if t := e.desc.Tool; t != "go" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

func unavailable(name, reason, detail, stub string) built {
	return built{missing: &Unavailable{Name: name, Reason: reason, Detail: detail}, stub: stub}
}

func buildGoVet(ctx context.Context, cfg Config, runner executor.Runner, goh *govet.Handler, _ map[string]pack.Toolchain) built {
	if cfg.GoExecutable == "" {
		return unavailable(govet.HandlerName, "not_configured", "", pack.ReasonToolNotConfigured)
	}
	if goh == nil {
		detail := "go toolchain is invalid"
		if _, err := govet.New(govet.Config{GoExecutable: cfg.GoExecutable, GoModCache: cfg.GoModCache}); err != nil {
			detail = err.Error()
		}
		return unavailable(govet.HandlerName, "invalid_config", detail, pack.ReasonToolUnavailable)
	}
	if runner == nil || cfg.ProbeDir == "" {
		return unavailable(govet.HandlerName, "probe_failed", "no runner or probe dir", pack.ReasonToolUnavailable)
	}
	if _, err := goh.Probe(ctx, runner, cfg.ProbeDir); err != nil {
		return unavailable(govet.HandlerName, "probe_failed", err.Error(), pack.ReasonToolUnavailable)
	}
	return built{analyzer: goh.Analyzer()}
}

func buildStaticcheck(ctx context.Context, cfg Config, runner executor.Runner, goh *govet.Handler, _ map[string]pack.Toolchain) built {
	exe := cfg.Tools[staticcheck.Tool]
	switch {
	case exe == "":
		return unavailable(staticcheck.Name, "not_configured", "", pack.ReasonToolNotConfigured)
	case goh == nil:
		// staticcheck loads packages through the go command.
		return unavailable(staticcheck.Name, "toolchain_not_configured", "staticcheck needs --go", pack.ReasonToolchainNotConfigured)
	}
	a, err := staticcheck.New(staticcheck.Config{Executable: exe, Go: goh})
	if err != nil {
		return unavailable(staticcheck.Name, "invalid_config", err.Error(), pack.ReasonToolUnavailable)
	}
	if runner == nil || cfg.ProbeDir == "" {
		return unavailable(staticcheck.Name, "probe_failed", "no runner or probe dir", pack.ReasonToolUnavailable)
	}
	if _, err := a.Probe(ctx, runner, cfg.ProbeDir); err != nil {
		return unavailable(staticcheck.Name, "probe_failed", err.Error(), pack.ReasonToolUnavailable)
	}
	return built{analyzer: a}
}

// Handlers probes the configured tools through runner and returns the
// handlers that may be advertised (the deep-review composite, if a baseline
// analyzer is usable) and the analyzers that are not usable.
func Handlers(ctx context.Context, cfg Config, runner executor.Runner) ([]analysis.Handler, []Unavailable) {
	h, missing, err := Composite(ctx, cfg, runner)
	var out []analysis.Handler
	if err == nil && h != nil {
		out = append(out, h)
	}
	return append(out, fixtureHandlers()...), missing
}

// ErrNoBaseline: no required analyzer is usable, so the node must not
// advertise deep-review (a lease would only produce unsupported tasks).
var ErrNoBaseline = errors.New("no baseline analyzer is usable")

// Composite builds the deep-review composite. A go toolchain that is set
// but invalid disables every Go analyzer; a shared toolchain that is not
// configured or fails its probe disables the analyzers that need it.
func Composite(ctx context.Context, cfg Config, runner executor.Runner) (*pack.Composite, []Unavailable, error) {
	return compose(ctx, cfg, runner, catalog())
}

func compose(ctx context.Context, cfg Config, runner executor.Runner, entries []entry) (*pack.Composite, []Unavailable, error) {
	var goh *govet.Handler
	if cfg.GoExecutable != "" {
		if h, err := govet.New(govet.Config{GoExecutable: cfg.GoExecutable, GoModCache: cfg.GoModCache}); err == nil {
			goh = h
		}
	}
	// Shared toolchains are validated once, before any analyzer is built.
	states, missing := validateToolchains(ctx, cfg, runner)
	known := map[string]bool{}
	for _, tc := range toolchainTable() {
		known[tc.name] = true
	}
	var members []pack.Analyzer
	baseline := false
	for _, e := range entries {
		known[e.desc.Tool] = true
		b, handles := gateToolchains(e.desc, cfg, states)
		if b == nil {
			r := e.build(ctx, cfg, runner, goh, handles)
			b = &r
		}
		if b.missing != nil {
			missing = append(missing, *b.missing)
		}
		if b.analyzer != nil {
			members = append(members, b.analyzer)
			baseline = baseline || e.desc.Required
			continue
		}
		stub, err := pack.NotConfigured(e.desc, e.plan, b.stub)
		if err != nil {
			return nil, missing, err
		}
		members = append(members, stub)
	}
	var unknown []string
	for tool, p := range cfg.Tools {
		if p != "" && !known[tool] {
			unknown = append(unknown, tool)
		}
	}
	sort.Strings(unknown)
	for _, tool := range unknown {
		missing = append(missing, Unavailable{Name: tool, Reason: "unknown_tool"})
	}
	if !baseline {
		return nil, missing, ErrNoBaseline
	}
	c, err := pack.NewComposite(members...)
	return c, missing, err
}

// Probes are the env.discover version probes of this build's analyzer
// tools other than the go toolchain (which discovery observes as a
// runtime): one per tool, not_probed when the operator configured no path.
// A tool run by an interpreter toolchain carries that toolchain's path.
func Probes(cfg Config) []pack.ToolProbe { return guardRustProbes(cfg, probesOf(cfg, catalog())) }

func probesOf(cfg Config, entries []entry) []pack.ToolProbe {
	byTool := map[string]*pack.ToolProbe{}
	var order []string
	for _, e := range entries {
		d := e.desc
		if d.Tool == "go" {
			continue
		}
		p := byTool[d.Tool]
		if p == nil {
			p = &pack.ToolProbe{Tool: d.Tool, Executable: cfg.Tools[d.Tool], Args: append([]string{}, d.VersionArgs...), ParseVersion: d.ParseVersion}
			if d.Interpreter != "" {
				p.Interpreter, p.InterpreterExecutable = d.Interpreter, cfg.Tools[d.Interpreter]
			}
			byTool[d.Tool] = p
			order = append(order, d.Tool)
		}
		p.Analyzers = append(p.Analyzers, d.ID)
		p.Ecosystems = append(p.Ecosystems, d.Ecosystem)
	}
	sort.Strings(order)
	out := make([]pack.ToolProbe, 0, len(order))
	for _, t := range order {
		p := byTool[t]
		sort.Strings(p.Analyzers)
		sort.Strings(p.Ecosystems)
		out = append(out, *p)
	}
	return out
}
