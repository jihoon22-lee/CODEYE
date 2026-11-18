package eslint

import (
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/jsts/jststest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/tsc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Engine overlap (docs/39 §6, docs/43 §6), measured on the real tools in one
// run of a module with both a TS and a JS copy of the same constructs:
//   - in TS files the baseline leaves to tsc what tsc checks itself
//     (typescript-eslint eslint-recommended turns off no-undef,
//     no-dupe-class-members, no-redeclare, ...), and tsc reports them;
//   - tsc runs with --noUnusedLocals/--noUnusedParameters/
//     --noFallthroughCasesInSwitch false even when the repository config
//     enables them, because the baseline's no-unused-vars and no-fallthrough
//     report the same constructs;
//   - in JS files (not type-checked) the baseline keeps those core rules.
const (
	fixOverlapConfig = "{\"compilerOptions\":{\"target\":\"ES2022\",\"module\":\"ESNext\",\"moduleResolution\":\"Bundler\",\"lib\":[\"ES2022\"],\"strict\":true," +
		"\"noUnusedLocals\":true,\"noUnusedParameters\":true,\"noFallthroughCasesInSwitch\":true},\"include\":[\"src\"]}\n"
	fixOverlapTS = "export function f(kind: number, unusedParam: string): number {\n  const unusedLocal = 1;\n  switch (kind) {\n    case 1:\n      kind++;\n" +
		"    case 2:\n      return kind;\n  }\n  return undeclared;\n}\n\nexport class C {\n  m(): number {\n    return 1;\n  }\n  m(): number {\n    return 2;\n  }\n}\n"
	fixOverlapJS = "export function f(kind, unusedParam) {\n  const unusedLocal = 1;\n  switch (kind) {\n    case 1:\n      kind++;\n" +
		"    case 2:\n      return kind;\n  }\n  return undeclared;\n}\n\nexport class C {\n  m() {\n    return 1;\n  }\n  m() {\n    return 2;\n  }\n}\n"
)

func TestRealEngineOverlapExcluded(t *testing.T) {
	a := newReal(t, false)
	tscScript := jststest.Tsc(t)
	tc, err := tsc.New(tsc.Config{Script: tscScript, Node: jststest.Node(t)})
	if err != nil {
		t.Fatal(err)
	}
	fx := jststest.NewFixture(t, "", caseFiles(map[string]string{"tsconfig.json": fixOverlapConfig, "src/o.ts": fixOverlapTS, "lib/o.js": fixOverlapJS}), domain.Scope{})
	res := jststest.RunTask(t, a, fx.Job, "eslint", &executor.Executor{})
	jststest.Capture(t, "overlap", execCheck, res)
	doc, _ := jststest.ReadEnvelope(t, fx.Job, res)
	byFile := map[string]map[string]int{}
	for _, o := range doc.Observations {
		if byFile[*o.Path] == nil {
			byFile[*o.Path] = map[string]int{}
		}
		byFile[*o.Path][o.RuleID]++
	}
	ts, js := byFile["src/o.ts"], byFile["lib/o.js"]
	for _, r := range []string{"no-undef", "no-dupe-class-members", "no-unused-vars"} {
		if ts[r] != 0 {
			t.Fatalf("baseline ran %s on TS: %v", r, ts)
		}
	}
	if ts["@typescript-eslint/no-unused-vars"] != 2 || ts["no-fallthrough"] != 1 {
		t.Fatalf("baseline TS rules %v", ts)
	}
	// no-undef is off everywhere: the managed config declares no globals.
	if js["no-dupe-class-members"] != 1 || js["no-unused-vars"] != 2 || js["no-fallthrough"] != 1 || js["no-undef"] != 0 {
		t.Fatalf("baseline JS rules %v", js)
	}
	tres := jststest.RunTask(t, tc, fx.Job, "tsc", &executor.Executor{})
	tdoc, _ := jststest.ReadEnvelope(t, fx.Job, tres)
	rules := jststest.Rules(tdoc)
	if rules["TS2304"] != 1 || rules["TS2393"] != 2 || rules["TS6133"] != 0 || rules["TS6196"] != 0 || rules["TS7029"] != 0 {
		t.Fatalf("tsc rules %v", rules)
	}
	if *tdoc.Coverage.FilesInScope != 2 || tdoc.Coverage.FilesSkipped != 1 {
		t.Fatalf("tsc coverage %+v", tdoc.Coverage)
	}
}
