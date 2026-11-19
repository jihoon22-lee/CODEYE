import { fetchItems } from "./client";

export async function main(): Promise<void> {
  const items = await fetchItems(5);
  console.log(items.join(","));
}
