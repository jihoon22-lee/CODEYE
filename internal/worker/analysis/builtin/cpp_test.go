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

// W16 (docs/ANALYZERS.md#기준-분석기와-추가-깊이, docs/ANALYZERS.md#cc-팩): cppcheck is the C/C++ baseline, so a node
// with only --cppcheck advertises deep-review; clang-tidy is extra depth and
// alone advertises nothing. A configured path that is not a file is
// invalid_config and the analyzer stays a member.
func TestCppAnalyzersAdvertiseOnlyWithTheBaseline(t *testing.T) {
	ctx := context.Background()
	probe := t.TempDir()
	bad := filepath.Join(t.TempDir(), "missing")
	hs, missing := Handlers(ctx, Config{ProbeDir: probe, Tools: map[string]string{"cppcheck": bad, "clang-tidy": bad}}, &executor.Executor{})
	if len(hs) != 0 || !slices.Contains(reasons(missing), "cppcheck:invalid_config") || !slices.Contains(reasons(missing), "clang-tidy:invalid_config") {
		t.Fatalf("invalid: %d %v", len(hs), reasons(missing))
	}
	probes := Probes(Config{Tools: map[string]string{"cppcheck": "/cc", "clang-tidy": "/ct"}})
	byTool := map[string]pack.ToolProbe{}
	for _, p := range probes {
		byTool[p.Tool] = p
	}
	if p := byTool["cppcheck"]; p.Executable != "/cc" || !slices.Equal(p.Analyzers, []string{"cpp.cppcheck"}) || !slices.Equal(p.Ecosystems, []string{"cpp"}) || !slices.Equal(p.Args, []string{"--version"}) {
		t.Fatalf("cppcheck probe %+v", p)
	}
	if p := byTool["clang-tidy"]; p.Executable != "/ct" || !slices.Equal(p.Analyzers, []string{"cpp.clang_tidy"}) || p.Interpreter != "" {
		t.Fatalf("clang-tidy probe %+v", p)
	}
	cc, ct := os.Getenv("CODEYE_TEST_CPPCHECK"), os.Getenv("CODEYE_TEST_CLANG_TIDY")
	if cc == "" || ct == "" {
		t.Skip("set CODEYE_TEST_CPPCHECK and CODEYE_TEST_CLANG_TIDY for the real probes")
	}
	hs, missing = Handlers(ctx, Config{ProbeDir: probe, Tools: map[string]string{"cppcheck": cc}}, &executor.Executor{})
	if len(hs) != 1 || slices.Contains(reasons(missing), "cppcheck:not_configured") || !slices.Contains(reasons(missing), "clang-tidy:not_configured") {
		t.Fatalf("cppcheck only: %d %v", len(hs), reasons(missing))
	}
	hs, missing = Handlers(ctx, Config{ProbeDir: probe, Tools: map[string]string{"clang-tidy": ct}}, &executor.Executor{})
	if len(hs) != 0 || slices.Contains(reasons(missing), "clang-tidy:not_configured") {
		t.Fatalf("clang-tidy only advertised: %d %v", len(hs), reasons(missing))
	}
	c, _, err := Composite(ctx, Config{ProbeDir: probe, Tools: map[string]string{"cppcheck": cc, "clang-tidy": ct}}, &executor.Executor{})
	if err != nil {
		t.Fatal(err)
	}
	// Catalog order (go, cpp, python, javascript, rust, any): the C/C++
	// members follow the Go members; other packs' members are not compared.
	var ids, cppIDs []string
	for _, d := range c.Descriptors() {
		ids = append(ids, d.ID)
		if d.Ecosystem == "cpp" {
			cppIDs = append(cppIDs, d.ID)
		}
	}
	if !slices.Equal(cppIDs, []string{"cpp.cppcheck", "cpp.clang_tidy"}) || slices.Index(ids, "go.staticcheck") > slices.Index(ids, "cpp.cppcheck") ||
		slices.Index(ids, "cpp.clang_tidy") != slices.Index(ids, "cpp.cppcheck")+1 {
		t.Fatalf("members %v", ids)
	}
	if entries, _ := os.ReadDir(probe); len(entries) != 0 {
		t.Fatalf("probe left %v", entries)
	}
}
