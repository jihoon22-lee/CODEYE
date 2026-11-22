package architecture

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

func TestRealDependencyCruiser(t *testing.T) {
	node, entry := os.Getenv("CODEYE_TEST_NODE"), os.Getenv("CODEYE_TEST_DEPENDENCY_CRUISER")
	if node == "" || entry == "" {
		t.Skip("configure Node and dependency-cruiser")
	}
	runner := &executor.Executor{}
	a, err := New(DependencyCruiser, entry, &pack.Toolchain{Executable: node, Dir: filepath.Dir(node)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Probe(context.Background(), runner, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	rules := `{"forbidden":[{"name":"no-cycle","severity":"error","from":{},"to":{"circular":true}}]}`
	for _, c := range []struct {
		name, other, config string
		reason              string
	}{
		{"violation", `import "./a.js";`, ".dependency-cruiser.json", ""},
		{"clean", `export const b=2;`, ".dependency-cruiser.json", ""},
		{"skip", `import "./a.js";`, ".dependency-cruiser.cjs", "dependency_cruiser_executable_configuration"},
	} {
		t.Run(c.name, func(t *testing.T) {
			fx := packtest.NewFixture(t, "", map[string]string{"a.js": `import "./b.js";`, "b.js": c.other, c.config: rules}, domain.Scope{})
			result := packtest.RunTask(t, a, fx.Job, a.desc.Name, runner)
			doc, body := packtest.ReadEnvelope(t, fx.Job, result, "CODEYE_ARCHITECTURE_DUMP")
			if result.Reason != c.reason || c.reason != "" && (result.Status != domain.TaskUnsupported || len(result.Executions) != 0) {
				t.Fatalf("task %+v\n%s", result, body)
			}
			if c.reason != "" {
				return
			}
			if result.Status != domain.TaskSucceeded || len(doc.Coverage.ProcessedPaths) != 2 || (len(doc.Observations) > 0) != (c.name == "violation") {
				t.Fatalf("result %+v\n%s", result, body)
			}
			if len(doc.Observations) > 0 && (doc.Observations[0].LocationState != obsdoc.LocationResolved || doc.Observations[0].RuleID != "no-cycle") {
				t.Fatalf("evidence %+v", doc.Observations)
			}
		})
	}
}

func TestRealImportLinter(t *testing.T) {
	entry := os.Getenv("CODEYE_TEST_IMPORT_LINTER")
	if entry == "" {
		t.Skip("configure Import Linter")
	}
	runner := &executor.Executor{}
	a, err := New(ImportLinter, entry, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Probe(context.Background(), runner, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, body, options, reason string }{
		{"violation", "import pkg.b\n", "", ""},
		{"clean", "value=1\n", "", ""},
		{"skip", "import pkg.b\n", "contract_types = forbidden: pkg.a.CustomContract\n", "import_linter_custom_contract_unsupported"},
	} {
		t.Run(c.name, func(t *testing.T) {
			config := "[importlinter]\nroot_package=pkg\n" + c.options + "\n[importlinter:contract:layering]\nname=Layering\ntype=forbidden\nsource_modules=pkg.a\nforbidden_modules=pkg.b\n"
			fx := packtest.NewFixture(t, "", map[string]string{"src/pkg/__init__.py": "raise RuntimeError('project code executed')\n", "src/pkg/a.py": c.body, "src/pkg/b.py": "value=2\n", ".importlinter": config}, domain.Scope{})
			result := packtest.RunTask(t, a, fx.Job, a.desc.Name, runner)
			doc, body := packtest.ReadEnvelope(t, fx.Job, result, "CODEYE_ARCHITECTURE_DUMP")
			if result.Reason != c.reason || c.reason != "" && result.Status != domain.TaskUnsupported {
				t.Fatalf("task %+v\n%s", result, body)
			}
			if c.reason != "" {
				return
			}
			if result.Status != domain.TaskSucceeded || len(doc.Coverage.ProcessedPaths) != 3 || (len(doc.Observations) > 0) != (c.name == "violation") {
				t.Fatalf("result %+v\n%s", result, body)
			}
			if len(doc.Observations) > 0 && (doc.Observations[0].LocationState != obsdoc.LocationResolved || doc.Observations[0].RuleID != "layering") {
				t.Fatalf("evidence %+v", doc.Observations)
			}
		})
	}
}

func TestRealDependencyCruiserTypeScript(t *testing.T) {
	node, entry := os.Getenv("CODEYE_TEST_NODE"), os.Getenv("CODEYE_TEST_DEPENDENCY_CRUISER_WITH_TYPESCRIPT")
	if node == "" || entry == "" {
		t.Skip("configure dependency-cruiser in an installation that includes TypeScript")
	}
	runner := &executor.Executor{}
	a, err := New(DependencyCruiser, entry, &pack.Toolchain{Executable: node, Dir: filepath.Dir(node)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Probe(context.Background(), runner, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	fx := packtest.NewFixture(t, "", map[string]string{
		"a.ts": `import { b } from "./b.js"; export const a: () => number = () => b();`,
		"b.ts": `import { a } from "./a.js"; export const b: () => number = () => a();`,
	}, domain.Scope{})
	result := packtest.RunTask(t, a, fx.Job, a.desc.Name, runner)
	doc, body := packtest.ReadEnvelope(t, fx.Job, result, "CODEYE_ARCHITECTURE_DUMP")
	if result.Status != domain.TaskSucceeded || len(doc.Coverage.ProcessedPaths) != 2 ||
		len(doc.Observations) != 1 || doc.Observations[0].LocationState != obsdoc.LocationResolved {
		t.Fatalf("TypeScript dependency result %+v\n%s", result, body)
	}
}
