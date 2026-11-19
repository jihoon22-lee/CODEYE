package builtin

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Every tool name of the catalog, "go" and the shared toolchains has a tested
// series entry, so detection cannot report a tool without saying whether its
// version is one the parsers were captured against.
func TestEveryToolHasATestedSeries(t *testing.T) {
	table := testedTable()
	for _, name := range Tools() {
		if _, ok := table[name]; !ok {
			t.Errorf("%s has no tested series", name)
		}
	}
	for name := range table {
		if !slices.Contains(Tools(), name) {
			t.Errorf("%s is in the tested series table but is not a tool of this build", name)
		}
	}
	if len(Tools()) < 16 {
		t.Fatalf("tools %v", Tools())
	}
}

func fakeTool(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// CheckTool runs the construction and the startup probe the worker runs: it
// returns the version and whether it is of the tested series, refuses a shape
// the analyzer refuses, and leaves nothing in the probe directory.
func TestCheckToolVersionsAndReasons(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script tools")
	}
	ctx := context.Background()
	dir, probe := t.TempDir(), t.TempDir()
	cfg := func(tool, path string) Config {
		return Config{ProbeDir: probe, Tools: map[string]string{tool: path}}
	}
	run := &executor.Executor{}
	ok := CheckTool(ctx, cfg("ruff", fakeTool(t, dir, "ruff", "printf 'ruff 0.16.3\\n'")), run, "ruff")
	if !ok.OK() || ok.Version != "0.16.3" || !ok.Tested || ok.Series != "0.16" {
		t.Fatalf("tested: %+v", ok)
	}
	old := CheckTool(ctx, cfg("ruff", fakeTool(t, dir, "ruff-old", "printf 'ruff 0.15.2\\n'")), run, "ruff")
	if !old.OK() || old.Version != "0.15.2" || old.Tested {
		t.Fatalf("untested: %+v", old)
	}
	bad := CheckTool(ctx, cfg("ruff", fakeTool(t, dir, "ruff-bad", "printf 'hello\\n'")), run, "ruff")
	if bad.OK() || bad.Reason != "probe_failed" || bad.Detail == "" {
		t.Fatalf("unrecognized: %+v", bad)
	}
	// An env-shebang script is refused by the Python adapter before anything runs.
	envShebang := filepath.Join(dir, "mypy")
	if err := os.WriteFile(envShebang, []byte("#!/usr/bin/env python3\nprint('mypy 2.4.1')\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if c := CheckTool(ctx, cfg("mypy", envShebang), run, "mypy"); c.OK() || c.Reason != "invalid_config" {
		t.Fatalf("env shebang: %+v", c)
	}
	// A tool that needs node says so when node is not given.
	if c := CheckTool(ctx, cfg("pyright", filepath.Join(dir, "index.js")), run, "pyright"); c.Reason != "toolchain_not_configured" {
		t.Fatalf("node missing: %+v", c)
	}
	if c := CheckTool(ctx, Config{ProbeDir: probe}, run, "lizard"); c.Reason != "not_configured" {
		t.Fatalf("not configured: %+v", c)
	}
	if c := CheckTool(ctx, Config{ProbeDir: probe}, run, "nope"); c.Reason != "unknown_tool" {
		t.Fatalf("unknown: %+v", c)
	}
	// node: the toolchain probe, no series.
	node := fakeTool(t, dir, "node", "echo v24.18.0")
	if c := CheckTool(ctx, cfg("node", node), run, "node"); !c.OK() || c.Version != "24.18.0" || !c.Tested {
		t.Fatalf("node: %+v", c)
	}
	// cargo: the rustup proxy shape is refused by the same construction.
	rust := fakeRust(t, true)
	if c := CheckTool(ctx, cfg("cargo", filepath.Join(rust, "cargo")), run, "cargo"); c.OK() || c.Reason != "invalid_config" {
		t.Fatalf("proxy: %+v", c)
	}
	rust = fakeRust(t, false)
	if c := CheckTool(ctx, cfg("cargo", filepath.Join(rust, "cargo")), run, "cargo"); !c.OK() || c.Version != "1.99.1 (5f94df478 2026-08-27)" || !c.Tested {
		t.Fatalf("cargo: %+v", c)
	}
	if entries, _ := os.ReadDir(probe); len(entries) != 0 {
		t.Fatalf("probes left %v", entries)
	}
}

// With AdvertiseWithoutBaseline a node without any usable tool still
// advertises the analysis capability, and the tasks of the tools it lacks are
// recorded unsupported (tool_not_configured), not run and not passed.
func TestAdvertiseWithoutBaseline(t *testing.T) {
	ctx := context.Background()
	probe := t.TempDir()
	if hs, _ := Handlers(ctx, Config{ProbeDir: probe}, &executor.Executor{}); len(hs) != 0 {
		t.Fatalf("default advertises %d handlers", len(hs))
	}
	cfg := Config{ProbeDir: probe, AdvertiseWithoutBaseline: true}
	hs, missing := Handlers(ctx, cfg, &executor.Executor{})
	if len(hs) != 1 || hs[0].Info().Name != pack.CompositeName || !slices.Contains(reasons(missing), "go-vet:not_configured") {
		t.Fatalf("%d handlers, missing %v", len(hs), reasons(missing))
	}
	c, ok := hs[0].(*pack.Composite)
	if !ok {
		t.Fatalf("handler %T", hs[0])
	}
	job, _ := snapshotJobs(t, map[string]string{"go.mod": "module example.test/m\n\ngo 1.22\n", "a.go": "package m\n", "pyproject.toml": "[project]\nname='x'\n", "a.py": "x = 1\n"})
	specs, err := c.Plan(ctx, job)
	if err != nil || len(specs) == 0 {
		t.Fatalf("plan %v %v", specs, err)
	}
	for _, spec := range specs {
		res := c.Run(ctx, job, spec, failingRunner{})
		if res.Status != domain.TaskUnsupported || res.Reason != pack.ReasonToolNotConfigured || len(res.Executions) != 0 {
			t.Fatalf("%s: %+v", spec.Key, res)
		}
	}
}
