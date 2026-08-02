import { beforeEach, describe, expect, it, vi } from 'vitest';

import { installLocalStorageStub } from '../test/localStorageStub';

/**
 * Signing out must revoke, not merely forget.
 *
 * Before the v0.4.1 trust patch `logout()` was `removeToken(); removeRefresh-
 * Token();` and nothing else, and there was no test for this module at all.
 * The tokens are stateless RS256 JWTs: a copy taken out of localStorage kept
 * working until it expired, however thoroughly the tab forgot it. Clearing
 * storage is housekeeping; the POST is the sign-out.
 *
 * MUTATION-TESTED, and the results are written down because one of them was
 * not what it looked like:
 *
 *   - delete the `await revokeSession()` call → "revokes the session on the
 *     server" and "revokes BEFORE clearing local state" both fail.
 *   - move the local clear above the `try` → the same two fail (the guard sees
 *     no token, so nothing is revoked).
 *   - move the clear INTO the `try`, after the await → "clears local state even
 *     when the server call fails" fails. This is the realistic slip: written
 *     sequentially it looks right and works on the happy path.
 *   - delete only the `finally` and leave the clear after the catch → NOTHING
 *     fails, and that is correct rather than a gap in the tests. The catch
 *     swallows the rejection, so control reaches the clear either way; the two
 *     spellings are genuinely equivalent. `finally` is kept because it stays
 *     correct if the catch is ever narrowed or removed.
 *
 * `./api` is mocked whole here rather than partially: importing the real
 * module would drag react-query and the entire client into a test about two
 * localStorage keys, and `auth.ts` uses exactly one thing from it.
 */

const { revokeSession, tokenAtCallTime } = vi.hoisted(() => {
  const seen: (string | null)[] = [];
  return {
    tokenAtCallTime: seen,
    revokeSession: vi.fn(async () => {
      // Recorded from inside the call, which is the only way to assert
      // ordering rather than merely assert that both things happened.
      seen.push(window.localStorage.getItem('azimuthal_access_token'));
    }),
  };
});

vi.mock('./api', () => ({ revokeSession }));

const { getToken, logout, setRefreshToken, setToken } = await import('./auth');

const ACCESS = 'azimuthal_access_token';
const REFRESH = 'azimuthal_refresh_token';

beforeEach(() => {
  installLocalStorageStub();
  tokenAtCallTime.length = 0;
  revokeSession.mockClear();
  revokeSession.mockImplementation(async () => {
    tokenAtCallTime.push(window.localStorage.getItem(ACCESS));
  });
});

describe('logout', () => {
  it('revokes the session on the server', async () => {
    setToken('a-token');
    setRefreshToken('a-refresh-token');

    await logout();

    expect(revokeSession).toHaveBeenCalledTimes(1);
  });

  it('revokes BEFORE clearing local state', async () => {
    // Revoking needs the credential. Clear first and the request goes out
    // unauthenticated, gets a 401, and revokes nothing — which is the
    // failure this ordering exists to prevent, and which no amount of
    // "both happened" assertion would catch.
    setToken('a-token');
    setRefreshToken('a-refresh-token');

    await logout();

    expect(tokenAtCallTime).toEqual(['a-token']);
  });

  it('clears both tokens', async () => {
    setToken('a-token');
    setRefreshToken('a-refresh-token');

    await logout();

    expect(getToken()).toBeNull();
    expect(window.localStorage.getItem(REFRESH)).toBeNull();
  });

  it('clears local state even when the server call fails', async () => {
    // Somebody who has pressed Sign out ends up signed out of this browser
    // whether or not the server was reachable.
    revokeSession.mockRejectedValueOnce(new Error('network down'));
    setToken('a-token');
    setRefreshToken('a-refresh-token');

    await expect(logout()).resolves.toBeUndefined();

    expect(getToken()).toBeNull();
    expect(window.localStorage.getItem(REFRESH)).toBeNull();
  });

  it('does not call the server when there is no token to revoke', async () => {
    // An unauthenticated POST could only ever produce a 401 to swallow.
    await logout();

    expect(revokeSession).not.toHaveBeenCalled();
  });

  it('leaves a portal session alone', async () => {
    // The internal and portal credential families are deliberately separate
    // stores (web/src/lib/portalSession.ts). An agent who has their own
    // customer portal open in the same browser must not lose it by signing
    // out of the internal product.
    setToken('a-token');
    window.localStorage.setItem('azimuthal_portal_session:abc', 'portal-token');

    await logout();

    expect(window.localStorage.getItem('azimuthal_portal_session:abc')).toBe('portal-token');
  });
});
