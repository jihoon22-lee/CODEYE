import { describe, expect, it } from 'vitest';
import { deepLinkFragment, readDeepLink } from './deepLink';

const P = '11111111-2222-4333-8444-555555555555';
const R = '66666666-7777-4888-8999-aaaaaaaaaaaa';

describe('readDeepLink', () => {
  it('reads the project and run of a codeye up link, with or without the launch token', () => {
    expect(readDeepLink(`#launch=tok&project=${P}&run=${R}`)).toEqual({ project: P, run: R });
    expect(readDeepLink(`#project=${P}`)).toEqual({ project: P, run: null });
  });
  it('accepts only canonical IDs and never a run without its project', () => {
    expect(readDeepLink('#project=P&run=R')).toEqual({ project: null, run: null });
    expect(readDeepLink(`#run=${R}`)).toEqual({ project: null, run: null });
    expect(readDeepLink(`#project=${R.toUpperCase()}`)).toEqual({ project: null, run: null });
    expect(readDeepLink('')).toEqual({ project: null, run: null });
  });
  it('keeps only the deep-link keys in the fragment', () => {
    expect(deepLinkFragment(new URLSearchParams(`launch=t&a=1&project=${P}&run=${R}`))).toBe(`#project=${P}&run=${R}`);
    expect(deepLinkFragment(new URLSearchParams('launch=t&a=1'))).toBe('');
  });
});
