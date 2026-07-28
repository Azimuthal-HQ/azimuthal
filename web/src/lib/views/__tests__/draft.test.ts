import { describe, expect, it } from 'vitest';
import {
  VIEW_DRAFT_STATE_KEY,
  beaconListDraft,
  readViewDraft,
  vectorBacklogDraft,
} from '../draft';
import { QUERY_LIMITS, defaultSort, validateQueryDoc } from '../query';

// P4: the filter -> QueryDoc translation behind "Save as view". The server
// validates a QueryDoc strictly (unknown field -> 422, kinds/sprint_ids
// alongside beacon -> 422), so these assertions are about what the document
// does NOT contain as much as what it does.

describe('beaconListDraft', () => {
  it('carries space, status, priority and text, with Critical on the wire as urgent', () => {
    const draft = beaconListDraft({
      spaceId: 'space-1',
      spaceName: 'Support',
      status: 'in_progress',
      priority: 'critical',
      text: 'outage',
    });

    expect(draft.query).toEqual({
      v: 1,
      filter: {
        modules: ['beacon'],
        space_ids: ['space-1'],
        statuses: ['in_progress'],
        priorities: ['urgent'],
        text: 'outage',
      },
      sort: { field: 'updated_at', dir: 'desc' },
    });
    expect(draft.name).toBe('Tickets in Support');
  });

  it("omits statuses and priorities entirely when the selects are on 'all'", () => {
    const draft = beaconListDraft({
      spaceId: 'space-1',
      spaceName: 'Support',
      status: 'all',
      priority: 'all',
      text: '',
    });

    // Absent, not empty: [] would read server-side as "match nothing", and the
    // literal 'all' is not a status. Deleting either guard fails this.
    expect(draft.query.filter).toEqual({ modules: ['beacon'], space_ids: ['space-1'] });
    expect('statuses' in draft.query.filter).toBe(false);
    expect('priorities' in draft.query.filter).toBe(false);
    expect('text' in draft.query.filter).toBe(false);
  });

  it('trims text and drops a whitespace-only search', () => {
    expect(beaconListDraft({
      spaceId: 's', status: 'all', priority: 'all', text: '  disk full  ',
    }).query.filter.text).toBe('disk full');

    expect(beaconListDraft({
      spaceId: 's', status: 'all', priority: 'all', text: '   ',
    }).query.filter.text).toBeUndefined();
  });

  // Drift guard. `kinds` and `sprint_ids` are 422 whenever beacon is in
  // modules, and the failure is a save that the user cannot complete. This
  // fails the moment either field is added to the beacon translator.
  it('never emits the vector-only fields', () => {
    const filter = beaconListDraft({
      spaceId: 's', spaceName: 'Support', status: 'open', priority: 'high', text: 'x',
    }).query.filter;

    expect(filter.modules).toEqual(['beacon']);
    expect(filter.kinds).toBeUndefined();
    expect(filter.sprint_ids).toBeUndefined();
  });

  it('falls back to a bare name before the space has loaded', () => {
    expect(beaconListDraft({
      spaceId: 's', status: 'all', priority: 'all', text: '',
    }).name).toBe('Tickets');
  });

  // The ticket list has no sort control, so there is no sort state to carry.
  // The draft takes the document's own default rather than inventing an order.
  it('takes the default sort rather than claiming one', () => {
    expect(beaconListDraft({
      spaceId: 's', status: 'all', priority: 'all', text: '',
    }).query.sort).toEqual(defaultSort());
  });

  it('clamps the suggested name but never the search term', () => {
    const draft = beaconListDraft({
      spaceId: 's',
      spaceName: 'S'.repeat(400),
      status: 'all',
      priority: 'all',
      text: 'x'.repeat(QUERY_LIMITS.text),
    });

    // The name is our suggestion, so it may be shortened to stay savable.
    expect([...(draft.name ?? '')].length).toBe(QUERY_LIMITS.name);
    // The user's own text is carried verbatim — truncating it would change
    // what the view means without saying so.
    expect(draft.query.filter.text).toHaveLength(QUERY_LIMITS.text);
  });
});

describe('vectorBacklogDraft', () => {
  it('carries the selected type slugs as kinds', () => {
    const draft = vectorBacklogDraft({
      spaceId: 'space-2',
      spaceName: 'Platform',
      kinds: new Set(['bug', 'task']),
      text: 'login',
    });

    expect(draft.query).toEqual({
      v: 1,
      filter: {
        modules: ['vector'],
        space_ids: ['space-2'],
        kinds: ['bug', 'task'],
        text: 'login',
      },
      sort: { field: 'updated_at', dir: 'desc' },
    });
    expect(draft.name).toBe('Backlog in Platform');
  });

  it('omits kinds when no type chip is selected', () => {
    const filter = vectorBacklogDraft({
      spaceId: 'space-2', kinds: new Set(), text: '',
    }).query.filter;

    // An empty selection means "every type" on the page; sending kinds: []
    // would invert that. Deleting the length guard fails this.
    expect(filter).toEqual({ modules: ['vector'], space_ids: ['space-2'] });
    expect('kinds' in filter).toBe(false);
  });

  it('does not invent statuses, priorities, assignees or sprint_ids', () => {
    // The backlog has no such filter. A draft that guessed one would silently
    // narrow the saved view past what the user was looking at.
    const filter = vectorBacklogDraft({
      spaceId: 'space-2', kinds: new Set(['bug']), text: 'x',
    }).query.filter;

    expect(filter.statuses).toBeUndefined();
    expect(filter.priorities).toBeUndefined();
    expect(filter.assignees).toBeUndefined();
    expect(filter.sprint_ids).toBeUndefined();
  });
});

describe('every prefilled draft is one the builder will accept', () => {
  // A draft that arrives at /views/new already refused is a dead end the user
  // cannot fix without understanding a document they never saw. Checking the
  // translators against the shared validator is what keeps the two in step.
  const drafts = [
    ['beacon, no filters', beaconListDraft({ spaceId: 'space-1', status: 'all', priority: 'all', text: '' })],
    ['beacon, every filter', beaconListDraft({ spaceId: 'space-1', spaceName: 'Support', status: 'closed', priority: 'critical', text: 'disk' })],
    ['vector, no filters', vectorBacklogDraft({ spaceId: 'space-2', kinds: new Set(), text: '' })],
    ['vector, every filter', vectorBacklogDraft({ spaceId: 'space-2', spaceName: 'Platform', kinds: new Set(['bug', 'epic']), text: 'login' })],
  ] as const;

  it.each(drafts)('%s', (_label, draft) => {
    expect(validateQueryDoc(draft.query)).toBeNull();
  });
});

describe('readViewDraft', () => {
  it('round-trips a draft placed in location state', () => {
    const draft = beaconListDraft({
      spaceId: 's', spaceName: 'Support', status: 'open', priority: 'low', text: '',
    });
    expect(readViewDraft({ [VIEW_DRAFT_STATE_KEY]: draft })).toEqual(draft);
  });

  it.each([
    ['null state', null],
    ['no state', undefined],
    ['a string', 'draft'],
    ['an unrelated key', { create: 'ticket' }],
    ['a draft with no query', { draft: { name: 'x' } }],
    ['a query with no filter', { draft: { query: { v: 1 } } }],
    ['an empty modules list', { draft: { query: { v: 1, filter: { modules: [] } } } }],
    ['an unknown module', { draft: { query: { v: 1, filter: { modules: ['codex'] } } } }],
  ])('rejects %s', (_label, state) => {
    // Location state survives reload and can be replayed, so it is parsed
    // rather than cast. Each of these must open an empty builder, not crash it.
    expect(readViewDraft(state)).toBeNull();
  });
});
