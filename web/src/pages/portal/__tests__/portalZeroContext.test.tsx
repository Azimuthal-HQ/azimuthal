import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type {
  PortalRequestDetailResponse,
  PortalRequestSummary,
} from '../../../lib/api';
import { setPortalSession } from '../../../lib/portalSession';
import { installLocalStorageStub } from '../../../test/localStorageStub';
import { RequirePortalSession } from '../../../components/portal/RequirePortalSession';
import { PortalLayout } from '../PortalLayout';
import { PortalSignInPage } from '../PortalSignInPage';
import { PortalRedeemPage } from '../PortalRedeemPage';
import { PortalRequestsPage } from '../PortalRequestsPage';
import { PortalNewRequestPage } from '../PortalNewRequestPage';
import { PortalRequestDetailPage } from '../PortalRequestDetailPage';
import { PortalNotFoundPage } from '../PortalNotFoundPage';

/**
 * THE ZERO-CONTEXT SWEEP.
 *
 * Ported from `components/search/__tests__/search.test.tsx`, which asserts the
 * same class of thing for share-only search hits: that a surface does not
 * reintroduce a container the API deliberately withheld, and does not render a
 * placeholder in its place.
 *
 * The method is what makes it worth having. Each portal page is rendered with
 * a fixture that has been ENRICHED — the portal DTO carries extra keys naming
 * a space, an organisation and an internal ticket reference, exactly as it
 * would if somebody widened a query, spread a wider row into the response, or
 * reached for `(row as any).space_key` to make a heading nicer. The DTO type
 * does not have those fields; a careless component would render them anyway.
 * The sweep then asserts none of those strings reaches the document, in TEXT
 * or in MARKUP — the markup pass is what covers title, aria-label and data-*
 * attributes, which textContent alone would miss.
 *
 * Every case also makes a POSITIVE sighting first. Without it a page that
 * rendered nothing at all — a crashed boundary, a guard that redirected, a
 * component renamed out from under the import — would pass every "does not
 * contain" assertion and read as proof of a guarantee it never tested.
 */

// The container identity that must never surface. These values exist ONLY in
// the enriched fixtures below, never in a portal DTO field.
const CONTAINER = {
  space_key: 'SVCDESK',
  space_slug: 'internal-service-desk',
  space_name: 'Tier Two Escalations',
  space_id: 'ffffffff-1111-2222-3333-444444444444',
  org_id: 'aaaaaaaa-9999-8888-7777-666666666666',
  ticket_ref: 'SVCDESK-42',
  ticket_number: 42,
  module: 'beacon',
  assignee_name: 'Priya Raghavan',
  workflow_state: 'Awaiting third party',
};

const FORBIDDEN = [
  CONTAINER.space_key,
  CONTAINER.space_slug,
  CONTAINER.space_name,
  CONTAINER.space_id,
  CONTAINER.org_id,
  CONTAINER.ticket_ref,
  CONTAINER.assignee_name,
  CONTAINER.workflow_state,
];

const PORTAL_KEY = 'examplesupportportal';
const REFERENCE = '6f1d8a20-4c11-4c9b-9f2e-7b3d5e0a1c44';
const SUMMARY = 'Card reader stopped taking chip payments';
const MESSAGE_BODY = 'Thanks — we have ordered a replacement unit.';

/** Attach the container fields the wire does not carry. */
function enrich<T extends object>(dto: T): T {
  return { ...dto, ...CONTAINER } as T;
}

const summaryFixture = enrich<PortalRequestSummary>({
  reference: REFERENCE,
  summary: SUMMARY,
  status: 'In progress',
  created_at: '2026-07-01T09:00:00Z',
  updated_at: '2026-07-28T16:30:00Z',
});

const detailFixture = enrich<PortalRequestDetailResponse>({
  request: enrich({
    reference: REFERENCE,
    summary: SUMMARY,
    status: 'In progress',
    description: 'It reads the magstripe but rejects every chip card.',
    created_at: '2026-07-01T09:00:00Z',
    updated_at: '2026-07-28T16:30:00Z',
  }),
  messages: [
    enrich({
      id: 'm-1',
      author: 'Sam Okoye',
      from_requester: false,
      body: MESSAGE_BODY,
      created_at: '2026-07-02T10:00:00Z',
    }),
    // An empty author is a real wire value — a display name is not required.
    enrich({
      id: 'm-2',
      author: '',
      from_requester: true,
      body: 'Any update on the replacement?',
      created_at: '2026-07-03T11:00:00Z',
    }),
  ],
});

const state = vi.hoisted(() => ({
  describe: {
    data: { name: 'Acme Support', intro: 'Tell us what you need and we’ll pick it up.' },
    isLoading: false,
    error: null as unknown,
  },
  requests: {
    data: undefined as PortalRequestSummary[] | undefined,
    isLoading: false,
    error: null as unknown,
    refetch: vi.fn(),
  },
  detail: {
    data: undefined as PortalRequestDetailResponse | undefined,
    isLoading: false,
    error: null as unknown,
    refetch: vi.fn(),
  },
  /** Drives PortalRedeemPage down its success, pending or failure path. */
  redeem: 'pending' as 'pending' | 'error',
}));

vi.mock('../../../lib/api', () => ({
  usePortalDescribe: () => state.describe,
  usePortalRequests: () => state.requests,
  usePortalRequest: () => state.detail,
  useRequestPortalLink: () => ({ mutate: vi.fn(), isPending: false }),
  useRedeemPortalLink: () => ({
    mutate: (
      _vars: unknown,
      opts?: { onError?: (e: unknown) => void },
    ) => {
      if (state.redeem === 'error') opts?.onError?.(new Error('unauthorized'));
    },
    isPending: false,
  }),
  useSubmitPortalRequest: () => ({ mutate: vi.fn(), isPending: false }),
  useReplyToPortalRequest: () => ({ mutate: vi.fn(), isPending: false }),
  usePortalSignOut: () => ({ mutate: vi.fn(), isPending: false }),
  friendlyErrorMessage: (_e: unknown, fallback: string) => fallback,
}));

/**
 * The portal subtree exactly as App.tsx declares it, so a sweep exercises the
 * real layout, the real guard and the real catch-all rather than a page in
 * isolation. That the shape here matches App.tsx is pinned separately by
 * portalRoutes.test.tsx, which reads the router itself.
 */
function renderPortal(path: string) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/portal/:portalKey" element={<PortalLayout />}>
            <Route index element={<PortalSignInPage />} />
            <Route path="signin/:linkToken" element={<PortalRedeemPage />} />
            <Route element={<RequirePortalSession />}>
              <Route path="requests" element={<PortalRequestsPage />} />
              <Route path="requests/new" element={<PortalNewRequestPage />} />
              <Route path="requests/:reference" element={<PortalRequestDetailPage />} />
            </Route>
            <Route path="*" element={<PortalNotFoundPage />} />
          </Route>
          {/* Leaving the portal is itself a defect, so the destinations are
              marked rather than silently rendered as a blank body. */}
          <Route path="/login" element={<div>INTERNAL LOGIN PAGE</div>} />
          <Route path="*" element={<div>OUTSIDE THE PORTAL SUBTREE</div>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

function signIn() {
  setPortalSession(PORTAL_KEY, {
    session_token: 'portal.session.token',
    expires_at: Date.now() + 60 * 60 * 1000,
    requester: { email: 'dana@customer.example', name: 'Dana Whitfield' },
    portal: { name: 'Acme Support', intro: 'Tell us what you need.' },
  });
}

/** (b) and the stronger markup form of it, plus (c) and (d). */
function assertNoContainerContext(container: HTMLElement) {
  const text = document.body.textContent ?? '';
  const markup = document.body.innerHTML;

  for (const token of FORBIDDEN) {
    expect(text, `rendered text discloses ${token}`).not.toContain(token);
    // Attributes — title, aria-label, data-* — are markup, not text. A leak
    // parked in one of those is invisible to a textContent assertion and fully
    // visible in the DOM inspector of anyone who looks.
    expect(markup, `markup discloses ${token}`).not.toContain(token);
  }

  const hrefs = Array.from(container.querySelectorAll('a')).map((a) => a.getAttribute('href'));
  for (const href of hrefs) {
    expect(href).toBeTruthy();
    for (const token of FORBIDDEN) {
      expect(href, `href discloses ${token}`).not.toContain(token);
    }
    expect(href, 'href reaches into the internal product').not.toContain('/beacon/');
    expect(href, 'href reaches into the internal product').not.toContain('/codex/');
    expect(href, 'href reaches into the internal product').not.toContain('/vector/');
    expect(href, 'href reaches into the internal product').not.toContain('/spaces/');
    expect(href, 'href built over a missing field').not.toContain('undefined');
    expect(href, 'the portal links only to the portal').toMatch(/^\/portal(\/|$)/);
  }

  // A placeholder in place of a withheld field is the failure mode the search
  // surface hit: "Unknown space" tells the reader there IS a space.
  expect(screen.queryByText(/unknown/i)).not.toBeInTheDocument();
  // And the portal never bounces a customer at the internal product.
  expect(screen.queryByText('INTERNAL LOGIN PAGE')).not.toBeInTheDocument();
  expect(screen.queryByText('OUTSIDE THE PORTAL SUBTREE')).not.toBeInTheDocument();
}

beforeEach(() => {
  // The portal session lives in localStorage, and this Node/jsdom pairing ships
  // a partial Storage whose setItem is missing — see test/localStorageStub.ts.
  installLocalStorageStub();
  state.describe = {
    data: { name: 'Acme Support', intro: 'Tell us what you need and we’ll pick it up.' },
    isLoading: false,
    error: null,
  };
  state.requests = { data: undefined, isLoading: false, error: null, refetch: vi.fn() };
  state.detail = { data: undefined, isLoading: false, error: null, refetch: vi.fn() };
  state.redeem = 'pending';
});

describe('the portal discloses no container context', () => {
  it('sign-in page', () => {
    const { container } = renderPortal(`/portal/${PORTAL_KEY}`);
    // (a) positive sighting: the portal's own public name, from the fixture.
    expect(screen.getByText('Acme Support')).toBeInTheDocument();
    expect(screen.getByTestId('portal-signin-page')).toBeInTheDocument();
    assertNoContainerContext(container);
  });

  it('redeem page, while the token is in flight', () => {
    const { container } = renderPortal(`/portal/${PORTAL_KEY}/signin/raw-token`);
    expect(screen.getByTestId('portal-redeem-pending')).toBeInTheDocument();
    assertNoContainerContext(container);
  });

  it('redeem page, after the link is refused', () => {
    state.redeem = 'error';
    const { container } = renderPortal(`/portal/${PORTAL_KEY}/signin/raw-token`);
    expect(screen.getByText('This sign-in link no longer works')).toBeInTheDocument();
    assertNoContainerContext(container);
  });

  it('requests list', () => {
    signIn();
    state.requests.data = [summaryFixture];
    const { container } = renderPortal(`/portal/${PORTAL_KEY}/requests`);
    // (a) positive sighting straight off the enriched fixture — the same object
    // that carries the forbidden keys IS being rendered.
    expect(screen.getByText(SUMMARY)).toBeInTheDocument();
    assertNoContainerContext(container);
  });

  it('requests list, when the requester has raised nothing', () => {
    signIn();
    state.requests.data = [];
    const { container } = renderPortal(`/portal/${PORTAL_KEY}/requests`);
    expect(screen.getByText('No requests yet')).toBeInTheDocument();
    assertNoContainerContext(container);
  });

  it('requests list, when the fetch fails', () => {
    signIn();
    state.requests.error = new Error('boom');
    const { container } = renderPortal(`/portal/${PORTAL_KEY}/requests`);
    expect(screen.getByText('Your requests couldn’t be loaded')).toBeInTheDocument();
    assertNoContainerContext(container);
  });

  it('new request form', () => {
    signIn();
    const { container } = renderPortal(`/portal/${PORTAL_KEY}/requests/new`);
    expect(screen.getByTestId('portal-new-request-page')).toBeInTheDocument();
    expect(screen.getByText('Acme Support')).toBeInTheDocument();
    assertNoContainerContext(container);
  });

  it('request detail, with its thread', () => {
    signIn();
    state.detail.data = detailFixture;
    const { container } = renderPortal(`/portal/${PORTAL_KEY}/requests/${REFERENCE}`);
    expect(screen.getByText(SUMMARY)).toBeInTheDocument();
    expect(screen.getByText(MESSAGE_BODY)).toBeInTheDocument();
    assertNoContainerContext(container);
  });

  it('request detail, when the request cannot be loaded', () => {
    signIn();
    state.detail.error = new Error('not found');
    const { container } = renderPortal(`/portal/${PORTAL_KEY}/requests/${REFERENCE}`);
    expect(screen.getByText('This request couldn’t be loaded')).toBeInTheDocument();
    assertNoContainerContext(container);
  });

  it('the subtree catch-all', () => {
    const { container } = renderPortal(`/portal/${PORTAL_KEY}/nothing/here`);
    expect(screen.getByTestId('portal-not-found')).toBeInTheDocument();
    // Not the shell's 404, and not a redirect to the internal sign-in.
    expect(screen.queryByText('OUTSIDE THE PORTAL SUBTREE')).not.toBeInTheDocument();
    assertNoContainerContext(container);
  });

  it('the layout when the portal itself does not resolve', () => {
    state.describe = { data: undefined as never, isLoading: false, error: new Error('404') };
    const { container } = renderPortal(`/portal/${PORTAL_KEY}`);
    expect(screen.getByText('This portal isn’t available')).toBeInTheDocument();
    assertNoContainerContext(container);
  });
});

describe('a request detail renders what the wire gave it', () => {
  it('shows an ABSENT description as a fallback, not as a crash or a blank', () => {
    signIn();
    // `description` is omitempty server-side: absent, not "". A component that
    // reaches for .trim() on it throws, and the boundary swallows the page.
    const withoutBody: PortalRequestDetailResponse = {
      request: {
        reference: REFERENCE,
        summary: SUMMARY,
        status: 'Received',
        created_at: '2026-07-01T09:00:00Z',
        updated_at: '2026-07-01T09:00:00Z',
      },
      messages: [],
    };
    state.detail.data = withoutBody;
    renderPortal(`/portal/${PORTAL_KEY}/requests/${REFERENCE}`);
    expect(screen.getByText(SUMMARY)).toBeInTheDocument();
    expect(screen.getByText('No further details were given.')).toBeInTheDocument();
  });

  it('labels an authorless message without naming a container', () => {
    signIn();
    state.detail.data = detailFixture;
    renderPortal(`/portal/${PORTAL_KEY}/requests/${REFERENCE}`);
    // author: '' on the requester's own message falls back to "You" — never to
    // the space, the team or the product name.
    expect(screen.getByText('You')).toBeInTheDocument();
    expect(screen.getByText('Sam Okoye')).toBeInTheDocument();
  });
});

describe('status is rendered exactly as the server sent it', () => {
  // The translation happens in requesterStatus() server-side, and it is not a
  // lookup the client could repeat: tickets.status has no database CHECK, the
  // workflow route writes arbitrary state names into it, and anything
  // unrecognised falls through to "In progress" — a Go test pins
  // "Awaiting legal sign-off" → "In progress" for exactly that reason.
  //
  // So a client-side status→label map would have to enumerate internal states
  // to key on them, and would either echo one back to a customer or render
  // "Unknown". Passing the string through has neither failure mode. These two
  // cases fail the moment such a map is introduced.
  const ALREADY_TRANSLATED = 'Awaiting legal sign-off';

  it('on a list row', () => {
    signIn();
    state.requests.data = [{ ...summaryFixture, status: ALREADY_TRANSLATED }];
    renderPortal(`/portal/${PORTAL_KEY}/requests`);
    expect(screen.getByTestId('portal-status')).toHaveTextContent(ALREADY_TRANSLATED);
    expect(screen.queryByText(/unknown/i)).not.toBeInTheDocument();
  });

  it('on the detail page', () => {
    signIn();
    state.detail.data = {
      ...detailFixture,
      request: { ...detailFixture.request, status: ALREADY_TRANSLATED },
    };
    renderPortal(`/portal/${PORTAL_KEY}/requests/${REFERENCE}`);
    expect(screen.getByTestId('portal-status')).toHaveTextContent(ALREADY_TRANSLATED);
  });

  it('passes an unfamiliar status straight through, unmapped', () => {
    // The value a map would most likely mangle: not one of the four the server
    // normally produces.
    signIn();
    state.requests.data = [{ ...summaryFixture, status: 'Received' }];
    renderPortal(`/portal/${PORTAL_KEY}/requests`);
    expect(screen.getByTestId('portal-status')).toHaveTextContent('Received');
  });
});

describe('the session guard', () => {
  it('sends a signed-out requester to the portal sign-in page, never to /login', () => {
    // No session stored: RequirePortalSession redirects.
    renderPortal(`/portal/${PORTAL_KEY}/requests`);
    expect(screen.getByTestId('portal-signin-page')).toBeInTheDocument();
    expect(screen.queryByText('INTERNAL LOGIN PAGE')).not.toBeInTheDocument();
  });

  it('treats an EXPIRED session as no session', () => {
    setPortalSession(PORTAL_KEY, {
      session_token: 'stale',
      expires_at: Date.now() - 1000,
      requester: { email: 'dana@customer.example', name: 'Dana Whitfield' },
      portal: { name: 'Acme Support', intro: '' },
    });
    renderPortal(`/portal/${PORTAL_KEY}/requests`);
    expect(screen.getByTestId('portal-signin-page')).toBeInTheDocument();
    expect(screen.queryByText('INTERNAL LOGIN PAGE')).not.toBeInTheDocument();
  });

  it('lets a live session through', () => {
    signIn();
    state.requests.data = [summaryFixture];
    renderPortal(`/portal/${PORTAL_KEY}/requests`);
    expect(screen.getByTestId('portal-requests-page')).toBeInTheDocument();
  });
});
