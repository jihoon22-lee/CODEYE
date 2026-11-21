package eslint

import (
	"encoding/json"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/jsts"
)

// The managed configuration replaces the repository's eslint.config.* (and
// any legacy .eslintrc), which are executable JavaScript CODEYE never loads
// without the repository-code grant. It is generated per task into task
// scratch, outside the snapshot, and loads @eslint/js and typescript-eslint
// by the absolute entry files of the operator's tool directory, so no module
// is resolved from the project or its ancestors (docs/ANALYZERS.md#jsts-실행).

var (
	jsGlobs = []string{"**/*.js", "**/*.jsx", "**/*.mjs", "**/*.cjs"}
	tsGlobs = []string{"**/*.ts", "**/*.tsx", "**/*.mts", "**/*.cts"}
	// dataIgnores mirror pack.SourceFiles: hidden, testdata, vendor and
	// node_modules directories are data, not the project's own source.
	dataIgnores = []string{"**/.*/**", "**/testdata/**", "**/vendor/**", "**/node_modules/**"}
)

// globEscape makes a directory name a literal minimatch pattern.
func globEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`*?[]{}()!+@\`, r) {
			b.WriteString("[" + string(r) + "]")
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ignores are the module-relative ignore patterns of a task: nested modules
// (their own tasks) and data directories.
func ignores(mod jsts.Module, typed bool) []string {
	out := append([]string{}, dataIgnores...)
	for _, n := range mod.Nested {
		out = append(out, globEscape(jsts.Rel(n, mod.Dir))+"/**")
	}
	if typed {
		// The type-aware task lints TypeScript files only; without this,
		// ESLint's default file patterns would "lint" JS with no rule.
		out = append(out, jsGlobs...)
	}
	return out
}

func lit(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// baselineConfig is the codeye-js-eslint configuration: @eslint/js
// recommended on JS and TS files, typescript-eslint recommended on TS files
// (its eslint-recommended part turns off the core rules TypeScript itself
// checks), no-undef off everywhere (TS: tsc reports it; JS: the globals come
// from the repository's executable configuration, which is not loaded).
// The core rejection rule runs on JS only; the type-aware task already
// checks rejection reasons on TS.
func baselineConfig(p plugins, mod jsts.Module) string {
	var b strings.Builder
	b.WriteString("\"use strict\";\n")
	b.WriteString("// CODEYE-managed ESLint configuration " + RulePackID + " v" + RulePackVersion + " (generated for one task; never the repository's).\n")
	b.WriteString("const js = require(" + lit(p.eslintJS.Entry) + ");\n")
	b.WriteString("const tseslint = require(" + lit(p.tseslint.Entry) + ");\n")
	b.WriteString("const JS = " + lit(jsGlobs) + ";\n")
	b.WriteString("const TS = " + lit(tsGlobs) + ";\n")
	b.WriteString("module.exports = [\n")
	b.WriteString("  { name: \"codeye/ignores\", ignores: " + lit(ignores(mod, false)) + " },\n")
	b.WriteString("  { name: \"codeye/jsx\", files: JS, languageOptions: { parserOptions: { ecmaFeatures: { jsx: true } } } },\n")
	b.WriteString("  { ...js.configs.recommended, name: \"codeye/eslint-js-recommended\", files: [...JS, ...TS] },\n")
	b.WriteString("  ...tseslint.configs.recommended.map((c) => ({ ...c, files: TS })),\n")
	b.WriteString("  { name: \"codeye/no-undef-off\", files: [...JS, ...TS], rules: { \"no-undef\": \"off\" } },\n")
	b.WriteString("  { name: \"codeye/promise-rejections\", files: JS, rules: { " + lit(promiseRejectRule) + ": [\"warn\", { allowEmptyReject: true }] } },\n")
	b.WriteString("];\n")
	return b.String()
}

// programRule is the managed codeye/program-diagnostics rule of the
// type-aware task. It reports, as tool records the adapter never turns into
// observations, the program's own errors (configuration, options, global)
// once and the dependency-gap diagnostics of each linted file (the code list
// is jsts.GapCodes, shared with the tsc adapter). typescript-eslint cannot
// tell a rule that a type came from a module it could not resolve; without
// this signal a type-aware run with missing dependencies would look complete.
const programRule = `const GAP = new Set(%GAP%);
const reported = new WeakSet();
function flat(m) {
  if (typeof m === "string") return m;
  let s = m.messageText;
  for (const n of m.next || []) s += "\n" + flat(n);
  return s;
}
const codeye = { meta: { name: "codeye", version: "1" }, rules: { "program-diagnostics": {
  meta: { type: "problem", schema: [], messages: { d: "TS{{code}} {{scope}}: {{text}}" } },
  create(context) {
    const s = context.sourceCode.parserServices;
    if (!s || !s.program || !s.esTreeNodeToTSNodeMap) return {};
    return { Program(node) {
      const p = s.program;
      const sf = s.esTreeNodeToTSNodeMap.get(node);
      if (!reported.has(p)) {
        reported.add(p);
        const seen = new Set();
        for (const d of [...p.getConfigFileParsingDiagnostics(), ...p.getOptionsDiagnostics(), ...p.getGlobalDiagnostics()]) {
          if (d.category !== 1) continue;
          const text = flat(d.messageText);
          if (seen.has(d.code + text)) continue;
          seen.add(d.code + text);
          context.report({ loc: { line: 1, column: 0 }, messageId: "d", data: { code: String(d.code), scope: "program", text } });
        }
      }
      if (!sf) return;
      for (const d of p.getSemanticDiagnostics(sf)) {
        if (!GAP.has(d.code) || d.file !== sf || d.start === undefined) continue;
        const at = sf.getLineAndCharacterOfPosition(d.start);
        context.report({ loc: { line: at.line + 1, column: at.character }, messageId: "d", data: { code: String(d.code), scope: "file", text: flat(d.messageText) } });
      }
    } };
  } } } };
`

// typedConfig is the codeye-ts-eslint-typed v1 configuration:
// typescript-eslint recommended-type-checked-only (without its
// eslint-recommended part, whose syntactic rules the baseline runs) on TS
// files of the module's tsconfig program, plus codeye/program-diagnostics.
// The project is "./tsconfig.json" relative to tsconfigRootDir: a relative
// path without glob characters is used as is, while an absolute workspace
// path holding "(", "[" or "*" would be expanded as a glob (measured,
// typescript-eslint 8.73 resolveProjectList).
func typedConfig(p plugins, mod jsts.Module, moduleAbs string) string {
	var b strings.Builder
	b.WriteString("\"use strict\";\n")
	b.WriteString("// CODEYE-managed ESLint configuration " + TypedRulePackID + " v" + TypedRulePackVersion + " (generated for one task; never the repository's).\n")
	b.WriteString("const tseslint = require(" + lit(p.tseslint.Entry) + ");\n")
	b.WriteString("const TS = " + lit(tsGlobs) + ";\n")
	b.WriteString(strings.Replace(programRule, "%GAP%", lit(jsts.GapCodes), 1))
	b.WriteString("module.exports = [\n")
	b.WriteString("  { name: \"codeye/ignores\", ignores: " + lit(ignores(mod, true)) + " },\n")
	b.WriteString("  ...tseslint.configs.recommendedTypeCheckedOnly.filter((c) => c.name !== \"typescript-eslint/eslint-recommended\").map((c) => ({ ...c, files: TS })),\n")
	b.WriteString("  { name: \"codeye/typed\", files: TS, plugins: { codeye }, rules: { \"codeye/program-diagnostics\": \"error\" },\n")
	b.WriteString("    languageOptions: { parserOptions: { project: [\"./tsconfig.json\"], tsconfigRootDir: " + lit(moduleAbs) + " } } },\n")
	b.WriteString("];\n")
	return b.String()
}
