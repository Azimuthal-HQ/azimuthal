import { describe, expect, it } from 'vitest';
import { APIError } from '../../lib/api';
import {
  runStatusChange,
  statusOutcomeApplied,
  statusOutcomeMessage,
  type StatusOutcome,
} from './statusOutcome';

/**
 * The three outcomes of a status change, and the one that used to be invisible.
 *
 * Before this PR every status call site in the product treated a resolved
 * promise as success. Two of the three outcomes are not:
 *
 *   - a 422 refusal REJECTED, and both detail pages awaited with no try/catch,
 *     so it was an unhandled rejection and the user saw nothing;
 *   - a 202 pending approval RESOLVES, carrying a PendingApprovalResponse cast
 *     to whatever type the caller declared. This is the dangerous one: adding
 *     only a try/catch fixes the refusal and leaves the false success intact.
 *
 * The 202 case is what these tests exist for. Deleting the pendingApprovalOf
 * branch in runStatusChange makes "a pending approval is not an applied
 * change" fail, which is the property the whole feature rests on — an item that
 * has NOT moved must never be reported as one that has.
 */

function apiError(code: string, message: string): APIError {
  return new APIError(422, { error: { code, message, request_id: 'req-1' } });
}

describe('runStatusChange classifies what actually happened', () => {
  it('reports an ordinary success as applied', async () => {
    const outcome = await runStatusChange(async () => ({ id: 't1', status: 'in_progress' }), 'fallback');
    expect(outcome.kind).toBe('applied');
    expect(statusOutcomeApplied(outcome)).toBe(true);
    expect(statusOutcomeMessage(outcome)).toBeNull();
  });

  it('recognises the 202 pending-approval body, which is not an error', async () => {
    // This is the exact body tiergate.Pending sends. It arrives through the
    // SUCCESS path, so nothing in a try/catch would ever see it.
    const outcome = await runStatusChange(
      async () => ({
        status: 'pending_approval',
        message: 'This transition needs approval. It has been requested and the item has not moved.',
        approval_id: 'a-1',
        from_status: 'open',
        to_status: 'closed',
        requested_at: '2026-07-30T10:00:00Z',
      }),
      'fallback',
    );

    expect(outcome.kind).toBe('pending');
    expect(statusOutcomeMessage(outcome)).toContain('has not moved');
  });

  it('does not treat a pending approval as an applied change', async () => {
    // The load-bearing assertion. If this ever answers true, a board leaves the
    // card in its new column and a detail page reports a move that did not
    // happen — while the item sits exactly where it started.
    const outcome = await runStatusChange(
      async () => ({ status: 'pending_approval', approval_id: 'a-1', message: 'waiting' }),
      'fallback',
    );
    expect(statusOutcomeApplied(outcome)).toBe(false);
  });

  it('passes a guard refusal through verbatim', async () => {
    // 422 VALIDATION_ERROR is the code tiergate.Refused chooses precisely so
    // friendlyErrorMessage does not collapse the sentence. ADR-0011's case for
    // tier 1 rests on the engine explaining itself; the last step must not
    // throw that away.
    const outcome = await runStatusChange(() => {
      throw apiError('VALIDATION_ERROR', 'An assignee must be set before this transition');
    }, 'The status could not be changed.');

    expect(outcome.kind).toBe('refused');
    expect(statusOutcomeMessage(outcome)).toBe('An assignee must be set before this transition');
    expect(statusOutcomeApplied(outcome)).toBe(false);
  });

  it("falls back when the failure has no sentence worth showing", async () => {
    // A 500 or a network failure carries backend internals, not an
    // explanation. The caller's fallback says what failed in the user's terms.
    const outcome = await runStatusChange(() => {
      throw apiError('INTERNAL_ERROR', 'sql: no rows in result set');
    }, 'The status could not be changed.');

    expect(statusOutcomeMessage(outcome)).toBe('The status could not be changed.');
  });

  it('does not mistake an ordinary entity for a pending approval', async () => {
    // Guarding against the reverse false positive: a ticket that happens to
    // carry a `status` field must not be read as a 202 body. That is why
    // pendingApprovalOf also requires a string approval_id.
    const outcome = await runStatusChange(
      async () => ({ id: 't1', status: 'pending_approval', title: 'a ticket with an odd status' }),
      'fallback',
    );
    expect(outcome.kind).toBe('applied');
  });
});

describe('statusOutcomeMessage says nothing when there is nothing to say', () => {
  it('is null for idle and applied', () => {
    expect(statusOutcomeMessage({ kind: 'idle' } as StatusOutcome)).toBeNull();
    expect(statusOutcomeMessage({ kind: 'applied' } as StatusOutcome)).toBeNull();
  });
});
