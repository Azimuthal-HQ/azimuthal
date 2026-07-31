import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { PortalRedeemPage } from '../PortalRedeemPage';
import { installLocalStorageStub } from '../../../test/localStorageStub';

/**
 * Redeeming a magic link, and in particular redeeming one the server refuses.
 *
 * A refused link is the ordinary case, not the exotic one: portal links are
 * single-use and short-lived, so a customer who clicks the same email twice, or
 * clicks yesterday's, lands here. Three things must hold, and each has a
 * plausible implementation that breaks it.
 *
 *  1. NO SESSION IS STORED. Writing the token before or alongside the request —
 *     "we have it anyway, store it and let the next call sort it out" — leaves
 *     a stored session whose token the server has already rejected. Every later
 *     request 401s, the guard sees a token and lets the page through, and the
 *     result reads like an outage rather than an expired link.
 *  2. THE PAGE DOES NOT NAVIGATE TO /login. That is the internal product's sign
 *     in. An external requester has no account there and cannot create one, so
 *     it is a dead end that also discloses what is on the other side of the
 *     domain.
 *  3. A TERMINAL STATE IS RENDERED, with a way back. A blank body or an endless
 *     "Signing you in…" is the same dead end wearing a spinner.
 */

const PORTAL_KEY = 'pk_7f3a9c2e1b';
const TOKEN = 'raw-link-token';
const SESSION_PREFIX = 'azimuthal_portal_session:';

const state = vi.hoisted(() => ({
  outcome: 'error' as 'error' | 'success',
  mutate: vi.fn(),
}));

vi.mock('../../../lib/api', () => ({
  useRedeemPortalLink: () => ({
    mutate: (
      vars: { portalKey: string; token: string },
      opts?: {
        onSuccess?: (res: unknown) => void;
        onError?: (err: unknown) => void;
      },
    ) => {
      state.mutate(vars);
      if (state.outcome === 'success') {
        opts?.onSuccess?.({
          session_token: 'portal.session.token',
          expires_in: 3600,
          requester: { email: 'dana@customer.example', name: 'Dana Whitfield' },
          portal: { name: 'Acme Support', intro: '' },
        });
      } else {
        // What a 401 looks like to the caller.
        opts?.onError?.(new Error('sign in link is no longer valid'));
      }
    },
    isPending: false,
  }),
}));

/** Every localStorage key, read through the Storage interface the stub gives. */
function storedKeys(): string[] {
  const keys: string[] = [];
  for (let i = 0; i < window.localStorage.length; i += 1) {
    const key = window.localStorage.key(i);
    if (key !== null) keys.push(key);
  }
  return keys;
}

function renderRedeem() {
  return render(
    <MemoryRouter initialEntries={[`/portal/${PORTAL_KEY}/signin/${TOKEN}`]}>
      <Routes>
        <Route path="/portal/:portalKey/signin/:linkToken" element={<PortalRedeemPage />} />
        <Route path="/portal/:portalKey" element={<div>PORTAL SIGN IN PAGE</div>} />
        <Route path="/portal/:portalKey/requests" element={<div>PORTAL REQUESTS PAGE</div>} />
        {/* The destination that must never be reached, made visible. */}
        <Route path="/login" element={<div>INTERNAL LOGIN PAGE</div>} />
        <Route path="*" element={<div>SOMEWHERE ELSE ENTIRELY</div>} />
      </Routes>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  installLocalStorageStub();
  state.mutate.mockClear();
  state.outcome = 'error';
});

describe('PortalRedeemPage — the server refuses the link', () => {
  it('renders a terminal state offering a fresh link', () => {
    renderRedeem();
    expect(screen.getByTestId('portal-redeem-failed')).toBeInTheDocument();
    expect(screen.getByText('This sign-in link no longer works')).toBeInTheDocument();
    const retry = screen.getByTestId('portal-redeem-retry');
    expect(retry).toHaveAttribute('href', `/portal/${PORTAL_KEY}`);
    // Not still spinning.
    expect(screen.queryByTestId('portal-redeem-pending')).not.toBeInTheDocument();
  });

  it('stores NO portal session', () => {
    renderRedeem();
    expect(storedKeys().filter((k) => k.startsWith(SESSION_PREFIX))).toEqual([]);
    // And nothing else either — a refused redemption writes no state at all.
    expect(storedKeys()).toEqual([]);
  });

  it('does not navigate to the internal sign-in page', () => {
    renderRedeem();
    expect(screen.queryByText('INTERNAL LOGIN PAGE')).not.toBeInTheDocument();
    expect(screen.queryByText('SOMEWHERE ELSE ENTIRELY')).not.toBeInTheDocument();
    expect(screen.queryByText('PORTAL REQUESTS PAGE')).not.toBeInTheDocument();
    // Every link on the failed page stays inside this portal.
    for (const anchor of Array.from(document.querySelectorAll('a'))) {
      expect(anchor.getAttribute('href')).toMatch(/^\/portal\//);
    }
  });
});

describe('PortalRedeemPage — the server accepts the link', () => {
  // Without this case the three assertions above would also pass against a
  // component that never called the endpoint at all.
  beforeEach(() => {
    state.outcome = 'success';
  });

  it('stores the session and lands on the requester’s own requests', () => {
    renderRedeem();
    expect(screen.getByText('PORTAL REQUESTS PAGE')).toBeInTheDocument();
    const stored = storedKeys().filter((k) => k.startsWith(SESSION_PREFIX));
    expect(stored).toEqual([`${SESSION_PREFIX}${PORTAL_KEY}`]);
    const record = JSON.parse(window.localStorage.getItem(stored[0]) ?? '{}');
    expect(record.session_token).toBe('portal.session.token');
    // expires_in is SECONDS on the wire; sessionFromRedeem converts to an
    // absolute ms epoch, so a session stored today is not read as fresh
    // forever — nor as already expired.
    expect(record.expires_at).toBeGreaterThan(Date.now());
    expect(record.expires_at).toBeLessThanOrEqual(Date.now() + 3600 * 1000);
  });

  it('redeems the token from the URL path, not from a query string', () => {
    renderRedeem();
    expect(state.mutate).toHaveBeenCalledWith({ portalKey: PORTAL_KEY, token: TOKEN });
  });
});
