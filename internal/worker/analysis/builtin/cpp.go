package builtin

import (
	"context"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/cpp"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// W16 C/C++ pack (docs/ANALYZERS.md): both tools are single native executables that
// need no shared toolchain; neither runs a compiler or build system.

type cppProber interface {
	pack.Analyzer
	Probe(ctx context.Context, runner executor.Runner, dir string) (string, error)
}

func buildCpp(ctx context.Context, cfg Config, runner executor.Runner, name, tool string, mk func(cpp.Config) (cppProber, error)) built {
	exe := cfg.Tools[tool]
	if exe == "" {
		return unavailable(name, "not_configured", "", pack.ReasonToolNotConfigured)
	}
	a, err := mk(cpp.Config{Executable: exe})
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

func buildCppcheck(ctx context.Context, cfg Config, runner executor.Runner, _ *govet.Handler, _ map[string]pack.Toolchain) built {
	return buildCpp(ctx, cfg, runner, cpp.CppcheckName, cpp.CppcheckTool, func(c cpp.Config) (cppProber, error) { return cpp.NewCppcheck(c) })
}

func buildClangTidy(ctx context.Context, cfg Config, runner executor.Runner, _ *govet.Handler, _ map[string]pack.Toolchain) built {
	return buildCpp(ctx, cfg, runner, cpp.ClangTidyName, cpp.ClangTidyTool, func(c cpp.Config) (cppProber, error) { return cpp.NewClangTidy(c) })
}
