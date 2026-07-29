/**
 * The queue-order arithmetic (P4 Beacon queues).
 *
 * # Why this is a function and not three lines in a click handler
 *
 * `PUT …/queues/order` takes a PERMUTATION of the space's live queues: every
 * one exactly once, and nothing else. A body that names only the pair that
 * swapped is refused with 422 and changes nothing — which is the server being
 * careful, not the server being awkward. A partial order would leave every
 * unnamed queue at a stale position and silently interleave them, and an
 * ordering bug of that shape never gets reported, because it reads as somebody
 * else's preference.
 *
 * So the move is expressed as "here is the whole list, with one entry in a new
 * slot" rather than as a swap instruction, and the whole list is what goes on
 * the wire. Keeping that in one tested function is how the rule stays true at
 * every call site instead of at the one that was written first.
 */

/** Anything with an id — `Queue` in production, a stub in tests. */
export interface Ordered {
  id: string;
}

/** Which way a move goes. Up is towards position 0, i.e. earlier in the list. */
export type MoveDirection = 'up' | 'down';

/**
 * moveInOrder returns the COMPLETE ordered id list with `id` moved one slot in
 * `direction`.
 *
 * Returns the current order unchanged — every id, still exactly once — when the
 * move would leave the list, or when the id is not in it. It never returns a
 * shortened list: "nothing to do" is a no-op order, not a partial one, so a
 * caller that sends the result regardless still sends something the server will
 * accept.
 */
export function moveInOrder(
  queues: readonly Ordered[],
  id: string,
  direction: MoveDirection,
): string[] {
  const ids = queues.map((q) => q.id);
  const from = ids.indexOf(id);
  if (from === -1) return ids;
  const to = direction === 'up' ? from - 1 : from + 1;
  if (to < 0 || to >= ids.length) return ids;
  const next = [...ids];
  [next[from], next[to]] = [next[to], next[from]];
  return next;
}

/** Whether a move in this direction would do anything. Drives button disabling. */
export function canMove(
  queues: readonly Ordered[],
  id: string,
  direction: MoveDirection,
): boolean {
  const index = queues.findIndex((q) => q.id === id);
  if (index === -1) return false;
  return direction === 'up' ? index > 0 : index < queues.length - 1;
}
