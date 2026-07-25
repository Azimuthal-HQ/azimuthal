/**
 * The wire contract of the ticket_ref query parameter, the ticket_ref
 * typeahead, and the item PATCH `kind` field.
 *
 * These pin URLs, not behaviour reachable through a page: api.ts is the single
 * network module, so the only observable it controls is the request it builds.
 * Every assertion here is on the exact string passed to fetch and the exact
 * JSON body, because "contains ticket_ref" would pass with the encoding wrong,
 * with the parameter appended to the wrong path, or with the reference
 * leaking into the body of a mutation that must not carry it.
 *
 * The omission cases matter as much as the append cases. The reference is
 * optional everywhere; a call site that collects none must produce byte for
 * byte the URL it produced before the parameter existed.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { createElement, type ReactNode } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { installLocalStorageStub } from '../test/localStorageStub';
import {
  queryKeys,
  useCreateInvites,
  useCreateSpace,
  useCreateTeam,
  useCreateTeamWithSpaces,
  useDeleteSpace,
  useDeleteTeam,
  usePersonLifecycle,
  usePutTeamMember,
  useRemovePerson,
  useRemoveTeamMember,
  useResendInvite,
  useRevokeInvite,
  useTicketRefSuggestions,
  useUpdatePerson,
  useUpdateProjectItem,
  useUpdateSpace,
  useUpdateTeam,
} from './api';

interface Captured {
  url: string;
  method: string;
  body: unknown;
}

let calls: Captured[] = [];
/** What the stub answers with; a few tests need a specific shape. */
let responseBody: unknown = { id: 'x', name: 'T', slug: 't' };

const qc = new QueryClient({
  defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
});

function wrapper({ children }: { children: ReactNode }) {
  return createElement(QueryClientProvider, { client: qc }, children);
}

beforeEach(() => {
  // getToken() reads localStorage on every request; the partial jsdom Storage
  // shim this repo works around would throw before fetch is ever reached.
  installLocalStorageStub();
  calls = [];
  responseBody = { id: 'x', name: 'T', slug: 't' };
  qc.clear();
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string, init?: RequestInit) => {
      calls.push({
        url: String(url),
        method: String(init?.method ?? 'GET'),
        body: init?.body === undefined ? undefined : JSON.parse(String(init.body)),
      });
      return Promise.resolve(
        new Response(JSON.stringify(responseBody), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
      );
    }),
  );
});

/** Waits for exactly n requests and returns them in order. */
async function requests(n = 1): Promise<Captured[]> {
  await waitFor(() => expect(calls.length).toBe(n));
  return calls;
}

const REF = 'OPS-1234';

// ---------------------------------------------------------------------------
// A3 — the reference travels as a query parameter, never in the body
// ---------------------------------------------------------------------------

describe('ticket_ref on administrative mutations', () => {
  it('appends an encoded reference to the people-lifecycle mutations', async () => {
    const person = renderHook(() => useUpdatePerson('o1'), { wrapper });
    person.result.current.mutate({ userId: 'u1', org_role: 'admin', ticketRef: REF });
    let [req] = await requests();
    expect(req.url).toBe(`/api/v1/orgs/o1/users/u1?ticket_ref=${REF}`);
    expect(req.method).toBe('PATCH');
    // The reference is transport, not payload: it must not reach the body,
    // where the backend would ignore it and the audit trail would lose it.
    expect(req.body).toEqual({ org_role: 'admin' });

    for (const action of ['deactivate', 'reactivate', 'force-logout'] as const) {
      calls = [];
      const life = renderHook(() => usePersonLifecycle('o1'), { wrapper });
      life.result.current.mutate({ userId: 'u1', action, ticketRef: REF });
      [req] = await requests();
      expect(req.url).toBe(`/api/v1/orgs/o1/users/u1/${action}?ticket_ref=${REF}`);
      expect(req.method).toBe('POST');
    }

    calls = [];
    const remove = renderHook(() => useRemovePerson('o1'), { wrapper });
    remove.result.current.mutate({ id: 'u1', ticketRef: REF });
    [req] = await requests();
    expect(req.url).toBe(`/api/v1/orgs/o1/users/u1?ticket_ref=${REF}`);
    expect(req.method).toBe('DELETE');
  });

  it('appends an encoded reference to the team mutations', async () => {
    const create = renderHook(() => useCreateTeam('o1'), { wrapper });
    create.result.current.mutate({ slug: 'ops', name: 'Ops', ticketRef: REF });
    let [req] = await requests();
    expect(req.url).toBe(`/api/v1/orgs/o1/teams?ticket_ref=${REF}`);
    expect(req.body).toEqual({ slug: 'ops', name: 'Ops' });

    calls = [];
    const update = renderHook(() => useUpdateTeam('o1'), { wrapper });
    update.result.current.mutate({ teamId: 't1', name: 'Ops 2', ticketRef: REF });
    [req] = await requests();
    expect(req.url).toBe(`/api/v1/orgs/o1/teams/t1?ticket_ref=${REF}`);
    expect(req.body).toEqual({ name: 'Ops 2' });

    calls = [];
    const del = renderHook(() => useDeleteTeam('o1'), { wrapper });
    del.result.current.mutate({ id: 't1', ticketRef: REF });
    [req] = await requests();
    expect(req.url).toBe(`/api/v1/orgs/o1/teams/t1?ticket_ref=${REF}`);
    expect(req.method).toBe('DELETE');

    calls = [];
    const put = renderHook(() => usePutTeamMember('o1', 't1'), { wrapper });
    put.result.current.mutate({ userId: 'u1', role: 'member', ticketRef: REF });
    [req] = await requests();
    expect(req.url).toBe(`/api/v1/orgs/o1/teams/t1/members/u1?ticket_ref=${REF}`);
    expect(req.body).toEqual({ role: 'member' });

    calls = [];
    const rm = renderHook(() => useRemoveTeamMember('o1', 't1'), { wrapper });
    rm.result.current.mutate({ id: 'u1', ticketRef: REF });
    [req] = await requests();
    expect(req.url).toBe(`/api/v1/orgs/o1/teams/t1/members/u1?ticket_ref=${REF}`);
    expect(req.method).toBe('DELETE');
  });

  it('appends an encoded reference to the space mutations', async () => {
    const create = renderHook(() => useCreateSpace('o1'), { wrapper });
    create.result.current.mutate({ name: 'S', slug: 's', type: 'beacon', ticketRef: REF });
    let [req] = await requests();
    expect(req.url).toBe(`/api/v1/orgs/o1/spaces?ticket_ref=${REF}`);
    expect(req.body).toEqual({ name: 'S', slug: 's', type: 'beacon' });

    calls = [];
    const update = renderHook(() => useUpdateSpace('o1', 's1'), { wrapper });
    update.result.current.mutate({ name: 'S2', ticketRef: REF });
    [req] = await requests();
    expect(req.url).toBe(`/api/v1/orgs/o1/spaces/s1?ticket_ref=${REF}`);
    expect(req.method).toBe('PUT');
    expect(req.body).toEqual({ name: 'S2' });

    calls = [];
    const del = renderHook(() => useDeleteSpace('o1'), { wrapper });
    del.result.current.mutate({ id: 's1', ticketRef: REF });
    [req] = await requests();
    expect(req.url).toBe(`/api/v1/orgs/o1/spaces/s1?ticket_ref=${REF}`);
    expect(req.method).toBe('DELETE');
  });

  it('appends an encoded reference to the invite mutations', async () => {
    const create = renderHook(() => useCreateInvites('o1'), { wrapper });
    create.result.current.mutate({ emails: ['a@b.c'], ticketRef: REF });
    let [req] = await requests();
    expect(req.url).toBe(`/api/v1/orgs/o1/invites?ticket_ref=${REF}`);
    expect(req.body).toEqual({ emails: ['a@b.c'] });

    calls = [];
    const revoke = renderHook(() => useRevokeInvite('o1'), { wrapper });
    revoke.result.current.mutate({ id: 'i1', ticketRef: REF });
    [req] = await requests();
    expect(req.url).toBe(`/api/v1/orgs/o1/invites/i1?ticket_ref=${REF}`);
    expect(req.method).toBe('DELETE');

    calls = [];
    const resend = renderHook(() => useResendInvite('o1'), { wrapper });
    resend.result.current.mutate({ id: 'i1', ticketRef: REF });
    [req] = await requests();
    expect(req.url).toBe(`/api/v1/orgs/o1/invites/i1/resend?ticket_ref=${REF}`);
    expect(req.method).toBe('POST');
  });

  it('carries one reference across every request the team+spaces composite makes', async () => {
    const { result } = renderHook(() => useCreateTeamWithSpaces('o1'), { wrapper });
    result.current.mutate({ slug: 'ops', name: 'Ops', modules: ['beacon'], ticketRef: REF });
    // team create, space create, grant create — in that order.
    const [team, space, grant] = await requests(3);
    expect(team.url).toBe(`/api/v1/orgs/o1/teams?ticket_ref=${REF}`);
    expect(space.url).toBe(`/api/v1/orgs/o1/spaces?ticket_ref=${REF}`);
    // The grant endpoint accepts no ticket_ref; it must not grow a stray one.
    expect(grant.url).toBe('/api/v1/orgs/o1/spaces/x/grants');
    // modules is a client-side concept and never reaches the wire.
    expect(team.body).toEqual({ slug: 'ops', name: 'Ops' });
  });

  it('percent-encodes a reference containing URL metacharacters', async () => {
    const { result } = renderHook(() => useDeleteTeam('o1'), { wrapper });
    result.current.mutate({ id: 't1', ticketRef: 'OPS 7&8/a?b=c#d' });
    const [req] = await requests();
    expect(req.url).toBe('/api/v1/orgs/o1/teams/t1?ticket_ref=OPS%207%268%2Fa%3Fb%3Dc%23d');
  });
});

// ---------------------------------------------------------------------------
// The other half of the contract: no reference, no parameter.
// ---------------------------------------------------------------------------

describe('ticket_ref omission', () => {
  /**
   * Every mutation that accepts a reference, invoked exactly as it was before
   * the reference existed. The assertion is that the URL grows no query string
   * at all — not that it lacks ticket_ref, which an empty `?ticket_ref=` would
   * satisfy while still changing the request.
   */
  it('leaves every URL untouched when no reference is supplied', async () => {
    const cases: Array<{ name: string; run: () => void; url: string }> = [
      {
        name: 'updatePerson',
        run: () => renderHook(() => useUpdatePerson('o1'), { wrapper })
          .result.current.mutate({ userId: 'u1', org_role: 'member' }),
        url: '/api/v1/orgs/o1/users/u1',
      },
      {
        name: 'deactivate',
        run: () => renderHook(() => usePersonLifecycle('o1'), { wrapper })
          .result.current.mutate({ userId: 'u1', action: 'deactivate' }),
        url: '/api/v1/orgs/o1/users/u1/deactivate',
      },
      {
        name: 'removePerson (bare id)',
        run: () => renderHook(() => useRemovePerson('o1'), { wrapper })
          .result.current.mutate('u1'),
        url: '/api/v1/orgs/o1/users/u1',
      },
      {
        name: 'createTeam',
        run: () => renderHook(() => useCreateTeam('o1'), { wrapper })
          .result.current.mutate({ slug: 'ops', name: 'Ops' }),
        url: '/api/v1/orgs/o1/teams',
      },
      {
        name: 'updateTeam',
        run: () => renderHook(() => useUpdateTeam('o1'), { wrapper })
          .result.current.mutate({ teamId: 't1', name: 'Ops' }),
        url: '/api/v1/orgs/o1/teams/t1',
      },
      {
        name: 'deleteTeam (bare id)',
        run: () => renderHook(() => useDeleteTeam('o1'), { wrapper })
          .result.current.mutate('t1'),
        url: '/api/v1/orgs/o1/teams/t1',
      },
      {
        name: 'putTeamMember',
        run: () => renderHook(() => usePutTeamMember('o1', 't1'), { wrapper })
          .result.current.mutate({ userId: 'u1', role: 'member' }),
        url: '/api/v1/orgs/o1/teams/t1/members/u1',
      },
      {
        name: 'removeTeamMember (bare id)',
        run: () => renderHook(() => useRemoveTeamMember('o1', 't1'), { wrapper })
          .result.current.mutate('u1'),
        url: '/api/v1/orgs/o1/teams/t1/members/u1',
      },
      {
        name: 'createSpace',
        run: () => renderHook(() => useCreateSpace('o1'), { wrapper })
          .result.current.mutate({ name: 'S', slug: 's', type: 'codex' }),
        url: '/api/v1/orgs/o1/spaces',
      },
      {
        name: 'updateSpace',
        run: () => renderHook(() => useUpdateSpace('o1', 's1'), { wrapper })
          .result.current.mutate({ name: 'S' }),
        url: '/api/v1/orgs/o1/spaces/s1',
      },
      {
        name: 'deleteSpace (bare id)',
        run: () => renderHook(() => useDeleteSpace('o1'), { wrapper })
          .result.current.mutate('s1'),
        url: '/api/v1/orgs/o1/spaces/s1',
      },
      {
        name: 'createInvites',
        run: () => renderHook(() => useCreateInvites('o1'), { wrapper })
          .result.current.mutate({ emails: ['a@b.c'] }),
        url: '/api/v1/orgs/o1/invites',
      },
      {
        name: 'revokeInvite (bare id)',
        run: () => renderHook(() => useRevokeInvite('o1'), { wrapper })
          .result.current.mutate('i1'),
        url: '/api/v1/orgs/o1/invites/i1',
      },
      {
        name: 'resendInvite (bare id)',
        run: () => renderHook(() => useResendInvite('o1'), { wrapper })
          .result.current.mutate('i1'),
        url: '/api/v1/orgs/o1/invites/i1/resend',
      },
    ];

    for (const c of cases) {
      calls = [];
      c.run();
      const [req] = await requests();
      expect(req.url, c.name).toBe(c.url);
    }
  });

  it('treats a whitespace-only reference as no reference', async () => {
    // Matches the server, which trims before deciding a reference was given:
    // sending `?ticket_ref=%20%20` would be a request the operator did not make.
    const { result } = renderHook(() => useDeleteTeam('o1'), { wrapper });
    result.current.mutate({ id: 't1', ticketRef: '   ' });
    const [req] = await requests();
    expect(req.url).toBe('/api/v1/orgs/o1/teams/t1');
  });

  it('trims a padded reference rather than encoding the padding', async () => {
    const { result } = renderHook(() => useDeleteTeam('o1'), { wrapper });
    result.current.mutate({ id: 't1', ticketRef: `  ${REF}  ` });
    const [req] = await requests();
    expect(req.url).toBe(`/api/v1/orgs/o1/teams/t1?ticket_ref=${REF}`);
  });
});

// ---------------------------------------------------------------------------
// A1 — the ticket_ref typeahead
// ---------------------------------------------------------------------------

describe('useTicketRefSuggestions', () => {
  it('queries the org-scoped suggest endpoint with q percent-encoded', async () => {
    responseBody = [
      {
        ref: 'BEA-42',
        ticket_id: '11111111-1111-1111-1111-111111111111',
        number: 42,
        title: 'Rotate the signing key',
        space_id: '22222222-2222-2222-2222-222222222222',
        space_key: 'BEA',
        status: 'open',
        assigned_to_me: true,
      },
    ];
    const { result } = renderHook(() => useTicketRefSuggestions('o1', 'BEA 4&2'), { wrapper });
    const [req] = await requests();
    expect(req.url).toBe('/api/v1/orgs/o1/tickets/suggest?q=BEA%204%262');
    expect(req.method).toBe('GET');
    await waitFor(() => expect(result.current.data).toBeDefined());
    expect(result.current.data?.[0].ref).toBe('BEA-42');
    expect(result.current.data?.[0].assigned_to_me).toBe(true);
  });

  it('stays disabled while orgId is empty, which is how a picker turns itself off', async () => {
    renderHook(() => useTicketRefSuggestions('', 'BEA'), { wrapper });
    await new Promise((r) => setTimeout(r, 50));
    expect(calls).toHaveLength(0);
  });

  it('stays disabled while q is empty or whitespace', async () => {
    renderHook(() => useTicketRefSuggestions('o1', ''), { wrapper });
    renderHook(() => useTicketRefSuggestions('o1', '   '), { wrapper });
    await new Promise((r) => setTimeout(r, 50));
    expect(calls).toHaveLength(0);
  });

  it('normalises a null body to an empty array', async () => {
    // The Go handler returns [] today, but every other list fetcher in this
    // module defends against null and a picker that crashes on one is worse
    // than a picker that shows nothing.
    responseBody = null;
    const { result } = renderHook(() => useTicketRefSuggestions('o1', 'BEA'), { wrapper });
    await waitFor(() => expect(result.current.data).toBeDefined());
    expect(result.current.data).toEqual([]);
  });

  it('keys the cache by org and query so two pickers do not share results', () => {
    expect(queryKeys.ticketRefSuggestions('o1', 'BEA')).toEqual([
      'ticketRefSuggestions',
      'o1',
      'BEA',
    ]);
    expect(queryKeys.ticketRefSuggestions('o1', 'BEA')).not.toEqual(
      queryKeys.ticketRefSuggestions('o2', 'BEA'),
    );
  });
});

// ---------------------------------------------------------------------------
// T1 — kind on the item PATCH body
// ---------------------------------------------------------------------------

describe('project item update: kind', () => {
  it('sends kind when supplied', async () => {
    // This also pins the type: `kind` is not in UpdateProjectItemRequest, the
    // call below stops compiling and `npm run type-check` fails.
    const { result } = renderHook(() => useUpdateProjectItem('s1', 'i1'), { wrapper });
    result.current.mutate({ kind: 'bug' });
    const [req] = await requests();
    expect(req.method).toBe('PATCH');
    expect(req.body).toEqual({ kind: 'bug' });
  });

  it('omits the key entirely when kind is not supplied, so the type is left unchanged', async () => {
    // The Go contract is `kind *string`: absent means "leave it alone". A
    // client that always sent the key would overwrite the type on every edit.
    const { result } = renderHook(() => useUpdateProjectItem('s1', 'i1'), { wrapper });
    result.current.mutate({ title: 'Renamed' });
    const [req] = await requests();
    expect(req.body).toEqual({ title: 'Renamed' });
    expect(Object.keys(req.body as object)).not.toContain('kind');
  });
});
