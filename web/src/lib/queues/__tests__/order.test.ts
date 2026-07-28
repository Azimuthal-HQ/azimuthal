import { describe, expect, it } from 'vitest';
import { canMove, moveInOrder } from '../order';

/**
 * `PUT …/queues/order` takes a PERMUTATION of the space's live queues. The
 * server refuses anything else with a 422 and changes nothing, so the only
 * thing worth asserting about this helper is the property that keeps it
 * acceptable: whatever it returns names every queue exactly once.
 */

const QUEUES = [{ id: 'a' }, { id: 'b' }, { id: 'c' }, { id: 'd' }];

/** The invariant, asserted separately so a failure names what actually broke. */
function expectPermutationOf(result: string[], source: readonly { id: string }[]) {
  expect(result).toHaveLength(source.length);
  expect(new Set(result).size).toBe(result.length);
  expect([...result].sort()).toEqual(source.map((q) => q.id).sort());
}

describe('moveInOrder', () => {
  it('moves one entry up and returns the whole order', () => {
    const result = moveInOrder(QUEUES, 'c', 'up');
    expect(result).toEqual(['a', 'c', 'b', 'd']);
    expectPermutationOf(result, QUEUES);
  });

  it('moves one entry down and returns the whole order', () => {
    const result = moveInOrder(QUEUES, 'b', 'down');
    expect(result).toEqual(['a', 'c', 'b', 'd']);
    expectPermutationOf(result, QUEUES);
  });

  it('returns the full order unchanged at the top, never a shortened list', () => {
    const result = moveInOrder(QUEUES, 'a', 'up');
    expect(result).toEqual(['a', 'b', 'c', 'd']);
    expectPermutationOf(result, QUEUES);
  });

  it('returns the full order unchanged at the bottom', () => {
    const result = moveInOrder(QUEUES, 'd', 'down');
    expect(result).toEqual(['a', 'b', 'c', 'd']);
    expectPermutationOf(result, QUEUES);
  });

  it('returns the full order when the id is not in the list', () => {
    const result = moveInOrder(QUEUES, 'zzz', 'down');
    expectPermutationOf(result, QUEUES);
  });

  it('does not mutate the list it was given', () => {
    const source = [{ id: 'a' }, { id: 'b' }];
    moveInOrder(source, 'a', 'down');
    expect(source.map((q) => q.id)).toEqual(['a', 'b']);
  });
});

describe('canMove', () => {
  it('refuses the ends and allows the middle', () => {
    expect(canMove(QUEUES, 'a', 'up')).toBe(false);
    expect(canMove(QUEUES, 'a', 'down')).toBe(true);
    expect(canMove(QUEUES, 'd', 'down')).toBe(false);
    expect(canMove(QUEUES, 'd', 'up')).toBe(true);
  });

  it('refuses an id the list does not hold', () => {
    expect(canMove(QUEUES, 'zzz', 'up')).toBe(false);
    expect(canMove(QUEUES, 'zzz', 'down')).toBe(false);
  });

  it('refuses both directions for a single-queue space', () => {
    expect(canMove([{ id: 'only' }], 'only', 'up')).toBe(false);
    expect(canMove([{ id: 'only' }], 'only', 'down')).toBe(false);
  });
});
