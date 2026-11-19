package builtin

import (
	"context"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/python"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// The Python pack (W17, docs/ANALYZERS.md): ruff is the baseline; mypy and pyright are
// alternative type checkers (both run only when the operator configured both,
// which the agent accepts only with --python-both-type-checkers); bandit is
// security depth.

// prober is the startup version probe every Python (W17) and W21 adapter
// implements.
type prober interface {
	pack.Analyzer
	Probe(ctx context.Context, runner executor.Runner, dir string) (string, error)
}

// buildPython configures one Python adapter from its --<tool> path.
func buildPython(ctx context.Context, cfg Config, runner executor.Runner, d pack.Descriptor, node *pack.Toolchain,
	newFn func(python.Config) (prober, error)) built {
	exe := cfg.Tools[d.Tool]
	if exe == "" {
		return unavailable(d.Name, "not_configured", "", pack.ReasonToolNotConfigured)
	}
	pc := python.Config{Executable: exe, Node: node}
	if d.ID == python.MypyID || d.ID == python.PyrightID {
		pc.BothTypeCheckers = cfg.Tools[python.MypyTool] != "" && cfg.Tools[python.PyrightTool] != ""
	}
	a, err := newFn(pc)
	if err != nil {
		return unavailable(d.Name, "invalid_config", err.Error(), pack.ReasonToolUnavailable)
	}
	if runner == nil || cfg.ProbeDir == "" {
		return unavailable(d.Name, "probe_failed", "no runner or probe dir", pack.ReasonToolUnavailable)
	}
	if _, err := a.Probe(ctx, runner, cfg.ProbeDir); err != nil {
		return unavailable(d.Name, "probe_failed", err.Error(), pack.ReasonToolUnavailable)
	}
	return built{analyzer: a}
}

func buildRuff(ctx context.Context, cfg Config, runner executor.Runner, _ *govet.Handler, _ map[string]pack.Toolchain) built {
	return buildPython(ctx, cfg, runner, python.RuffDescriptor(), nil, func(c python.Config) (prober, error) { return python.NewRuff(c) })
}

func buildMypy(ctx context.Context, cfg Config, runner executor.Runner, _ *govet.Handler, _ map[string]pack.Toolchain) built {
	return buildPython(ctx, cfg, runner, python.MypyDescriptor(), nil, func(c python.Config) (prober, error) { return python.NewMypy(c) })
}

func buildPyright(ctx context.Context, cfg Config, runner executor.Runner, _ *govet.Handler, tcs map[string]pack.Toolchain) built {
	node, ok := tcs["node"]
	if !ok {
		// gateToolchains passes the node handle; reaching here without it is
		// a wiring error, never a silent run without the toolchain.
		return unavailable(python.PyrightName, "toolchain_unavailable", "node toolchain is unavailable", pack.ReasonToolUnavailable)
	}
	return buildPython(ctx, cfg, runner, python.PyrightDescriptor(), &node, func(c python.Config) (prober, error) { return python.NewPyright(c) })
}

func buildBandit(ctx context.Context, cfg Config, runner executor.Runner, _ *govet.Handler, _ map[string]pack.Toolchain) built {
	return buildPython(ctx, cfg, runner, python.BanditDescriptor(), nil, func(c python.Config) (prober, error) { return python.NewBandit(c) })
}
