"use strict";

const fs = require("node:fs");
const path = require("node:path");

module.exports = function ({typescript, typescriptESLint, cwd, files, ruleName}) {
  const ts = require(typescript);
  const tseslint = require(typescriptESLint);
  const roots = files.map((file) => path.resolve(cwd, file));
  const texts = new Map(roots.map((file) => [file, fs.readFileSync(file, "utf8").replace(/^\uFEFF/, "")]));
  const libDir = path.dirname(ts.getDefaultLibFilePath({target: ts.ScriptTarget.ES2022}));
  const libraries = ["lib.es2022.d.ts", "lib.dom.d.ts"];
  const pending = new Set(libraries);
  let libBytes = 0;
  for (const lib of pending) {
    const filename = path.join(libDir, lib);
    const stat = fs.lstatSync(filename);
    libBytes += stat.size;
    if (!stat.isFile() || pending.size > 64 || libBytes > 4 * 1024 * 1024) {
      throw new Error("Managed TypeScript standard library is unavailable or oversized.");
    }
    const text = fs.readFileSync(filename, "utf8");
    texts.set(filename, text);
    for (const ref of ts.preProcessFile(text, true).libReferenceDirectives) {
      if (!/^[a-z0-9._]+$/.test(ref.fileName)) {
        throw new Error("Invalid managed TypeScript standard library reference.");
      }
      pending.add("lib." + ref.fileName + ".d.ts");
    }
  }

  // The compiler never sees the OS filesystem. Source roots are the only
  // files staged by the worker, and runtime imports and ambient types have
  // no way to obtain a declaration.
  const read = (name) => texts.get(path.resolve(cwd, name));
  const program = ts.createProgram(roots, {
    allowJs: true, checkJs: true, noEmit: true, noResolve: true, types: [],
    target: ts.ScriptTarget.ES2022, lib: libraries, libReplacement: false,
    module: ts.ModuleKind.ESNext, moduleDetection: ts.ModuleDetectionKind.Force,
    strictNullChecks: true, jsx: ts.JsxEmit.Preserve,
  }, {
    getSourceFile(name, options) {
      const text = read(name);
      return text === undefined ? undefined : ts.createSourceFile(name, text, options, true);
    },
    getDefaultLibFileName: () => path.join(libDir, libraries[0]),
    getCurrentDirectory: () => cwd,
    useCaseSensitiveFileNames: () => true,
    getCanonicalFileName: (name) => name,
    getNewLine: () => "\n",
    fileExists: (name) => read(name) !== undefined,
    readFile: read,
    resolveModuleNameLiterals: (names) => names.map(() => ({resolvedModule: undefined})),
    writeFile: () => { throw new Error("Managed TypeScript checker cannot emit files."); },
  });
  for (const lib of libraries) {
    const file = program.getSourceFile(path.join(libDir, lib));
    if (!file || !program.isSourceFileDefaultLibrary(file)) {
      throw new Error("Managed TypeScript standard library could not be loaded.");
    }
  }

  // Use the stock rule for Promise identification. Only emit when every
  // possible resolved type is a primitive. Unknown, error types, and
  // structural objects require more information than this program has.
  const stock = tseslint.plugin.rules[ruleName];
  const primitive = ts.TypeFlags.StringLike | ts.TypeFlags.NumberLike | ts.TypeFlags.BigIntLike |
    ts.TypeFlags.BooleanLike | ts.TypeFlags.ESSymbolLike | ts.TypeFlags.Null | ts.TypeFlags.Undefined;
  const scoped = {...stock, create(context) {
    const services = context.sourceCode.parserServices;
    return stock.create(Object.create(context, {report: {value(diagnostic) {
      const arg = diagnostic.node.arguments?.[0];
      if (!arg || arg.type === "SpreadElement") return;
      const type = services.getTypeAtLocation(arg);
      const variants = type.isUnion() ? type.types : [type];
      if (variants.every((type) => (type.flags & primitive) !== 0)) context.report(diagnostic);
    }}}));
  }};
  return [{
    name: "codeye/javascript-rejection-types",
    files: ["**/*.js", "**/*.jsx", "**/*.mjs", "**/*.cjs"],
    languageOptions: {
      parser: tseslint.parser,
      parserOptions: {tsconfigRootDir: cwd, project: false, projectService: false, programs: [program]},
    },
    plugins: {"@typescript-eslint": {
      meta: tseslint.plugin.meta,
      rules: {[ruleName]: scoped},
    }},
    rules: {
      ["@typescript-eslint/" + ruleName]: ["warn", {allowEmptyReject: true, allowThrowingAny: true, allowThrowingUnknown: true}],
    },
  }];
};
