/**
 * The saved-view filter vocabulary — the client's half of the document defined
 * by `internal/core/views/filter.go`.
 *
 * That file says it is "the single place the filter vocabulary is defined" and
 * that its consumers "will drift the moment it is duplicated". This module is a
 * duplicate, unavoidably: a builder cannot offer a sort field or a priority it
 * does not know the name of, and Vite cannot bundle a Go file. So the drift is
 * made a test rather than a hope — `query.test.ts` reads `filter.go` and fails
 * in both directions on the modules, the priorities, the sort fields, the
 * assignee tokens, the version and every bound.
 *
 * # The server is the authority, always
 *
 * Nothing here replaces the 422. The server validates strictly (unknown fields
 * included, which a TypeScript type cannot express at runtime) and its
 * rejection messages are written for people and pass through
 * `friendlyErrorMessage` verbatim. `validateQueryDoc` exists to disable a
 * control and explain why *before* a round trip — never to decide that a
 * document is acceptable. When the two disagree, the server is right.
 *
 * # The one rule that is not a nicety
 *
 * `kinds` and `sprint_ids` read columns that exist on `project_items` and do
 * not exist on `tickets`. Naming either alongside Beacon is a 422, not an empty
 * result — see `vectorOnlyFieldsAllowed`, which is the single answer to that
 * question for the whole frontend.
 */

/** The only filter-document version this build reads or writes. */
export const QUERY_DOC_VERSION = 1;

/** The two modules a saved view can search. Codex is deliberately absent. */
export type ViewModule = 'beacon' | 'vector';

/** Every module, in the order a picker should offer them. */
export const VIEW_MODULES: readonly ViewModule[] = ['beacon', 'vector'];

/**
 * Priorities as the WIRE spells them. The UI vocabulary is different — it says
 * "critical" where the wire says "urgent" — and the translation lives in
 * `components/priority.tsx` (`PRIORITY_TO_API` / `normalizePriority`). Do not
 * add a second mapping here.
 */
export type ViewPriority = 'urgent' | 'high' | 'medium' | 'low';

/** Every priority, most severe first. */
export const VIEW_PRIORITIES: readonly ViewPriority[] = ['urgent', 'high', 'medium', 'low'];

/**
 * The sortable fields. Status is absent on purpose: it is free text with no
 * total order, so sorting by it would order alphabetically and read as a bug.
 */
export type ViewSortField =
  | 'updated_at'
  | 'created_at'
  | 'due_at'
  | 'resolved_at'
  | 'priority'
  | 'title';

/** Every sortable field. */
export const VIEW_SORT_FIELDS: readonly ViewSortField[] = [
  'updated_at',
  'created_at',
  'due_at',
  'resolved_at',
  'priority',
  'title',
];

/** A single sort direction. The sort is one field, never a list. */
export type ViewSortDir = 'asc' | 'desc';

/**
 * The viewer-relative assignee token. It is stored verbatim and resolved to the
 * calling user at query time, which is what lets one shared view mean "assigned
 * to me" for each of its viewers independently.
 *
 * A builder must offer this as its own choice, labelled for the current user,
 * and must NEVER substitute the current user's id — doing so would freeze the
 * view to one person and quietly change what everybody else sees.
 */
export const ASSIGNEE_ME = 'me';

/** Matches rows with no assignee. */
export const ASSIGNEE_UNASSIGNED = 'unassigned';

/**
 * Bounds on a stored filter. They are not security boundaries — every value is
 * a bound parameter server-side — they stop one saved view from becoming an
 * unbounded query against the person who saved it.
 */
export const QUERY_LIMITS = {
  space_ids: 50,
  statuses: 50,
  assignees: 50,
  kinds: 50,
  sprint_ids: 50,
  /** Counted in code points, as the server counts runes. */
  text: 200,
  name: 120,
  description: 500,
} as const;

/**
 * The closed set of things a saved view can ask for.
 *
 * Fields are AND-ed with each other; the values within one field are OR-ed. An
 * empty or absent field is not a filter at all — it never means "match none".
 * There is no `op`, no nesting, and no way to name a column: this is a record,
 * not a query language.
 */
export interface QueryFilter {
  /** Required, non-empty. Naming both modules is how a view crosses modules. */
  modules: ViewModule[];
  /** Empty means every space the viewer can read — the cross-container default. */
  space_ids?: string[];
  /** Free text: workflow states are user-defined per space. */
  statuses?: string[];
  priorities?: ViewPriority[];
  /** Any mix of `ASSIGNEE_ME`, `ASSIGNEE_UNASSIGNED` and user ids. */
  assignees?: string[];
  /** VECTOR ONLY — see `vectorOnlyFieldsAllowed`. */
  kinds?: string[];
  /** VECTOR ONLY — see `vectorOnlyFieldsAllowed`. */
  sprint_ids?: string[];
  /** A literal substring matched against the title, not a pattern. */
  text?: string;
}

/** One ordering. Singular on purpose — the results cursor encodes one key. */
export interface QuerySort {
  field: ViewSortField;
  dir: ViewSortDir;
}

/** The stored document. */
export interface QueryDoc {
  /**
   * Declared as `number`, not the literal 1, so a client can RECOGNISE a
   * document from a newer build rather than mistype it as one it understands.
   */
  v: number;
  filter: QueryFilter;
  sort: QuerySort;
}

/** What a view gets when it does not say: most recently touched first. */
export function defaultSort(): QuerySort {
  return { field: 'updated_at', dir: 'desc' };
}

/** A valid, maximally broad document over the given modules. */
export function emptyQueryDoc(modules: readonly ViewModule[] = VIEW_MODULES): QueryDoc {
  return { v: QUERY_DOC_VERSION, filter: { modules: [...modules] }, sort: defaultSort() };
}

/** Whether a module selection reads the given module. */
export function hasModule(modules: readonly ViewModule[], m: ViewModule): boolean {
  return modules.includes(m);
}

/**
 * vectorOnlyFieldsAllowed reports whether `kinds` and `sprint_ids` may appear
 * in a filter with this module selection. It is the ONE answer to that question
 * in the frontend; a second copy of the rule is a defect.
 *
 * True only when Vector is selected and Beacon is not.
 *
 *   ['vector']            -> true
 *   ['beacon']            -> false   (a ticket has no type and no sprint)
 *   ['beacon','vector']   -> false   (the server 422s the whole document)
 *   []                    -> false   (nothing to filter yet)
 *
 * The both-modules case is the one worth stating plainly: the server does not
 * apply the field to the Vector half and ignore it for the Beacon half. It
 * rejects the document, because a view that returns an empty Beacon half
 * forever is a defect its author cannot see.
 */
export function vectorOnlyFieldsAllowed(modules: readonly ViewModule[]): boolean {
  return modules.includes('vector') && !modules.includes('beacon');
}

/**
 * Why the type and sprint filters are unavailable, in words fit for a hint
 * beside a disabled control. Returns null when they ARE available.
 */
export function vectorOnlyFieldsReason(modules: readonly ViewModule[]): string | null {
  if (vectorOnlyFieldsAllowed(modules)) return null;
  if (modules.includes('beacon')) {
    return 'Type and sprint filters apply to Vector items only — a Beacon ticket has neither, so a view that includes Beacon cannot use them.';
  }
  return 'Type and sprint filters apply to Vector items only. Include Vector to use them.';
}

/**
 * pruneVectorOnlyFields drops `kinds` and `sprint_ids` when the module
 * selection does not permit them, and returns the filter UNCHANGED (by
 * identity) when it does or when there is nothing to drop.
 *
 * Call it from the module toggle, where a person can see their type selection
 * clear as a consequence of what they just did. Do not call it on the way to
 * the wire: silently removing a filter the user set, at save time, is how a
 * saved view comes to mean something other than what its author read on screen.
 */
export function pruneVectorOnlyFields(filter: QueryFilter): QueryFilter {
  if (vectorOnlyFieldsAllowed(filter.modules)) return filter;
  if (filter.kinds === undefined && filter.sprint_ids === undefined) return filter;
  const next = { ...filter };
  delete next.kinds;
  delete next.sprint_ids;
  return next;
}

/** Whether a string is one of the two viewer-relative assignee tokens. */
export function isAssigneeToken(value: string): boolean {
  return value === ASSIGNEE_ME || value === ASSIGNEE_UNASSIGNED;
}

/**
 * Canonical dashed UUID only — deliberately narrower than Go's `uuid.Parse`,
 * which also accepts URN and undashed forms. Every id the builder can produce
 * comes from `PersonTeamPicker` or an API row, and both are canonical.
 */
const UUID_RE = /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/;

/** Whether an assignee entry is one the server will accept. */
export function isValidAssignee(value: string): boolean {
  return isAssigneeToken(value) || UUID_RE.test(value);
}

/** Code points, matching Go's `len([]rune(s))` rather than UTF-16 units. */
function codePoints(s: string): number {
  return [...s].length;
}

/**
 * validateQueryDoc returns the first reason this document would be refused, or
 * null if it looks acceptable.
 *
 * "Looks acceptable" is the strongest claim it makes. The server owns the real
 * decision — it also rejects unknown fields, which no runtime check here can
 * see once JSON has been parsed into a typed object. Use this to disable a Save
 * button with an explanation, never to skip handling a 422.
 */
// One linear rule per field. Splitting it into helpers would hide the
// vocabulary, which is the thing this file exists to make visible.
export function validateQueryDoc(doc: QueryDoc): string | null {
  if (doc.v !== QUERY_DOC_VERSION) {
    return `This view was written by a different version of Azimuthal (document version ${doc.v}, this build reads ${QUERY_DOC_VERSION}).`;
  }

  if (!VIEW_SORT_FIELDS.includes(doc.sort?.field)) {
    return `"${doc.sort?.field}" is not a field a view can sort by.`;
  }
  if (doc.sort.dir !== 'asc' && doc.sort.dir !== 'desc') {
    return 'Choose whether to sort ascending or descending.';
  }

  const f = doc.filter;
  if (!f || !f.modules || f.modules.length === 0) {
    return 'Choose at least one module for this view to search.';
  }
  const seen = new Set<ViewModule>();
  for (const m of f.modules) {
    if (!VIEW_MODULES.includes(m)) {
      return `"${m}" is not a module a saved view can search.`;
    }
    if (seen.has(m)) return `"${m}" is listed twice.`;
    seen.add(m);
  }

  if ((f.space_ids?.length ?? 0) > QUERY_LIMITS.space_ids) {
    return `At most ${QUERY_LIMITS.space_ids} spaces may be named (this view names ${f.space_ids!.length}).`;
  }
  if ((f.statuses?.length ?? 0) > QUERY_LIMITS.statuses) {
    return `At most ${QUERY_LIMITS.statuses} statuses may be named (this view names ${f.statuses!.length}).`;
  }
  for (const s of f.statuses ?? []) {
    if (s.trim() === '') return 'A status filter may not be blank.';
  }
  for (const p of f.priorities ?? []) {
    if (!VIEW_PRIORITIES.includes(p)) {
      return `"${p}" is not one of urgent, high, medium or low.`;
    }
  }
  if ((f.assignees?.length ?? 0) > QUERY_LIMITS.assignees) {
    return `At most ${QUERY_LIMITS.assignees} assignees may be named (this view names ${f.assignees!.length}).`;
  }
  for (const a of f.assignees ?? []) {
    if (!isValidAssignee(a)) {
      return `An assignee filter must be a person, "${ASSIGNEE_ME}", or "${ASSIGNEE_UNASSIGNED}".`;
    }
  }
  const textLen = codePoints(f.text ?? '');
  if (textLen > QUERY_LIMITS.text) {
    return `The text term may be at most ${QUERY_LIMITS.text} characters (this one is ${textLen}).`;
  }

  const usesVectorOnly = (f.kinds?.length ?? 0) > 0 || (f.sprint_ids?.length ?? 0) > 0;
  if (usesVectorOnly && !vectorOnlyFieldsAllowed(f.modules)) {
    return vectorOnlyFieldsReason(f.modules);
  }
  if ((f.kinds?.length ?? 0) > QUERY_LIMITS.kinds) {
    return `At most ${QUERY_LIMITS.kinds} types may be named (this view names ${f.kinds!.length}).`;
  }
  for (const k of f.kinds ?? []) {
    if (k.trim() === '') return 'A type filter may not be blank.';
  }
  if ((f.sprint_ids?.length ?? 0) > QUERY_LIMITS.sprint_ids) {
    return `At most ${QUERY_LIMITS.sprint_ids} sprints may be named (this view names ${f.sprint_ids!.length}).`;
  }

  return null;
}
