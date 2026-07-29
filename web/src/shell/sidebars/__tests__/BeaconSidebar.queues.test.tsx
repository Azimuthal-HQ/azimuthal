import { render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { BeaconSidebar } from '../BeaconSidebar';
import type { Queue, QueueList } from '../../../lib/api';

/**
 * The Queues section of the Beacon sidebar (P4).
 *
 * Two things are asserted, and both are load-bearing:
 *
 *  - The queues render in the ORDER THE SERVER SENT. `position` is what a
 *    service desk edits so an agent's eye lands on the right queue first; a
 *    client-side sort by name would throw that away everywhere except the one
 *    screen where the order is edited.
 *  - The "new queue" affordance follows `can_manage` from the list response,
 *    both ways. Only checking that it is hidden would pass with the control
 *    deleted outright.
 */

function queue(id: string, name: string, position: number): Queue {
  return {
    id,
    space_id: 'space-1',
    position,
    name,
    description: '',
    query: { v: 1, filter: { modules: ['beacon'] }, sort: { field: 'updated_at', dir: 'desc' } },
    owner_id: 'u1',
    can_manage: true,
  };
}

const state = vi.hoisted(() => ({
  list: { queues: [], can_manage: false } as QueueList,
}));

vi.mock('../../../lib/api', () => ({
  useQueues: () => ({ data: state.list, isLoading: false, error: null }),
  useSpaces: () => ({ data: [], isLoading: false }),
  useTeams: () => ({ data: [], isLoading: false }),
  friendlyErrorMessage: (_e: unknown, fallback: string) => fallback,
}));

vi.mock('../../../lib/auth', () => ({
  useAuth: () => ({ user: { id: 'u1', email: 'me@example.com', orgId: 'org-1', role: 'member' } }),
  getCurrentOrgId: () => 'org-1',
}));

function renderSidebar(list: QueueList) {
  state.list = list;
  return render(
    <MemoryRouter initialEntries={['/beacon/space-1/tickets']}>
      <BeaconSidebar space={undefined} spaceId="space-1" />
    </MemoryRouter>,
  );
}

// Deliberately out of alphabetical order: an accidental sort would reorder
// these, and the test would catch it.
const ORDERED = [
  queue('q1', 'Unassigned', 0),
  queue('q2', 'All open', 1),
  queue('q3', 'Assigned to me', 2),
];

describe('BeaconSidebar — the Queues section', () => {
  it('lists the space queues in the order the server sent', () => {
    renderSidebar({ queues: ORDERED, can_manage: true });

    const items = within(screen.getByTestId('sidebar-queues')).getAllByTestId(
      'sidebar-queue-item',
    );
    expect(items.map((el) => el.textContent)).toEqual([
      'Unassigned',
      'All open',
      'Assigned to me',
    ]);
    expect(items[0]).toHaveAttribute('href', '/beacon/space-1/queues/q1');
  });

  it('offers the new-queue affordance when the server says can_manage', () => {
    renderSidebar({ queues: ORDERED, can_manage: true });

    expect(screen.getByTestId('sidebar-new-queue')).toBeInTheDocument();
  });

  it('withholds it when the server says it cannot manage', () => {
    renderSidebar({ queues: ORDERED, can_manage: false });

    expect(screen.queryByTestId('sidebar-new-queue')).toBeNull();
    // The queues themselves stay: reading needs only space-readability.
    expect(
      within(screen.getByTestId('sidebar-queues')).getAllByTestId('sidebar-queue-item'),
    ).toHaveLength(3);
  });

  it('says so plainly when a space has no queues yet', () => {
    renderSidebar({ queues: [], can_manage: false });

    expect(screen.queryByTestId('sidebar-queues')).toBeNull();
    expect(screen.getByText('No queues yet.')).toBeInTheDocument();
  });
});
