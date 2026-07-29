import { fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { QueuesPage } from '../QueuesPage';
import type { Queue, QueueList } from '../../../../lib/api';

/**
 * Two rules this page must not get wrong (P4 Beacon queues).
 *
 * # 1. `can_manage` arrives on the wire and is the only authority
 *
 * The server gates every queue mutation on `manage_queue`, which ADR-0007 puts
 * at the AGENT role. A contributor lists a space's queues perfectly well and
 * gets `can_manage: false` — so the persona that proves this gating is a
 * contributor-shaped response, not a viewer who would have been refused
 * upstream by the write floor anyway.
 *
 * Both directions are asserted. A test that only checked "hidden when false"
 * would still pass if the controls were deleted outright, which is the
 * §2 negative-test question: it would assert nothing.
 *
 * # 2. Reorder sends the COMPLETE order
 *
 * `PUT …/queues/order` takes a permutation of the space's live queues — every
 * one exactly once. A body naming only the two that swapped is refused with a
 * 422 and changes nothing, so the assertion below is on the exact array, and
 * separately on the property that makes it acceptable.
 */

const BANNED_ERROR_COPY = [
  'Something went wrong',
  'Failed to load',
  'could not be loaded',
  'invalid space_id',
  'invalid request body',
  'UNAUTHORIZED',
];

function queue(overrides: Partial<Queue>): Queue {
  return {
    id: 'q1',
    space_id: 'space-1',
    position: 0,
    name: 'All open',
    description: 'Everything not yet resolved.',
    query: { v: 1, filter: { modules: ['beacon'] }, sort: { field: 'updated_at', dir: 'desc' } },
    owner_id: 'u1',
    can_manage: true,
    ...overrides,
  };
}

const state = vi.hoisted(() => ({
  list: { queues: [], can_manage: false } as QueueList,
  isLoading: false,
  error: null as unknown,
  reorder: vi.fn(),
  createDefaults: vi.fn(),
  remove: vi.fn(),
}));

vi.mock('../../../../lib/api', () => ({
  useQueues: () => ({
    data: state.list,
    isLoading: state.isLoading,
    isSuccess: !state.isLoading && !state.error,
    error: state.error,
  }),
  useReorderQueues: () => ({ mutate: state.reorder, isPending: false, error: null }),
  useCreateDefaultQueues: () => ({
    mutateAsync: state.createDefaults,
    isPending: false,
    error: null,
  }),
  useDeleteQueue: () => ({ mutateAsync: state.remove, isPending: false, error: null }),
  friendlyErrorMessage: (_e: unknown, fallback: string) => fallback,
}));

vi.mock('../../../../lib/auth', () => ({
  useAuth: () => ({ user: { id: 'u1', email: 'me@example.com', orgId: 'org-1', role: 'member' } }),
}));

function renderPage(list: QueueList) {
  state.list = list;
  state.isLoading = false;
  state.error = null;
  state.reorder.mockClear();
  state.createDefaults.mockClear();
  state.remove.mockClear();
  return render(
    <MemoryRouter initialEntries={['/beacon/space-1/queues']}>
      <Routes>
        <Route path="/beacon/:spaceId/queues" element={<QueuesPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

const THREE = [
  queue({ id: 'q1', name: 'All open', position: 0 }),
  queue({ id: 'q2', name: 'Assigned to me', position: 1 }),
  queue({ id: 'q3', name: 'Unassigned', position: 2 }),
];

describe('QueuesPage — can_manage gating', () => {
  it('offers every management control when the server says can_manage', () => {
    renderPage({ queues: THREE, can_manage: true });

    expect(screen.getByTestId('new-queue')).toBeInTheDocument();
    const rows = screen.getAllByTestId('queue-row');
    expect(rows).toHaveLength(3);
    for (const row of rows) {
      expect(within(row).getByTestId('edit-queue')).toBeInTheDocument();
      expect(within(row).getByTestId('delete-queue')).toBeInTheDocument();
      expect(within(row).getByTestId('queue-move-up')).toBeInTheDocument();
      expect(within(row).getByTestId('queue-move-down')).toBeInTheDocument();
    }
  });

  it('hides every management control when the server says it cannot manage', () => {
    // A CONTRIBUTOR: past the write floor, short of manage_queue. Not a viewer
    // — a viewer is refused upstream and would prove nothing about this gate.
    renderPage({ queues: THREE, can_manage: false });

    expect(screen.queryByTestId('new-queue')).toBeNull();
    expect(screen.queryByTestId('edit-queue')).toBeNull();
    expect(screen.queryByTestId('delete-queue')).toBeNull();
    expect(screen.queryByTestId('queue-move-up')).toBeNull();
    expect(screen.queryByTestId('queue-move-down')).toBeNull();
  });

  it('still shows every queue to a reader who cannot manage them', () => {
    // Hiding the controls must not hide the content: reading a queue needs
    // only space-readability, which is exactly the audience a queue has.
    renderPage({ queues: THREE, can_manage: false });

    const rows = screen.getAllByTestId('queue-row');
    expect(rows).toHaveLength(3);
    expect(within(rows[1]).getByRole('link', { name: 'Assigned to me' })).toHaveAttribute(
      'href',
      '/beacon/space-1/queues/q2',
    );
  });

  it('offers the one-click defaults on an empty space it can manage', () => {
    renderPage({ queues: [], can_manage: true });

    expect(screen.getByTestId('create-default-queues')).toBeInTheDocument();
  });

  it('withholds the defaults button from a reader who cannot manage', () => {
    renderPage({ queues: [], can_manage: false });

    expect(screen.queryByTestId('create-default-queues')).toBeNull();
    // …and the empty state is still content, never an error.
    expect(screen.getByText('No queues in this space yet')).toBeInTheDocument();
    expect(screen.queryByTestId('queues-error')).toBeNull();
  });

  it('keeps the banned fallback copy off this page entirely', () => {
    const { container } = renderPage({ queues: [], can_manage: false });
    for (const banned of BANNED_ERROR_COPY) {
      expect(container.textContent).not.toContain(banned);
    }
  });
});

describe('QueuesPage — reorder sends the complete order', () => {
  it('sends every live queue exactly once when one moves down', () => {
    renderPage({ queues: THREE, can_manage: true });

    const rows = screen.getAllByTestId('queue-row');
    fireEvent.click(within(rows[0]).getByTestId('queue-move-down'));

    expect(state.reorder).toHaveBeenCalledTimes(1);
    const sent = state.reorder.mock.calls[0][0] as string[];

    // The whole order, with one entry moved — NOT the pair that swapped.
    expect(sent).toEqual(['q2', 'q1', 'q3']);
    expect(sent).toHaveLength(THREE.length);
    expect(new Set(sent).size).toBe(sent.length);
    expect([...sent].sort()).toEqual(['q1', 'q2', 'q3']);
  });

  it('sends every live queue exactly once when one moves up', () => {
    renderPage({ queues: THREE, can_manage: true });

    const rows = screen.getAllByTestId('queue-row');
    fireEvent.click(within(rows[2]).getByTestId('queue-move-up'));

    const sent = state.reorder.mock.calls[0][0] as string[];
    expect(sent).toEqual(['q1', 'q3', 'q2']);
    expect(sent).toHaveLength(THREE.length);
  });

  it('never sends a partial list — a two-element body would be a 422', () => {
    renderPage({ queues: THREE, can_manage: true });

    const rows = screen.getAllByTestId('queue-row');
    fireEvent.click(within(rows[1]).getByTestId('queue-move-down'));

    const sent = state.reorder.mock.calls[0][0] as string[];
    expect(sent).not.toEqual(['q3', 'q2']);
    expect(sent).toEqual(['q1', 'q3', 'q2']);
  });

  it('disables the move that would leave the list, and sends nothing', () => {
    renderPage({ queues: THREE, can_manage: true });

    const rows = screen.getAllByTestId('queue-row');
    const topUp = within(rows[0]).getByTestId('queue-move-up');
    const bottomDown = within(rows[2]).getByTestId('queue-move-down');
    expect(topUp).toBeDisabled();
    expect(bottomDown).toBeDisabled();

    fireEvent.click(topUp);
    fireEvent.click(bottomDown);
    expect(state.reorder).not.toHaveBeenCalled();
  });
});

describe('QueuesPage — the default queues are idempotent', () => {
  it('reports how many it created rather than claiming four', async () => {
    renderPage({ queues: [], can_manage: true });
    state.createDefaults.mockResolvedValueOnce(2);

    fireEvent.click(screen.getByTestId('create-default-queues'));

    expect(await screen.findByTestId('queue-defaults-message')).toHaveTextContent('Added 2 queues.');
  });

  it('says nothing was duplicated when the space already had them', async () => {
    renderPage({ queues: [], can_manage: true });
    state.createDefaults.mockResolvedValueOnce(0);

    fireEvent.click(screen.getByTestId('create-default-queues'));

    expect(await screen.findByTestId('queue-defaults-message')).toHaveTextContent(
      'This space already had all four. Nothing was duplicated.',
    );
  });
});
