'use strict'
// gtr/ltr answers for versions on or next to strict and inclusive range bounds.
// Copied to <work>/check/outside-endpoints.js; the package root is one level up.
const path = require('node:path')
const root = path.resolve(__dirname, '..')
const gtr = require(path.join(root, 'ranges/gtr'))
const ltr = require(path.join(root, 'ranges/ltr'))

// [function, name, version, range, expected]
const cases = [
  [gtr, 'gtr', '1.0.0', '>1.0.0 <2.0.0', false],
  [gtr, 'gtr', '1.2.3', '>1.2.3 <1.3.0', false],
  [gtr, 'gtr', '2.0.0', '>1.0.0 <2.0.0', true],
  [gtr, 'gtr', '2.0.1', '>1.0.0 <=2.0.0', true],
  [gtr, 'gtr', '0.9.9', '>=1.0.0 <2.0.0', false],
  [ltr, 'ltr', '2.0.0', '>1.0.0 <2.0.0', false],
  [ltr, 'ltr', '1.3.0', '>1.2.3 <1.3.0', false],
  [ltr, 'ltr', '1.0.0', '>1.0.0 <2.0.0', true],
  [ltr, 'ltr', '0.9.9', '>=1.0.0 <=2.0.0', true],
  [ltr, 'ltr', '2.0.1', '>1.0.0 <=2.0.0', false],
]

let failed = 0
for (const [fn, name, version, range, expected] of cases) {
  const got = fn(version, range)
  if (got !== expected) {
    failed++
    console.log(`MISMATCH ${name}(${version}, '${range}') = ${got}, expected ${expected}`)
  }
}
if (failed) {
  process.exitCode = 1
} else {
  console.log(`all ${cases.length} cases agree`)
}
