import { describe, expect, it } from 'vitest';
import { queryKeys } from './api';

/**
 * Query keys are a namespace, and two families that produce the same key share
 * a cache entry.
 *
 * REGRESSION. `dashboards(orgId, 'home')` — the sidebar's list of Home
 * dashboards, an ARRAY — and `homeDashboard(orgId)` — the resolved Home
 * dashboard with its gadgets, an OBJECT — once produced the identical key.
 * Whichever request resolved last won, and when it was the list, Home read
 * `.gadgets` off an array and rendered a blank page. TypeScript cannot see it:
 * both hooks are correctly typed on their own.
 *
 * This test fails if the discriminator is removed from either family.
 */

const org = '11111111-1111-1111-1111-111111111111';
const id = '22222222-2222-2222-2222-222222222222';

function key(k: readonly unknown[]): string {
  return JSON.stringify(k);
}

describe('dashboard query keys are pairwise disjoint', () => {
  it('never collides a list key with the resolved Home key', () => {
    const home = key(queryKeys.homeDashboard(org));
    const lists = ['home', 'beacon', 'vector', undefined].map((m) =>
      key(queryKeys.dashboards(org, m)),
    );
    for (const l of lists) {
      expect(l).not.toBe(home);
    }
  });

  it('never collides a list key with a single-dashboard key', () => {
    expect(key(queryKeys.dashboards(org, 'home'))).not.toBe(key(queryKeys.dashboard(org, id)));
    // The pathological case: a dashboard whose id is literally a module name.
    expect(key(queryKeys.dashboards(org, 'home'))).not.toBe(key(queryKeys.dashboard(org, 'home')));
  });

  it('never collides the resolved Home key with a single-dashboard key', () => {
    expect(key(queryKeys.homeDashboard(org))).not.toBe(key(queryKeys.dashboard(org, 'home')));
  });

  // All three still nest under one prefix, so a single invalidation after a
  // create, rename, layout save or delete drops every cached copy.
  it('keeps all three under the same invalidation prefix', () => {
    for (const k of [
      queryKeys.dashboards(org, 'home'),
      queryKeys.dashboard(org, id),
      queryKeys.homeDashboard(org),
    ]) {
      expect(k[0]).toBe('dashboards');
      expect(k[1]).toBe(org);
    }
  });

  // The same hazard one family over: a gadget's data is keyed by the document
  // it resolves, and results and aggregates of the same document must not
  // share an entry — one is a page of rows, the other a number.
  it('never collides gadget results with gadget aggregates', () => {
    const doc = '{"v":1}';
    expect(key(queryKeys.gadgetResults(org, doc, 5))).not.toBe(
      key(queryKeys.gadgetAggregate(org, doc, '')),
    );
  });
});
