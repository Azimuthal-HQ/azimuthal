import { render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { ViewResultList, ViewResultRow } from '../ViewResultList';
import type { ViewResult } from '../../../lib/api';

// P4: one list, both modules. A result row has to say where it came from and
// route back to it, and the two modules have different detail routes — a
// ticket lives at /beacon/{space}/tickets/{id}, a project item at
// /vector/{space}/backlog/{id}. Getting that mapping wrong produces a link
// that lands on a space's not-found state, which reads as missing data.

vi.mock('../../../lib/api', () => ({
  friendlyErrorMessage: (_e: unknown, fallback: string) => fallback,
}));

function result(overrides: Partial<ViewResult>): ViewResult {
  return {
    module: 'beacon',
    id: 'id-1',
    key: 'SD-4',
    title: 'Login returns 500',
    space_id: 'space-1',
    space_key: 'SD',
    space_name: 'Support',
    status: 'in_progress',
    priority: 'urgent',
    assignee_id: null,
    assignee_name: null,
    labels: [],
    created_at: '2026-07-01T00:00:00Z',
    updated_at: '2026-07-02T00:00:00Z',
    ...overrides,
  };
}

function renderResults(results: ViewResult[], meId?: string) {
  return render(
    <MemoryRouter>
      <ViewResultList
        page={{ results, next_cursor: '', has_more: false }}
        errorFallback="These results are unavailable right now."
        emptyTitle="Nothing matches this view"
        emptyDescription="Widen the filters."
        meId={meId}
      />
    </MemoryRouter>,
  );
}

describe('ViewResultList', () => {
  it('routes each row to its own module’s detail page', () => {
    renderResults([
      result({ id: 't1', key: 'SD-4', module: 'beacon' }),
      result({ id: 'p1', key: 'PLAT-9', module: 'vector', space_id: 'space-2', title: 'Rate limiter' }),
    ]);

    const rows = screen.getAllByTestId('view-result-row');
    expect(within(rows[0]).getByRole('link', { name: 'SD-4' })).toHaveAttribute(
      'href',
      '/beacon/space-1/tickets/t1',
    );
    expect(within(rows[1]).getByRole('link', { name: 'PLAT-9' })).toHaveAttribute(
      'href',
      '/vector/space-2/backlog/p1',
    );
  });

  it('carries a module provenance chip on every row', () => {
    renderResults([
      result({ id: 't1', module: 'beacon' }),
      result({ id: 'p1', module: 'vector' }),
    ]);

    const chips = screen.getAllByTestId('module-chip');
    expect(chips.map((c) => c.getAttribute('data-module'))).toEqual(['beacon', 'vector']);
  });

  it('says who the row is for without inventing a name for anyone else', () => {
    renderResults(
      [
        result({ id: 'a', assignee_id: null }),
        result({ id: 'b', assignee_id: 'u1' }),
        result({ id: 'c', assignee_id: 'ffffffff-1111-2222-3333-444444444444' }),
      ],
      'u1',
    );

    const rows = screen.getAllByTestId('view-result-row');
    expect(within(rows[0]).getByText('Unassigned')).toBeInTheDocument();
    expect(within(rows[1]).getByText('You')).toBeInTheDocument();
    // No join happens per row, so an unknown assignee is shown as the id it is
    // rather than a name fetched one request at a time.
    expect(within(rows[2]).getByTitle('ffffffff-1111-2222-3333-444444444444')).toBeInTheDocument();
  });

  it('renders the branded empty state rather than an empty list', () => {
    renderResults([]);

    expect(screen.queryByTestId('view-result-row')).toBeNull();
    expect(screen.getByText('Nothing matches this view')).toBeInTheDocument();
  });
});

describe('the assignee cell', () => {
  // The name is joined in the fan-out (one LEFT JOIN, never a per-row lookup —
  // §2.5 case 23). These pin the four states the cell can truthfully show, and
  // in particular that a name-less id is NOT reported as unassigned: the two
  // mean different things, and conflating them under-reports assigned work.
  it('shows the name when the join found one', () => {
    render(
      <MemoryRouter>
        <ViewResultRow result={result({ assignee_id: 'u-9', assignee_name: 'Ada Lovelace' })} />
      </MemoryRouter>,
    );
    expect(screen.getByText('Ada Lovelace')).toBeInTheDocument();
  });

  it('says Unassigned only when there is no assignee at all', () => {
    render(
      <MemoryRouter>
        <ViewResultRow result={result({ assignee_id: null, assignee_name: null })} />
      </MemoryRouter>,
    );
    expect(screen.getByText('Unassigned')).toBeInTheDocument();
  });

  it('prefers You over the name for the current viewer', () => {
    render(
      <MemoryRouter>
        <ViewResultRow
          result={result({ assignee_id: 'me-1', assignee_name: 'Ada Lovelace' })}
          meId="me-1"
        />
      </MemoryRouter>,
    );
    expect(screen.getByText('You')).toBeInTheDocument();
    expect(screen.queryByText('Ada Lovelace')).not.toBeInTheDocument();
  });

  it('falls back to the short id when the id names no user, not to Unassigned', () => {
    render(
      <MemoryRouter>
        <ViewResultRow result={result({ assignee_id: 'deadbeef-0000', assignee_name: null })} />
      </MemoryRouter>,
    );
    expect(screen.getByText('deadbeef')).toBeInTheDocument();
    expect(screen.queryByText('Unassigned')).not.toBeInTheDocument();
  });
});
