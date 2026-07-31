import { friendlyErrorMessage, pendingApprovalOf, type PendingApprovalResponse } from '../../lib/api';

/**
 * What actually happened when somebody tried to change an item's status.
 *
 * # Three outcomes, and only one of them was ever handled
 *
 * A gated transition can end three ways, and before this PR every status call
 * site in the product treated all three as "it worked":
 *
 *   applied  — 2xx with the updated entity.
 *   refused  — 422 VALIDATION_ERROR carrying the guard's own sentence. Both
 *              detail pages awaited `mutateAsync` with no try/catch and never
 *              read `.error`, so this was an unhandled promise rejection: the
 *              <select> kept the new value, the refetch never ran, and the user
 *              saw a state change that had not happened.
 *   pending  — 202 with a PendingApprovalResponse body. This is the dangerous
 *              one. It is NOT an error, so apiFetch resolves with it, cast to
 *              whatever type the caller declared. Adding only a try/catch would
 *              have fixed the refusal and left this reporting a move that never
 *              occurred. PR #86's contract says a blocked transition is never a
 *              silent no-op; a silent false SUCCESS is worse.
 *
 * runStatusChange is the one place that tells them apart, so no call site has
 * to remember that a 202 exists.
 */
export type StatusOutcome =
  | { kind: 'idle' }
  | { kind: 'applied' }
  | { kind: 'pending'; response: PendingApprovalResponse }
  | { kind: 'refused'; message: string };

/**
 * Runs a status mutation and classifies the result.
 *
 * The fallback is deliberately caller-supplied: the guard's own sentence
 * arrives under VALIDATION_ERROR and passes through friendlyErrorMessage
 * unchanged, but a network failure or a 500 has no sentence worth showing and
 * needs the caller to say what failed in the user's own terms.
 */
export async function runStatusChange(
  mutate: () => Promise<unknown>,
  fallback: string,
): Promise<StatusOutcome> {
  try {
    const result = await mutate();
    const pending = pendingApprovalOf(result);
    if (pending) {
      return { kind: 'pending', response: pending };
    }
    return { kind: 'applied' };
  } catch (err) {
    return { kind: 'refused', message: friendlyErrorMessage(err, fallback) };
  }
}

/**
 * The sentence to show for an outcome, or null when there is nothing to say.
 *
 * The pending message comes from the SERVER rather than being written here.
 * tiergate.Pending already sends "This transition needs approval. It has been
 * requested and the item has not moved." — which is the correct framing and the
 * one place it should live. Writing a second copy in the client is how the two
 * come to disagree about whether the item moved.
 */
export function statusOutcomeMessage(outcome: StatusOutcome): string | null {
  switch (outcome.kind) {
    case 'refused':
      return outcome.message;
    case 'pending':
      return outcome.response.message;
    default:
      return null;
  }
}

/**
 * Whether the outcome means the item actually changed.
 *
 * Callers use this to decide whether to refetch and, on a board, whether to
 * leave a card where the user dropped it. `pending` answers false: the item is
 * exactly where it was, which is the whole point of the gate.
 */
export function statusOutcomeApplied(outcome: StatusOutcome): boolean {
  return outcome.kind === 'applied';
}
