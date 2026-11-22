package main

import (
	"context"
	"maps"
	"os"
	"path/filepath"

	"github.com/jihoon22-lee/CODEYE/internal/instance"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review"
	"github.com/jihoon22-lee/CODEYE/internal/workerproto"
)

type workerRuntimePaths struct {
	hello   *workerproto.Hello
	current map[string]string
	caller  review.Caller
}

func helloPaths(h *workerproto.Hello) map[string]string {
	paths := map[string]string{}
	for _, name := range instance.RuntimeToolNames {
		p := h.AnalyzerTools[name]
		switch name {
		case "go":
			p = h.GoExecutable
		case "python":
			p = h.TestPackageManagers[name]
			if p == "" && h.Dependencies != nil {
				p = h.Dependencies.PackageManagers[name]
			}
		}
		if p != "" {
			paths[name] = p
		}
	}
	return paths
}

func applyWorkerPaths(h *workerproto.Hello, paths map[string]string, cfg *instance.Config) workerproto.Hello {
	next := *h
	next.AnalyzerTools = make(map[string]string, len(h.AnalyzerTools))
	maps.Copy(next.AnalyzerTools, h.AnalyzerTools)
	for _, name := range []string{"cargo", "java", "node"} {
		if paths[name] == "" {
			delete(next.AnalyzerTools, name)
		} else {
			next.AnalyzerTools[name] = paths[name]
		}
	}
	if paths["cargo"] != h.AnalyzerTools["cargo"] && cfg.Agent.AutoDetectEnabled() && cfg.Agent.Tools["clippy"] == "" {
		delete(next.AnalyzerTools, "clippy")
		if cargo, err := filepath.EvalSymlinks(paths["cargo"]); err == nil && paths["cargo"] != "" {
			bin := filepath.Join(filepath.Dir(cargo), "cargo-clippy")
			if usableExecutable(bin) {
				next.AnalyzerTools["clippy"] = bin
			}
		}
	}
	next.GoExecutable = paths["go"]
	next.TestPackageManagers = map[string]string{}
	maps.Copy(next.TestPackageManagers, h.TestPackageManagers)
	next.TestPackageManagers["python"] = paths["python"]
	if h.Dependencies != nil {
		deps := *h.Dependencies
		deps.PackageManagers = map[string]string{}
		maps.Copy(deps.PackageManagers, h.Dependencies.PackageManagers)
		deps.PackageManagers["python"] = paths["python"]
		next.Dependencies = &deps
	}
	return next
}

// Only the local installation supplies the trusted configuration reference.
// A worker runs one attempt at a time, so tasks in flight keep their registry.
func (s *workerRuntimePaths) refresh(ctx context.Context, runner executor.Runner) (*analysis.Registry, error) {
	h := s.hello
	if h == nil || h.ModelSettings == nil || !h.AdvertiseWithoutBaseline {
		return nil, nil
	}
	layout := instance.Layout{Root: h.ModelSettings.StateDir}
	paths, err := layout.RuntimeDefaults()
	// Standalone agents may have no managed startup metadata.
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	cfg, err := instance.LoadForStateDir(h.ModelSettings.InstancePath, knownTools(), layout.Root)
	if err != nil {
		return nil, err
	}
	if cfg.StateDir != layout.Root {
		return nil, os.ErrInvalid
	}
	if !cfg.Agent.AutoDetectEnabled() {
		clear(paths)
	}
	maps.Copy(paths, cfg.RuntimePaths())
	if maps.Equal(paths, s.current) {
		return nil, nil
	}
	next := applyWorkerPaths(h, paths, cfg)
	handlers, _, err := workerHandlersWithCaller(ctx, &next, runner, s.caller)
	if err != nil {
		return nil, err
	}
	registry, err := analysis.NewRegistry(handlers...)
	if err == nil {
		s.current = paths
	}
	return registry, err
}
