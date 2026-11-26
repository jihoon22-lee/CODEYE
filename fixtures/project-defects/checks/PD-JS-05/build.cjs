'use strict';
// Transpile the package sources to CommonJS next to the installed packages, so the compiled
// modules and the check resolve the same dependency instances.
// Usage: node pd_check/build.cjs <prefix with node_modules> <source directory>
const fs = require('node:fs');
const path = require('node:path');

const prefix = path.resolve(process.argv[2]);
const ts = require(path.join(prefix, 'node_modules', 'typescript'));
const srcRoot = path.resolve(process.argv[3]);
const outRoot = path.join(prefix, 'out');

function walk(dir) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      if (entry.name !== '__tests__') walk(full);
      continue;
    }
    if (!entry.name.endsWith('.ts') || entry.name.endsWith('.d.ts')) continue;
    const rel = path.relative(srcRoot, full);
    const result = ts.transpileModule(fs.readFileSync(full, 'utf8'), {
      fileName: full,
      compilerOptions: {
        module: ts.ModuleKind.CommonJS,
        target: ts.ScriptTarget.ES2020,
        esModuleInterop: true,
      },
    });
    const target = path.join(outRoot, rel.replace(/\.ts$/, '.js'));
    fs.mkdirSync(path.dirname(target), { recursive: true });
    fs.writeFileSync(target, result.outputText);
  }
}

walk(srcRoot);
fs.writeFileSync(path.join(outRoot, 'package.json'), '{"type":"commonjs"}\n');
console.log('transpiled into ' + outRoot);
