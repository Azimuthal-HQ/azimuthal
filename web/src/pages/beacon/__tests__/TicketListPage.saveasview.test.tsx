import { render, screen, fireEvent } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { TicketListPage } from '../TicketListPage';

// P4: the "Save as view" entry point on the ticket list. The control must
// carry the filters that are on screen at the moment of the click — a draft
// snapshotted at mount would silently save the wrong query.

vi.mock('../../../lib/api', () => ({
  useTickets: () => ({ data: [], isLoading: false, error: null }),
  useSpace: () => ({ data: { key: 'SD', name: 'Support' } }),
  useCreateTicket: () => ({ mutateAsync: vi.fn(), isPending: false, error: null }),
  friendlyErrorMessage: (_e: unknown, fallback: string) => fallback,
}));

/** Stands in for the view builder: dumps whatever draft arrived. */
function DraftProbe() {
  const { state } = useLocation();
  return <pre data-testid="draft">{JSON.stringify(state)}</pre>;
}

function renderList() {
  return render(
    <MemoryRouter initialEntries={['/beacon/s1/tickets']}>
      <Routes>
        <Route path="/beacon/:spaceId/tickets" element={<TicketListPage />} />
        <Route path="/views/new" element={<DraftProbe />} />
      </Routes>
    </MemoryRouter>,
  );
}

function arrivedDraft() {
  return JSON.parse(screen.getByTestId('draft').textContent ?? 'null').draft;
}

describe('TicketListPage "Save as view"', () => {
  it('hands the live filter state to /views/new as a beacon QueryDoc', () => {
    renderList();

    fireEvent.change(screen.getByDisplayValue('All Statuses'), { target: { value: 'open' } });
    fireEvent.change(screen.getByDisplayValue('All Priorities'), { target: { value: 'critical' } });
    fireEvent.change(screen.getByPlaceholderText('Search tickets...'), {
      target: { value: 'outage' },
    });

    fireEvent.click(screen.getByTestId('save-as-view'));

    expect(arrivedDraft()).toEqual({
      name: 'Tickets in Support',
      query: {
        v: 1,
        filter: {
          modules: ['beacon'],
          space_ids: ['s1'],
          statuses: ['open'],
          priorities: ['urgent'], // Critical is 'urgent' on the wire, never 'critical'
          text: 'outage',
        },
        sort: { field: 'updated_at', dir: 'desc' },
      },
    });
  });

  // The negative twin: without this, a draft frozen at mount would still pass
  // the test above by accident once a default filter existed.
  it('carries no status or priority when the filters are untouched', () => {
    renderList();
    fireEvent.click(screen.getByTestId('save-as-view'));

    expect(arrivedDraft().query.filter).toEqual({
      modules: ['beacon'],
      space_ids: ['s1'],
    });
  });
});
