package builtin

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// jsReasons are the rows of the JS/TS analyzers.
func jsReasons(missing []Unavailable) []string {
	var out []string
	for _, m := range missing {
		if m.Name == "eslint" || m.Name == "tsc" || m.Name == "eslint-typed" {
			out = append(out, m.Name+":"+m.Reason)
		}
	}
	return out
}

// The JS/TS analyzers need --node: without it they are not configured (or
// toolchain_not_configured when only the script is given), and the tool
// flags and probes carry the node interpreter.
func TestJavaScriptEntriesGateOnNode(t *testing.T) {
	ctx := context.Background()
	_, missing := Handlers(ctx, Config{}, &executor.Executor{})
	if got := jsReasons(missing); !slices.Equal(got, []string{"eslint:not_configured", "tsc:not_configured", "eslint-typed:not_configured"}) {
		t.Fatalf("unconfigured %v", got)
	}
	_, missing = Handlers(ctx, Config{Tools: map[string]string{"eslint": "/x/eslint.js", "tsc": "/x/tsc"}}, &executor.Executor{})
	if got := jsReasons(missing); !slices.Equal(got, []string{"eslint:toolchain_not_configured", "tsc:toolchain_not_configured", "eslint-typed:toolchain_not_configured"}) {
		t.Fatalf("without node %v", got)
	}
	flags := ToolFlags()
	if !slices.Contains(flags, "eslint") || !slices.Contains(flags, "tsc") {
		t.Fatalf("flags %v", flags)
	}
	probes := Probes(Config{Tools: map[string]string{"eslint": "/x/eslint.js", "node": "/n/node"}})
	i := slices.IndexFunc(probes, func(p pack.ToolProbe) bool { return p.Tool == "eslint" })
	if i < 0 || probes[i].Interpreter != "node" || probes[i].InterpreterExecutable != "/n/node" || !slices.Equal(probes[i].Analyzers, []string{"javascript.eslint", "javascript.eslint_typed"}) {
		t.Fatalf("probes %+v", probes)
	}
}

// With CODEYE_TEST_NODE, CODEYE_TEST_ESLINT and CODEYE_TEST_TSC the real
// probes make the three analyzers members; the JS/TS baseline alone is
// enough to advertise deep-review.
func TestHandlersProbeRealJavaScriptTools(t *testing.T) {
	node, eslint, tsc := os.Getenv("CODEYE_TEST_NODE"), os.Getenv("CODEYE_TEST_ESLINT"), os.Getenv("CODEYE_TEST_TSC")
	if node == "" || eslint == "" || tsc == "" {
		t.Skip("set CODEYE_TEST_NODE, CODEYE_TEST_ESLINT and CODEYE_TEST_TSC to run the real JS/TS probes")
	}
	probe := t.TempDir()
	hs, missing := Handlers(context.Background(), Config{ProbeDir: probe, Tools: map[string]string{"node": node, "eslint": eslint, "tsc": tsc}}, &executor.Executor{})
	if len(hs) != 1 || len(jsReasons(missing)) != 0 {
		t.Fatalf("handlers %d missing %+v", len(hs), missing)
	}
	c := hs[0].(*pack.Composite)
	var ids []string
	for _, d := range c.Descriptors() {
		if d.Ecosystem == "javascript" {
			ids = append(ids, d.ID)
		}
	}
	if !slices.Equal(ids, []string{"javascript.eslint", "javascript.tsc", "javascript.eslint_typed"}) {
		t.Fatalf("members %v", ids)
	}
	if entries, _ := os.ReadDir(probe); len(entries) != 0 {
		t.Fatalf("probe left files: %v", entries)
	}
	// A shim or a script outside its npm package is invalid, never run.
	_, missing = Handlers(context.Background(), Config{ProbeDir: probe, Tools: map[string]string{"node": node, "eslint": filepath.Join(filepath.Dir(node), "npm"), "tsc": tsc}}, &executor.Executor{})
	if got := jsReasons(missing); !slices.Contains(got, "eslint:invalid_config") {
		t.Fatalf("shim %v", got)
	}
}
