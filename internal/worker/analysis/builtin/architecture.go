package builtin

import (
	"context"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/architecture"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

func buildDependencyCruiser(ctx context.Context, cfg Config, runner executor.Runner, _ *govet.Handler, tools map[string]pack.Toolchain) built {
	node := tools["node"]
	name := architecture.DependencyCruiser
	a, err := architecture.New(name, cfg.Tools[name], &node)
	return probeBuilt(ctx, cfg, runner, name, a, err)
}

func buildImportLinter(ctx context.Context, cfg Config, runner executor.Runner, _ *govet.Handler, _ map[string]pack.Toolchain) built {
	name := architecture.ImportLinter
	if cfg.Tools[name] == "" {
		return unavailable(name, "not_configured", "", pack.ReasonToolNotConfigured)
	}
	a, err := architecture.New(name, cfg.Tools[name], nil)
	return probeBuilt(ctx, cfg, runner, name, a, err)
}
