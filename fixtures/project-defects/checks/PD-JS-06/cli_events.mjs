// Drive the command-line `run` helper against a destination that already holds a file, with the
// force option, a local ref lookup and a download that fails. Collect what the helper prints and
// check that the progress notice about the non-empty destination reaches the terminal.
// Usage: node pd_check/cli_events.mjs <prefix with node_modules> <scratch dir>
import { register } from 'node:module';
import { pathToFileURL } from 'node:url';
import fs from 'node:fs';
import path from 'node:path';

const deps = path.resolve(process.argv[2]);
const scratch = path.resolve(process.argv[3]);
const here = path.dirname(new URL(import.meta.url).pathname);

fs.rmSync(scratch, { recursive: true, force: true });
const dest = path.join(scratch, 'existing');
fs.mkdirSync(dest, { recursive: true });
fs.writeFileSync(path.join(dest, 'keep.txt'), 'already here\n');
process.env.HOME = path.join(scratch, 'home');
process.env.XDG_CACHE_HOME = path.join(scratch, 'home', '.cache');
fs.mkdirSync(process.env.XDG_CACHE_HOME, { recursive: true });
// The module runs its own command line on import unless this is set.
process.env.VITEST = '1';

register(pathToFileURL(path.join(here, 'hooks.mjs')).href, { data: { deps } });

const printed = [];
const capture = (stream) => (...args) => printed.push(`${stream}: ${args.map(String).join(' ')}`);
const original = { log: console.log, warn: console.warn, error: console.error, exit: process.exit };
let exited;
const done = new Promise((resolve) => {
  process.exit = (code) => {
    exited = code;
    resolve();
  };
});

const { run } = await import(pathToFileURL(path.resolve('src/bin.ts')).href);

const hash = '0123456789abcdef0123456789abcdef01234567';
const git = {
  clone: () => Promise.reject(new Error('git transport is not used here')),
  fetchRefs: async () => [{ hash, name: 'main', type: 'HEAD' }],
};
const fetch = () => Promise.reject(new Error('offline download'));

console.log = capture('out');
console.warn = capture('warn');
console.error = capture('err');
try {
  run('example-user/example-repo', dest, { force: true, git, fetch });
  await Promise.race([done, new Promise((r) => setTimeout(r, 10000))]);
} finally {
  console.log = original.log;
  console.warn = original.warn;
  console.error = original.error;
  process.exit = original.exit;
}

for (const line of printed) console.log('  ' + line);
let bad = 0;
if (exited !== 1) {
  bad++;
  console.log(`unexpected: the failed download did not end with exit status 1 (got ${exited})`);
}
if (!printed.some((line) => line.startsWith('out: ') && line.includes('destination directory is not empty'))) {
  bad++;
  console.log('MISMATCH progress notice for the non-empty destination was not printed');
}
process.exit(bad ? 1 : 0);
