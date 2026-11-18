package builtin

import (
	"context"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/gitleaks"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/jscpd"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/lizard"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// W21 analyzers of ecosystem "any" (docs/45): lizard complexity, jscpd
// duplication (node script) and gitleaks secrets, all extra depth.

func probeBuilt(ctx context.Context, cfg Config, runner executor.Runner, name string, a prober, err error) built {
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

func buildLizard(ctx context.Context, cfg Config, runner executor.Runner, _ *govet.Handler, _ map[string]pack.Toolchain) built {
	exe := cfg.Tools[lizard.Tool]
	if exe == "" {
		return unavailable(lizard.Name, "not_configured", "", pack.ReasonToolNotConfigured)
	}
	a, err := lizard.New(lizard.Config{Executable: exe})
	return probeBuilt(ctx, cfg, runner, lizard.Name, a, err)
}

func buildJscpd(ctx context.Context, cfg Config, runner executor.Runner, _ *govet.Handler, toolchains map[string]pack.Toolchain) built {
	exe := cfg.Tools[jscpd.Tool]
	if exe == "" {
		return unavailable(jscpd.Name, "not_configured", "", pack.ReasonToolNotConfigured)
	}
	a, err := jscpd.New(jscpd.Config{Executable: exe, Node: toolchains[jscpd.Interpreter]})
	return probeBuilt(ctx, cfg, runner, jscpd.Name, a, err)
}

func buildGitleaks(ctx context.Context, cfg Config, runner executor.Runner, _ *govet.Handler, _ map[string]pack.Toolchain) built {
	exe := cfg.Tools[gitleaks.Tool]
	if exe == "" {
		return unavailable(gitleaks.Name, "not_configured", "", pack.ReasonToolNotConfigured)
	}
	a, err := gitleaks.New(gitleaks.Settings{Executable: exe})
	return probeBuilt(ctx, cfg, runner, gitleaks.Name, a, err)
}
