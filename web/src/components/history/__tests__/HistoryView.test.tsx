import { render, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { HistoryView } from '../HistoryView';
import type { HistoryEvent } from '../../../lib/api';

/**
 * The History feed's rendering (D5). The maintainer's acceptance case is the
 * first test: a ticket that was closed and then reopened shows BOTH actions,
 * each with its old -> new status. The empty state renders honestly rather than
 * blank, and a non-status event renders its verb without a transition.
 */

function ev(overrides: Partial<HistoryEvent>): HistoryEvent {
  return {
    id: overrides.id ?? Math.random().toString(36).slice(2),
    actor_id: 'u1',
    actor_name: 'Someone',
    action: 'ticket.status_changed',
    payload: {},
    created_at: '2026-01-15T10:30:00Z',
    ...overrides,
  };
}

describe('HistoryView', () => {
  it('renders a close and a reopen, each with old -> new', () => {
    // Server order is newest-first: reopen, then the close before it.
    const events: HistoryEvent[] = [
      ev({ actor_name: 'Ben Boss', payload: { from: 'closed', to: 'open' } }),
      ev({ actor_name: 'Ada Agent', payload: { from: 'in_progress', to: 'closed' } }),
    ];

    render(<HistoryView events={events} />);

    expect(screen.getAllByTestId('history-row')).toHaveLength(2);
    expect(screen.getAllByTestId('history-status-transition')).toHaveLength(2);

    // The close: in_progress -> closed, attributed to Ada.
    const closeRow = screen.getByText('Ada Agent').closest('[data-testid="history-row"]') as HTMLElement;
    expect(within(closeRow).getByText('In progress')).toBeInTheDocument();
    expect(within(closeRow).getByText('Closed')).toBeInTheDocument();

    // The reopen: closed -> open, attributed to Ben.
    const reopenRow = screen.getByText('Ben Boss').closest('[data-testid="history-row"]') as HTMLElement;
    expect(within(reopenRow).getByText('Closed')).toBeInTheDocument();
    expect(within(reopenRow).getByText('Open')).toBeInTheDocument();
  });

  it('renders an honest empty state, not a blank', () => {
    render(<HistoryView events={[]} />);
    expect(screen.getByTestId('history-empty')).toHaveTextContent(/no history yet/i);
    expect(screen.queryByTestId('history-row')).toBeNull();
  });

  it('shows a loading state while fetching', () => {
    render(<HistoryView events={undefined} isLoading />);
    expect(screen.getByTestId('history-loading')).toBeInTheDocument();
    expect(screen.queryByTestId('history-row')).toBeNull();
  });

  it('renders a non-status event as a verb with no transition', () => {
    render(<HistoryView events={[ev({ action: 'ticket.created', payload: {}, actor_name: 'Ada Agent' })]} />);
    expect(screen.getByText(/created this ticket/i)).toBeInTheDocument();
    expect(screen.queryByTestId('history-status-transition')).toBeNull();
  });

  it('renders an item status change (the item-side vocabulary)', () => {
    render(<HistoryView events={[ev({ action: 'item.status_changed', payload: { from: 'todo', to: 'done' }, actor_name: 'Ada Agent' })]} />);
    const row = screen.getByTestId('history-row');
    expect(within(row).getByText('Todo')).toBeInTheDocument();
    expect(within(row).getByText('Done')).toBeInTheDocument();
  });
});
