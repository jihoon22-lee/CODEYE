import { fetchUser, type User } from "./util";

export async function load(ids: string[]): Promise<User[]> {
  const out: User[] = [];
  for (const id of ids) {
    out.push(await fetchUser(id));
  }
  return out;
}

export function start(ids: string[]): void {
  load(ids);
  ids.forEach(async (id) => {
    await fetchUser(id);
  });
}

export function count(raw: string): number {
  const value: any = JSON.parse(raw);
  return value.count;
}

export function label(user: User): string {
  const unused = user.id;
  return user.name;
}

export const total: number = "3";
