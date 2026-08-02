import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { installLocalStorageStub } from '../test/localStorageStub';
import { setToken } from './auth';
import { revokeSession } from './api';

/**
 * The wire contract of the sign-out request.
 *
 * `auth.logout.test.ts` mocks `./api` wholesale, so it proves `logout()` calls
 * `revokeSession` and nothing about what `revokeSession` does. That gap is not
 * academic: `logout()` swallows a failed revocation on purpose, so a wrong
 * path, a wrong method or a missing Authorization header produces a 404 or a
 * 401 that is silently discarded — the tokens still clear, the user still
 * lands on /login, every test still passes, and the session is never actually
 * revoked. Exactly the state this whole change exists to leave behind.
 *
 * So the request itself is asserted here, against a stubbed global fetch,
 * which is the only observable this module owns.
 */

const ACCESS = 'azimuthal_access_token';

let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  installLocalStorageStub();
  fetchMock = vi.fn(async () =>
    new Response(JSON.stringify({ message: 'logged out' }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }),
  );
  vi.stubGlobal('fetch', fetchMock);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('revokeSession', () => {
  it('POSTs the access token to /auth/logout', async () => {
    setToken('the-access-token');

    await revokeSession();

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe('/api/v1/auth/logout');
    expect(init.method).toBe('POST');
    expect(new Headers(init.headers).get('Authorization')).toBe('Bearer the-access-token');
  });

  it('rejects when the server refuses, rather than reporting success', async () => {
    // `logout()` swallows this deliberately, but it must have something to
    // swallow: a revokeSession that resolved on a 401 would make the failure
    // undetectable everywhere, including in any future caller that does want
    // to know.
    setToken('a-stale-token');
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: { code: 'UNAUTHORIZED', message: 'no', request_id: 'r' } }), {
        status: 401,
        headers: { 'Content-Type': 'application/json' },
      }),
    );

    await expect(revokeSession()).rejects.toThrow();
    expect(window.localStorage.getItem(ACCESS)).toBe('a-stale-token');
  });

  it('does not clear local state itself', async () => {
    // The clear belongs to `logout()`'s finally block, so that it runs on the
    // failure path too. If revokeSession started clearing as well, the
    // ordering guarantee in auth.logout.test.ts would silently stop meaning
    // anything.
    setToken('the-access-token');

    await revokeSession();

    expect(window.localStorage.getItem(ACCESS)).toBe('the-access-token');
  });
});
