// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createIdempotency, newKey } from './idempotency';

afterEach(() => vi.unstubAllGlobals());

function memoryStorage() {
  const data = new Map<string, string>();
  return { getItem: (k: string) => data.get(k) ?? null, setItem: (k: string, v: string) => void data.set(k, v), data };
}

describe('idempotency keys', () => {
  it('reuses a key for the same user, project and body until the server accepts it', () => {
    const idem = createIdempotency();
    const first = idem.keyFor('u', 'p');
    expect(idem.keyFor('u', 'p')).toBe(first);
    expect(idem.pending('u', 'p')).toBe(true);
    idem.complete('u', 'p');
    expect(idem.pending('u', 'p')).toBe(false);
    expect(idem.keyFor('u', 'p')).not.toBe(first);
  });

  it('separates users, projects and bodies', () => {
    const idem = createIdempotency();
    const base = idem.keyFor('u', 'p');
    expect(idem.keyFor('u2', 'p')).not.toBe(base);
    expect(idem.keyFor('u', 'p2')).not.toBe(base);
    expect(idem.keyFor('u', 'p', { profile: 'focused' })).not.toBe(base);
    expect(idem.keyFor('u', 'p')).toBe(base);
  });

  it('survives a page reload through session storage and forgets accepted requests', () => {
    const storage = memoryStorage();
    const key = createIdempotency(storage).keyFor('u', 'p');
    const reloaded = createIdempotency(storage);
    expect(reloaded.keyFor('u', 'p')).toBe(key);
    reloaded.complete('u', 'p');
    expect(createIdempotency(storage).pending('u', 'p')).toBe(false);
  });

  it('works when storage is unavailable or corrupt', () => {
    const broken = { getItem: () => { throw new Error('denied'); }, setItem: () => { throw new Error('denied'); } };
    const idem = createIdempotency(broken);
    expect(idem.keyFor('u', 'p')).toBe(idem.keyFor('u', 'p'));
    const corrupt = memoryStorage();
    corrupt.data.set('codeye.idempotency.v1', '{not json');
    expect(() => createIdempotency(corrupt).keyFor('u', 'p')).not.toThrow();
  });

  it('produces header-safe keys within the 200 character limit, even without randomUUID', () => {
    const real = globalThis.crypto;
    vi.stubGlobal('crypto', { getRandomValues: real.getRandomValues.bind(real) });
    const a = newKey();
    const b = newKey();
    expect(a).not.toBe(b);
    for (const key of [a, b, newKeyWithUUID()]) expect(key).toMatch(/^codeye-[0-9a-f-]{32,36}$/);
  });
});

function newKeyWithUUID() {
  vi.unstubAllGlobals();
  return newKey();
}
