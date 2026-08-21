import { fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { setToken } from '../../../lib/auth';
import { installLocalStorageStub } from '../../../test/localStorageStub';
import { PortalSignInPage } from '../PortalSignInPage';

/**
 * THE STAFF SIGNPOST, BOTH DIRECTIONS.
 *
 * A signed-in staff member who follows a portal URL lands on the guest form.
 * The signpost is a client-side affordance that keys off the PRESENCE of a
 * stored internal token — the same key `setToken` writes, read through the same
 * `getToken` the component uses, so this exercises the real storage coupling
 * rather than a hardcoded key that could drift. Its contents are never read: the
 * assertions below never mint a decodable token.
 *
 * The two directions are the whole test. Token present → the signpost renders
 * AND the guest form beneath it still submits (a staff member may be testing as
 * a requester, which is exactly how this gap was found). Token absent → no
 * signpost at all. Deleting the `signedInAsStaff` guard fails the first; making
 * it render unconditionally fails the second.
 *
 * The signpost is also asserted to be ZERO-CONTEXT: it is rendered under a
 * distinctive portal key and must not echo it, because it must be a generic line
 * — a role and a product name, never a space, an organisation or an identity —
 * or it becomes the very context leak the portal's zero-context sweep exists to
 * catch.
 */

const mocks = vi.hoisted(() => ({
  requestLinkMutate: vi.fn(),
}));

// The component reaches `lib/api` for the request-link mutation and for
// `friendlyErrorMessage`. Mocking the module also breaks the api↔auth import
// cycle, so the real `lib/auth` (getToken/setToken) runs untouched.
vi.mock('../../../lib/api', () => ({
  useRequestPortalLink: () => ({
    mutate: (
      vars: { email: string; name?: string },
      opts?: { onSuccess?: () => void; onError?: (e: unknown) => void },
    ) => {
      mocks.requestLinkMutate(vars);
      // Mirror the server's 202 happy path so the form reaches its terminal
      // state — proof the signpost does not sit in front of a broken form.
      opts?.onSuccess?.();
    },
    isPending: false,
  }),
  friendlyErrorMessage: (_e: unknown, fallback: string) => fallback,
}));

// A portal key with no overlap with the signpost's copy: if it appeared in the
// signpost the "generic, not personalised" assertion would catch it.
const PORTAL_KEY = 'distinctivesupportkey';

function renderSignIn() {
  return render(
    <MemoryRouter initialEntries={[`/portal/${PORTAL_KEY}`]}>
      <Routes>
        <Route path="/portal/:portalKey" element={<PortalSignInPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  // The internal token lives in localStorage, and this Node/jsdom pairing ships
  // a partial Storage whose setItem is missing — see test/localStorageStub.ts.
  // A fresh stub each test also guarantees no token leaks between the two
  // directions.
  installLocalStorageStub();
  mocks.requestLinkMutate.mockClear();
});

describe('the portal sign-in staff signpost', () => {
  it('renders when an internal session is present, and the guest form still submits', () => {
    // Presence is all that is read. This string is deliberately NOT a decodable
    // JWT — the signpost must not care what the token says.
    setToken('an-opaque-internal-token');

    renderSignIn();

    const signpost = screen.getByTestId('portal-staff-signpost');
    expect(signpost).toBeInTheDocument();
    expect(signpost).toHaveTextContent(/signed in as staff/i);
    expect(signpost).toHaveTextContent(/raise and work tickets in Beacon/i);

    // The link points at the internal product, not back into the portal.
    const link = within(signpost).getByRole('link', { name: 'Beacon' });
    expect(link).toHaveAttribute('href', '/beacon');

    // Generic, not personalised: the portal key must not have leaked in.
    expect(signpost.innerHTML).not.toContain(PORTAL_KEY);

    // The guest form beneath the signpost is fully usable.
    fireEvent.change(screen.getByTestId('portal-email'), {
      target: { value: 'dana@customer.example' },
    });
    fireEvent.click(screen.getByTestId('portal-signin-submit'));

    expect(mocks.requestLinkMutate).toHaveBeenCalledTimes(1);
    expect(mocks.requestLinkMutate).toHaveBeenCalledWith({
      email: 'dana@customer.example',
      name: undefined,
    });
    // onSuccess ran, so the form advanced to its conditional confirmation.
    expect(screen.getByTestId('portal-link-sent')).toBeInTheDocument();
  });

  it('renders no signpost when no internal session is present', () => {
    // No setToken: getToken() returns null.
    renderSignIn();

    expect(screen.queryByTestId('portal-staff-signpost')).not.toBeInTheDocument();
    // The guest form is unchanged and present.
    expect(screen.getByTestId('portal-signin-page')).toBeInTheDocument();
    expect(screen.getByTestId('portal-email')).toBeInTheDocument();
    expect(screen.getByTestId('portal-signin-submit')).toBeInTheDocument();
  });
});
