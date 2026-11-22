package builtin

import (
	"context"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/rustpack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// buildCargoCheck configures the Rust baseline analyzer from --cargo (the
// absolute cargo of a toolchain bin directory; a rustup proxy is refused).
func buildCargoCheck(ctx context.Context, cfg Config, runner executor.Runner, _ *govet.Handler, _ map[string]pack.Toolchain) built {
	return buildCargoAnalyzer(ctx, cfg, runner, rustpack.KindCheck, rustpack.CheckName)
}

func buildRustdoc(ctx context.Context, cfg Config, runner executor.Runner, _ *govet.Handler, _ map[string]pack.Toolchain) built {
	return buildCargoAnalyzer(ctx, cfg, runner, rustpack.KindDoc, "cargo-doc")
}

func buildCargoAnalyzer(ctx context.Context, cfg Config, runner executor.Runner, kind rustpack.Kind, name string) built {
	exe := cfg.Tools[rustpack.CheckTool]
	if exe == "" {
		return unavailable(name, "not_configured", "", pack.ReasonToolNotConfigured)
	}
	a, err := rustpack.New(kind, rustpack.Config{Cargo: exe, ToolPath: cfg.ToolPath, DependencyCacheRoot: cfg.DependencyCacheRoot})
	return probeBuilt(ctx, cfg, runner, name, a, err)
}

// buildClippy configures the Rust extra-depth analyzer from --clippy (the
// cargo-clippy of the same toolchain bin directory as --cargo).
func buildClippy(ctx context.Context, cfg Config, runner executor.Runner, _ *govet.Handler, _ map[string]pack.Toolchain) built {
	exe, cargo := cfg.Tools[rustpack.ClippyTool], cfg.Tools[rustpack.CheckTool]
	switch {
	case exe == "":
		return unavailable(rustpack.ClippyName, "not_configured", "", pack.ReasonToolNotConfigured)
	case cargo == "":
		// clippy is run by the toolchain's cargo.
		return unavailable(rustpack.ClippyName, "toolchain_not_configured", "clippy needs --cargo", pack.ReasonToolchainNotConfigured)
	}
	a, err := rustpack.New(rustpack.KindClippy, rustpack.Config{Cargo: cargo, Clippy: exe, ToolPath: cfg.ToolPath, DependencyCacheRoot: cfg.DependencyCacheRoot})
	return probeBuilt(ctx, cfg, runner, rustpack.ClippyName, a, err)
}

// guardRustProbes keeps env.discover from executing a Rust tool path the pack
// refuses (a rustup proxy would pick a toolchain from the repository and may
// download one). The path of a refused tool is replaced by a non-absolute
// marker: discovery records probe_failed and starts nothing.
func guardRustProbes(cfg Config, probes []pack.ToolProbe) []pack.ToolProbe {
	for i := range probes {
		p := &probes[i]
		if p.Executable == "" {
			continue
		}
		var err error
		switch p.Tool {
		case rustpack.CheckTool:
			_, err = rustpack.New(rustpack.KindCheck, rustpack.Config{Cargo: p.Executable})
		case rustpack.ClippyTool:
			_, err = rustpack.New(rustpack.KindClippy, rustpack.Config{Cargo: cfg.Tools[rustpack.CheckTool], Clippy: p.Executable})
		}
		if err != nil {
			p.Executable = "refused"
		}
	}
	return probes
}
