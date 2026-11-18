import { Queue } from "./queue.js";

const queue = new Queue(2);
queue.add(async () => {
  console.log("job ran");
});
await queue.drain();
