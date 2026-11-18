package eslint

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/jsts/jststest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

type neverRunner struct{ t *testing.T }

func (r neverRunner) Run(_ context.Context, s executor.Spec) executor.Result {
	r.t.Errorf("a refused task started %v", s.Args)
	return executor.Result{Status: executor.StatusStartFailed, Cleanup: executor.CleanupNotStarted}
}

func fakeAnalyzer(t *testing.T, typed bool) *Analyzer {
	t.Helper()
	script, _, node := jststest.FakeTools(t)
	a, err := newAnalyzer(Config{Script: script, Node: node}, typed)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// The type-aware task reads the module's tsconfig chain through
// typescript-eslint: the chain checks of tsc apply (docs/43 §3.2), before
// ESLint runs, with option names only.
func TestTypedTsconfigChainRefused(t *testing.T) {
	a := fakeAnalyzer(t, true)
	for _, c := range []struct{ config, reason, limitation string }{
		{"{\"compilerOptions\":{\"generateTrace\":\"./t\"},\"include\":[\"src\"]}\n", ReasonWriteOption, "tsconfig_option_generate_trace"},
		{"{\"include\":[\"/home/canary-secret/**/*.ts\"]}\n", ReasonConfigOutside, "tsconfig_option_include"},
		{"{\"compilerOptions\":{\"typeRoots\":[\"../canary-secret\"]},\"include\":[\"src\"]}\n", ReasonConfigOutside, "tsconfig_option_type_roots"},
	} {
		fx := jststest.NewFixture(t, "", caseFiles(map[string]string{"tsconfig.json": c.config, "src/main.ts": "export const one = 1;\n"}), domain.Scope{})
		res := jststest.RunTask(t, a, fx.Job, "eslint-typed", neverRunner{t})
		doc, raw := jststest.ReadEnvelope(t, fx.Job, res)
		if res.Status != domain.TaskSkippedPolicy || res.Reason != c.reason || !slices.Contains(doc.Limitations, c.limitation) || strings.Contains(string(raw), "canary-secret") {
			t.Fatalf("%s/%s %v", res.Status, res.Reason, doc.Limitations)
		}
	}
}

// Repository-derived positionals follow "--" and are "./"-prefixed: a
// bare "--help.ts" would print ESLint's help (measured).
func TestCheckArgsPositionalsNeverOptions(t *testing.T) {
	args := CheckArgs("/s/c.cjs", "/s/s.json", []string{".", "--help.ts", "-x.js", "src"})
	i := slices.Index(args, "--")
	if i < 0 || !slices.Equal(args[i+1:], []string{".", "./--help.ts", "./-x.js", "./src"}) {
		t.Fatalf("argv %v", args)
	}
}

// Real ESLint on files named like options, in a full and a path scope.
func TestRealESLintOptionLikeFileNames(t *testing.T) {
	a := newReal(t, false)
	files := caseFiles(map[string]string{"--help.ts": "export const a = 1;\nconst unused = 2;\n", "-x.js": "const unused = 1;\n"})
	for _, scope := range []domain.Scope{{Kind: "full"}, {Kind: "paths", Paths: []string{"--help.ts", "-x.js"}}} {
		fx := jststest.NewFixture(t, "", files, scope)
		res := jststest.RunTask(t, a, fx.Job, "eslint", &executor.Executor{})
		doc, _ := jststest.ReadEnvelope(t, fx.Job, res)
		if res.Status != domain.TaskSucceeded || !slices.Equal(doc.Coverage.ProcessedPaths, []string{"--help.ts", "-x.js"}) || len(doc.Observations) != 2 {
			t.Fatalf("%s: %s/%s %+v %+v", scope.Kind, res.Status, res.Reason, doc.Coverage, doc.Observations)
		}
	}
}
