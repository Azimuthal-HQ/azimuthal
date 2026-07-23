import { describe, expect, it } from 'vitest';
import { notificationRoute } from '../modules';

// S1: the bell builds a route from a notification's denormalised
// entity_kind + entity_space_id + entity_id. Legacy/degraded rows return null
// so the bell falls back to mark-read-only rather than navigating nowhere.

describe('notificationRoute', () => {
  it('routes a ticket notification into Beacon', () => {
    expect(
      notificationRoute({ entity_kind: 'ticket', entity_space_id: 'sp1', entity_id: 'tk1' }),
    ).toBe('/beacon/sp1/tickets/tk1');
  });

  it('routes a page notification into Codex', () => {
    expect(
      notificationRoute({ entity_kind: 'page', entity_space_id: 'sp2', entity_id: 'pg1' }),
    ).toBe('/codex/sp2/pages/pg1');
  });

  it('routes an item notification into Vector', () => {
    expect(
      notificationRoute({ entity_kind: 'item', entity_space_id: 'sp3', entity_id: 'it1' }),
    ).toBe('/vector/sp3/backlog/it1');
    // project_item is the wire spelling from other dispatch paths.
    expect(
      notificationRoute({ entity_kind: 'project_item', entity_space_id: 'sp3', entity_id: 'it2' }),
    ).toBe('/vector/sp3/backlog/it2');
  });

  it('returns null when the space is missing (legacy row) — degrade to mark-read-only', () => {
    expect(notificationRoute({ entity_kind: 'ticket', entity_id: 'tk1' })).toBeNull();
  });

  it('returns null for an unknown or absent entity kind', () => {
    expect(notificationRoute({ entity_kind: 'org', entity_space_id: 'sp1', entity_id: 'x' })).toBeNull();
    expect(notificationRoute({ entity_space_id: 'sp1', entity_id: 'x' })).toBeNull();
  });

  it('returns null when the entity id is missing', () => {
    expect(notificationRoute({ entity_kind: 'ticket', entity_space_id: 'sp1' })).toBeNull();
  });
});
