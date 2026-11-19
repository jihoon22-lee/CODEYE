package builtin

import (
	"context"
	"slices"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/cpp"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/eslint"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/gitleaks"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/jscpd"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/lizard"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/python"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/rustpack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/staticcheck"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/tsc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// ToolCheck is the outcome of validating one operator tool path the way the
// worker validates it at startup: the same toolchain gate, the same
// analyzer construction (shape rules such as an absolute #! line, the JS
// entry script or the refusal of a rustup proxy) and the same version probe.
// Tool detection (internal/toolfind) uses it so that what it reports is what
// the agent will run.
type ToolCheck struct {
	Tool    string
	Version string
	// Tested reports a version of the series the parsers were captured
	// against. A tool without a series (node) is tested once its version
	// output is recognized.
	Tested bool
	// Series is the tested series for messages ("" when there is none).
	Series string
	// Reason is empty when the tool is usable, otherwise the Unavailable
	// reason (invalid_config, probe_failed, not_configured,
	// toolchain_not_configured, toolchain_unavailable, unknown_tool).
	Reason string
	// Detail is the failure detail; it names no path.
	Detail string
}

// OK reports a usable tool.
func (c ToolCheck) OK() bool { return c.Reason == "" }

// testedInfo describes the release series a tool's parser is pinned to.
type testedInfo struct {
	series string
	tested func(version string) bool
}

func seriesOf(series string) testedInfo {
	return testedInfo{series: series, tested: func(v string) bool { return pack.SeriesTested(v, series) }}
}

// testedTable has one entry per tool name of the catalog, per shared
// toolchain and for go (TestEveryToolHasATestedSeries). The series are the
// constants of the packs, so a parser re-capture changes them in one place.
func testedTable() map[string]testedInfo {
	return map[string]testedInfo{
		"go":                {series: "1.27", tested: govet.GoVersionTested},
		staticcheck.Tool:    {series: staticcheck.TestedRelease, tested: staticcheck.VersionTested},
		cpp.CppcheckTool:    {series: cpp.CppcheckTestedRelease, tested: cpp.CppcheckVersionTested},
		cpp.ClangTidyTool:   {series: cpp.ClangTidyTestedRelease, tested: cpp.ClangTidyVersionTested},
		python.RuffTool:     seriesOf(python.RuffTestedSeries),
		python.MypyTool:     seriesOf(python.MypyTestedSeries),
		python.PyrightTool:  {series: python.PyrightTestedVersion, tested: func(v string) bool { return v == python.PyrightTestedVersion }},
		python.BanditTool:   seriesOf(python.BanditTestedSeries),
		eslint.Tool:         {series: eslint.TestedSeries, tested: eslint.VersionTested},
		tsc.Tool:            {series: tsc.TestedSeries, tested: tsc.VersionTested},
		rustpack.CheckTool:  {series: rustpack.TestedCargo, tested: rustpack.CargoVersionTested},
		rustpack.ClippyTool: {series: rustpack.TestedClippy, tested: rustpack.ClippyVersionTested},
		lizard.Tool:         {series: lizard.TestedSeries, tested: lizard.VersionTested},
		jscpd.Tool:          {series: jscpd.TestedSeries, tested: jscpd.VersionTested},
		gitleaks.Tool:       {series: gitleaks.TestedSeries, tested: gitleaks.VersionTested},
		"node":              {},
	}
}

// CheckTool validates the path configured for tool in cfg (cfg.GoExecutable
// for "go", cfg.Tools[tool] otherwise) and returns its version. The shared
// toolchain a tool needs (node) and the go toolchain Staticcheck needs are
// taken from cfg and validated first, so the precedence of reasons is the one
// of Composite.
func CheckTool(ctx context.Context, cfg Config, runner executor.Runner, tool string) ToolCheck {
	info, known := testedTable()[tool]
	out := ToolCheck{Tool: tool, Series: info.series}
	fail := func(reason, detail string) ToolCheck {
		out.Reason, out.Detail = reason, detail
		return out
	}
	if !known {
		return fail("unknown_tool", "")
	}
	finish := func(b built) ToolCheck {
		if b.missing != nil {
			return fail(b.missing.Reason, b.missing.Detail)
		}
		out.Version = b.version
		out.Tested = info.tested == nil || info.tested(b.version)
		return out
	}
	for _, tc := range toolchainTable() {
		if tc.name != tool {
			continue
		}
		exe := cfg.Tools[tool]
		if exe == "" {
			return fail("not_configured", "")
		}
		h, reason, err := probeToolchain(ctx, runner, cfg.ProbeDir, tc, exe)
		if err != nil {
			return fail(reason, err.Error())
		}
		return finish(built{version: h.Version})
	}
	goh := goHandler(cfg)
	states, _ := validateToolchains(ctx, cfg, runner)
	for _, e := range catalog() {
		if e.desc.Tool != tool {
			continue
		}
		b, handles := gateToolchains(e.desc, cfg, states)
		if b == nil {
			r := e.build(ctx, cfg, runner, goh, handles)
			b = &r
		}
		return finish(*b)
	}
	return fail("unknown_tool", "")
}

// Tools lists every tool name CheckTool knows, sorted: the catalog's tools,
// "go" and the shared toolchains.
func Tools() []string {
	out := append([]string{"go"}, ToolFlags()...)
	out = append(out, ToolchainFlags()...)
	slices.Sort(out)
	return slices.Compact(out)
}
