/**
 * B1 frontend recovery: an auth-expired 401 on the INTERNAL credential clears
 * the stored tokens and sends the user to /login — on ANY endpoint, not just
 * the three auth URLs the old code keyed on.
 *
 * This is the T1 review's "other device's death is ugly" case. When another
 * device revokes this session (a logout-all, or this device signed out from
 * elsewhere), the next 401 arrives on whatever ordinary data endpoint the app
 * happened to call — an org's space list here — and before B1 that neither
 * cleared the dead token nor redirected, so the app sat wedged. The property
 * under test is that it now recovers.
 *
 * The portal flow is asserted UNTOUCHED in the same file: a portal 401 must
 * never run the internal handler, because sending an external customer to the
 * internal product's /login is the one thing that surface exists to prevent.
 * The exhaustive portal-credential contract lives in api.portal-credential.test.ts;
 * this file only pins the two ends of the B1 broadening.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { createElement, type ReactNode } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { installLocalStorageStub } from '../test/localStorageStub';
import { getToken, setToken } from './auth';
import { getPortalToken, setPortalSession } from './portalSession';
import { useSpaces, usePortalRequests } from './api';

const ORG_ID = '11111111-1111-1111-1111-111111111111';
const PORTAL_KEY = 'k3y0fth3p0rtal99';
const EMAIL = 'customer@example.test';
const INTERNAL_TOKEN = 'INTERNAL-TOKEN';
const PORTAL_TOKEN = 'PORTAL-TOKEN';

let nextStatus = 200;
let nextBody: unknown = {};
let nextURL = '';
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
  Object.defineProperty(window, 'location', { value: stub, configurable: true, writable: true });
}

const qc = new QueryClient({
  defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
});

function wrapper({ children }: { children: ReactNode }) {
  return createElement(QueryClientProvider, { client: qc }, children);
}

beforeEach(() => {
  installLocalStorageStub();
  realLocation = window.location;
  hrefWrites = [];
  installLocationStub('/vector/backlog');
  nextStatus = 200;
  nextBody = {};
  nextURL = '';
  qc.clear();
  vi.stubGlobal(
    'fetch',
    vi.fn(() => {
      const res = new Response(JSON.stringify(nextBody), {
        status: nextStatus,
        headers: { 'Content-Type': 'application/json' },
      });
      Object.defineProperty(res, 'url', { value: nextURL, configurable: true });
      return Promise.resolve(res);
    }),
  );
});

afterEach(() => {
  Object.defineProperty(window, 'location', { value: realLocation, configurable: true, writable: true });
  vi.unstubAllGlobals();
});

describe('an internal 401 recovers the session on any endpoint', () => {
  it('clears tokens and redirects on a 401 from an ordinary data endpoint', async () => {
    // The revoked-elsewhere case: the app polls the space list, the middleware
    // refuses the credential, and this is NOT one of /auth/login|me|refresh.
    setToken(INTERNAL_TOKEN);
    nextStatus = 401;
    nextBody = { error: { code: 'UNAUTHORIZED', message: 'missing or invalid authentication', request_id: 'r' } };
    nextURL = `http://localhost/api/v1/orgs/${ORG_ID}/spaces`;

    const { result } = renderHook(() => useSpaces(ORG_ID), { wrapper });
    await waitFor(() => expect(result.current.isError).toBe(true));

    expect(getToken()).toBeNull();
    expect(hrefWrites).toEqual(['/login?redirect=%2Fvector%2Fbacklog']);
  });
});

describe('the portal flow is untouched by the broadening', () => {
  it('does not clear tokens or redirect on a 401 from a portal route', async () => {
    setToken(INTERNAL_TOKEN);
    setPortalSession(PORTAL_KEY, {
      session_token: PORTAL_TOKEN,
      expires_at: Date.now() + 60 * 60 * 1000,
      requester: { email: EMAIL, name: 'A Customer' },
      portal: { name: 'Support', intro: 'Hello' },
    });
    nextStatus = 401;
    nextBody = { error: { code: 'UNAUTHORIZED', message: 'no', request_id: 'r' } };
    nextURL = `http://localhost/api/v1/portal/${PORTAL_KEY}/my/requests`;

    const { result } = renderHook(() => usePortalRequests(PORTAL_KEY, EMAIL), { wrapper });
    await waitFor(() => expect(result.current.isError).toBe(true));

    // The internal token is still there and no redirect happened: the portal
    // 401 belongs to the portal, not the internal session.
    expect(getToken()).toBe(INTERNAL_TOKEN);
    expect(getPortalToken(PORTAL_KEY)).toBe(PORTAL_TOKEN);
    expect(hrefWrites).toEqual([]);
  });
});
