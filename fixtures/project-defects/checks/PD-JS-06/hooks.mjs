// Module resolution for the check: relative `.js` specifiers inside the work copy resolve to the
// TypeScript source next to them (node strips the types), and packages come from the prefix in
// `data.deps`.
import { existsSync } from 'node:fs';
import { fileURLToPath, pathToFileURL } from 'node:url';
import path from 'node:path';

let deps;

export async function initialize(data) {
  deps = pathToFileURL(path.join(data.deps, 'package.json')).href;
}

export async function resolve(specifier, context, next) {
  if ((specifier.startsWith('./') || specifier.startsWith('../')) && specifier.endsWith('.js') && context.parentURL) {
    const candidate = path.resolve(path.dirname(fileURLToPath(context.parentURL)), specifier.replace(/\.js$/, '.ts'));
    if (existsSync(candidate)) {
      return { url: pathToFileURL(candidate).href, shortCircuit: true };
    }
  }
  if (!specifier.startsWith('.') && !specifier.startsWith('node:') && !specifier.startsWith('file:') && !specifier.startsWith('/')) {
    return next(specifier, { ...context, parentURL: deps });
  }
  return next(specifier, context);
}
