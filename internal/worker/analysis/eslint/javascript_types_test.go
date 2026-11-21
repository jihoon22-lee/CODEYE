package eslint

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/jsts/jststest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

var managedJSScope = domain.Scope{Kind: "paths", Paths: []string{"src"}}

const managedJSFixture = `/// <reference path="../outside.d.ts" />
import {message} from "../unrelated.js";
export const fetchText = () => fetch("/items").then(response => response.text()).then(reason => Promise.reject(reason));
export const upload = () => new Promise((resolve, reject) => {
  const xhr = new XMLHttpRequest();
  xhr.addEventListener("error", () => reject(xhr.statusText));
  xhr.addEventListener("load", () => resolve(xhr.response));
});
export const rethrow = (condition) => Promise.reject(condition ? new Error("failed") : "failed");
export const carryError = () => Promise.reject(new Error("failed"));
export const unknownReason = (reason) => Promise.reject(reason);
export const ownReceiver = (Promise) => Promise.reject(Number("1"));
export const useImport = () => Promise.reject(message);
export const useAmbient = () => Promise.reject(ambientString());
export const omitReason = () => Promise.reject();
export const adjacentReasons = () => [Promise.reject("failure"), Promise.reject(Number("404"))];
`

func managedJSFixtureFiles() map[string]string {
	return caseFiles(map[string]string{
		"src/index.js":                  managedJSFixture,
		"unrelated.js":                  "export const message = \"unexpected type\";\n",
		"outside.d.ts":                  "declare function ambientString(): string;\n",
		"src/eslint.config.js":          "throw new Error(\"repository config executed\");\n",
		"src/.eslintrc.cjs":             "throw new Error(\"repository config executed\");\n",
		"src/dist/bundle.js":            "export const bundle = () => Promise.reject(Number(\"1\"));\n",
		"src/Third-Party/dependency.js": "export const thirdParty = () => Promise.reject(Number(\"1\"));\n",
		"src/third_party/dependency.js": "export const thirdParty = () => Promise.reject(Number(\"1\"));\n",
	})
}

func assertManagedJavaScript(t *testing.T, doc obsdoc.Document) {
	t.Helper()
	if doc.Outcome.TaskStatus != domain.TaskSucceeded || doc.Outcome.ExitClass != obsdoc.ExitSuccessWithFindings ||
		doc.Analyzer.Required || doc.RulePack.ID != ManagedJSRulePackID || doc.RulePack.RepositoryConfig != obsdoc.RepoConfigOverridden ||
		doc.Target.Properties["type_analysis"] != "isolated_javascript_primitive_rejections" ||
		doc.Target.Properties["javascript_files_excluded"] != "5" || !slices.Contains(doc.Limitations, managedJSExcluded) ||
		len(doc.RulePack.ConfigFiles) != 0 || !slices.Equal(doc.Coverage.ProcessedPaths, []string{"src/index.js"}) {
		t.Fatalf("managed JavaScript context: outcome %+v, coverage %+v, pack %+v", doc.Outcome, doc.Coverage, doc.RulePack)
	}
	var lines []int
	for _, o := range doc.Observations {
		if o.RuleID != typedRejectRule || o.Category != "error_handling" || o.RawSeverity == nil || *o.RawSeverity != "warning" ||
			o.Location == nil || o.Location.Path != "src/index.js" || o.RawRef.JSONPointer == "" {
			t.Fatalf("unexpected diagnostic %+v", o)
		}
		lines = append(lines, o.Location.StartLine)
	}
	if !slices.Equal(lines, []int{3, 6, 16, 16}) {
		t.Fatalf("known primitive diagnostics at lines %v", lines)
	}
}

func TestManagedJavaScriptExplicitVendorScopeIsSkipped(t *testing.T) {
	scope := domain.Scope{Kind: "paths", Paths: []string{"src/Third-Party"}}
	fx := packtest.NewFixture(t, "", managedJSFixtureFiles(), scope)
	r := packtest.RunTask(t, fakeAnalyzer(t, true), fx.Job, TypedName, neverRunner{t})
	doc, _ := jststest.ReadEnvelope(t, fx.Job, r)
	if r.Status != domain.TaskSkippedPolicy || r.Reason != managedJSNoSources || len(r.Executions) != 0 ||
		doc.Coverage.FilesInScope == nil || *doc.Coverage.FilesInScope != 0 || doc.Target.Properties["javascript_files_excluded"] != "1" {
		t.Fatalf("explicitly scoped vendor code: %+v %+v", doc.Outcome, doc.Coverage)
	}
	base, err := Plan(t.Context(), fx.Job)
	if err != nil || len(base) != 1 || base[0].FilesInScope == nil || *base[0].FilesInScope != 1 {
		t.Fatalf("baseline still covers requested file: %+v (%v)", base, err)
	}
}

func TestRealESLintManagedJavaScriptRejections(t *testing.T) {
	a := newReal(t, true)
	fx := packtest.NewFixture(t, "", managedJSFixtureFiles(), managedJSScope)
	packtest.WriteTree(t, fx.Job.Snapshot.WorkspaceDir, map[string]string{
		"node_modules/@types/ambient/index.d.ts": "declare function ambientString(): string;\n",
	})
	r := packtest.RunTask(t, a, fx.Job, TypedName, &executor.Executor{})
	jststest.Capture(t, "managed-js-rejections", r)
	doc, raw := jststest.ReadEnvelope(t, fx.Job, r)
	assertManagedJavaScript(t, doc)
	packtest.AssertNoNodePaths(t, raw, nodePaths(a, fx)...)
	staged := filepath.Join(fx.Job.Scratch, "analyzers", TypedName, "sources")
	entries, err := os.ReadDir(staged)
	if err != nil || len(entries) != 1 || entries[0].Name() != "src" {
		t.Fatalf("unexpected supplemental workspace: %+v (%v)", entries, err)
	}
	entries, err = os.ReadDir(filepath.Join(staged, "src"))
	if err != nil || len(entries) != 1 || entries[0].Name() != "index.js" {
		t.Fatalf("unexpected staged files: %+v (%v)", entries, err)
	}
	t.Logf("supplement: %s, peak RSS %d bytes", r.Executions[1].Result.EndedAt.Sub(r.Executions[0].Result.StartedAt), r.Executions[1].Result.Resources.PeakMemoryBytes)

	base := newReal(t, false)
	b := packtest.RunTask(t, base, fx.Job, Name, &executor.Executor{})
	bdoc, _ := jststest.ReadEnvelope(t, fx.Job, b)
	diagnostics := 0
	for _, o := range bdoc.Observations {
		if o.RuleID == promiseRejectRule && o.Path != nil && *o.Path == "src/index.js" {
			diagnostics++
		}
	}
	if b.Status != domain.TaskSucceeded || diagnostics != 1 {
		t.Fatalf("baseline outcome %+v, promise diagnostics %d", bdoc.Outcome, diagnostics)
	}
	t.Logf("baseline: %s, peak RSS %d bytes", b.Executions[1].Result.EndedAt.Sub(b.Executions[0].Result.StartedAt), b.Executions[1].Result.Resources.PeakMemoryBytes)
}

func TestManagedJavaScriptOverBudgetSkipsOnlySupplement(t *testing.T) {
	fx := packtest.NewFixture(t, "", caseFiles(map[string]string{"index.js": strings.Repeat("/* long */\n", managedJSMaxFileBytes/10+1)}), domain.Scope{})
	a := fakeAnalyzer(t, true)
	r := packtest.RunTask(t, a, fx.Job, TypedName, neverRunner{t})
	doc, _ := jststest.ReadEnvelope(t, fx.Job, r)
	if r.Status != domain.TaskSkippedPolicy || r.Reason != managedJSBudget || len(r.Executions) != 0 ||
		doc.Coverage.FilesInScope == nil || *doc.Coverage.FilesInScope != 1 || len(doc.Coverage.ProcessedPaths) != 0 {
		t.Fatalf("budget must leave an explicit skipped task: %+v %+v", r, doc.Coverage)
	}
	tasks, err := Plan(t.Context(), fx.Job)
	if err != nil || len(tasks) != 1 || !tasks[0].Spec.Required {
		t.Fatalf("baseline plan %+v: %v", tasks, err)
	}
}

func TestManagedJavaScriptDoesNotFollowSnapshotLinks(t *testing.T) {
	fx := packtest.NewFixture(t, "", caseFiles(map[string]string{"index.js": "export const skip = true;\n"}), domain.Scope{})
	input := filepath.Join(fx.Job.Snapshot.SnapshotDir, "index.js")
	outside := filepath.Join(t.TempDir(), "outside.js")
	if err := os.WriteFile(outside, []byte("export const skip = true;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(input); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, input); err != nil {
		t.Skipf("cannot make a symlink: %v", err)
	}
	r := packtest.RunTask(t, fakeAnalyzer(t, true), fx.Job, TypedName, neverRunner{t})
	if r.Status != domain.TaskFailed || r.Reason != ReasonSnapshotIntegrity || len(r.Executions) != 0 {
		t.Fatalf("symlink must fail before running tools: %+v", r)
	}
}
