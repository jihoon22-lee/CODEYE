import { fetchItems } from "./client";

test("fetchItems returns an array", async () => {
  expect(Array.isArray(await fetchItems(0))).toBe(true);
});
