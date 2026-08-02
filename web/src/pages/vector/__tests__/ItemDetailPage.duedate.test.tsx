import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ItemDetailPage } from '../ItemDetailPage';

/**
 * A6: the due-date control on item detail.
 *
 * The item write path already accepted due_at with correct three-state
 * semantics — this control is simply the first surface ever to send it. Which
 * is exactly why the field had been silently cleared on every edit before
 * `optionalField` landed: nothing exercised it.
 *
 * The wire assertions are the point. An `<input type="date">` yields a bare
 * `YYYY-MM-DD`, which the API rejects with 400, so "the handler fired" proves
 * nothing on its own.
 *
 * `lib/api` is mocked wholesale, following TicketDetailPage.portal.test.tsx.
 * The enumeration is an inventory of what this page reaches for: an omitted
 * dependency throws rather than returning undefined.
 */

const state = vi.hoisted(() => ({
  item: null as Record<string, unknown> | null,
  updateItem: vi.fn(),
  refetchItem: vi.fn(),
}));

vi.mock('../../../lib/api', () => ({
  useProjectItem: () => ({
    data: state.item,
    isLoading: false,
    error: null,
    refetch: state.refetchItem,
  }),
  useUpdateProjectItem: () => ({ mutateAsync: state.updateItem, isPending: false }),
  useAvailableTransitions: () => ({ data: undefined, refetch: vi.fn() }),
  useTransitionProjectItemStatus: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useMembers: () => ({ data: [] }),
  useComments: () => ({ data: [], refetch: vi.fn() }),
  useCreateComment: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useMe: () => ({ data: { id: 'u-1', org_id: 'org-1', display_name: 'Ada' } }),
  useRelations: () => ({ data: [], refetch: vi.fn() }),
  useCreateRelation: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useDeleteRelation: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useItemSearch: () => ({ data: [], isLoading: false }),
  useSpace: () => ({ data: { key: 'VEC' } }),
  useItemTypes: () => ({ data: [] }),
  useSprints: () => ({ data: [] }),
  useAssignItemSprint: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useEffectiveAccess: () => ({ data: undefined }),
  useEntityShares: () => ({ data: undefined }),
  useEntityApprovals: () => ({ data: [], isLoading: false, error: null }),
  // Reached through CustomFieldsSection, which the rail renders below the
  // timestamps — not imported by the page itself. The wholesale mock does not
  // care where in the tree the call comes from.
  useItemFields: () => ({ data: [], isLoading: false, error: null }),
  useSetItemField: () => ({ mutateAsync: vi.fn(), isPending: false }),
  friendlyErrorMessage: (_e: unknown, fallback: string) => fallback,
}));

const baseItem = {
  id: 'item-1',
  space_id: 's1',
  number: 7,
  item_key: 'VEC-7',
  title: 'Implement search',
  description: '',
  kind: 'task',
  status: 'open',
  priority: 'medium',
  assignee_id: null,
  reporter_id: 'u-1',
  sprint_id: null,
  rank: '',
  labels: [] as string[],
  due_at: null as string | null,
  created_at: '2026-07-01T00:00:00Z',
  updated_at: '2026-07-02T00:00:00Z',
};

function renderDetail() {
  return render(
    // The real route: App.tsx mounts this page at backlog/:itemKey, and the
    // page reads `itemKey`. Spelling it :itemId here would still pass — the
    // query is mocked — while testing a route the product does not have.
    <MemoryRouter initialEntries={['/vector/s1/backlog/VEC-7']}>
      <Routes>
        <Route path="/vector/:spaceId/backlog/:itemKey" element={<ItemDetailPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

const dueInput = () => screen.getByTestId('item-due-date') as HTMLInputElement;

beforeEach(() => {
  state.item = { ...baseItem };
  state.updateItem = vi.fn().mockResolvedValue({});
  state.refetchItem = vi.fn();
});

afterEach(() => {
  vi.clearAllMocks();
});

describe('item due-date control', () => {
  it('renders empty for an item with no due date', () => {
    renderDetail();
    expect(dueInput().value).toBe('');
  });

  it('renders the stored due date as its UTC calendar date', () => {
    state.item = { ...baseItem, due_at: '2026-09-01T00:00:00Z' };
    renderDetail();
    expect(dueInput().value).toBe('2026-09-01');
  });

  /**
   * The negative direction: postgres returns `2026-09-01T00:00:00Z` as
   * `2026-08-31T20:00:00-04:00` under a non-UTC session zone. Slicing the first
   * ten characters renders the PREVIOUS DAY. This case fails against a
   * `.slice(0, 10)` implementation and passes against `formatUTCDate`.
   */
  it('renders the correct day even when the server serializes a non-UTC offset', () => {
    state.item = { ...baseItem, due_at: '2026-08-31T20:00:00-04:00' };
    renderDetail();
    expect(dueInput().value).toBe('2026-09-01');
  });

  it('sends RFC3339, not the bare date the input produces', async () => {
    renderDetail();

    fireEvent.change(dueInput(), { target: { value: '2026-09-01' } });

    await waitFor(() => expect(state.updateItem).toHaveBeenCalledTimes(1));
    expect(state.updateItem).toHaveBeenCalledWith({ due_at: '2026-09-01T00:00:00Z' });
  });

  /**
   * Clearing must send an explicit null. `toRFC3339Date('')` returns undefined
   * and `JSON.stringify` drops undefined keys, so the naive wiring produces a
   * body with no due_at at all — which the server reads as "leave it alone".
   * That is the tri-state collapse this field's history is made of.
   */
  it('sends an explicit null when the date is cleared', async () => {
    state.item = { ...baseItem, due_at: '2026-09-01T00:00:00Z' };
    renderDetail();

    fireEvent.change(dueInput(), { target: { value: '' } });

    await waitFor(() => expect(state.updateItem).toHaveBeenCalledTimes(1));
    expect(state.updateItem).toHaveBeenCalledWith({ due_at: null });

    const [[body]] = state.updateItem.mock.calls as [[Record<string, unknown>]];
    expect(Object.hasOwn(body, 'due_at')).toBe(true);
    expect(body.due_at).not.toBeUndefined();
  });

  it('refetches the item after a successful change', async () => {
    renderDetail();

    fireEvent.change(dueInput(), { target: { value: '2026-09-01' } });

    await waitFor(() => expect(state.refetchItem).toHaveBeenCalled());
  });

  it('reports a refused change inline instead of throwing', async () => {
    state.updateItem = vi.fn().mockRejectedValue(new Error('nope'));
    renderDetail();

    fireEvent.change(dueInput(), { target: { value: '2026-09-01' } });

    expect(await screen.findByText('The due date could not be changed.')).toBeInTheDocument();
    expect(state.refetchItem).not.toHaveBeenCalled();
  });
});
