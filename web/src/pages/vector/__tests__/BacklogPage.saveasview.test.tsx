import { render, screen, fireEvent, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { BacklogPage } from '../BacklogPage';
import type { ProjectItem } from '../../../lib/api';

// P4: the "Save as view" entry point on the backlog. The type chips are the
// only structured filter this page has, and they map to the vector-only
// `kinds` field.

function item(id: string, title: string, kind: string): ProjectItem {
  return {
    id, space_id: 's1', number: 1, item_key: `VEC-${id}`,
    title, description: '', kind, status: 'open', priority: 'medium',
    assignee_id: null, reporter_id: 'u1', sprint_id: null, rank: `0|${id}:`,
    labels: [], created_at: '', updated_at: '',
  };
}

vi.mock('../../../lib/auth', () => ({ getCurrentOrgId: () => 'org1' }));

vi.mock('../../../lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../lib/api')>();
  return {
    ...actual,
    useProjectItems: () => ({
      data: [item('1', 'Fix crash', 'bug'), item('2', 'Add feature', 'task')],
      isLoading: false,
      error: null,
    }),
    useCreateProjectItem: () => ({ mutateAsync: vi.fn(), isPending: false, error: null }),
    useRankItem: () => ({ mutate: vi.fn() }),
    useSprints: () => ({ data: [] }),
    useAssignItemSprint: () => ({ mutateAsync: vi.fn(), isPending: false }),
    useSpace: () => ({ data: { id: 's1', key: 'VEC', name: 'Platform' } }),
    useItemTypes: () => ({
      data: [
        { id: 't1', org_id: 'org1', slug: 'bug', name: 'Bug', position: 1, archived_at: null, created_at: '', updated_at: '' },
        { id: 't2', org_id: 'org1', slug: 'task', name: 'Task', position: 2, archived_at: null, created_at: '', updated_at: '' },
      ],
    }),
  };
});

/** Stands in for the view builder: dumps whatever draft arrived. */
function DraftProbe() {
  const { state } = useLocation();
  return <pre data-testid="draft">{JSON.stringify(state)}</pre>;
}

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/vector/s1/backlog']}>
      <Routes>
        <Route path="/vector/:spaceId/backlog" element={<BacklogPage />} />
        <Route path="/views/new" element={<DraftProbe />} />
      </Routes>
    </MemoryRouter>,
  );
}

function arrivedDraft() {
  return JSON.parse(screen.getByTestId('draft').textContent ?? 'null').draft;
}

describe('BacklogPage "Save as view"', () => {
  it('hands the selected type chips and search to /views/new as a vector QueryDoc', () => {
    renderPage();

    fireEvent.click(within(screen.getByTestId('type-filter')).getByRole('button', { name: 'Bug' }));
    fireEvent.change(screen.getByPlaceholderText('Search items...'), {
      target: { value: 'login' },
    });

    fireEvent.click(screen.getByTestId('save-as-view'));

    expect(arrivedDraft()).toEqual({
      name: 'Backlog in Platform',
      query: {
        v: 1,
        filter: {
          modules: ['vector'],
          space_ids: ['s1'],
          kinds: ['bug'],
          text: 'login',
        },
        sort: { field: 'updated_at', dir: 'desc' },
      },
    });
  });

  // Negative twin: an empty chip selection means "every type" on the page, so
  // the draft must carry no kinds rather than an empty list.
  it('carries no kinds when no type chip is selected', () => {
    renderPage();
    fireEvent.click(screen.getByTestId('save-as-view'));

    expect(arrivedDraft().query.filter).toEqual({
      modules: ['vector'],
      space_ids: ['s1'],
    });
  });
});
