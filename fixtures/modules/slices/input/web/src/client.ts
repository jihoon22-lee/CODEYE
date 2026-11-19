export async function fetchItems(limit: number): Promise<string[]> {
  const res = await fetch(`/items?limit=${limit}`);
  if (!res.ok) {
    return [];
  }
  return (await res.json()) as string[];
}
