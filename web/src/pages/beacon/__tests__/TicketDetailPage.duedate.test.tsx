import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { TicketDetailPage } from '../TicketDetailPage';

/**
 * A6: the due-date control on ticket detail.
 *
 * A workflow guard can require `due_at`, and no surface could set one — the
 * ticket PATCH did not carry the field at all. This covers the frontend half:
 * that the control reflects the stored date, and that what it puts on the wire
 * is what the server accepts.
 *
 * The wire assertions are the point. An `<input type="date">` yields a bare
 * `YYYY-MM-DD`, which the API rejects with 400, so "the handler fired" proves
 * nothing on its own — the payload has to be checked.
 *
 * `lib/api` is mocked wholesale, following TicketDetailPage.portal.test.tsx.
 */

const state = vi.hoisted(() => ({
  ticket: null as Record<string, unknown> | null,
  updateTicket: vi.fn(),
  refetchTicket: vi.fn(),
}));

vi.mock('../../../lib/api', () => ({
  useTicket: () => ({
    data: state.ticket,
    isLoading: false,
    error: null,
    refetch: state.refetchTicket,
  }),
  useAvailableTransitions: () => ({ data: undefined, refetch: vi.fn() }),
  useTransitionTicketStatus: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useAssignTicket: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useUpdateTicket: () => ({ mutateAsync: state.updateTicket, isPending: false }),
  useMembers: () => ({ data: [] }),
  useComments: () => ({ data: [], refetch: vi.fn() }),
  useCreateComment: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useMe: () => ({ data: { id: 'u-agent', org_id: 'org-1', display_name: 'Ada Agent' } }),
  useSpace: () => ({ data: { key: 'SD' } }),
  useEffectiveAccess: () => ({ data: undefined }),
  useEntityShares: () => ({ data: undefined }),
  useEntityApprovals: () => ({ data: [], isLoading: false, error: null }),
  friendlyErrorMessage: (_e: unknown, fallback: string) => fallback,
}));

const baseTicket = {
  id: 'ticket-1',
  space_id: 's1',
  number: 12,
  title: 'Printer on fire',
  description: '',
  status: 'open',
  priority: 'high',
  assignee_id: null,
  reporter_id: 'u-reporter',
  requester_id: null,
  requester: null,
  label_ids: [],
  due_at: null as string | null,
  created_at: '2026-07-01T00:00:00Z',
  updated_at: '2026-07-02T00:00:00Z',
};

function renderDetail() {
  return render(
    <MemoryRouter initialEntries={['/beacon/s1/tickets/ticket-1']}>
      <Routes>
        <Route path="/beacon/:spaceId/tickets/:ticketId" element={<TicketDetailPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

const dueInput = () => screen.getByTestId('ticket-due-date') as HTMLInputElement;

beforeEach(() => {
  state.ticket = { ...baseTicket };
  state.updateTicket = vi.fn().mockResolvedValue({});
  state.refetchTicket = vi.fn();
});

afterEach(() => {
  vi.clearAllMocks();
});

describe('ticket due-date control', () => {
  it('renders empty for a ticket with no due date', () => {
    renderDetail();
    expect(dueInput().value).toBe('');
  });

  it('renders the stored due date as its UTC calendar date', () => {
    state.ticket = { ...baseTicket, due_at: '2026-09-01T00:00:00Z' };
    renderDetail();
    expect(dueInput().value).toBe('2026-09-01');
  });

  /**
   * The server does not echo the timestamp it was sent: postgres returns
   * `2026-09-01T00:00:00Z` as `2026-08-31T20:00:00-04:00` under a non-UTC
   * session zone — the same instant, a different string. Slicing the first ten
   * characters would render the PREVIOUS DAY, which is the bug `formatUTCDate`
   * exists to prevent. This is the negative direction of the test above: it
   * fails against a `.slice(0, 10)` implementation and passes against a parsed
   * one.
   */
  it('renders the correct day even when the server serializes a non-UTC offset', () => {
    state.ticket = { ...baseTicket, due_at: '2026-08-31T20:00:00-04:00' };
    renderDetail();
    expect(dueInput().value).toBe('2026-09-01');
  });

  it('sends RFC3339, not the bare date the input produces', async () => {
    renderDetail();

    fireEvent.change(dueInput(), { target: { value: '2026-09-01' } });

    await waitFor(() => expect(state.updateTicket).toHaveBeenCalledTimes(1));
    // The whole payload, so a body that also resent the title would fail here.
    // The ticket PATCH is a true partial update; resending fields the user did
    // not touch is the race, not the safeguard.
    expect(state.updateTicket).toHaveBeenCalledWith({ due_at: '2026-09-01T00:00:00Z' });
  });

  /**
   * Clearing must send an explicit null. `toRFC3339Date('')` returns undefined,
   * and `JSON.stringify` drops undefined keys — an absent due_at means "leave
   * it alone" to the server, so the naive wiring produces a control that cannot
   * be cleared. Asserting `null` specifically is what separates the two.
   */
  it('sends an explicit null when the date is cleared', async () => {
    state.ticket = { ...baseTicket, due_at: '2026-09-01T00:00:00Z' };
    renderDetail();

    fireEvent.change(dueInput(), { target: { value: '' } });

    await waitFor(() => expect(state.updateTicket).toHaveBeenCalledTimes(1));
    expect(state.updateTicket).toHaveBeenCalledWith({ due_at: null });

    const [[body]] = state.updateTicket.mock.calls as [[Record<string, unknown>]];
    expect(Object.hasOwn(body, 'due_at')).toBe(true);
    expect(body.due_at).not.toBeUndefined();
  });

  it('refetches the ticket after a successful change', async () => {
    renderDetail();

    fireEvent.change(dueInput(), { target: { value: '2026-09-01' } });

    await waitFor(() => expect(state.refetchTicket).toHaveBeenCalled());
  });

  it('reports a refused change inline instead of throwing', async () => {
    state.updateTicket = vi.fn().mockRejectedValue(new Error('nope'));
    renderDetail();

    fireEvent.change(dueInput(), { target: { value: '2026-09-01' } });

    expect(await screen.findByText('The due date could not be changed.')).toBeInTheDocument();
    // Refused, so there is nothing new to read back.
    expect(state.refetchTicket).not.toHaveBeenCalled();
  });
});
