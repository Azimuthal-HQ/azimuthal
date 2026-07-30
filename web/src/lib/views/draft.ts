// ---------------------------------------------------------------------------
// P4 saved views (ADR-0009, ADR-0010) — the "Save as view" draft seam
// ---------------------------------------------------------------------------
//
// A module list page already holds filter state. "Save as view" translates that
// state into a QueryDoc and hands it to the view builder at /views/new through
// router location state. This module owns that translation and nothing else:
// it performs no network access, and every entry point routes through it so
// there is exactly one place where a page's filters become a QueryDoc.
//
// The vocabulary itself is NOT defined here. `./query` is the single client
// definition of the filter document — its version, its priorities, its sort
// fields, its bounds, and the one answer to whether `kinds` and `sprint_ids`
// are permitted. This module composes those pieces; it never restates them.

import { PRIORITY_TO_API, normalizePriority } from '../../components/priority';
import {
  requiredVersion,
  QUERY_LIMITS,
  VIEW_MODULES,
  defaultSort,
  vectorOnlyFieldsAllowed,
  type QueryDoc,
  type QueryFilter,
  type ViewModule,
  type ViewPriority,
} from './query';

/**
 * A prefilled, not-yet-saved view.
 *
 * `name` is a suggestion the builder shows in an editable field — never a
 * final value. Visibility is deliberately absent: no list page can imply who a
 * view should be shared with, so the builder asks.
 */
export interface ViewDraft {
  name?: string;
  description?: string;
  query: QueryDoc;
}

// --- The /views/new seam ----------------------------------------------------

/** Where a prefilled draft is handed off. */
export const VIEW_DRAFT_ROUTE = '/views/new';

/** The router-location-state key the draft travels under. */
export const VIEW_DRAFT_STATE_KEY = 'draft';

/**
 * Reads a draft back out of `useLocation().state` on the receiving side.
 *
 * Location state is user-reachable — history entries survive a reload and can
 * be replayed — so it is parsed rather than cast: anything that is not a
 * recognisably shaped draft yields null and the builder opens empty. This
 * checks the shape only. `validateQueryDoc` in `./query` is what decides
 * whether the document is one the server will accept, and the 422 remains the
 * real authority over both.
 */
export function readViewDraft(state: unknown): ViewDraft | null {
  if (typeof state !== 'object' || state === null) return null;
  const raw = (state as Record<string, unknown>)[VIEW_DRAFT_STATE_KEY];
  if (typeof raw !== 'object' || raw === null) return null;

  const draft = raw as Record<string, unknown>;
  const query = draft.query;
  if (typeof query !== 'object' || query === null) return null;

  const filter = (query as Record<string, unknown>).filter;
  if (typeof filter !== 'object' || filter === null) return null;

  const modules = (filter as Record<string, unknown>).modules;
  if (!Array.isArray(modules) || modules.length === 0) return null;
  if (!modules.every((m) => VIEW_MODULES.includes(m as ViewModule))) return null;

  return draft as unknown as ViewDraft;
}

// --- Suggested names --------------------------------------------------------

/** "Tickets" -> "Tickets in Support", once the space name has loaded. */
function inSpace(base: string, spaceName?: string): string {
  const name = spaceName?.trim();
  const full = name ? `${base} in ${name}` : base;
  // The suggestion is ours, so clamping it is safe — it can never be the
  // reason a save is refused. The user's own text is never clamped: silently
  // shortening a search term would change what the view means.
  return [...full].slice(0, QUERY_LIMITS.name).join('');
}

// --- Per-surface translation ------------------------------------------------

export interface BeaconListFilterState {
  spaceId: string;
  /** Undefined until useSpace resolves; used only for the suggested name. */
  spaceName?: string;
  /** The page's status select: a TicketStatus, or 'all'. */
  status: string;
  /** The page's priority select: a UI PriorityKey ('critical', not 'urgent'), or 'all'. */
  priority: string;
  /** The page's free-text box. */
  text: string;
}

/**
 * Beacon ticket list -> QueryDoc.
 *
 * Two deliberate gaps, recorded here rather than papered over:
 *
 *   - The page's text box matches a ticket's TITLE OR ITS ID as a substring.
 *     `filter.text` is a single literal substring matched against the title,
 *     with no id half, so a saved view carries the text term but will not
 *     reproduce an id match. The text is kept; the id behaviour is lost.
 *   - The page has no sort control, so there is no sort state to prefill and
 *     the draft takes `defaultSort()`. That is the document's own default, not
 *     a claim about the order the list was in.
 */
export function beaconListDraft(state: BeaconListFilterState): ViewDraft {
  const filter: QueryFilter = { modules: ['beacon'] };

  if (state.spaceId) filter.space_ids = [state.spaceId];
  if (state.status && state.status !== 'all') filter.statuses = [state.status];

  if (state.priority && state.priority !== 'all') {
    // The select holds UI keys; the wire wants 'urgent' for Critical. Routing
    // through the one vocabulary keeps 'critical' off the wire by construction.
    filter.priorities = [PRIORITY_TO_API[normalizePriority(state.priority)] as ViewPriority];
  }

  const text = state.text.trim();
  if (text) filter.text = text;

  // No kinds and no sprint_ids: `vectorOnlyFieldsAllowed(['beacon'])` is false,
  // and naming either is a 422 on the whole document.
  return {
    name: inSpace('Tickets', state.spaceName),
    query: { v: requiredVersion(filter), filter, sort: defaultSort() },
  };
}

export interface VectorBacklogFilterState {
  spaceId: string;
  spaceName?: string;
  /** Selected type slugs; empty means every type, matching the page's predicate. */
  kinds: Iterable<string>;
  text: string;
}

/**
 * Vector backlog -> QueryDoc.
 *
 * Gaps, again stated rather than dropped quietly:
 *
 *   - The same text/id-substring gap as Beacon.
 *   - The backlog has no status, priority or assignee filter, so a draft from
 *     this page carries none. The builder is where those get added.
 *   - The page GROUPS by sprint and orders within a group by manual `rank`.
 *     Neither is expressible: `sprint_ids` is a filter rather than a grouping,
 *     and `rank` is not a sortable field. A saved view reproduces the
 *     membership of the list, not its shape on screen.
 */
export function vectorBacklogDraft(state: VectorBacklogFilterState): ViewDraft {
  const filter: QueryFilter = { modules: ['vector'] };

  if (state.spaceId) filter.space_ids = [state.spaceId];

  const kinds = Array.from(state.kinds);
  // Asking rather than assuming: the permission question has one answer in the
  // frontend, and this call site is not allowed a second one.
  if (kinds.length > 0 && vectorOnlyFieldsAllowed(filter.modules)) {
    filter.kinds = kinds;
  }

  const text = state.text.trim();
  if (text) filter.text = text;

  return {
    name: inSpace('Backlog', state.spaceName),
    query: { v: requiredVersion(filter), filter, sort: defaultSort() },
  };
}
