package builtin

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/gitleaks"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/jscpd"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/lizard"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// w21Missing are the W21 analyzers a node without their tool flags
// reports, in catalog order (docs/45).
var w21Missing = []string{"lizard:not_configured", "jscpd:not_configured", "gitleaks:not_configured"}

func w21Task(key string) bool {
	return pack.Owns(lizard.Name, key) || pack.Owns(jscpd.Name, key) || pack.Owns(gitleaks.Name, key)
}

func withoutW21(in []pack.ToolProbe) []pack.ToolProbe {
	var out []pack.ToolProbe
	for _, p := range in {
		if p.Tool != lizard.Tool && p.Tool != jscpd.Tool && p.Tool != gitleaks.Tool {
			out = append(out, p)
		}
	}
	return out
}

// The W21 entries follow the Go section, are extra depth of ecosystem
// "any", and jscpd is the only one that needs the node toolchain.
func TestW21CatalogEntries(t *testing.T) {
	var ids []string
	for _, e := range catalog() {
		ids = append(ids, e.desc.ID)
		if w21Task(e.desc.Name) {
			if e.desc.Required || e.desc.Ecosystem != "any" || e.desc.Validate() != nil {
				t.Fatalf("W21 descriptor %+v", e.desc)
			}
		}
	}
	if !slices.Equal(ids[len(ids)-3:], []string{"any.lizard", "any.jscpd", "any.gitleaks"}) {
		t.Fatalf("catalog order %v", ids)
	}
	if d := jscpd.Descriptor(); !slices.Equal(d.Toolchains, []string{"node"}) || d.Interpreter != "node" {
		t.Fatalf("jscpd toolchains %+v", d)
	}
	if lizard.Descriptor().Toolchains != nil || gitleaks.Descriptor().Toolchains != nil {
		t.Fatal("lizard and gitleaks run directly")
	}
	probes := Probes(Config{Tools: map[string]string{"jscpd": "/opt/jscpd/run-jscpd.js", "node": "/opt/node/bin/node"}})
	for _, p := range probes {
		if p.Tool == jscpd.Tool && (p.Interpreter != "node" || p.InterpreterExecutable != "/opt/node/bin/node" || !slices.Equal(p.Ecosystems, []string{"any"})) {
			t.Fatalf("jscpd probe %+v", p)
		}
		if p.Tool == gitleaks.Tool && (!slices.Equal(p.Args, []string{"version"}) || p.Interpreter != "") {
			t.Fatalf("gitleaks probe %+v", p)
		}
	}
}

// Reasons of the W21 build functions: no path, a toolchain jscpd needs,
// an invalid path and a failing startup probe.
func TestW21BuildReasons(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	script := filepath.Join(dir, "run-jscpd.js")
	if err := os.WriteFile(script, []byte("// not run\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "tool")
	if err := os.WriteFile(exe, []byte("#!/bin/false\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name  string
		tools map[string]string
		want  []string
	}{
		{"jscpd without node", map[string]string{"jscpd": script}, []string{"lizard:not_configured", "jscpd:toolchain_not_configured", "gitleaks:not_configured"}},
		{"relative paths", map[string]string{"lizard": "bin/lizard", "gitleaks": "gitleaks"}, []string{"lizard:invalid_config", "jscpd:not_configured", "gitleaks:invalid_config"}},
		{"probe fails", map[string]string{"lizard": exe, "gitleaks": exe}, []string{"lizard:probe_failed", "jscpd:not_configured", "gitleaks:probe_failed"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			for _, e := range catalog() {
				if !w21Task(e.desc.Name) {
					continue
				}
				b, handles := gateToolchains(e.desc, Config{Tools: c.tools}, map[string]toolchainState{})
				if b == nil {
					r := e.build(ctx, Config{Tools: c.tools, ProbeDir: t.TempDir()}, failingRunner{}, nil, handles)
					b = &r
				}
				if b.analyzer != nil || b.missing == nil {
					t.Fatalf("%s built without a probe: %+v", e.desc.Name, b)
				}
				got = append(got, b.missing.Name+":"+b.missing.Reason)
			}
			if !slices.Equal(got, c.want) {
				t.Fatalf("reasons %v, want %v", got, c.want)
			}
		})
	}
	// The jscpd tool path must be the JavaScript entry script, never a shim.
	if _, err := jscpd.New(jscpd.Config{Executable: exe, Node: pack.Toolchain{Name: "node", Executable: exe, Dir: dir, Version: "24.18.0"}}); err == nil {
		t.Fatal("non-script jscpd path accepted")
	}
}

// With the real tools (CODEYE_TEST_LIZARD, CODEYE_TEST_JSCPD with
// CODEYE_TEST_NODE, CODEYE_TEST_GITLEAKS) the startup probes make the
// adapters members and nothing W21 is reported missing.
func TestW21HandlersProbeRealTools(t *testing.T) {
	tools := map[string]string{"lizard": os.Getenv("CODEYE_TEST_LIZARD"), "jscpd": os.Getenv("CODEYE_TEST_JSCPD"), "node": os.Getenv("CODEYE_TEST_NODE"), "gitleaks": os.Getenv("CODEYE_TEST_GITLEAKS")}
	for k, v := range tools {
		if v == "" {
			t.Skipf("set CODEYE_TEST_LIZARD, CODEYE_TEST_JSCPD, CODEYE_TEST_NODE and CODEYE_TEST_GITLEAKS to probe the real W21 tools (%s missing)", k)
		}
	}
	_, missing, _ := Composite(context.Background(), Config{ProbeDir: t.TempDir(), Tools: tools}, &executor.Executor{})
	for _, m := range missing {
		if w21Task(m.Name) || m.Name == "node" {
			t.Fatalf("W21 tool not usable: %+v", m)
		}
	}
}
