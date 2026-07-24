import { render, screen, fireEvent, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { BacklogPage } from '../BacklogPage';
import type { ProjectItem } from '../../../lib/api';

// W5 type-filter surface on the backlog: selecting a type chip narrows the
// list to that type, composing with (not replacing) the other filters.

function item(id: string, title: string, kind: string): ProjectItem {
  return {
    id, space_id: 's1', number: 1, item_key: `VEC-${id}`,
    title, description: '', kind, status: 'open', priority: 'medium',
    assignee_id: null, reporter_id: 'u1', sprint_id: null, rank: `0|${id}:`,
    labels: [], created_at: '', updated_at: '',
  };
}

const items = [item('1', 'Fix crash', 'bug'), item('2', 'Add feature', 'task')];

vi.mock('../../../lib/auth', () => ({ getCurrentOrgId: () => 'org1' }));

vi.mock('../../../lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../lib/api')>();
  return {
    ...actual,
    useProjectItems: () => ({ data: items, isLoading: false, error: null }),
    useCreateProjectItem: () => ({ mutateAsync: vi.fn(), isPending: false, error: null }),
    useRankItem: () => ({ mutate: vi.fn() }),
    // W2 added sprint grouping/assignment to this page. These items carry no
    // sprint, so the page renders a single Backlog group either way — the
    // stubs exist so the hooks resolve outside a QueryClientProvider.
    useSprints: () => ({ data: [] }),
    useAssignItemSprint: () => ({ mutateAsync: vi.fn(), isPending: false }),
    useSpace: () => ({ data: { id: 's1', key: 'VEC' } }),
    useItemTypes: () => ({
      data: [
        { id: 't1', org_id: 'org1', slug: 'bug', name: 'Bug', position: 1, archived_at: null, created_at: '', updated_at: '' },
        { id: 't2', org_id: 'org1', slug: 'task', name: 'Task', position: 2, archived_at: null, created_at: '', updated_at: '' },
      ],
    }),
  };
});

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/vector/s1/backlog']}>
      <Routes>
        <Route path="/vector/:spaceId/backlog" element={<BacklogPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

afterEach(() => vi.clearAllMocks());

describe('BacklogPage type filter', () => {
  it('shows all items until a type is selected, then narrows to that type', () => {
    renderPage();
    expect(screen.getByText('Fix crash')).toBeInTheDocument();
    expect(screen.getByText('Add feature')).toBeInTheDocument();

    // The type filter renders a chip per active type.
    const typeFilter = screen.getByTestId('type-filter');
    fireEvent.click(within(typeFilter).getByRole('button', { name: 'Bug' }));

    // Only the bug item survives; the task item is filtered out.
    expect(screen.getByText('Fix crash')).toBeInTheDocument();
    expect(screen.queryByText('Add feature')).not.toBeInTheDocument();
  });

  it('deselecting the type restores the full list (no sticky filter)', () => {
    renderPage();
    const typeFilter = screen.getByTestId('type-filter');
    const bug = within(typeFilter).getByRole('button', { name: 'Bug' });
    fireEvent.click(bug);
    expect(screen.queryByText('Add feature')).not.toBeInTheDocument();
    fireEvent.click(bug);
    expect(screen.getByText('Add feature')).toBeInTheDocument();
  });
});
