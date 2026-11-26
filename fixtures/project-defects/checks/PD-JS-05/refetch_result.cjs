'use strict';
// Start a query-backed collection, trigger a manual refetch through its utilities and look at
// what the returned promise resolves to.
// Usage: node pd_check/refetch_result.cjs <prefix with node_modules and out>
const path = require('node:path');

const prefix = path.resolve(process.argv[2]);
const { QueryClient } = require(path.join(prefix, 'node_modules', '@tanstack', 'query-core'));
const { createCollection } = require(path.join(prefix, 'node_modules', '@tanstack', 'db'));
const { queryCollectionOptions } = require(path.join(prefix, 'out', 'query.js'));

async function main() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 0 } } });
  let calls = 0;
  const collection = createCollection(
    queryCollectionOptions({
      id: 'refetch-result-check',
      queryClient,
      queryKey: ['refetch-result-check'],
      queryFn: async () => {
        calls++;
        return [{ id: 'a', n: calls }];
      },
      getKey: (item) => item.id,
      startSync: true,
    }),
  );

  const deadline = Date.now() + 5000;
  while (collection.status !== 'ready' && Date.now() < deadline) {
    await new Promise((r) => setTimeout(r, 10));
  }
  if (collection.status !== 'ready') {
    console.log(`collection never became ready (status ${collection.status})`);
    process.exit(2);
  }

  const before = calls;
  const result = await collection.utils.refetch();
  let bad = 0;
  if (calls !== before + 1) {
    bad++;
    console.log(`MISMATCH refetch ran the query function ${calls - before} times, want 1`);
  }
  if (!Array.isArray(result) || result.length === 0) {
    bad++;
    console.log(`MISMATCH refetch resolved to ${result === undefined ? 'undefined' : JSON.stringify(result)}, want a non-empty array of observer results`);
  } else {
    for (const r of result) {
      if (!r || !('status' in r) || !('data' in r)) {
        bad++;
        console.log('MISMATCH refetch result entry without status and data');
      } else {
        console.log(`ok entry status=${r.status} data=${JSON.stringify(r.data)}`);
      }
    }
  }
  await collection.cleanup();
  queryClient.clear();
  process.exit(bad ? 1 : 0);
}

main().catch((e) => {
  console.log('error ' + (e && e.stack ? e.stack : e));
  process.exit(2);
});
