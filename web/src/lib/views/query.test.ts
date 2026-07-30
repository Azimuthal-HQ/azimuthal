import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

import {
  ASSIGNEE_ME,
  ASSIGNEE_UNASSIGNED,
  DATE_NOW,
  QUERY_DOC_MIN_VERSION,
  QUERY_DOC_VERSION,
  QUERY_LIMITS,
  VIEW_MODULES,
  VIEW_PRIORITIES,
  VIEW_SORT_FIELDS,
  defaultSort,
  emptyQueryDoc,
  isValidAssignee,
  isValidDateBound,
  pruneVectorOnlyFields,
  requiredVersion,
  validateQueryDoc,
  vectorOnlyFieldsAllowed,
  vectorOnlyFieldsReason,
  withRequiredVersion,
  type QueryDoc,
  type QueryFilter,
  type ViewModule,
} from './query';

/**
 * The saved-view filter vocabulary, from two directions.
 *
 * 1. The MODULE-BOUND FIELDS. `kinds` and `sprint_ids` read columns that exist
 *    on project_items and do not exist on tickets, so the server refuses the
 *    whole document — 422, not an empty Beacon half — whenever either appears
 *    beside Beacon. A builder that offers those controls with Beacon selected
 *    produces a save button that always fails, and one that hides them with
 *    Vector alone selected has removed a filter for no reason. Both directions
 *    are asserted, on the predicate and on the validator that consults it.
 *
 * 2. The GO CONTRACT. Everything in query.ts is a hand-written mirror of
 *    internal/core/views/filter.go, which states that its vocabulary "will
 *    drift the moment it is duplicated". Vite cannot bundle a Go file, but a
 *    test running in Node can read one — so the drift check is a test, exactly
 *    as it is for the Codex schema. It fails in both directions: a name the
 *    server adds and the client never offers, and a name the client offers that
 *    the server would reject, are each a failure here.
 */

// ---------------------------------------------------------------------------
// 1. The module-bound fields
// ---------------------------------------------------------------------------

const BOTH: ViewModule[] = ['beacon', 'vector'];

function docWith(filter: Partial<QueryFilter> & { modules: ViewModule[] }): QueryDoc {
  return { v: QUERY_DOC_VERSION, filter: { ...filter }, sort: defaultSort() };
}

describe('vectorOnlyFieldsAllowed', () => {
  // Table-driven so the four selections are compared against one rule rather
  // than four hand-written expectations that could each drift alone.
  const CASES: { modules: ViewModule[]; allowed: boolean; why: string }[] = [
    { modules: ['vector'], allowed: true, why: 'project_items has kind and sprint_id' },
    { modules: ['beacon'], allowed: false, why: 'a ticket has neither column' },
    { modules: BOTH, allowed: false, why: 'the server rejects the whole document' },
    { modules: [], allowed: false, why: 'nothing is selected to filter yet' },
  ];

  it.each(CASES)('$modules -> $allowed ($why)', ({ modules, allowed }) => {
    expect(vectorOnlyFieldsAllowed(modules)).toBe(allowed);
  });

  it('explains itself only when the fields are unavailable', () => {
    // Guards against a reason string that is always present (which would read
    // as a permanent warning) or never present (no explanation on a disabled
    // control). The Beacon wording must name Beacon, or the person reading it
    // cannot tell which choice of theirs caused it.
    expect(vectorOnlyFieldsReason(['vector'])).toBeNull();
    expect(vectorOnlyFieldsReason(BOTH)).toMatch(/Beacon/);
    expect(vectorOnlyFieldsReason(['beacon'])).toMatch(/Beacon/);
    expect(vectorOnlyFieldsReason([])).toMatch(/Vector/);
  });

  it('never emits the fallback phrases the e2e harness fails on', () => {
    const banned = [
      'Something went wrong',
      'Failed to load',
      'could not be loaded',
      'invalid space_id',
      'invalid request body',
      'UNAUTHORIZED',
    ];
    const messages = [
      vectorOnlyFieldsReason(BOTH),
      vectorOnlyFieldsReason([]),
      validateQueryDoc(docWith({ modules: BOTH, kinds: ['bug'] })),
      validateQueryDoc(docWith({ modules: [] })),
    ];
    for (const message of messages) {
      expect(message).not.toBeNull();
      for (const phrase of banned) {
        expect(message).not.toContain(phrase);
      }
    }
  });
});

describe('validateQueryDoc and the module-bound fields', () => {
  it('refuses kinds when Beacon is in the module set', () => {
    expect(validateQueryDoc(docWith({ modules: BOTH, kinds: ['bug'] }))).toMatch(/Vector/);
    expect(validateQueryDoc(docWith({ modules: ['beacon'], kinds: ['bug'] }))).toMatch(/Vector/);
  });

  it('refuses sprint_ids when Beacon is in the module set', () => {
    const sprint = '7f3a1c22-0000-4000-8000-000000000001';
    expect(validateQueryDoc(docWith({ modules: BOTH, sprint_ids: [sprint] }))).toMatch(/Vector/);
    expect(validateQueryDoc(docWith({ modules: ['beacon'], sprint_ids: [sprint] }))).toMatch(/Vector/);
  });

  // The other direction. Without these the rule could be "always refuse
  // kinds", and every test above would still pass.
  it('accepts the identical fields when Vector is the only module', () => {
    const sprint = '7f3a1c22-0000-4000-8000-000000000001';
    expect(validateQueryDoc(docWith({ modules: ['vector'], kinds: ['bug'] }))).toBeNull();
    expect(validateQueryDoc(docWith({ modules: ['vector'], sprint_ids: [sprint] }))).toBeNull();
    expect(
      validateQueryDoc(docWith({ modules: ['vector'], kinds: ['bug', 'task'], sprint_ids: [sprint] })),
    ).toBeNull();
  });

  it('accepts Beacon beside EMPTY module-bound arrays', () => {
    // The server refuses only a non-empty one. A builder that clears its type
    // chips leaves `kinds: []` behind, and blocking Save on that would be a
    // dead end with nothing to undo.
    expect(validateQueryDoc(docWith({ modules: BOTH, kinds: [], sprint_ids: [] }))).toBeNull();
  });

  it('accepts a bare module selection with no other filter', () => {
    expect(validateQueryDoc(emptyQueryDoc())).toBeNull();
    expect(validateQueryDoc(emptyQueryDoc(['beacon']))).toBeNull();
  });
});

describe('pruneVectorOnlyFields', () => {
  it('drops both fields when the selection does not permit them', () => {
    const before: QueryFilter = {
      modules: BOTH,
      kinds: ['bug'],
      sprint_ids: ['7f3a1c22-0000-4000-8000-000000000001'],
      text: 'kept',
    };
    const after = pruneVectorOnlyFields(before);
    expect(after).not.toBe(before); // pure: the caller's object is untouched
    expect(before.kinds).toEqual(['bug']);
    expect('kinds' in after).toBe(false);
    expect('sprint_ids' in after).toBe(false);
    expect(after.text).toBe('kept');
    expect(validateQueryDoc({ v: QUERY_DOC_VERSION, filter: after, sort: defaultSort() })).toBeNull();
  });

  it('returns the filter untouched when the selection does permit them', () => {
    // Identity, not deep equality: a new object every call would re-render a
    // builder on every keystroke. Also the negative twin of the case above —
    // a prune that always dropped would fail here.
    const keep: QueryFilter = { modules: ['vector'], kinds: ['bug'] };
    expect(pruneVectorOnlyFields(keep)).toBe(keep);

    const nothingToDrop: QueryFilter = { modules: ['beacon'], text: 'x' };
    expect(pruneVectorOnlyFields(nothingToDrop)).toBe(nothingToDrop);
  });
});

// ---------------------------------------------------------------------------
// The rest of the vocabulary, each with the case that would pass if the check
// were deleted
// ---------------------------------------------------------------------------

describe('validateQueryDoc', () => {
  it('requires a module', () => {
    expect(validateQueryDoc(docWith({ modules: [] }))).toMatch(/at least one module/i);
  });

  it('refuses a module the server cannot search, and a repeated one', () => {
    // Codex is the interesting rejection: it is a real module everywhere else
    // in the product, so a picker built from MODULE_KEYS would offer it.
    expect(validateQueryDoc(docWith({ modules: ['codex' as ViewModule] }))).toMatch(/codex/);
    expect(validateQueryDoc(docWith({ modules: ['vector', 'vector'] }))).toMatch(/twice/);
  });

  it('refuses a priority outside the four, and accepts each of them', () => {
    // 'critical' is the UI's word for 'urgent'. Putting it on the wire is the
    // exact mistake components/priority.tsx exists to prevent, so the client
    // must catch it rather than send it.
    expect(validateQueryDoc(docWith({ modules: ['vector'], priorities: ['critical' as never] })))
      .toMatch(/urgent/);
    for (const p of VIEW_PRIORITIES) {
      expect(validateQueryDoc(docWith({ modules: ['vector'], priorities: [p] }))).toBeNull();
    }
  });

  it('accepts the two viewer-relative assignee tokens and a uuid, and nothing else', () => {
    const uuid = '3b2f9a10-1111-4111-8111-111111111111';
    expect(validateQueryDoc(docWith({ modules: BOTH, assignees: [ASSIGNEE_ME] }))).toBeNull();
    expect(validateQueryDoc(docWith({ modules: BOTH, assignees: [ASSIGNEE_UNASSIGNED] }))).toBeNull();
    expect(validateQueryDoc(docWith({ modules: BOTH, assignees: [uuid, ASSIGNEE_ME] }))).toBeNull();
    // An email is what a picker would produce if somebody wired the label
    // instead of the id.
    expect(validateQueryDoc(docWith({ modules: BOTH, assignees: ['nobody@example.com'] })))
      .toMatch(/assignee/i);
    expect(isValidAssignee(ASSIGNEE_ME)).toBe(true);
    expect(isValidAssignee('not-a-uuid')).toBe(false);
  });

  it('refuses a blank status or a blank type', () => {
    expect(validateQueryDoc(docWith({ modules: BOTH, statuses: ['  '] }))).toMatch(/status/i);
    expect(validateQueryDoc(docWith({ modules: ['vector'], kinds: [''] }))).toMatch(/type/i);
  });

  it('counts the text bound in code points, as the server counts runes', () => {
    // 'a'.repeat(200) and an emoji do not have the same .length in JavaScript,
    // and the one that differs is the one a user hits. A UTF-16 count would
    // pass the first of these and fail the second.
    const limit = QUERY_LIMITS.text;
    expect(validateQueryDoc(docWith({ modules: BOTH, text: 'a'.repeat(limit) }))).toBeNull();
    expect(validateQueryDoc(docWith({ modules: BOTH, text: 'a'.repeat(limit + 1) }))).toMatch(/characters/);
    // 200 astral code points = 400 UTF-16 units. The server accepts it.
    expect(validateQueryDoc(docWith({ modules: BOTH, text: '𐐷'.repeat(limit) }))).toBeNull();
  });

  it('enforces the collection bounds', () => {
    const many = (n: number) => Array.from({ length: n }, (_, i) => `s${i}`);
    expect(validateQueryDoc(docWith({ modules: BOTH, statuses: many(QUERY_LIMITS.statuses) }))).toBeNull();
    expect(validateQueryDoc(docWith({ modules: BOTH, statuses: many(QUERY_LIMITS.statuses + 1) })))
      .toMatch(/At most/);
  });

  it('refuses an unsortable field and a nonsense direction', () => {
    // Status is the one worth naming: it is the column every list surface
    // shows, and it is deliberately not sortable.
    expect(validateQueryDoc({ ...emptyQueryDoc(), sort: { field: 'status' as never, dir: 'asc' } }))
      .toMatch(/sort/i);
    expect(validateQueryDoc({ ...emptyQueryDoc(), sort: { field: 'title', dir: 'up' as never } }))
      .toMatch(/ascending/i);
    for (const field of VIEW_SORT_FIELDS) {
      expect(validateQueryDoc({ ...emptyQueryDoc(), sort: { field, dir: 'asc' } })).toBeNull();
    }
  });

  it('refuses a document from another build', () => {
    expect(validateQueryDoc({ ...emptyQueryDoc(), v: QUERY_DOC_VERSION + 1 })).toMatch(/version/i);
    expect(validateQueryDoc({ ...emptyQueryDoc(), v: 0 })).toMatch(/version/i);
  });
});

// ---------------------------------------------------------------------------
// 2. The Go contract
// ---------------------------------------------------------------------------

const filterGoPath = resolve(
  dirname(fileURLToPath(import.meta.url)),
  '../../../../internal/core/views/filter.go',
);

const filterGo = readFileSync(filterGoPath, 'utf8');

/** The quoted keys of a `map[string]struct{}{…}` literal in filter.go. */
function goStringSet(varName: string): string[] {
  const block = new RegExp(`var ${varName} = map\\[string\\]struct\\{\\}\\{([\\s\\S]*?)\\n\\}`).exec(
    filterGo,
  );
  // If the literal is ever reshaped, this fails loudly rather than comparing
  // against an empty set and passing — a guard that cannot fail is worse than
  // no guard.
  expect(block, `could not find "var ${varName} = map[string]struct{}{" in filter.go`).not.toBeNull();
  return [...block![1].matchAll(/"([^"]+)"/g)].map((m) => m[1]).sort();
}

function goConstString(name: string): string {
  const m = new RegExp(`^const ${name} = "([^"]+)"`, 'm').exec(filterGo);
  expect(m, `could not find "const ${name}" in filter.go`).not.toBeNull();
  return m![1];
}

describe('the filter vocabulary matches internal/core/views/filter.go', () => {
  it('names the same two modules', () => {
    const goModules = [...filterGo.matchAll(/^\s*Module(\w+)\s+Module = "([^"]+)"/gm)].map((m) => m[2]);
    expect(goModules.length).toBeGreaterThan(0);
    expect([...goModules].sort()).toEqual([...VIEW_MODULES].sort());
  });

  it('names the same four priorities', () => {
    expect(goStringSet('validPriorities')).toEqual([...VIEW_PRIORITIES].sort());
  });

  it('names the same six sort fields', () => {
    expect(goStringSet('validSortFields')).toEqual([...VIEW_SORT_FIELDS].sort());
  });

  it('spells the two viewer-relative assignee tokens the same way', () => {
    expect(goConstString('AssigneeMe')).toBe(ASSIGNEE_ME);
    expect(goConstString('AssigneeUnassigned')).toBe(ASSIGNEE_UNASSIGNED);
  });

  it('reads the same document version RANGE', () => {
    const m = /^const Version = (\d+)/m.exec(filterGo);
    expect(m, 'could not find "const Version" in filter.go').not.toBeNull();
    expect(Number(m![1])).toBe(QUERY_DOC_VERSION);

    // Read support is a range on both sides. A client that tracked only the
    // newest version would refuse to open every stored v1 view, which the
    // server never rewrites and will go on serving indefinitely.
    const min = /^const MinVersion = (\d+)/m.exec(filterGo);
    expect(min, 'could not find "const MinVersion" in filter.go').not.toBeNull();
    expect(Number(min![1])).toBe(QUERY_DOC_MIN_VERSION);
  });

  it('spells the relative-date grammar the same way', () => {
    // The grammar itself is duplicated — a regexp cannot cross the Go/TS
    // boundary — so the SOURCE of the Go regexp is compared against this
    // module's. The month unit being `mo` rather than `m` is the part that
    // matters: JQL spells minutes `m`, so a drift back to `m` would make the
    // importer mistranslate by three orders of magnitude.
    const m = /^var relativeToken = regexp\.MustCompile\(`([^`]+)`\)/m.exec(filterGo);
    expect(m, 'could not find "var relativeToken" in filter.go').not.toBeNull();
    expect(m![1]).toBe('^([+-])([0-9]{1,3})(d|w|mo)$');

    const now = /^const DateNow = "([^"]+)"/m.exec(filterGo);
    expect(now, 'could not find "const DateNow" in filter.go').not.toBeNull();
    expect(now![1]).toBe(DATE_NOW);
  });

  it('carries the same bounds, with no bound on either side the other lacks', () => {
    const GO_LIMIT_TO_KEY: Record<string, keyof typeof QUERY_LIMITS> = {
      MaxSpaceIDs: 'space_ids',
      MaxStatuses: 'statuses',
      MaxAssignees: 'assignees',
      MaxKinds: 'kinds',
      MaxSprintIDs: 'sprint_ids',
      MaxTextLen: 'text',
      MaxNameLen: 'name',
      MaxDescLen: 'description',
      MaxRelativeUnits: 'relative_units',
    };
    const goLimits = Object.fromEntries(
      [...filterGo.matchAll(/^\s*(Max\w+)\s*=\s*(\d+)$/gm)].map((m) => [m[1], Number(m[2])]),
    );
    expect(Object.keys(goLimits).length).toBeGreaterThan(0);

    // A bound added in Go must be taught to the client, and a bound the client
    // invents must exist in Go. Both halves fail here, not silently.
    expect(Object.keys(goLimits).sort()).toEqual(Object.keys(GO_LIMIT_TO_KEY).sort());
    expect(Object.values(GO_LIMIT_TO_KEY).sort()).toEqual(Object.keys(QUERY_LIMITS).sort());

    for (const [goName, key] of Object.entries(GO_LIMIT_TO_KEY)) {
      expect(QUERY_LIMITS[key], `${goName} disagrees with QUERY_LIMITS.${key}`).toBe(goLimits[goName]);
    }
  });

  it('still refuses the module-bound fields beside Beacon', () => {
    // The client rule is only correct while the server rule exists. If these
    // rejections were ever removed server-side, vectorOnlyFieldsAllowed would
    // be hiding two working filters and this test says so.
    expect(filterGo).toContain('Beacon tickets have no type');
    expect(filterGo).toContain('Beacon tickets have no sprint');
  });
});

// ---------------------------------------------------------------------------
// 3. v2 — dates and negation
// ---------------------------------------------------------------------------

describe('v2 date bounds', () => {
  it('accepts both spellings and the now token', () => {
    for (const ok of ['-7d', '+2w', '-1mo', '+999d', DATE_NOW, '2026-01-31T00:00:00Z', '2026-01-31T12:30:00+01:00']) {
      expect(isValidDateBound(ok), ok).toBe(true);
    }
  });

  it('refuses everything outside the closed grammar', () => {
    for (const bad of [
      '7d', // the sign is required — a bound must say which way it points
      '-7', // no unit
      '-7x', // not a unit
      '-1m', // MINUTES in JQL; the month unit here is "mo", and the collision is the reason
      '-2h', // finer than a day
      '-0d', // zero units is not a period
      '-1000d', // past MaxRelativeUnits
      '2026-01-31', // a date is not an instant; RFC3339 requires the time and offset
      '2026', // Date.parse would accept this; the server would not
      'yesterday',
      '',
    ]) {
      expect(isValidDateBound(bad), bad).toBe(false);
    }
  });

  it('refuses an empty range and an inverted absolute one', () => {
    const doc = (filter: Partial<QueryFilter>): QueryDoc => ({
      v: 2,
      filter: { modules: ['beacon'], ...filter },
      sort: defaultSort(),
    });
    expect(validateQueryDoc(doc({ updated_at: {} }))).toMatch(/neither a start nor an end/);
    expect(
      validateQueryDoc(
        doc({ created_at: { after: '2026-02-01T00:00:00Z', before: '2026-01-01T00:00:00Z' } }),
      ),
    ).toMatch(/starts at or after it ends/);
    // Two RELATIVE bounds are ordered on the same fixed number line the server
    // uses, so this half must be caught too — the client mirrors the server's
    // rule rather than a weaker version of it.
    expect(validateQueryDoc(doc({ due_at: { after: '-1d', before: '-7d' } }))).toMatch(
      /starts at or after it ends/,
    );
    expect(validateQueryDoc(doc({ due_at: { after: '+7d', before: 'now' } }))).toMatch(
      /starts at or after it ends/,
    );
    expect(validateQueryDoc(doc({ due_at: { after: '-1mo', before: '-1d' } }))).toBeNull();
    expect(validateQueryDoc(doc({ due_at: { after: 'now', before: '+7d' } }))).toBeNull();

    // A mixed absolute/relative pair cannot be ordered without knowing when the
    // query runs, so it is accepted here exactly as the server accepts it.
    expect(
      validateQueryDoc(doc({ created_at: { after: '-7d', before: '2026-01-01T00:00:00Z' } })),
    ).toBeNull();
  });
});

describe('v2 negation', () => {
  const doc = (filter: Partial<QueryFilter>, v = 2): QueryDoc => ({
    v,
    filter: { modules: ['beacon'], ...filter },
    sort: defaultSort(),
  });

  it('refuses a flag with nothing to negate', () => {
    expect(validateQueryDoc(doc({ not: { statuses: true } }))).toMatch(/names none/);
    // With values beside it, the same flag is fine.
    expect(validateQueryDoc(doc({ statuses: ['closed'], not: { statuses: true } }))).toBeNull();
  });

  it('is off when the flag is present but false', () => {
    expect(validateQueryDoc(doc({ not: { statuses: false } }))).toBeNull();
  });
});

describe('the document declares the version it needs', () => {
  const filter = (extra: Partial<QueryFilter>): QueryFilter => ({ modules: ['beacon'], ...extra });

  it('stays at v1 for anything v1 could express', () => {
    expect(requiredVersion(filter({}))).toBe(QUERY_DOC_MIN_VERSION);
    expect(requiredVersion(filter({ statuses: ['open'], text: 'x' }))).toBe(QUERY_DOC_MIN_VERSION);
    expect(emptyQueryDoc().v).toBe(QUERY_DOC_MIN_VERSION);
    // A negation record present but entirely false is not a v2 capability.
    expect(requiredVersion(filter({ not: { statuses: false } }))).toBe(QUERY_DOC_MIN_VERSION);
  });

  it('rises to v2 for a date range or a live negation', () => {
    expect(requiredVersion(filter({ updated_at: { after: '-7d' } }))).toBe(2);
    expect(requiredVersion(filter({ statuses: ['x'], not: { statuses: true } }))).toBe(2);
  });

  it('refuses a v2 capability inside a document declaring v1', () => {
    const d: QueryDoc = {
      v: 1,
      filter: filter({ updated_at: { after: '-7d' } }),
      sort: defaultSort(),
    };
    expect(validateQueryDoc(d)).toMatch(/need document version 2/);
    // withRequiredVersion is the fix, and it lowers as well as raises.
    expect(validateQueryDoc(withRequiredVersion(d))).toBeNull();
    expect(withRequiredVersion({ ...d, v: 2, filter: filter({}) }).v).toBe(QUERY_DOC_MIN_VERSION);
  });

  it('still opens a stored v1 document', () => {
    // The regression this guards: bumping the client to a strict v2 would make
    // every stored v1 view unopenable in the builder, with no path forward.
    const stored: QueryDoc = { v: 1, filter: filter({ statuses: ['open'] }), sort: defaultSort() };
    expect(validateQueryDoc(stored)).toBeNull();
  });

  it('still refuses a version outside the range', () => {
    for (const v of [0, 3, -1]) {
      const d: QueryDoc = { v, filter: filter({}), sort: defaultSort() };
      expect(validateQueryDoc(d), `v=${v}`).toMatch(/different version of Azimuthal/);
    }
  });
});
