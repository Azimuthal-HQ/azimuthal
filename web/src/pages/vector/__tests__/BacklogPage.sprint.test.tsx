import { render, screen, fireEvent, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { BacklogPage } from '../BacklogPage';
import type { ProjectItem, Sprint } from '../../../lib/api';

// W2: assigning backlog items to sprints from the backlog, and the group
// headings that name the sprint rather than printing its UUID.

const SPRINT_ONE = '11111111-1111-1111-1111-111111111111';
const SPRINT_DONE = '22222222-2222-2222-2222-222222222222';

function item(id: string, title: string, sprintId: string | null): ProjectItem {
  return {
    id, space_id: 's1', number: 1, item_key: `VEC-${id}`,
    title, description: '', kind: 'task', status: 'open', priority: 'medium',
    assignee_id: null, reporter_id: 'u1', sprint_id: sprintId, rank: `0|${id}:`,
    labels: [], due_at: null, created_at: '', updated_at: '',
  };
}

function sprint(id: string, name: string, status: Sprint['status']): Sprint {
  return {
    id, space_id: 's1', name, goal: '', status,
    starts_at: null, ends_at: null,
    created_by: 'u1', created_at: '', updated_at: '',
  };
}

const sprints = [
  sprint(SPRINT_ONE, 'Sprint One', 'active'),
  sprint(SPRINT_DONE, 'Old Sprint', 'completed'),
];

const items = [
  item('1', 'Loose item', null),
  item('2', 'Sprinted item', SPRINT_ONE),
  item('3', 'Archived item', SPRINT_DONE),
];

const assignMock = vi.fn().mockResolvedValue(undefined);

vi.mock('../../../lib/auth', () => ({ getCurrentOrgId: () => 'org1' }));

vi.mock('../../../lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../lib/api')>();
  return {
    ...actual,
    useProjectItems: () => ({ data: items, isLoading: false, error: null }),
    useCreateProjectItem: () => ({ mutateAsync: vi.fn(), isPending: false, error: null }),
    useRankItem: () => ({ mutate: vi.fn() }),
    useSprints: () => ({ data: sprints }),
    useAssignItemSprint: () => ({ mutateAsync: assignMock, isPending: false }),
    useSpace: () => ({ data: { id: 's1', key: 'VEC' } }),
    useItemTypes: () => ({ data: [] }),
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

beforeEach(() => assignMock.mockClear());
afterEach(() => vi.clearAllMocks());

describe('BacklogPage sprint grouping', () => {
  it('names each sprint group and never renders a raw sprint id', () => {
    renderPage();

    // Level 2 — the page's own <h1> is also titled "Backlog".
    expect(screen.getByRole('heading', { level: 2, name: /Sprint One/ })).toBeInTheDocument();
    expect(screen.getByRole('heading', { level: 2, name: /Old Sprint/ })).toBeInTheDocument();
    expect(screen.getByRole('heading', { level: 2, name: /Backlog/ })).toBeInTheDocument();

    // The defect this replaces: the heading was the sprint's UUID. If the
    // group label regressed to the id, this assertion fails.
    expect(screen.queryByText(new RegExp(SPRINT_ONE))).not.toBeInTheDocument();
    expect(screen.queryByText(new RegExp(SPRINT_DONE))).not.toBeInTheDocument();
  });

  it('orders the active sprint before the completed one, with Backlog last', () => {
    renderPage();
    const headings = screen.getAllByRole('heading', { level: 2 }).map(h => h.textContent ?? '');
    const order = headings.map(h => h.replace(/\s*\(\d+ items\)\s*$/, '').trim());
    expect(order).toEqual(['Sprint One', 'Old Sprint', 'Backlog']);
  });
});

describe('BacklogPage sprint assignment', () => {
  it('assigns an item to a sprint from its row control', async () => {
    renderPage();

    const select = screen.getByLabelText('Sprint for VEC-1') as HTMLSelectElement;
    expect(select.value).toBe('__backlog__');

    fireEvent.change(select, { target: { value: SPRINT_ONE } });

    expect(assignMock).toHaveBeenCalledWith({ itemId: '1', sprintId: SPRINT_ONE });
  });

  it('returns an item to the backlog by selecting Backlog', () => {
    renderPage();

    const select = screen.getByLabelText('Sprint for VEC-2') as HTMLSelectElement;
    expect(select.value).toBe(SPRINT_ONE);

    fireEvent.change(select, { target: { value: '__backlog__' } });

    // null, not the sentinel string — the wire contract is a nullable sprint_id.
    expect(assignMock).toHaveBeenCalledWith({ itemId: '2', sprintId: null });
  });

  it('does not offer a completed sprint as an assignment target', () => {
    renderPage();
    const select = screen.getByLabelText('Sprint for VEC-1');
    const options = within(select).getAllByRole('option').map(o => o.textContent);
    expect(options).toContain('Sprint One');
    expect(options).not.toContain('Old Sprint');
  });

  it('keeps the owning completed sprint listed on an item that is already on it', () => {
    renderPage();
    // Otherwise the control would read "Backlog" for an item that is not in
    // the backlog — showing the user a value that is not the truth.
    const select = screen.getByLabelText('Sprint for VEC-3') as HTMLSelectElement;
    expect(select.value).toBe(SPRINT_DONE);
    const options = within(select).getAllByRole('option').map(o => o.textContent);
    expect(options).toContain('Old Sprint');
  });

  it('bulk-moves every selected item and then clears the selection', async () => {
    renderPage();

    fireEvent.click(screen.getByLabelText('Select VEC-1'));
    fireEvent.click(screen.getByLabelText('Select VEC-2'));

    const bar = await screen.findByTestId('backlog-bulk-bar');
    expect(within(bar).getByText('2 selected')).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('Move selected to sprint'), {
      target: { value: SPRINT_ONE },
    });

    // Both items move; the bar disappears once the selection is emptied.
    await vi.waitFor(() => expect(assignMock).toHaveBeenCalledTimes(2));
    expect(assignMock).toHaveBeenCalledWith({ itemId: '1', sprintId: SPRINT_ONE });
    expect(assignMock).toHaveBeenCalledWith({ itemId: '2', sprintId: SPRINT_ONE });
    await vi.waitFor(() =>
      expect(screen.queryByTestId('backlog-bulk-bar')).not.toBeInTheDocument(),
    );
  });

  it('shows no bulk bar until something is selected', () => {
    renderPage();
    expect(screen.queryByTestId('backlog-bulk-bar')).not.toBeInTheDocument();
  });
});
