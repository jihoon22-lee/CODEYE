import { test } from "node:test";
import assert from "node:assert/strict";

import { Queue } from "./queue.js";

test("drain waits for every job", async () => {
  const queue = new Queue(1);
  let ran = 0;
  queue.add(async () => {
    ran++;
  });
  await queue.drain();
  assert.equal(ran, 1);
});
