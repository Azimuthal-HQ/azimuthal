/**
 * The credential boundary in `apiFetch`, and the portal surface's wire
 * contract.
 *
 * A portal session token and an internal access token are signed with the SAME
 * RSA key and separated only by their `aud` claim
 * (`internal/core/portal/token.go`), so which token `apiFetch` reaches for is
 * the whole boundary between an external customer and an org member. Two
 * failures live here and neither is visible to the type checker, because both
 * are a matter of which of two `localStorage` keys was read:
 *
 *  1. Sending the wrong token. It does not fail loudly — it 401s, which reads
 *     as "your session expired" on a surface where the customer has no way to
 *     tell the difference.
 *  2. Letting a portal 401 run the internal 401 handler. That handler clears
 *     `azimuthal_access_token` and hard-navigates to `/login`. An agent who
 *     opened their own service desk in the same browser would silently lose
 *     their internal session, and an external customer would be shown the
 *     internal product's sign-in page — the one thing this whole surface
 *     exists to keep them from learning about.
 *
 * Everything asserted here is a request `apiFetch` built or a side effect it
 * performed, because that is the only observable this module owns.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { createElement, type ReactNode } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { installLocalStorageStub } from '../test/localStorageStub';
import { getToken, setToken } from './auth';
import { getPortalToken, setPortalSession } from './portalSession';
import {
  useMe,
  usePortalDescribe,
  usePortalRequest,
  usePortalRequests,
  usePortalSignOut,
  useRedeemPortalLink,
  useReplyToPortalRequest,
  useRequestPortalLink,
  useSubmitPortalRequest,
} from './api';

const PORTAL_KEY = 'k3y0fth3p0rtal99';
const EMAIL = 'customer@example.test';
const REFERENCE = '9f1c2d3e-4a5b-6c7d-8e9f-0a1b2c3d4e5f';

// Distinguishable on sight, so a failure message names which store was read
// rather than showing two opaque JWT-shaped strings.
const INTERNAL_TOKEN = 'INTERNAL-TOKEN-must-never-reach-a-portal-route';
const PORTAL_TOKEN = 'PORTAL-TOKEN-must-never-reach-an-internal-route';

interface Captured {
  url: string;
  method: string;
  /** undefined when the request carried no body at all. */
  body: string | undefined;
  authorization: string | null;
  contentType: string | null;
}

let calls: Captured[] = [];
/** Set per test: what the stub answers with. */
let nextStatus = 200;
let nextBody: unknown = {};
/**
 * What `response.url` reports. A hand-built `Response` has `url === ''`, and
 * the 401 handler keys on it, so every test that cares must set this — an
 * empty url matches none of the auth-critical substrings and would make the
 * redirect tests pass for the wrong reason.
 */
let nextURL = '';

/** Every value written to `window.location.href`, in order. */
let hrefWrites: string[] = [];
let realLocation: Location;

function installLocationStub(pathname: string): void {
  const stub = {
    pathname,
    get href(): string {
      return hrefWrites[hrefWrites.length - 1] ?? '';
    },
    set href(value: string) {
      hrefWrites.push(value);
    },
  };
  Object.defineProperty(window, 'location', {
    value: stub,
    configurable: true,
    writable: true,
  });
}

const qc = new QueryClient({
  defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
});

function wrapper({ children }: { children: ReactNode }) {
  return createElement(QueryClientProvider, { client: qc }, children);
}

beforeEach(() => {
  // Both token stores read localStorage on every request, and the partial
  // jsdom Storage shim this repo works around would throw before fetch is
  // reached.
  installLocalStorageStub();
  realLocation = window.location;
  hrefWrites = [];
  installLocationStub('/portal/somewhere');
  calls = [];
  nextStatus = 200;
  nextBody = {};
  nextURL = '';
  qc.clear();
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string, init?: RequestInit) => {
      const headers = new Headers(init?.headers);
      calls.push({
        url: String(url),
        method: String(init?.method ?? 'GET'),
        body: init?.body === undefined ? undefined : String(init.body),
        authorization: headers.get('Authorization'),
        contentType: headers.get('Content-Type'),
      });
      const res = new Response(JSON.stringify(nextBody), {
        status: nextStatus,
        headers: { 'Content-Type': 'application/json' },
      });
      // `url` is a prototype getter that always reports '' for a constructed
      // Response; an own property shadows it.
      Object.defineProperty(res, 'url', { value: nextURL, configurable: true });
      return Promise.resolve(res);
    }),
  );
});

afterEach(() => {
  Object.defineProperty(window, 'location', {
    value: realLocation,
    configurable: true,
    writable: true,
  });
  vi.unstubAllGlobals();
});

/** Seeds BOTH stores, so every test can tell which one was read. */
function seedBothSessions(): void {
  setToken(INTERNAL_TOKEN);
  setPortalSession(PORTAL_KEY, {
    session_token: PORTAL_TOKEN,
    expires_at: Date.now() + 60 * 60 * 1000,
    requester: { email: EMAIL, name: 'A Customer' },
    portal: { name: 'Support', intro: 'Hello' },
  });
}

/** Waits for exactly n requests and returns them in order. */
async function requests(n = 1): Promise<Captured[]> {
  await waitFor(() => expect(calls.length).toBe(n));
  return calls;
}

// ---------------------------------------------------------------------------
// 1 — which store each credential reads
// ---------------------------------------------------------------------------

describe('apiFetch selects a credential family, it does not guess', () => {
  it('sends the PORTAL token on a portal route and never the internal one', async () => {
    seedBothSessions();
    renderHook(() => usePortalRequests(PORTAL_KEY, EMAIL), { wrapper });

    const [req] = await requests();
    expect(req.url).toBe(`/api/v1/portal/${PORTAL_KEY}/my/requests`);
    expect(req.authorization).toBe(`Bearer ${PORTAL_TOKEN}`);
    // Stated separately from the equality above: if the selection ever becomes
    // a fallback chain rather than a choice, this is the assertion that names
    // what leaked.
    expect(req.authorization).not.toContain(INTERNAL_TOKEN);
  });

  it('sends the INTERNAL token on an internal route and never the portal one', async () => {
    seedBothSessions();
    renderHook(() => useMe(), { wrapper });

    const [req] = await requests();
    expect(req.url).toBe('/api/v1/auth/me');
    expect(req.authorization).toBe(`Bearer ${INTERNAL_TOKEN}`);
    expect(req.authorization).not.toContain(PORTAL_TOKEN);
  });

  it('sends NO credential on an internal route when only a portal session exists', async () => {
    // The equality above cannot catch a fallback from the internal store to
    // the portal one, because both stores were populated. Here only the portal
    // store is: an internal request must go out unauthenticated rather than
    // presenting a token whose audience the internal parser refuses.
    setPortalSession(PORTAL_KEY, {
      session_token: PORTAL_TOKEN,
      expires_at: Date.now() + 60 * 60 * 1000,
      requester: { email: EMAIL, name: 'A Customer' },
      portal: { name: 'Support', intro: 'Hello' },
    });
    renderHook(() => useMe(), { wrapper });

    const [req] = await requests();
    expect(req.authorization).toBeNull();
  });

  it("sends NO credential on the portal's three unauthenticated routes", async () => {
    // A stale internal token here would attach an org member's identity to a
    // request made by a stranger who merely opened the sign-in page.
    seedBothSessions();
    renderHook(() => usePortalDescribe(PORTAL_KEY), { wrapper });
    let [req] = await requests();
    expect(req.url).toBe(`/api/v1/portal/${PORTAL_KEY}`);
    expect(req.authorization).toBeNull();

    calls = [];
    const link = renderHook(() => useRequestPortalLink(PORTAL_KEY), { wrapper });
    link.result.current.mutate({ email: EMAIL });
    [req] = await requests();
    expect(req.url).toBe(`/api/v1/portal/${PORTAL_KEY}/auth/request-link`);
    expect(req.authorization).toBeNull();

    calls = [];
    const redeem = renderHook(() => useRedeemPortalLink(), { wrapper });
    redeem.result.current.mutate({ portalKey: PORTAL_KEY, token: 'magic' });
    [req] = await requests();
    expect(req.url).toBe('/api/v1/portal/auth/redeem');
    expect(req.authorization).toBeNull();
  });

  it('sends no credential at all when the portal session has expired', async () => {
    // getPortalToken returns null for an expired session rather than sending a
    // token the server will refuse. Asserting it here keeps the expiry check
    // from being deleted as redundant with the server's own.
    setToken(INTERNAL_TOKEN);
    setPortalSession(PORTAL_KEY, {
      session_token: PORTAL_TOKEN,
      expires_at: Date.now() - 1000,
      requester: { email: EMAIL, name: 'A Customer' },
      portal: { name: 'Support', intro: 'Hello' },
    });
    renderHook(() => usePortalRequests(PORTAL_KEY, EMAIL), { wrapper });

    const [req] = await requests();
    expect(req.authorization).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// 2 — a portal 401 never touches the internal session
// ---------------------------------------------------------------------------

describe('the 401 handler belongs to the internal credential alone', () => {
  it('does not clear tokens or redirect on a 401 from a portal route', async () => {
    seedBothSessions();
    nextStatus = 401;
    nextURL = `http://localhost/api/v1/portal/${PORTAL_KEY}/my/requests`;

    const { result } = renderHook(() => usePortalRequests(PORTAL_KEY, EMAIL), { wrapper });
    await waitFor(() => expect(result.current.isError).toBe(true));

    expect(getToken()).toBe(INTERNAL_TOKEN);
    expect(getPortalToken(PORTAL_KEY)).toBe(PORTAL_TOKEN);
    expect(hrefWrites).toEqual([]);
  });

  it('still does not, when the portal URL itself looks auth-critical', async () => {
    // THIS is the case that pins the `credential === 'internal'` conjunct. The
    // test above passes with the conjunct deleted, because the substring check
    // that follows it matches no portal path that exists TODAY — api.ts says
    // as much, and says the gate is there because that is one rename away.
    //
    // So this test IS the rename. The URL below is not a route we serve; it is
    // the hypothetical in which a portal path acquires an '/auth/me' segment,
    // and the property under test is that the credential decides the outcome
    // irrespective of what the path happens to spell.
    seedBothSessions();
    nextStatus = 401;
    nextURL = `http://localhost/api/v1/portal/${PORTAL_KEY}/my/auth/me`;

    const { result } = renderHook(() => usePortalRequests(PORTAL_KEY, EMAIL), { wrapper });
    await waitFor(() => expect(result.current.isError).toBe(true));

    expect(getToken()).toBe(INTERNAL_TOKEN);
    expect(getPortalToken(PORTAL_KEY)).toBe(PORTAL_TOKEN);
    expect(hrefWrites).toEqual([]);
  });

  it('leaves an internal session alone when a stale sign-in link is refused', async () => {
    // 401 is redeem's NORMAL failure — "this sign-in link is no longer valid" —
    // and it is the likeliest 401 anyone will ever see on this surface. The
    // credential is 'none' here rather than 'portal', and the conjunct excludes
    // that too; the URL is the plausible rename of redeem to a refresh route.
    seedBothSessions();
    nextStatus = 401;
    nextURL = 'http://localhost/api/v1/portal/auth/refresh';

    const { result } = renderHook(() => useRedeemPortalLink(), { wrapper });
    result.current.mutate({ portalKey: PORTAL_KEY, token: 'stale' });
    await waitFor(() => expect(result.current.isError).toBe(true));

    expect(getToken()).toBe(INTERNAL_TOKEN);
    expect(hrefWrites).toEqual([]);
  });

  it('DOES clear and redirect on a 401 from /auth/me — the internal path is intact', async () => {
    // The other half of the conjunct. Without this, every assertion above is
    // satisfied by an apiFetch that simply never redirects at all.
    seedBothSessions();
    installLocationStub('/beacon/tickets');
    nextStatus = 401;
    nextURL = 'http://localhost/api/v1/auth/me';

    const { result } = renderHook(() => useMe(), { wrapper });
    await waitFor(() => expect(result.current.isError).toBe(true));

    expect(getToken()).toBeNull();
    expect(hrefWrites).toEqual(['/login?redirect=%2Fbeacon%2Ftickets']);
  });
});

// ---------------------------------------------------------------------------
// 3 — the request bodies DecodeJSON will accept
// ---------------------------------------------------------------------------
//
// `respond.DecodeJSON` sets DisallowUnknownFields and refuses an empty body,
// so both an extra key and a body where none is wanted are a 400 whose message
// names neither the field nor the cause.

describe('portal request bodies', () => {
  it('sends NO body on sign-out, not even {}', async () => {
    seedBothSessions();
    nextStatus = 204;

    const { result } = renderHook(() => usePortalSignOut(PORTAL_KEY), { wrapper });
    result.current.mutate();

    const [req] = await requests();
    expect(req.url).toBe(`/api/v1/portal/${PORTAL_KEY}/my/auth/sign-out`);
    expect(req.method).toBe('POST');
    expect(req.body).toBeUndefined();
    // A Content-Type announcing JSON that is not there is the other half of
    // the same mistake, and apiFetch only sets the header when a body exists.
    expect(req.contentType).toBeNull();
    expect(req.authorization).toBe(`Bearer ${PORTAL_TOKEN}`);
  });

  it('sends exactly the keys each route declares, and no others', async () => {
    seedBothSessions();

    const link = renderHook(() => useRequestPortalLink(PORTAL_KEY), { wrapper });
    link.result.current.mutate({ email: EMAIL });
    let [req] = await requests();
    // `name` is omitted entirely rather than sent as null: the server's field
    // is a string, and null decodes to a type error before the handler runs.
    expect(JSON.parse(req.body!)).toEqual({ email: EMAIL });

    calls = [];
    const named = renderHook(() => useRequestPortalLink(PORTAL_KEY), { wrapper });
    named.result.current.mutate({ email: EMAIL, name: 'A Customer' });
    [req] = await requests();
    expect(JSON.parse(req.body!)).toEqual({ email: EMAIL, name: 'A Customer' });

    calls = [];
    const redeem = renderHook(() => useRedeemPortalLink(), { wrapper });
    // portalKey is a mutation variable the caller needs for storage; it must
    // not travel in the body, where DisallowUnknownFields rejects it.
    redeem.result.current.mutate({ portalKey: PORTAL_KEY, token: 'magic' });
    [req] = await requests();
    expect(JSON.parse(req.body!)).toEqual({ token: 'magic' });

    calls = [];
    const submit = renderHook(() => useSubmitPortalRequest(PORTAL_KEY, EMAIL), { wrapper });
    submit.result.current.mutate({ summary: 'Printer is on fire', description: 'Badly' });
    [req] = await requests();
    expect(req.url).toBe(`/api/v1/portal/${PORTAL_KEY}/my/requests`);
    expect(JSON.parse(req.body!)).toEqual({
      summary: 'Printer is on fire',
      description: 'Badly',
    });

    calls = [];
    const reply = renderHook(
      () => useReplyToPortalRequest(PORTAL_KEY, EMAIL, REFERENCE),
      { wrapper },
    );
    reply.result.current.mutate({ body: 'Still on fire' });
    [req] = await requests();
    expect(req.url).toBe(
      `/api/v1/portal/${PORTAL_KEY}/my/requests/${REFERENCE}/replies`,
    );
    expect(JSON.parse(req.body!)).toEqual({ body: 'Still on fire' });
  });

  it('reads one request from the reference the list handed it', async () => {
    seedBothSessions();
    renderHook(() => usePortalRequest(PORTAL_KEY, EMAIL, REFERENCE), { wrapper });

    const [req] = await requests();
    expect(req.url).toBe(`/api/v1/portal/${PORTAL_KEY}/my/requests/${REFERENCE}`);
    expect(req.method).toBe('GET');
    expect(req.authorization).toBe(`Bearer ${PORTAL_TOKEN}`);
  });
});

// ---------------------------------------------------------------------------
// 4 — read paths do not fire without what they need
// ---------------------------------------------------------------------------

describe('portal reads are gated on having a session to read with', () => {
  it('issues nothing without a portal key, a requester or a reference', async () => {
    seedBothSessions();

    renderHook(() => usePortalDescribe(''), { wrapper });
    renderHook(() => usePortalRequests(PORTAL_KEY, ''), { wrapper });
    renderHook(() => usePortalRequests('', EMAIL), { wrapper });
    renderHook(() => usePortalRequest(PORTAL_KEY, EMAIL, ''), { wrapper });
    renderHook(() => usePortalRequest(PORTAL_KEY, '', REFERENCE), { wrapper });

    // Nothing to wait for, which is the assertion. A short flush gives any
    // ungated query the chance to fire and fail this.
    await new Promise((r) => setTimeout(r, 20));
    expect(calls).toEqual([]);
  });
});
