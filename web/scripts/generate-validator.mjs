// Build-time generation keeps runtime validation compatible with strict CSP.
import { readFile, mkdir, writeFile } from 'node:fs/promises';
import Ajv2020 from 'ajv/dist/2020.js';
import standaloneCode from 'ajv/dist/standalone/index.js';
const schema = JSON.parse(await readFile(new URL('../../schemas/doctor.schema.json', import.meta.url), 'utf8'));
const ajv = new Ajv2020({ strict: true, code: { source: true, esm: true } });
const source = standaloneCode(ajv, ajv.compile(schema));
const directory = new URL('../src/generated/', import.meta.url);
await mkdir(directory, { recursive: true });
await writeFile(new URL('doctor-validator.js', directory), source);
await writeFile(new URL('doctor-validator.d.ts', directory), 'declare const validate: (value: unknown) => boolean;\nexport default validate;\n');
