package builtin

import (
	"context"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/eslint"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/tsc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// The W18 JS/TS pack's catalog entries (docs/43). Each analyzer runs its
// operator script through the shared node toolchain; builtin builds it only
// when --node is configured and probed (gateToolchains), with the handle.

func eslintEntry() entry {
	return entry{desc: eslint.Descriptor(), plan: eslint.Plan, build: buildESLint(false)}
}

func tscEntry() entry {
	return entry{desc: tsc.Descriptor(), plan: tsc.Plan, build: buildTsc}
}

func eslintTypedEntry() entry {
	return entry{desc: eslint.TypedDescriptor(), plan: eslint.TypedPlan, build: buildESLint(true)}
}

// buildESLint configures the baseline (typed=false) or type-aware ESLint
// analyzer and runs its version probe.
func buildESLint(typed bool) buildFunc {
	return func(ctx context.Context, cfg Config, runner executor.Runner, _ *govet.Handler, toolchains map[string]pack.Toolchain) built {
		name, newA := eslint.Name, eslint.New
		if typed {
			name, newA = eslint.TypedName, eslint.NewTyped
		}
		a, err := newA(eslint.Config{Script: cfg.Tools[eslint.Tool], Node: toolchains["node"]})
		if err != nil {
			return unavailable(name, "invalid_config", err.Error(), pack.ReasonToolUnavailable)
		}
		if runner == nil || cfg.ProbeDir == "" {
			return unavailable(name, "probe_failed", "no runner or probe dir", pack.ReasonToolUnavailable)
		}
		if _, err := a.Probe(ctx, runner, cfg.ProbeDir); err != nil {
			return unavailable(name, "probe_failed", err.Error(), pack.ReasonToolUnavailable)
		}
		return built{analyzer: a}
	}
}

// buildTsc configures the tsc analyzer and runs its version probe.
func buildTsc(ctx context.Context, cfg Config, runner executor.Runner, _ *govet.Handler, toolchains map[string]pack.Toolchain) built {
	a, err := tsc.New(tsc.Config{Script: cfg.Tools[tsc.Tool], Node: toolchains["node"]})
	if err != nil {
		return unavailable(tsc.Name, "invalid_config", err.Error(), pack.ReasonToolUnavailable)
	}
	if runner == nil || cfg.ProbeDir == "" {
		return unavailable(tsc.Name, "probe_failed", "no runner or probe dir", pack.ReasonToolUnavailable)
	}
	if _, err := a.Probe(ctx, runner, cfg.ProbeDir); err != nil {
		return unavailable(tsc.Name, "probe_failed", err.Error(), pack.ReasonToolUnavailable)
	}
	return built{analyzer: a}
}
