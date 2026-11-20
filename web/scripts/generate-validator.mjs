// Build-time generation keeps runtime validation compatible with strict CSP.
import { readFile, mkdir, writeFile } from 'node:fs/promises';
import Ajv2020 from 'ajv/dist/2020.js';
import standaloneCode from 'ajv/dist/standalone/index.js';
const schema = JSON.parse(await readFile(new URL('../../schemas/doctor.schema.json', import.meta.url), 'utf8'));
const ajv = new Ajv2020({ strict: true, code: { source: true, esm: true } });

// Standalone ESM still refers to Ajv's CommonJS runtime helpers with require().
// Import those modules so the validator also runs as a browser module in Vite dev.
const helpers = new Map();
const source = standaloneCode(ajv, ajv.compile(schema)).replace(
  /\brequire\("(ajv\/dist\/runtime\/[\w-]+)"\)\.([\w$]+)/g,
  (_, path, member) => {
    if (!helpers.has(path)) helpers.set(path, `ajvHelper${helpers.size}`);
    const binding = helpers.get(path);
    // Node and Vite may expose CommonJS exports on either the namespace or its default.
    return `(${binding}.default?.${member} ?? ${binding}.${member})`;
  },
);
if (/\brequire\s*\(/.test(source)) throw new Error('Unexpected CommonJS dependency in the standalone doctor validator');
const imports = [...helpers].map(([path, binding]) => `import * as ${binding} from ${JSON.stringify(`${path}.js`)};`);
const directory = new URL('../src/generated/', import.meta.url);
await mkdir(directory, { recursive: true });
await writeFile(new URL('doctor-validator.js', directory), [...imports, source].join('\n'));
await writeFile(new URL('doctor-validator.d.ts', directory), 'declare const validate: (value: unknown) => boolean;\nexport default validate;\n');
