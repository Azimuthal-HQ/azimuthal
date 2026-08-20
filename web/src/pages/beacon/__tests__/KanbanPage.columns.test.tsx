import { render, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { buildKanbanColumns, DEFAULT_KANBAN_COLUMNS } from '../kanbanColumns';
import { KanbanPage } from '../KanbanPage';
import type { Ticket, WorkflowState } from '../../../lib/api';

// D7 item 4: the Beacon board must render EVERY state the space's workflow has,
// including custom states. The old board hardcoded {open, in_progress,
// resolved, closed} and dropped every ticket in any other state — invisible
// work. These tests pin the column derivation and the rendered result.

function state(name: string, position: number, over: Partial<WorkflowState> = {}): WorkflowState {
  return {
    id: `st-${name}`,
    workflow_id: 'w1',
    name,
    category: 'in_progress',
    color: '#6b7280',
    position,
    is_initial: false,
    created_at: '',
    ...over,
  };
}

function ticket(id: string, status: string, over: Partial<Ticket> = {}): Ticket {
  return {
    id,
    space_id: 's1',
    number: Number(id.replace(/\D/g, '')) || 1,
    title: `Ticket ${id}`,
    description: '',
    // status is genuinely free text on the wire (a workflow state name); the
    // narrow TicketStatus union is what item 4 is about widening in practice.
    status: status as Ticket['status'],
    priority: 'medium',
    assignee_id: null,
    reporter_id: 'u1',
    requester_id: null,
    requester: null,
    label_ids: [],
    due_at: null,
    created_at: '',
    updated_at: '',
    ...over,
  };
}

describe('buildKanbanColumns', () => {
  it('derives one column per workflow state, in position order, coloured as configured', () => {
    const states = [
      state('closed', 3, { category: 'done', color: '#111111' }),
      state('open', 1, { category: 'todo', color: '#222222', is_initial: true }),
      state('Awaiting legal sign-off', 2, { color: '#ff8800' }),
    ];
    const cols = buildKanbanColumns(states, []);
    expect(cols.map((c) => c.id)).toEqual(['open', 'Awaiting legal sign-off', 'closed']);
    expect(cols.map((c) => c.label)).toEqual(['open', 'Awaiting legal sign-off', 'closed']);
    // The custom state carries its configured colour so it is recognisable.
    expect(cols[1].color).toBe('#ff8800');
  });

  it('never drops a ticket: a status matching no state still gets a column', () => {
    // A renamed state, or a space whose workflow_state_id drifted from status
    // text — the ticket must still be visible somewhere.
    const cols = buildKanbanColumns([state('open', 1)], [ticket('t1', 'Escalated')]);
    expect(cols.map((c) => c.id)).toContain('Escalated');
  });

  it('falls back to the well-known columns only when there is no workflow and no tickets', () => {
    expect(buildKanbanColumns(undefined, undefined)).toEqual(DEFAULT_KANBAN_COLUMNS);
    expect(buildKanbanColumns([], [])).toEqual(DEFAULT_KANBAN_COLUMNS);
  });
});

// ── Rendered board: the custom-state ticket appears in its own column ────────

const customState = state('Awaiting legal sign-off', 2, { color: '#ff8800' });
const openState = state('open', 1, { category: 'todo', is_initial: true });
const customTicket = ticket('t1', 'Awaiting legal sign-off', { title: 'Contract review' });

vi.mock('../../../lib/api', () => ({
  useTickets: () => ({ data: [customTicket], isLoading: false, error: null }),
  useSpace: () => ({ data: { key: 'SD' } }),
  useWorkflowStates: () => ({ data: [openState, customState] }),
  useTicketStatusTransition: () => ({ mutate: vi.fn() }),
  useMe: () => ({ data: { org_id: 'o1' } }),
  useMembers: () => ({ data: [] }),
  friendlyErrorMessage: (_e: unknown, fallback: string) => fallback,
  pendingApprovalOf: () => null,
}));

function renderBoard() {
  return render(
    <MemoryRouter initialEntries={['/beacon/s1/board']}>
      <Routes>
        <Route path="/beacon/:spaceId/board" element={<KanbanPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe('KanbanPage custom-state rendering', () => {
  it('renders a column bearing the custom state name, with the ticket inside it', () => {
    renderBoard();

    // The custom state is a real column now (data-column-id is the status text).
    const column = document.querySelector('[data-column-id="Awaiting legal sign-off"]');
    expect(column).not.toBeNull();

    // Its header shows the state's name…
    expect(within(column as HTMLElement).getByText('Awaiting legal sign-off')).toBeInTheDocument();
    // …and the ticket that sits in that state renders inside it, not nowhere.
    expect(within(column as HTMLElement).getByText('Contract review')).toBeInTheDocument();

    // The old hardcoded 'resolved' column is no longer forced onto the board.
    expect(document.querySelector('[data-column-id="resolved"]')).toBeNull();
    // And the well-known 'open' state from the workflow is still a column.
    expect(document.querySelector('[data-column-id="open"]')).not.toBeNull();
  });
});
