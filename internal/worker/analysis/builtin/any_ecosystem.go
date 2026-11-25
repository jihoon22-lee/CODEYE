package builtin

import (
	"context"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/gitleaks"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/jscpd"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/lizard"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/opengrep"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Analyzers of ecosystem "any" (docs/ANALYZERS.md): lizard complexity, jscpd
// duplication (node script), gitleaks secrets and Opengrep intra-file taint
// rules, all extra depth.

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

func buildOpengrep(ctx context.Context, cfg Config, runner executor.Runner, _ *govet.Handler, _ map[string]pack.Toolchain) built {
	exe := cfg.Tools[opengrep.Tool]
	if exe == "" {
		return unavailable(opengrep.Name, "not_configured", "", pack.ReasonToolNotConfigured)
	}
	a, err := opengrep.New(opengrep.Settings{Executable: exe})
	return probeBuilt(ctx, cfg, runner, opengrep.Name, a, err)
}
