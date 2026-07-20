import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { useAuditBatch } from '../../../lib/api';
import { AuditLogPage } from '../AuditLogPage';

// One batch representative (a bulk grant change of 3 events, with a ticket
// reference) plus one plain single entry. Fixtures live inside the factory
// because vi.mock hoists above file-scope consts.
vi.mock('../../../lib/api', () => ({
  useAuditLog: vi.fn(() => ({
    data: {
      entries: [
        {
          id: 'e1',
          actor_id: 'u1',
          actor_name: 'Ada Admin',
          action: 'grant.created',
          entity_kind: 'grant',
          entity_id: 'aaaabbbb-cccc-dddd-eeee-ffff00001111',
          payload: {},
          batch_id: 'b1',
          ticket_ref: 'OPS-123',
          created_at: '2026-07-19T10:00:00Z',
          batch_size: 3,
        },
        {
          id: 'e2',
          actor_id: null,
          action: 'auth.login_failed',
          entity_kind: 'user',
          entity_id: '99998888-7777-6666-5555-444433332222',
          payload: { email: 'intruder@example.com' },
          batch_id: null,
          ticket_ref: null,
          created_at: '2026-07-19T09:00:00Z',
          batch_size: 1,
        },
      ],
      next_cursor: undefined,
    },
    isLoading: false,
    isFetching: false,
    error: null,
  })),
  useAuditBatch: vi.fn(() => ({
    data: [
      {
        id: 'be1',
        actor_id: 'u1',
        actor_name: 'Ada Admin',
        action: 'grant.created',
        entity_kind: 'grant',
        entity_id: '11111111-aaaa-bbbb-cccc-dddddddddddd',
        payload: { role: 'contributor' },
        batch_id: 'b1',
        ticket_ref: 'OPS-123',
        created_at: '2026-07-19T09:59:58Z',
        batch_size: 3,
      },
      {
        id: 'be2',
        actor_id: 'u1',
        actor_name: 'Ada Admin',
        action: 'grant.created',
        entity_kind: 'grant',
        entity_id: '22222222-aaaa-bbbb-cccc-dddddddddddd',
        payload: { role: 'contributor' },
        batch_id: 'b1',
        ticket_ref: 'OPS-123',
        created_at: '2026-07-19T09:59:59Z',
        batch_size: 3,
      },
      {
        id: 'be3',
        actor_id: 'u1',
        actor_name: 'Ada Admin',
        action: 'grant.revoked',
        entity_kind: 'grant',
        entity_id: '33333333-aaaa-bbbb-cccc-dddddddddddd',
        payload: { role: 'viewer' },
        batch_id: 'b1',
        ticket_ref: 'OPS-123',
        created_at: '2026-07-19T10:00:00Z',
        batch_size: 3,
      },
    ],
    isLoading: false,
    error: null,
  })),
  friendlyErrorMessage: vi.fn((_err: unknown, fallback: string) => fallback),
}));

vi.mock('../../../lib/auth', () => ({
  useAuth: vi.fn(() => ({
    user: { id: 'u1', email: 'admin@example.com', orgId: 'org-1', role: 'admin' },
    isAuthenticated: true,
    login: vi.fn(),
    logout: vi.fn(),
  })),
}));

describe('AuditLogPage', () => {
  it('renders a batch as a single row with its count pill and ticket ref', () => {
    render(<AuditLogPage />);

    // The batch of 3 collapses to exactly one representative row.
    const batchRows = screen.getAllByTestId('audit-batch-row-b1');
    expect(batchRows).toHaveLength(1);
    expect(batchRows[0]).toHaveTextContent('×3');
    expect(batchRows[0]).toHaveTextContent('grant.created');
    expect(batchRows[0]).toHaveTextContent('OPS-123');

    // The single entry renders as one plain row, with the em-dash actor.
    const plainRows = screen.getAllByTestId('audit-entry-row');
    expect(plainRows).toHaveLength(1);
    expect(plainRows[0]).toHaveTextContent('—');
  });

  it('expands a batch row into its constituent events', () => {
    render(<AuditLogPage />);

    // Collapsed: no sub-rows yet.
    expect(screen.queryByTestId('audit-batch-event')).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId('audit-batch-row-b1'));

    // The expansion mounts the batch query for this batch...
    expect(vi.mocked(useAuditBatch)).toHaveBeenCalledWith('org-1', 'b1');

    // ...and renders all three constituent events.
    const events = screen.getAllByTestId('audit-batch-event');
    expect(events).toHaveLength(3);
    expect(events[2]).toHaveTextContent('grant.revoked');
    expect(events[2]).toHaveTextContent('role=viewer');

    // Clicking again collapses.
    fireEvent.click(screen.getByTestId('audit-batch-row-b1'));
    expect(screen.queryByTestId('audit-batch-event')).not.toBeInTheDocument();
  });
});
