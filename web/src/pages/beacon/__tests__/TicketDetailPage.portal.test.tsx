import { render, screen, fireEvent, waitFor, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { TicketDetailPage } from '../TicketDetailPage';

/**
 * Agent-side portal affordances on the ticket detail page (customer portal,
 * migrations 044/045). There was no test file for this page and none anywhere
 * for a comment thread or composer, so this is the first coverage of either.
 *
 * `lib/api` is mocked wholesale (the TicketListPage pattern) while
 * `components/priority.tsx` stays real, so the priority vocabulary is genuinely
 * exercised rather than stubbed.
 */

const state = vi.hoisted(() => ({
  ticket: null as Record<string, unknown> | null,
  comments: [] as Record<string, unknown>[],
  createComment: vi.fn(),
  refetchComments: vi.fn(),
}));

vi.mock('../../../lib/api', () => ({
  useTicket: () => ({ data: state.ticket, isLoading: false, error: null, refetch: vi.fn() }),
  // The status picker reads its options from the server's offering. `undefined`
  // is the still-loading answer, which falls back to the page's own vocabulary
  // — the right stub here, because these tests are about the COMMENT surface
  // and the picker only has to render at all.
  useAvailableTransitions: () => ({ data: undefined, refetch: vi.fn() }),
  useTransitionTicketStatus: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useAssignTicket: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useMembers: () => ({ data: members }),
  useComments: () => ({ data: state.comments, refetch: state.refetchComments }),
  useCreateComment: () => ({ mutateAsync: state.createComment, isPending: false }),
  useMe: () => ({ data: { id: 'u-agent', org_id: 'org-1', display_name: 'Ada Agent' } }),
  useSpace: () => ({ data: { key: 'SD' } }),
  // EntityShareControl renders nothing without manage_shares; stubbing these
  // keeps the share surface out of this page's assertions.
  useEffectiveAccess: () => ({ data: undefined }),
  useEntityShares: () => ({ data: undefined }),
  // ApprovalBlock (P-W PR-B) renders nothing for an item with no approval
  // history, which keeps the ADR-0011 surface out of this page's assertions
  // exactly as the two above keep the share surface out. Declared rather than
  // omitted because this mock replaces the module wholesale: an unenumerated
  // dependency throws, which is what makes the block a real inventory of what
  // the page reaches for.
  useEntityApprovals: () => ({ data: [], isLoading: false, error: null }),
  friendlyErrorMessage: (_e: unknown, fallback: string) => fallback,
}));

const members = [
  { user_id: 'u-reporter', org_id: 'org-1', display_name: 'Ingrid Internal', email: 'ingrid@corp.test', role: 'member' },
  { user_id: 'u-agent', org_id: 'org-1', display_name: 'Ada Agent', email: 'ada@corp.test', role: 'member' },
];

const baseTicket = {
  id: 'ticket-1',
  space_id: 's1',
  number: 12,
  title: 'Printer on fire',
  description: '',
  status: 'open',
  priority: 'high',
  assignee_id: null,
  label_ids: [],
  created_at: '2026-07-01T00:00:00Z',
  updated_at: '2026-07-02T00:00:00Z',
};

/** Raised by an org member: reporter set, requester null (the 044 XOR). */
const internalTicket = {
  ...baseTicket,
  reporter_id: 'u-reporter',
  requester_id: null,
  requester: null,
};

/** Raised through the portal: requester set, reporter null — and the requester
 *  has no `users` row, so it is absent from `members` by design. */
const portalTicket = {
  ...baseTicket,
  reporter_id: null,
  requester_id: 'req-1',
  requester: { id: 'req-1', display_name: 'Cora Customer', email: 'cora@example.test' },
};

function renderDetail() {
  return render(
    <MemoryRouter initialEntries={['/beacon/s1/tickets/ticket-1']}>
      <Routes>
        <Route path="/beacon/:spaceId/tickets/:ticketId" element={<TicketDetailPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  state.ticket = internalTicket;
  state.comments = [];
  state.createComment = vi.fn().mockResolvedValue({});
  state.refetchComments = vi.fn();
});

afterEach(() => {
  vi.clearAllMocks();
});

// ---------------------------------------------------------------------------
// 1. The default is the whole safety property of this feature.
// ---------------------------------------------------------------------------

describe('comment composer visibility default', () => {
  it('sends visibility:internal explicitly when the toggle is never touched', async () => {
    renderDetail();

    fireEvent.change(screen.getByTestId('comment-composer'), {
      target: { value: 'Checked the logs, nothing yet.' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Comment' }));

    await waitFor(() => expect(state.createComment).toHaveBeenCalledTimes(1));

    // The PAYLOAD, not the DOM. An internal note posted publicly is a
    // disclosure to an external customer that cannot be recalled, and the only
    // thing standing between the operator and that is this default — so it is
    // pinned on the wire, where the consequence actually lives.
    expect(state.createComment).toHaveBeenCalledWith({
      content: 'Checked the logs, nothing yet.',
      visibility: 'internal',
    });
  });

  it('sends visibility:public only after the toggle is moved there', async () => {
    renderDetail();

    fireEvent.click(
      within(screen.getByTestId('comment-visibility')).getByRole('radio', {
        name: 'Reply to customer',
      }),
    );
    fireEvent.change(screen.getByTestId('comment-composer'), {
      target: { value: 'We have shipped a replacement.' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Comment' }));

    await waitFor(() => expect(state.createComment).toHaveBeenCalledTimes(1));
    expect(state.createComment).toHaveBeenCalledWith({
      content: 'We have shipped a replacement.',
      visibility: 'public',
    });
  });

  it('refetches the thread rather than rendering the create response', async () => {
    renderDetail();

    fireEvent.change(screen.getByTestId('comment-composer'), { target: { value: 'note' } });
    fireEvent.click(screen.getByRole('button', { name: 'Comment' }));

    // from_requester is populated only on the LIST path; the create response
    // literal always returns false, so the echo must never reach the thread.
    await waitFor(() => expect(state.refetchComments).toHaveBeenCalledTimes(1));
  });
});

// ---------------------------------------------------------------------------
// 6. The state must be legible while typing, not only at the toggle.
// ---------------------------------------------------------------------------

describe('comment composer visibility state chip', () => {
  it('states the audience as a full sentence and changes it with the toggle', () => {
    renderDetail();

    const chip = screen.getByTestId('comment-visibility-state');
    expect(chip).toHaveTextContent('Only your team can see this.');

    fireEvent.click(
      within(screen.getByTestId('comment-visibility')).getByRole('radio', {
        name: 'Reply to customer',
      }),
    );
    expect(screen.getByTestId('comment-visibility-state')).toHaveTextContent(
      'The customer will see this.',
    );

    // And back — the chip tracks the control in both directions.
    fireEvent.click(
      within(screen.getByTestId('comment-visibility')).getByRole('radio', {
        name: 'Internal note',
      }),
    );
    expect(screen.getByTestId('comment-visibility-state')).toHaveTextContent(
      'Only your team can see this.',
    );
  });

  it('keeps the state chip in the DOM while the operator is composing', () => {
    renderDetail();

    fireEvent.click(
      within(screen.getByTestId('comment-visibility')).getByRole('radio', {
        name: 'Reply to customer',
      }),
    );
    fireEvent.change(screen.getByTestId('comment-composer'), {
      target: { value: 'Half-written reply' },
    });

    expect(screen.getByTestId('comment-visibility-state')).toHaveTextContent(
      'The customer will see this.',
    );
  });
});

// ---------------------------------------------------------------------------
// 3. Public comments are marked in the thread — both directions.
// ---------------------------------------------------------------------------

describe('comment thread visibility markers', () => {
  beforeEach(() => {
    state.comments = [
      {
        id: 'c-internal',
        entity_type: 'ticket',
        entity_id: 'ticket-1',
        author_id: 'u-agent',
        author_name: 'Ada Agent',
        body: 'Escalating to infra.',
        content: 'Escalating to infra.',
        visibility: 'internal',
        from_requester: false,
        created_at: '2026-07-01T01:00:00Z',
        updated_at: '2026-07-01T01:00:00Z',
      },
      {
        id: 'c-public',
        entity_type: 'ticket',
        entity_id: 'ticket-1',
        author_id: 'u-agent',
        author_name: 'Ada Agent',
        body: 'A replacement is on its way.',
        content: 'A replacement is on its way.',
        visibility: 'public',
        from_requester: false,
        created_at: '2026-07-01T02:00:00Z',
        updated_at: '2026-07-01T02:00:00Z',
      },
    ];
  });

  it('marks the public comment and leaves the internal one unmarked', () => {
    renderDetail();

    const rows = screen.getAllByTestId('comment-row');
    expect(rows).toHaveLength(2);

    const internalRow = rows.find((r) => r.dataset.visibility === 'internal')!;
    const publicRow = rows.find((r) => r.dataset.visibility === 'public')!;

    // Positive direction.
    expect(within(publicRow).getByTestId('comment-public-marker')).toHaveTextContent(
      'Visible to customer',
    );
    // Negative direction — without this a marker rendered unconditionally
    // would still pass, and the thread would claim every note went out.
    expect(within(internalRow).queryByTestId('comment-public-marker')).toBeNull();
    expect(screen.getAllByTestId('comment-public-marker')).toHaveLength(1);
  });

  it('keys the requester chip on from_requester, not on visibility', () => {
    state.comments = [
      { ...state.comments[1], id: 'c-agent-public', from_requester: false },
      {
        ...state.comments[1],
        id: 'c-customer',
        author_name: 'Cora Customer',
        author_id: null,
        from_requester: true,
      },
    ];
    renderDetail();

    // Both are public; only one came from the customer.
    expect(screen.getAllByTestId('comment-public-marker')).toHaveLength(2);
    expect(screen.getAllByTestId('comment-requester-chip')).toHaveLength(1);
    expect(screen.getByTestId('comment-requester-chip')).toHaveTextContent('Customer');
  });
});

// ---------------------------------------------------------------------------
// 4. Provenance chip: rendered iff requester_id !== null.
// ---------------------------------------------------------------------------

describe('portal provenance chip', () => {
  it('renders on a portal-originated ticket', () => {
    state.ticket = portalTicket;
    renderDetail();
    expect(screen.getByTestId('portal-origin-chip')).toHaveTextContent('Portal');
  });

  it('does not render on an internally raised ticket', () => {
    state.ticket = internalTicket;
    renderDetail();
    expect(screen.queryByTestId('portal-origin-chip')).toBeNull();
  });

  /**
   * The colour rule made executable (tokens.css: "Hue with matching text means
   * state. Hue with neutral text means provenance. Chip text is always
   * --module-chip-fg, never the module hue."). Mirrors
   * shell/__tests__/ModuleChip.test.tsx, including its negative clause.
   */
  it('carries the module hue as background only, with neutral foreground', () => {
    state.ticket = portalTicket;
    renderDetail();
    const chip = screen.getByTestId('portal-origin-chip');

    expect(chip.style.color).toBe('var(--module-chip-fg)');
    expect(chip.style.getPropertyValue('--chip-hue')).toBe('var(--module-beacon)');
    expect(chip.classList.contains('module-chip')).toBe(true);

    // Never the hue as text colour — that would read as state.
    expect(chip.style.color).not.toContain('--module-beacon');
  });
});

// ---------------------------------------------------------------------------
// 5. Regression: portal tickets rendered their identity field as "Unknown".
// ---------------------------------------------------------------------------

describe('portal ticket identity field', () => {
  /**
   * The defect: the identity field resolved `members.find(m => m.user_id ===
   * ticket.reporter_id)`. `useMembers` returns org members — `users` rows — and
   * a portal requester has no `users` row by design, so every portal ticket
   * rendered "Unknown" behind a `?` avatar. Fails before the fix, passes after.
   */
  it('does not render Unknown for a ticket raised by an external requester', () => {
    state.ticket = portalTicket;
    renderDetail();

    const field = screen.getByTestId('ticket-requester');
    expect(field).not.toHaveTextContent('Unknown');
    expect(field).toHaveTextContent('Cora Customer');
    expect(field).toHaveTextContent('cora@example.test');
    expect(screen.getByText('Requester')).toBeInTheDocument();
    expect(screen.queryByTestId('ticket-reporter')).toBeNull();
  });

  it('still resolves an org member through the member list as Reporter', () => {
    state.ticket = internalTicket;
    renderDetail();

    const field = screen.getByTestId('ticket-reporter');
    expect(field).toHaveTextContent('Ingrid Internal');
    expect(screen.getByText('Reporter')).toBeInTheDocument();
    expect(screen.queryByTestId('ticket-requester')).toBeNull();
  });
});
