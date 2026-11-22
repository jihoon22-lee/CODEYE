package builtin

import (
	"context"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/cpp"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// C/C++ pack (docs/ANALYZERS.md): operator-installed native tools with
// no separate build system or repository compiler invocation.

func buildCpp(ctx context.Context, cfg Config, runner executor.Runner, name, tool string, mk func(cpp.Config) (prober, error)) built {
	exe := cfg.Tools[tool]
	if exe == "" {
		return unavailable(name, "not_configured", "", pack.ReasonToolNotConfigured)
	}
	a, err := mk(cpp.Config{Executable: exe})
	return probeBuilt(ctx, cfg, runner, name, a, err)
}

func buildCppcheck(ctx context.Context, cfg Config, runner executor.Runner, _ *govet.Handler, _ map[string]pack.Toolchain) built {
	return buildCpp(ctx, cfg, runner, cpp.CppcheckName, cpp.CppcheckTool, func(c cpp.Config) (prober, error) { return cpp.NewCppcheck(c) })
}

func buildClangTidy(ctx context.Context, cfg Config, runner executor.Runner, _ *govet.Handler, _ map[string]pack.Toolchain) built {
	return buildCpp(ctx, cfg, runner, cpp.ClangTidyName, cpp.ClangTidyTool, func(c cpp.Config) (prober, error) { return cpp.NewClangTidy(c) })
}

func buildGCCAnalyzer(ctx context.Context, cfg Config, runner executor.Runner, _ *govet.Handler, _ map[string]pack.Toolchain) built {
	return buildCpp(ctx, cfg, runner, cpp.GCCAnalyzerName, cpp.GCCAnalyzerTool, func(c cpp.Config) (prober, error) { return cpp.NewGCCAnalyzer(c) })
}
