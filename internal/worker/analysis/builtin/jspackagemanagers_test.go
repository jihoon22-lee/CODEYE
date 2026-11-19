package builtin

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// pmRunner answers `node --version` and `<node> <script> --version`.
type pmRunner struct {
	node, manager string
	specs         []executor.Spec
}

func (r *pmRunner) Run(_ context.Context, s executor.Spec) executor.Result {
	r.specs = append(r.specs, s)
	if len(s.Args) == 1 && s.Args[0] == "--version" {
		return exitedWith(0, r.node)
	}
	return exitedWith(0, r.manager)
}

// The package managers are tools of their own: no analyzer, one flag each,
// checked as the deps.javascript task uses them (node first, then the entry
// script inside its npm package, then --version).
func TestCheckToolJSPackageManagers(t *testing.T) {
	if !slices.Equal(JSPackageManagerFlags(), []string{"npm", "pnpm", "yarn"}) {
		t.Fatalf("flags %v", JSPackageManagerFlags())
	}
	for _, f := range JSPackageManagerFlags() {
		if !slices.Contains(Tools(), f) {
			t.Fatalf("%s is not a tool", f)
		}
	}
	ctx := context.Background()
	root := t.TempDir()
	nm := filepath.Join(root, "node_modules")
	packtest.WriteTree(t, nm, map[string]string{
		"pnpm/package.json":  "{\"name\":\"pnpm\",\"version\":\"11.28.13\"}\n",
		"pnpm/bin/pnpm.cjs":  "#!/usr/bin/env node\n",
		"other/package.json": "{\"name\":\"other\"}\n",
		"other/bin/pnpm.cjs": "#!/usr/bin/env node\n",
		"shim/pnpm.cmd":      "@echo off\n",
	})
	script := filepath.Join(nm, "pnpm", "bin", "pnpm.cjs")
	node := fakeNode(t)
	probe := t.TempDir()
	cfg := func(tools map[string]string) Config { return Config{ProbeDir: probe, Tools: tools} }

	r := &pmRunner{node: "v24.18.0\n", manager: "11.28.13\n"}
	c := CheckTool(ctx, cfg(map[string]string{"pnpm": script, "node": node}), r, "pnpm")
	if !c.OK() || c.Version != "11.28.13" || !c.Tested {
		t.Fatalf("pnpm: %+v", c)
	}
	last := r.specs[len(r.specs)-1]
	if last.Executable != node || !slices.Equal(last.Args, []string{script, "--version"}) {
		t.Fatalf("probe spec %+v", last)
	}
	for name, tc := range map[string]struct {
		tools  map[string]string
		runner *pmRunner
		reason string
	}{
		"not configured":   {map[string]string{"node": node}, r, "not_configured"},
		"no node":          {map[string]string{"pnpm": script}, r, "toolchain_not_configured"},
		"other package":    {map[string]string{"pnpm": filepath.Join(nm, "other", "bin", "pnpm.cjs"), "node": node}, r, "invalid_config"},
		"command shim":     {map[string]string{"pnpm": filepath.Join(nm, "shim", "pnpm.cmd"), "node": node}, r, "invalid_config"},
		"unknown version":  {map[string]string{"pnpm": script, "node": node}, &pmRunner{node: "v24.18.0\n", manager: "Update available\n"}, "probe_failed"},
		"node unavailable": {map[string]string{"pnpm": script, "node": node}, &pmRunner{node: "nope\n", manager: "11.28.13\n"}, "toolchain_unavailable"},
	} {
		if c := CheckTool(ctx, cfg(tc.tools), tc.runner, "pnpm"); c.OK() || c.Reason != tc.reason {
			t.Errorf("%s: %+v", name, c)
		}
	}
	for in, want := range map[string]string{"11.16.0\n": "11.16.0", "v1.22.22\n": "1.22.22", "4.0.0-rc.1\n": "4.0.0", "pnpm 11\n": ""} {
		if got, ok := parsePackageManagerVersion([]byte(in)); got != want || ok != (want != "") {
			t.Errorf("version %q = %q %v", in, got, ok)
		}
	}
}
