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

/**
 * The same hazard with a different blast radius: the customer portal.
 *
 * One QueryClient serves the whole app and outlives a portal sign-out, so a
 * key that identifies only the PORTAL and not the REQUESTER hands requester
 * A's cached request list straight to requester B when both sign in from the
 * same browser — a shared terminal, a household laptop, a support agent
 * testing their own service desk. Nothing corrects it: the data is already in
 * memory, so no request goes out and no 401 intervenes. It is not a stale
 * render, it is one customer reading another customer's support history.
 *
 * These tests fail if the requester email is dropped from either
 * requester-scoped factory.
 */
const portalKey = 'k3y0fth3p0rtal99';
const otherPortal = 'an0th3rp0rtalk3y1';
const alice = 'alice@example.test';
const bob = 'bob@example.test';
const ref = '33333333-3333-3333-3333-333333333333';

describe('portal query keys separate requesters, not just portals', () => {
  it('gives two requesters on one portal different list keys', () => {
    expect(key(queryKeys.portalRequests(portalKey, alice))).not.toBe(
      key(queryKeys.portalRequests(portalKey, bob)),
    );
  });

  it('gives two requesters different keys for the same request reference', () => {
    // The reference is a bare ticket UUID and is unguessable, but a cache hit
    // needs no guess: it needs only the same key, and without the email that
    // is what a second requester on the same machine produces.
    expect(key(queryKeys.portalRequest(portalKey, alice, ref))).not.toBe(
      key(queryKeys.portalRequest(portalKey, bob, ref)),
    );
  });

  it('separates one requester across two portals', () => {
    // A session is bound to one portal; presenting portal A's session to
    // portal B answers 404, so their caches must not be shared either.
    expect(key(queryKeys.portalRequests(portalKey, alice))).not.toBe(
      key(queryKeys.portalRequests(otherPortal, alice)),
    );
    expect(key(queryKeys.portalRequest(portalKey, alice, ref))).not.toBe(
      key(queryKeys.portalRequest(otherPortal, alice, ref)),
    );
  });

  it('keeps the three portal families pairwise disjoint', () => {
    const keys = [
      key(queryKeys.portalDescribe(portalKey)),
      key(queryKeys.portalRequests(portalKey, alice)),
      key(queryKeys.portalRequest(portalKey, alice, ref)),
    ];
    expect(new Set(keys).size).toBe(keys.length);
  });

  it('never lets a request reference collide with the list', () => {
    // The pathological case the dashboards families already taught us: a
    // reference that is literally the discriminator. 'list' is not a UUID, but
    // the discriminator is what makes that irrelevant rather than lucky.
    expect(key(queryKeys.portalRequest(portalKey, alice, 'list'))).not.toBe(
      key(queryKeys.portalRequests(portalKey, alice)),
    );
  });

  it('never collides a describe key with a requester whose email is "describe"', () => {
    // Describe carries no email because it is the portal's public face. That
    // makes its third element positionally where a requester email sits, so
    // the two families must be told apart by more than length.
    expect(key(queryKeys.portalDescribe(portalKey))).not.toBe(
      key(queryKeys.portalRequests(portalKey, 'describe')),
    );
  });

  it('collides with no existing family', () => {
    // Every other factory in the object, evaluated with the portal key in each
    // string position it accepts. A new family that reuses the 'portal' root
    // would show up here rather than as a blank page in production.
    const existing = [
      queryKeys.me(),
      queryKeys.organization(portalKey),
      queryKeys.space(portalKey),
      queryKeys.spaces(portalKey),
      queryKeys.tickets(portalKey),
      queryKeys.ticket(portalKey, ref),
      queryKeys.wikiPages(portalKey),
      queryKeys.projectItems(portalKey),
      queryKeys.sprints(portalKey),
      queryKeys.labels(portalKey),
      queryKeys.members(portalKey, portalKey),
      queryKeys.comments(portalKey, 'ticket', ref),
      queryKeys.notifications(),
      queryKeys.teams(portalKey),
      queryKeys.views(portalKey),
      queryKeys.view(portalKey, ref),
      queryKeys.queues(portalKey, portalKey),
      queryKeys.dashboards(portalKey, 'home'),
      queryKeys.dashboard(portalKey, ref),
      queryKeys.homeDashboard(portalKey),
      queryKeys.search(portalKey, alice, 0, '', false),
    ].map(key);

    for (const mine of [
      key(queryKeys.portal(portalKey)),
      key(queryKeys.portalDescribe(portalKey)),
      key(queryKeys.portalRequests(portalKey, alice)),
      key(queryKeys.portalRequest(portalKey, alice, ref)),
    ]) {
      expect(existing).not.toContain(mine);
    }
  });

  it('nests every portal key under the prefix sign-out invalidates', () => {
    // usePortalSignOut and useRedeemPortalLink remove queryKeys.portal(key).
    // If a portal family ever stopped nesting under it, the removal would
    // silently stop covering it and a previous requester's data would survive
    // in memory.
    const prefix = queryKeys.portal(portalKey);
    for (const k of [
      queryKeys.portalDescribe(portalKey),
      queryKeys.portalRequests(portalKey, alice),
      queryKeys.portalRequest(portalKey, alice, ref),
    ]) {
      expect(k.slice(0, prefix.length)).toEqual([...prefix]);
    }
    // And that the prefix does NOT cover another portal.
    const other = queryKeys.portalRequests(otherPortal, alice);
    expect(other.slice(0, prefix.length)).not.toEqual([...prefix]);
  });
});
