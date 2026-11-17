// Idempotency keys for run creation. A key is reused for as long as the same request
// (user, project, exact body) has no definitive answer, so a retry after a lost response
// cannot create a second run. Once the server accepted it, the next request is a new intent.
import { RUN_BODY } from './api';

const STORAGE_KEY = 'codeye.idempotency.v1';

type StorageLike = Pick<Storage, 'getItem' | 'setItem'>;

export function newKey(): string {
  const c = globalThis.crypto;
  if (typeof c?.randomUUID === 'function') return `codeye-${c.randomUUID()}`;
  // randomUUID needs a secure context; plain http on a LAN does not have one.
  const bytes = c.getRandomValues(new Uint8Array(16));
  return 'codeye-' + Array.from(bytes, b => b.toString(16).padStart(2, '0')).join('');
}

export function createIdempotency(storage?: StorageLike | null) {
  const keys = new Map<string, string>();
  try {
    const raw = storage?.getItem(STORAGE_KEY);
    if (raw) for (const [k, v] of Object.entries(JSON.parse(raw) as Record<string, unknown>)) if (typeof v === 'string' && v.length <= 200) keys.set(k, v);
  } catch { /* unreadable storage: start empty, correctness does not depend on it */ }
  const save = () => {
    try { storage?.setItem(STORAGE_KEY, JSON.stringify(Object.fromEntries(keys))); } catch { /* quota or privacy mode */ }
  };
  const id = (userId: string, projectId: string, body: unknown) => `${userId}\n${projectId}\n${JSON.stringify(body)}`;
  return {
    keyFor(userId: string, projectId: string, body: unknown = RUN_BODY): string {
      const slot = id(userId, projectId, body);
      let key = keys.get(slot);
      if (!key) { key = newKey(); keys.set(slot, key); save(); }
      return key;
    },
    // Call when the server accepted the request (2xx).
    complete(userId: string, projectId: string, body: unknown = RUN_BODY) {
      if (keys.delete(id(userId, projectId, body))) save();
    },
    pending(userId: string, projectId: string, body: unknown = RUN_BODY): boolean {
      return keys.has(id(userId, projectId, body));
    },
    clear() { keys.clear(); save(); },
  };
}

export type Idempotency = ReturnType<typeof createIdempotency>;

export function browserStorage(): StorageLike | null {
  try { return globalThis.sessionStorage ?? null; } catch { return null; }
}
