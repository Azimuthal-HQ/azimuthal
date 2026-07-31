import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApprovalBlock } from './ApprovalBlock';
import type { WorkflowApproval } from '../../lib/api';

/**
 * The approval surface on a ticket or project item.
 *
 * There was no approval UI anywhere in web/src before this PR, so none of this
 * is regression cover — it is the first statement of what the surface owes the
 * two people involved. The requester must be able to learn that their item did
 * not move and why; the approver must be able to decide without being shown a
 * button they cannot use.
 */

const decideMutate = vi.fn();
let approvals: WorkflowApproval[] = [];

function approval(over: Partial<WorkflowApproval> = {}): WorkflowApproval {
  return {
    id: 'ap-1',
    transition_id: 'tr-1',
    entity_type: 'ticket',
    entity_id: 'e-1',
    space_id: 's-1',
    from_state_id: 'st-1',
    to_state_id: 'st-2',
    from_status: 'open',
    to_status: 'closed',
    requested_by: 'u-1',
    requested_by_name: 'Rae',
    requested_at: '2026-07-30T10:00:00Z',
    can_decide: false,
    ...over,
  };
}

vi.mock('../../lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../lib/api')>();
  return {
    ...actual,
    useEntityApprovals: () => ({ data: approvals, isLoading: false, error: null }),
    useDecideApproval: () => ({ mutate: decideMutate, isPending: false, error: null }),
  };
});

beforeEach(() => {
  approvals = [];
  decideMutate.mockClear();
});

function renderBlock() {
  return render(<ApprovalBlock spaceId="s-1" entityType="ticket" entityId="e-1" />);
}

describe('an item with no approvals says nothing', () => {
  it('renders nothing at all', () => {
    const { container } = renderBlock();
    // Not an empty panel and not a "no approvals" line. An item in a space
    // nobody has configured must look exactly as it did before this shipped.
    expect(container).toBeEmptyDOMElement();
  });
});

describe('a pending approval shows who asked and since when', () => {
  beforeEach(() => {
    approvals = [approval()];
  });

  it('names the requester, the move, and states that the item has not moved', () => {
    renderBlock();
    expect(screen.getByTestId('approval-pending')).toBeInTheDocument();
    expect(screen.getByTestId('approval-requester')).toHaveTextContent('Rae');
    expect(screen.getByTestId('approval-pending')).toHaveTextContent('open');
    expect(screen.getByTestId('approval-pending')).toHaveTextContent('closed');
    // The framing that matters: the gate BLOCKS, it does not move-and-revert.
    expect(screen.getByText(/has not moved/i)).toBeInTheDocument();
  });

  it('falls back to the id when the requester has been deleted', () => {
    approvals = [approval({ requested_by_name: '' })];
    renderBlock();
    // subject/requester names are resolved at read time and come back empty for
    // a deleted user. Rendering the name alone would print a blank sentence.
    expect(screen.getByTestId('approval-requester')).toHaveTextContent('u-1');
  });

  it('offers no decision buttons to somebody who is not an approver', () => {
    renderBlock(); // can_decide: false
    expect(screen.queryByTestId('approval-approve')).not.toBeInTheDocument();
    expect(screen.queryByTestId('approval-decline')).not.toBeInTheDocument();
    // But the block itself IS shown. The pending read is space-scoped by
    // design: hiding the block from non-approvers would hide it from the
    // person it affects most, the requester.
    expect(screen.getByTestId('approval-pending')).toBeInTheDocument();
  });
});

describe('a named approver can decide, both ways', () => {
  beforeEach(() => {
    approvals = [approval({ can_decide: true })];
  });

  it('approves with no reason required', () => {
    renderBlock();
    fireEvent.click(screen.getByTestId('approval-approve'));

    expect(decideMutate).toHaveBeenCalledTimes(1);
    const [payload] = decideMutate.mock.calls[0];
    expect(payload).toMatchObject({ approvalId: 'ap-1', decision: 'approved' });
    // The transition itself is the record; an approval needs no justification.
    expect(payload.reason).toBeUndefined();
  });

  it('requires a reason before a decline can be submitted', () => {
    renderBlock();
    fireEvent.click(screen.getByTestId('approval-decline'));

    const submit = screen.getByTestId('approval-decline-submit');
    expect(submit).toBeDisabled();

    // Whitespace is not a reason. The server refuses it too — this says so
    // before the round trip rather than after it.
    fireEvent.change(screen.getByTestId('approval-decline-reason'), { target: { value: '   ' } });
    expect(submit).toBeDisabled();

    fireEvent.change(screen.getByTestId('approval-decline-reason'), {
      target: { value: 'the release is frozen' },
    });
    expect(submit).toBeEnabled();

    fireEvent.click(submit);
    expect(decideMutate).toHaveBeenCalledTimes(1);
    expect(decideMutate.mock.calls[0][0]).toMatchObject({
      decision: 'declined',
      reason: 'the release is frozen',
    });
  });

  it('offers nothing when the transition has been deleted under the request', () => {
    // migration 047's ON DELETE SET NULL keeps the row so the request does not
    // vanish with the process state, but there is nothing left to traverse and
    // Decide answers 409. Buttons here would only produce that.
    approvals = [approval({ can_decide: true, transition_id: null })];
    renderBlock();

    expect(screen.getByTestId('approval-stuck')).toBeInTheDocument();
    expect(screen.queryByTestId('approval-approve')).not.toBeInTheDocument();
  });
});

describe('a decline stays visible, with its reason, after the decision', () => {
  it('shows the reason and does NOT claim the item was returned', () => {
    approvals = [
      approval({
        decided_at: '2026-07-30T12:00:00Z',
        decided_by: 'u-2',
        decided_by_name: 'Sam',
        decision: 'declined',
        reason: 'the release is frozen until Monday',
      }),
    ];
    renderBlock();

    expect(screen.getByTestId('approval-declined')).toBeInTheDocument();
    expect(screen.getByTestId('approval-decider')).toHaveTextContent('Sam');
    expect(screen.getByTestId('approval-decline-reason-text')).toHaveTextContent(
      'the release is frozen until Monday',
    );

    // "Decline returns the item to the source status" is satisfied by the item
    // never having LEFT it. Copy describing a rollback would describe something
    // that did not happen, and would contradict migration 047's header.
    expect(screen.getByTestId('approval-declined')).toHaveTextContent(/stayed in/i);
    expect(screen.queryByText(/returned to/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/rolled back/i)).not.toBeInTheDocument();
  });

  it('says nothing after an approval, because the status is the report', () => {
    approvals = [
      approval({
        decided_at: '2026-07-30T12:00:00Z',
        decided_by: 'u-2',
        decision: 'approved',
      }),
    ];
    const { container } = renderBlock();
    expect(container).toBeEmptyDOMElement();
  });

  it('prefers a live pending request over an older decision', () => {
    // Newest-first from the server. A fresh request after an earlier decline
    // must show the block that can still be acted on.
    approvals = [
      approval({ id: 'ap-2' }),
      approval({ id: 'ap-1', decided_at: '2026-07-29T12:00:00Z', decision: 'declined', reason: 'old' }),
    ];
    renderBlock();

    expect(screen.getByTestId('approval-pending')).toBeInTheDocument();
    expect(screen.queryByTestId('approval-declined')).not.toBeInTheDocument();
  });
});
