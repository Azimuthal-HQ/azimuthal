import { render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { ViewsListPage } from '../ViewsListPage';
import type { SavedView } from '../../../lib/api';

// P4, ADR-0009 case C1: a view whose scope has gone.
//
// `is_valid: false` means the spaces the view named, or the team it was shared
// with, no longer exist. The view itself is intact — it still lists, it still
// opens, and its owner can still re-scope it. So it must render as CONTENT with
// a neutral marker and the server's reason, and it must never render as an
// error: an error panel would tell the owner something is broken and push them
// towards deleting a view that needs one edit.
//
// The six strings web/e2e/helpers/setup.ts hunts must never appear on this
// path either — an invalid view is the case most likely to trip them.

const BANNED_ERROR_COPY = [
  'Something went wrong',
  'Failed to load',
  'could not be loaded',
  'invalid space_id',
  'invalid request body',
  'UNAUTHORIZED',
];

function savedView(overrides: Partial<SavedView>): SavedView {
  return {
    id: 'v1',
    owner_id: 'u1',
    name: 'My open work',
    description: '',
    query: { v: 1, filter: { modules: ['vector'] }, sort: { field: 'updated_at', dir: 'desc' } },
    visibility: 'private',
    visibility_team_id: null,
    is_owner: true,
    is_valid: true,
    created_at: '2026-07-01T00:00:00Z',
    updated_at: '2026-07-01T00:00:00Z',
    ...overrides,
  };
}

const views = vi.hoisted(() => ({ current: [] as SavedView[] }));

vi.mock('../../../lib/api', () => ({
  useSavedViews: () => ({ data: views.current, isLoading: false, error: null }),
  useDeleteView: () => ({ mutateAsync: vi.fn(), isPending: false, error: null }),
  friendlyErrorMessage: (_e: unknown, fallback: string) => fallback,
}));

vi.mock('../../../lib/auth', () => ({
  useAuth: () => ({ user: { id: 'u1', email: 'me@example.com', orgId: 'org-1', role: 'member' } }),
}));

function renderList(rows: SavedView[]) {
  views.current = rows;
  return render(
    <MemoryRouter>
      <ViewsListPage />
    </MemoryRouter>,
  );
}

describe('ViewsListPage — a view whose scope is unavailable', () => {
  const invalid = savedView({
    id: 'v-gone',
    name: 'Platform triage',
    is_valid: false,
    invalid_reason: 'The spaces this view searched have been deleted.',
  });

  it('still lists the view, with the reason and a route to re-scope it', () => {
    renderList([invalid]);

    const row = screen.getByTestId('view-row');
    expect(row).toHaveAttribute('data-valid', 'false');
    expect(within(row).getByText('Platform triage')).toBeInTheDocument();
    expect(screen.getByTestId('view-scope-chip')).toHaveTextContent('Scope unavailable');
    expect(screen.getByTestId('view-invalid-reason')).toHaveTextContent(
      'The spaces this view searched have been deleted.',
    );
    expect(screen.getByRole('link', { name: 'Re-scope it' })).toHaveAttribute(
      'href',
      '/views/v-gone/edit',
    );
  });

  it('renders it as content, never as an error', () => {
    const { container } = renderList([invalid]);

    expect(screen.queryByTestId('views-error')).toBeNull();
    for (const banned of BANNED_ERROR_COPY) {
      expect(container.textContent).not.toContain(banned);
    }
  });

  it('points a non-owner at the owner rather than at an edit they cannot make', () => {
    renderList([
      savedView({
        ...invalid,
        is_owner: false,
        owner_name: 'Ana Roy',
        visibility: 'org',
      }),
    ]);

    expect(screen.getByTestId('view-invalid-reason')).toHaveTextContent('Its owner can re-scope it.');
    expect(screen.queryByRole('link', { name: 'Re-scope it' })).toBeNull();
  });
});

describe('ViewsListPage — provenance and ownership', () => {
  it('shows own and shared views in one list, distinguished by chip', () => {
    renderList([
      savedView({ id: 'mine', name: 'Mine', is_owner: true }),
      savedView({
        id: 'theirs',
        name: 'Theirs',
        is_owner: false,
        owner_name: 'Ana Roy',
        visibility: 'team',
        visibility_team_id: 'team-1',
        team_name: 'Design',
      }),
    ]);

    const rows = screen.getAllByTestId('view-row');
    expect(rows).toHaveLength(1 + 1);
    expect(within(rows[0]).getByTestId('view-owner-chip')).toHaveTextContent('Yours');
    expect(within(rows[1]).getByTestId('view-owner-chip')).toHaveTextContent('Shared by Ana Roy');
    expect(within(rows[1]).getByTestId('view-visibility-chip')).toHaveTextContent('Team · Design');
  });

  it('offers edit and delete on an owned view only', () => {
    renderList([
      savedView({ id: 'mine', name: 'Mine', is_owner: true }),
      savedView({ id: 'theirs', name: 'Theirs', is_owner: false, owner_name: 'Ana Roy' }),
    ]);

    const rows = screen.getAllByTestId('view-row');
    expect(within(rows[0]).getByTestId('edit-view')).toBeInTheDocument();
    expect(within(rows[0]).getByTestId('delete-view')).toBeInTheDocument();
    // Ownership is the server's answer, arriving pre-computed as is_owner; the
    // client never compares the owner id to the session to decide this.
    expect(within(rows[1]).queryByTestId('edit-view')).toBeNull();
    expect(within(rows[1]).queryByTestId('delete-view')).toBeNull();
  });

  it('renders the branded empty state when there are none', () => {
    renderList([]);

    expect(screen.queryByTestId('views-list')).toBeNull();
    expect(screen.getByText('No saved views yet')).toBeInTheDocument();
    expect(screen.getByTestId('new-view-empty')).toBeInTheDocument();
  });
});
