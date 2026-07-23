import { render, screen, fireEvent } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { TicketListPage } from '../TicketListPage';

// S4 regression: the Critical filter must match tickets stored under the
// legacy wire spelling 'urgent'. The pill already normalises 'urgent' ->
// Critical (priority.tsx); the filter predicate did not, so a Critical
// ticket showed a Critical pill yet was excluded by the Critical filter.
// This test mocks the api hooks and keeps components/priority.tsx real so
// normalizePriority is genuinely exercised.

const urgentTicket = {
  id: 'ticket-1',
  number: 7,
  title: 'DB outage',
  status: 'open',
  priority: 'urgent', // wire spelling of Critical
  created_at: '2026-07-01T00:00:00Z',
};

vi.mock('../../../lib/api', () => ({
  useTickets: () => ({ data: [urgentTicket], isLoading: false, error: null }),
  useSpace: () => ({ data: { key: 'SD' } }),
  useCreateTicket: () => ({ mutateAsync: vi.fn(), isPending: false, error: null }),
  friendlyErrorMessage: (_e: unknown, fallback: string) => fallback,
}));

function renderList() {
  return render(
    <MemoryRouter initialEntries={['/beacon/s1/tickets']}>
      <Routes>
        <Route path="/beacon/:spaceId/tickets" element={<TicketListPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe('TicketListPage Critical filter', () => {
  it('matches an urgent-priority ticket when the Critical filter is selected', () => {
    renderList();

    // Sanity: the ticket is visible with a Critical pill under the default filter.
    expect(screen.getByText('DB outage')).toBeInTheDocument();
    expect(screen.getByTestId('priority-pill')).toHaveAttribute('data-priority', 'critical');

    // Select the Critical filter (the priority <select> initially shows "All Priorities").
    fireEvent.change(screen.getByDisplayValue('All Priorities'), {
      target: { value: 'critical' },
    });

    // The urgent/Critical ticket must survive the filter.
    expect(screen.getByText('DB outage')).toBeInTheDocument();
    expect(
      screen.queryByText('No tickets match the current filters.'),
    ).not.toBeInTheDocument();
  });

  it('still excludes an urgent ticket when a different level (High) is selected', () => {
    renderList();

    fireEvent.change(screen.getByDisplayValue('All Priorities'), {
      target: { value: 'high' },
    });

    // Guards against a fix that just makes every ticket match: High must not
    // surface the Critical/urgent ticket.
    expect(screen.queryByText('DB outage')).not.toBeInTheDocument();
    expect(
      screen.getByText('No tickets match the current filters.'),
    ).toBeInTheDocument();
  });
});
