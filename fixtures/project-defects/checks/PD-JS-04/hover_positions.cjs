'use strict';
// Hover over every character of a field name, from its first to its last character, and compare
// the tooltip with the field's documentation.
// Usage: node pd_check/hover_positions.cjs <prefix with node_modules and out>
const path = require('node:path');

const prefix = path.resolve(process.argv[2]);
const { buildSchema } = require(path.join(prefix, 'node_modules', 'graphql'));
const { getHoverInformation } = require(path.join(prefix, 'out', 'interface', 'getHoverInformation.js'));

const schema = buildSchema(`
type Query {
  "Root entry"
  thing(id: ID): Thing
}

type Thing {
  "Measured weight in grams"
  weight: Int
  label: String
}
`);

const cases = [];
const query = 'query { thing(id: 1) { weight label } }';
const field = 'weight';
const at = query.indexOf(field);
for (let column = at; column < at + field.length; column++) {
  cases.push({ query, column, want: 'Thing.weight: Int\n\nMeasured weight in grams' });
}
const rootAt = query.indexOf('thing');
cases.push({ query, column: rootAt, want: 'Query.thing: Thing\n\nRoot entry' });
cases.push({ query, column: rootAt + 2, want: 'Query.thing: Thing\n\nRoot entry' });

let bad = 0;
for (const c of cases) {
  const got = getHoverInformation(schema, c.query, { line: 0, character: c.column });
  if (got !== c.want) {
    bad++;
    console.log(`MISMATCH hover(0,${c.column}) on ${JSON.stringify(c.query[c.column])}: got ${JSON.stringify(got)}`);
  } else {
    console.log(`ok hover(0,${c.column})`);
  }
}
console.log(`${cases.length - bad}/${cases.length} positions give the field documentation`);
process.exit(bad ? 1 : 0);
