package builtin

import (
	"context"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/javapack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

func buildPMD(ctx context.Context, cfg Config, runner executor.Runner, _ *govet.Handler, tools map[string]pack.Toolchain) built {
	return buildJavaAnalyzer(ctx, cfg, runner, tools, javapack.PMD)
}

func buildSpotBugs(ctx context.Context, cfg Config, runner executor.Runner, _ *govet.Handler, tools map[string]pack.Toolchain) built {
	return buildJavaAnalyzer(ctx, cfg, runner, tools, javapack.SpotBugs)
}

func buildJavaAnalyzer(ctx context.Context, cfg Config, runner executor.Runner, tools map[string]pack.Toolchain, name string) built {
	a, err := javapack.New(name, cfg.Tools[name], tools["java"])
	if err == nil && name == javapack.SpotBugs {
		a.ClasspathCatalog = cfg.JavaClasspathCatalog
	}
	return probeBuilt(ctx, cfg, runner, name, a, err)
}

func javaProbes(cfg Config, probes []pack.ToolProbe) []pack.ToolProbe {
	for i := range probes {
		p := &probes[i]
		if (p.Tool != javapack.PMD && p.Tool != javapack.SpotBugs) || p.Executable == "" {
			continue
		}
		java := cfg.Tools["java"]
		args, err := javapack.ToolProbeArguments(p.Tool, p.Executable, java)
		if err != nil {
			p.Executable = "refused"
			continue
		}
		p.Executable, p.Args = java, args
	}
	return probes
}
