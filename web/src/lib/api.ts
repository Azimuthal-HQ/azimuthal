import {
  useQuery,
  useMutation,
  useQueryClient,
} from '@tanstack/react-query';
import type { UseQueryOptions } from '@tanstack/react-query';
import { getToken, setToken, setRefreshToken, getRefreshToken, removeToken, removeRefreshToken, getCurrentOrgId } from './auth';
import { getPortalToken } from './portalSession';
import type { CodexDoc } from './codex/schema';
import type { QueryDoc, ViewModule } from './views/query';

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

const API_BASE_URL: string =
  import.meta.env.VITE_API_BASE_URL ?? '/api/v1';

// ---------------------------------------------------------------------------
// Error types
// ---------------------------------------------------------------------------

export interface APIErrorBody {
  error: {
    code: string;
    message: string;
    request_id: string;
  };
}

export class APIError extends Error {
  code: string;
  status: number;
  requestId: string;

  constructor(status: number, body: APIErrorBody) {
    super(body.error.message);
    this.name = 'APIError';
    this.code = body.error.code;
    this.status = status;
    this.requestId = body.error.request_id;
  }
}

/**
 * friendlyErrorMessage translates an API failure into text fit for a person
 * (P2.5 W5): raw backend strings like "invalid request body" never reach
 * the UI. Messages behind VALIDATION_ERROR / CONFLICT / GONE are written
 * for humans server-side and pass through; everything else — malformed-
 * request internals, server errors, network failures — collapses to the
 * caller's fallback, which should say what failed in the user's terms.
 */
export function friendlyErrorMessage(err: unknown, fallback: string): string {
  if (err instanceof APIError && err.message) {
    // INVALID_TRANSITION joined the list with the workflow admin surface. It is
    // the state machine explaining itself — "cannot move from Closed to In
    // Progress" — and collapsing it into the caller's fallback told the user
    // only that something failed, on the one screen where the reason is the
    // whole point. ADR-0011's case for tier 1 rests on the engine being able to
    // explain a refusal; throwing the sentence away at the last step would undo
    // that. Note the tier guards themselves answer 422 VALIDATION_ERROR rather
    // than this code, deliberately, and were already covered.
    const humanCodes = ['VALIDATION_ERROR', 'CONFLICT', 'GONE', 'INVALID_TRANSITION'];
    if (humanCodes.includes(err.code)) {
      return err.message;
    }
  }
  return fallback;
}


// spaceBase builds the org+space scoped URL prefix for a space resource —
// the single scoping convention: /orgs/{orgId}/spaces/{spaceId}/...
function spaceBase(spaceId: string): string {
  return `/orgs/${getCurrentOrgId()}/spaces/${spaceId}`;
}

/**
 * ticketRefQuery builds the `?ticket_ref=…` suffix administrative mutations
 * carry. The reference travels in the query string rather than the body
 * because the mutations that most need it have no body at all — team delete,
 * space delete, member removal and person removal are all DELETE. (Bulk-apply
 * is the one exception: it keeps its shipped body field.) A blank or absent
 * reference yields '', so a call site that collects none produces exactly the
 * URL it always did. The trim mirrors the server's `ticketref.FromRequest`,
 * which trims before deciding a reference was supplied. Assumes the path it
 * is appended to carries no query string of its own — none of these do.
 */
function ticketRefQuery(ticketRef?: string): string {
  const ref = ticketRef?.trim() ?? '';
  return ref ? `?ticket_ref=${encodeURIComponent(ref)}` : '';
}

/**
 * IdWithTicketRef is the mutation variable of an administrative mutation that
 * names its target with a single id. The bare id stays valid — that is what
 * every call site passed before references existed — and the object form adds
 * the operator's reference without churning the surfaces that collect none.
 */
export type IdWithTicketRef = string | { id: string; ticketRef?: string };

/** Normalises both spellings of IdWithTicketRef into { id, ticketRef }. */
function splitIdRef(v: IdWithTicketRef): { id: string; ticketRef?: string } {
  return typeof v === 'string' ? { id: v } : v;
}

// ---------------------------------------------------------------------------
// Base fetch helper
// ---------------------------------------------------------------------------

/**
 * ClaimErrorBody lets one caller recognise a failure body that is NOT the
 * standard `{error:{…}}` envelope and throw its own typed error for it.
 *
 * Exactly one surface needs this. The Codex publish route answers 409 with a
 * bare `ConflictDetail` or `LostContentDetail` object rather than an envelope,
 * because those bodies *are* the dialogue the author reads — see
 * `internal/core/api/wiki/document_handler.go`. Without a hook here the
 * envelope parser reads `body.error.message` off an object with no `error`
 * member, throws a TypeError, and destroys the only description of what would
 * have been lost. Returning `undefined` falls through to the ordinary
 * `APIError` path, so this changes nothing for a caller that passes none.
 */
type ClaimErrorBody = (status: number, body: unknown) => Error | undefined;

/**
 * Which credential family a request carries.
 *
 * The customer portal (migration 044) authenticates an EXTERNAL requester who
 * holds no `users` row at all. Its session token is RS256-signed with the same
 * key as an internal token, so the `aud` claim is the entire boundary between
 * the two — see `internal/core/portal/token.go`. Presenting the wrong one is
 * not a soft failure: the internal parser refuses a portal audience and vice
 * versa, so a mixed-up credential 401s.
 *
 * `'none'` is for the portal's three unauthenticated routes (describe, request
 * a sign-in link, redeem one). Sending a stale internal token to those would
 * attach an org member's identity to a request made by a stranger.
 */
type Credential = 'internal' | 'portal' | 'none';

interface FetchOptions {
  credential?: Credential;
  /** Required when `credential` is `'portal'` — sessions are per portal. */
  portalKey?: string;
  /**
   * Opt this request out of the internal-401 clear-and-redirect. Only the
   * deliberate sign-out call sets it: `logout()` owns the token clear in its
   * own `finally` block (mutation-tested in auth.logout.test.ts), and a 401
   * from /auth/logout means the session is already gone — there is nothing to
   * recover, so apiFetch must not reach in, clear the tokens out from under
   * that ordering, and hard-navigate. Every other internal 401 still recovers.
   */
  skipAuthRedirect?: boolean;
}

async function apiFetch<T>(
  path: string,
  options: RequestInit = {},
  claimErrorBody?: ClaimErrorBody,
  fetchOpts: FetchOptions = {},
): Promise<T> {
  const headers = new Headers(options.headers);

  // Default to JSON, but never for FormData — the browser must set the
  // multipart Content-Type with its boundary itself.
  if (!headers.has('Content-Type') && options.body && !(options.body instanceof FormData)) {
    headers.set('Content-Type', 'application/json');
  }

  const credential = fetchOpts.credential ?? 'internal';
  const token =
    credential === 'internal'
      ? getToken()
      : credential === 'portal'
        ? getPortalToken(fetchOpts.portalKey ?? '')
        : null;
  if (token) {
    headers.set('Authorization', `Bearer ${token}`);
  }

  const response = await fetch(`${API_BASE_URL}${path}`, {
    ...options,
    headers,
  });

  if (!response.ok) {
    // A 401 on the INTERNAL credential means the middleware refused the
    // credential itself — an expired token, a stale generation, or (since B1)
    // a session another device revoked. Clear the stored tokens and send the
    // user to /login.
    //
    // The credential is the whole gate, and deliberately so: a requester whose
    // PORTAL session expired must never be sent to /login — that is the
    // internal product leaking into a surface whose entire purpose is that an
    // external customer learns nothing about it. So 'portal' and 'none' 401s
    // fall straight through to the thrown error below. Pinned by
    // api.portal-credential.test.ts.
    //
    // The scope BROADENED in B1. It used to fire only on three auth URLs
    // (/auth/login, /auth/me, /auth/refresh), which left the case the T1 review
    // named unhandled: when another device revokes this session — a
    // logout-all, or this device signed out from elsewhere — the 401 lands on
    // whatever ordinary data endpoint the app called next, none of those three,
    // so nothing cleared the dead token or redirected, and the app sat wedged.
    // Every internal 401 is the middleware refusing the credential (code
    // UNAUTHORIZED); this API has no resource-level 401 (permission denials are
    // 403/404), so clearing on any of them is correct and is exactly what
    // recovers the revoked-elsewhere device.
    if (response.status === 401 && credential === 'internal' && !fetchOpts.skipAuthRedirect) {
      removeToken();
      removeRefreshToken();
      const currentPath = window.location.pathname;
      if (currentPath !== '/login') {
        window.location.href = `/login?redirect=${encodeURIComponent(currentPath)}`;
      }
    }

    let parsed: unknown;
    let parseFailed = false;
    try {
      parsed = await response.json();
    } catch {
      parseFailed = true;
    }

    // Give the caller first refusal on a non-envelope body, before the
    // envelope parser touches it.
    if (!parseFailed && claimErrorBody) {
      const claimed = claimErrorBody(response.status, parsed);
      if (claimed) throw claimed;
    }

    const envelope = parsed as APIErrorBody | undefined;
    const body: APIErrorBody =
      !parseFailed && envelope?.error
        ? envelope
        : {
            error: {
              code: 'unknown',
              message: response.statusText || 'Request failed',
              request_id: '',
            },
          };
    throw new APIError(response.status, body);
  }

  // 204 No Content
  if (response.status === 204) {
    return undefined as T;
  }

  return (await response.json()) as T;
}

// ---------------------------------------------------------------------------
// Domain types
// ---------------------------------------------------------------------------

import type {
  ApprovalEntityType,
  ApproverSubjectType,
  Decision,
  GuardCapability,
  GuardClass,
  GuardFieldKey,
  GuardKind,
  PostFieldKey,
  PostFunctionKind,
} from './workflow/vocabulary';

// Re-exported so consumers reach one module for the whole workflow contract
// rather than importing the wire types from api.ts and the vocabulary from
// somewhere else. `isolatedModules` requires the explicit `export type` form —
// a plain re-export of a type-only import compiles under `tsc --noEmit` and
// fails under `tsc -b`, which is what the production build runs.
export type {
  ApprovalEntityType,
  ApproverSubjectType,
  Decision,
  GuardCapability,
  GuardClass,
  GuardFieldKey,
  GuardKind,
  PostFieldKey,
  PostFunctionKind,
};

export type SpaceType = 'beacon' | 'codex' | 'vector';
export type TicketStatus = 'open' | 'in_progress' | 'resolved' | 'closed';
export type SprintStatus = 'planned' | 'active' | 'completed';

export interface WorkflowState {
  id: string;
  workflow_id: string;
  name: string;
  category: 'todo' | 'in_progress' | 'done';
  color: string;
  position: number;
  is_initial: boolean;
  created_at: string;
}

export interface Workflow {
  id: string;
  org_id: string;
  name: string;
  description?: string | null;
  is_default: boolean;
  applies_to: 'tickets' | 'project_items';
  created_at: string;
  updated_at: string;
}

export interface Organization {
  id: string;
  name: string;
  slug: string;
  description: string | null;
  created_at: string;
  updated_at: string;
  /** P2.5: the caller's membership role, resolved server-side per request. */
  caller_org_role?: string;
  /** P2.5: true for org admins — drives the avatar menu's Admin entry. */
  caller_is_admin?: boolean;
}

export type SpaceVisibility = 'hidden' | 'discoverable' | 'org';

/**
 * Space is both the single-space record (GET /orgs/{o}/spaces/{s}) and a
 * row of the org space directory (GET /orgs/{o}/spaces, P2). Directory
 * rows carry the governance fields; discoverable-but-unreadable spaces
 * arrive as LOCKED rows (readable: false) with identity fields only, so
 * everything beyond the identity core is optional.
 */
export interface Space {
  id: string;
  org_id?: string;
  name: string;
  slug: string;
  key?: string;
  type: SpaceType;
  description?: string | null;
  icon?: string | null;
  is_private?: boolean;
  owner_team_id?: string;
  visibility?: SpaceVisibility;
  /** false marks a locked directory row: listed but not readable. */
  readable?: boolean;
  /** Caller's effective role on a readable directory row. */
  effective_role?: string;
  created_by?: string | null;
  created_at?: string;
  updated_at?: string;
}

// ---------------------------------------------------------------------------
// Team types (P2, spec §6)
// ---------------------------------------------------------------------------

export type TeamRole = 'member' | 'lead';

export interface Team {
  id: string;
  org_id: string;
  /** Absent/null for root teams (wire omits it when null). */
  parent_id?: string | null;
  /** Materialised ancestor chain ending in the team's own id; length = depth. */
  path: string[];
  slug: string;
  name: string;
  description: string;
  is_default: boolean;
  source: string;
  created_at: string;
  updated_at: string;
}

export interface TeamMember {
  team_id: string;
  user_id: string;
  org_id: string;
  /** Metadata only — never a permission input. */
  role: TeamRole;
  is_primary: boolean;
  created_at: string;
  email?: string;
  display_name?: string;
  avatar_url?: string | null;
}

// ---------------------------------------------------------------------------
// Grant types (P2, spec §6)
// ---------------------------------------------------------------------------

export type GrantSubjectType = 'user' | 'team';
export type GrantRole = 'viewer' | 'contributor' | 'agent' | 'space_admin';

export interface SpaceGrant {
  id: string;
  space_id: string;
  subject_type: GrantSubjectType;
  subject_id: string;
  subject_name?: string;
  subject_missing?: boolean;
  role: GrantRole;
  created_at: string;
  created_by?: string | null;
}

export interface EffectiveAccessGrant {
  grant_id: string;
  subject_type: GrantSubjectType;
  subject_id: string;
  role: GrantRole;
  team_name?: string;
  matched_team_id?: string;
  matched_team_name?: string;
  /** Tree distance from the matched direct team down to the granted team. */
  depth: number;
}

export interface EffectiveAccess {
  user_id: string;
  space_id: string;
  access: boolean;
  /** Effective role wire form, '' when no access. */
  role: string;
  org_admin: boolean;
  org_visibility: boolean;
  grants: EffectiveAccessGrant[];
}

export interface User {
  id: string;
  email: string;
  display_name: string;
  org_id: string;
  role: string;
  created_at: string;
  updated_at: string;
}

/**
 * The external customer who raised a ticket through a portal, resolved
 * server-side (`internal/core/api/tickets/requester_view.go`).
 *
 * A requester holds NO `users` row (migration 044), so none of the member
 * lookups a client runs against `reporter_id` or `assignee_id` will ever find
 * them. That is why the identity travels resolved on the ticket rather than as
 * an id to look up: there is nowhere to look it up.
 */
export interface TicketRequester {
  id: string;
  display_name: string;
  email: string;
}

export interface Ticket {
  id: string;
  space_id: string;
  number: number | null;
  title: string;
  description: string;
  status: TicketStatus;
  priority: string;
  assignee_id: string | null;
  /**
   * The internal user who raised the ticket, or null for one raised through a
   * customer portal — a requester has no `users` row to point at. Nullable
   * since migration 044; the Go field is a `*uuid.UUID` with no omitempty, so
   * the key is always present and `null` is the wire form of "nobody".
   */
  reporter_id: string | null;
  /**
   * The external requester's id, or null for a ticket raised inside the
   * product. THIS IS THE PROVENANCE PREDICATE: `requester_id !== null` is what
   * makes a ticket a customer request, and it is the only field that says so —
   * status, priority and space tell you nothing about where it came from.
   */
  requester_id: string | null;
  /**
   * The resolved requester, or null. Present on every agent-side ticket
   * response — single, list, search and kanban all serialise through
   * `respondTicket`/`respondTickets` — so its absence is never "this endpoint
   * forgot", and null always means "no external requester" or "their record is
   * gone", not "not looked up".
   */
  requester: TicketRequester | null;
  label_ids: string[];
  /**
   * The ticket's due date, or null. RFC3339 on the wire, but a calendar date in
   * meaning — render it with `formatUTCDate`, not by slicing the string.
   *
   * A workflow transition guard can require this field (`field_required:
   * due_at`), so a space whose workflow demands it needs a surface that can set
   * it. Until the due-date control on ticket detail there was none, and the
   * ticket PATCH rejected the key outright.
   */
  due_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface WikiPage {
  id: string;
  space_id: string;
  title: string;
  content: string;
  /**
   * The page's ProseMirror document, or null for a page that has only ever
   * held markdown (migration 036). This is the *stored* document, so it still
   * carries any node types the editor's schema does not define — reading it
   * through ProseMirror would drop them. Anything that renders or edits a
   * document reads {@link CodexEditableDocument.doc} from the `/document`
   * route instead, which is shielded; `doc` here answers exactly one
   * question, and it is the dual-format one: markdown page, or document page?
   */
  doc: CodexDoc | null;
  version: number;
  parent_id: string | null;
  author_id: string;
  /** Dot-separated materialized path of ancestor ids; used to compute
   *  cascade share coverage client-side (ADR-0008). */
  path: string;
  created_at: string;
  updated_at: string;
}

export interface ProjectItem {
  id: string;
  space_id: string;
  number: number | null;
  /**
   * Permanent, org-unique human-readable key (<SPACE_KEY>-<n>), assigned at
   * creation and immutable — survives a move between spaces. Prefer this over
   * deriving <spaceKey>-<number> client-side. Optional only for items fetched
   * before the field existed.
   */
  item_key?: string;
  title: string;
  description: string;
  /** task | story | bug | epic — set at creation, carried on the wire. */
  kind?: string;
  status: string;
  priority: string;
  assignee_id: string | null;
  reporter_id: string;
  sprint_id: string | null;
  rank: string;
  /**
   * The item's due date, or null. RFC3339 on the wire, but a calendar date in
   * meaning — render it with `formatUTCDate`, not by slicing the string, or a
   * non-UTC offset shows the previous day.
   *
   * Served since the field existed and declared only now: the Go field is a
   * `*time.Time` with no omitempty, so the key is always present and `null` is
   * the wire form of "no due date".
   */
  due_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface Sprint {
  id: string;
  space_id: string;
  name: string;
  goal: string;
  status: SprintStatus;
  starts_at: string | null;
  ends_at: string | null;
  /**
   * The user who created the sprint. Always present and never null: the Go
   * serializer sends it as a bare uuid.UUID with no omitempty. Its absence
   * from this interface was noted in #68.
   */
  created_by: string;
  created_at: string;
  updated_at: string;
}

// ---------------------------------------------------------------------------
// Board configuration (W4)
// ---------------------------------------------------------------------------

export interface BoardColumn {
  id: string;
  space_id: string;
  name: string;
  position: number;
  /** null means no limit. Limits are soft — never a hard block on a drop. */
  wip_limit: number | null;
  statuses: string[];
  created_at?: string;
  updated_at?: string;
}

export interface BoardConfig {
  space_id: string;
  columns: BoardColumn[];
  /**
   * false when the space has no stored configuration and these columns were
   * derived from its workflow states — i.e. the board it has always had.
   */
  customized: boolean;
}

/**
 * Who a comment is for. Mirrors migration 045's `comments_visibility_valid`.
 *
 * `'internal'` is the DEFAULT and the safe direction: an agent's note is
 * private unless they explicitly publish it, because an internal note the
 * customer never sees is a delay while a public note the agent thought was
 * private is a disclosure that cannot be recalled.
 */
export type CommentVisibility = 'internal' | 'public';

export interface Comment {
  id: string;
  entity_type: string;
  entity_id: string;
  /**
   * Null for a comment written by an external requester, who has no `users`
   * row (migration 044). `author_name` carries the name in both cases, so
   * render that rather than resolving this id — a null here is a customer,
   * not a deleted account.
   */
  author_id: string | null;
  author_name?: string;
  /**
   * Whether this is the customer's own message. Agent surface only: it exists
   * so the thread can mark a customer's turn without the client reasoning
   * about which of two author columns is populated.
   */
  from_requester: boolean;
  /**
   * Agent surface only. The portal's serialiser has no such field because the
   * portal query returns public rows exclusively and there is nothing to
   * disambiguate — see `PortalMessage`.
   */
  visibility: CommentVisibility;
  body: string;
  content?: string;
  created_at: string;
  updated_at: string;
}

export interface Member {
  user_id: string;
  org_id: string;
  display_name: string;
  email: string;
  role: string;
}

export interface Notification {
  id: string;
  kind: string;
  title: string;
  is_read: boolean;
  entity_kind?: string;
  entity_id?: string;
  entity_space_id?: string;
  created_at: string;
  /**
   * True when the caller may no longer read the entity this notification
   * named. The row survives so the unread count and mark-read still work; the
   * title, body and entity ids do not come back from the server at all.
   */
  redacted?: boolean;
}

export interface NotificationListResponse {
  notifications: Notification[];
  unread_count: number;
}

export interface WikiTreeNode {
  id: string;
  space_id: string;
  parent_id: string | null;
  title: string;
  version: number;
  position: number;
  children: WikiTreeNode[];
}

export interface WikiRevision {
  id: string;
  page_id: string;
  version: number;
  title: string;
  author_id: string;
  /**
   * The author's display name, resolved server-side.
   *
   * Null when the account has been removed. `page_revisions.author_id` has been
   * stored since migration 005, so the name was never missing from the data —
   * only from the read — and a deleted account must not make its revisions
   * vanish from a page's history. The surface renders "Unknown" rather than
   * inventing an author for an old row.
   */
  author_name: string | null;
  created_at: string;
}

/**
 * One run of text in a revision diff, and what happened to it.
 *
 * The numbers are the server's `DiffOp`: -1 removed, 0 unchanged, 1 added.
 * Segments rather than a rendered string, because the endpoint used to return
 * ANSI terminal colour codes — unprintable bytes in the middle of the text as
 * far as a browser is concerned.
 *
 * What is compared is the markdown projection, not the document. That is what
 * makes a page written before the editor and one written after it comparable
 * against each other at all, and it is why the UI calls it a text comparison
 * rather than implying a structural one.
 */
export interface WikiDiffSegment {
  op: -1 | 0 | 1;
  text: string;
}

export interface WikiRevisionDiff {
  from_version: number;
  to_version: number;
  /** Empty when the title did not change, so the title row can be omitted. */
  title_segments: WikiDiffSegment[] | null;
  content_segments: WikiDiffSegment[] | null;
}

/** An org-scoped tag, carried by pages, tickets and project items alike. */
export interface CodexTag {
  id: string;
  org_id: string;
  /** The immutable identity, derived server-side. Underscore-separated. */
  slug: string;
  /** The display form: the first spelling anybody used. */
  name: string;
  created_at: string;
}

/** The three entity kinds that carry tags (migration 055). */
export type TagEntityType = 'page' | 'ticket' | 'project_item';

/**
 * One row of the tag browse: an entity carrying a tag, with its space context.
 */
export interface TaggedEntity {
  entity_type: TagEntityType;
  entity_id: string;
  space_id: string;
  space_name: string;
  space_key: string;
  title: string;
  /**
   * The kind's one human-readable reference, composed server-side: a page's
   * path, a ticket's ref ("BEA-42"), a project item's item_key ("VEC-14").
   */
  ref: string;
  updated_at: string;
}

export interface TaggedEntities {
  tag: CodexTag;
  entities: TaggedEntity[] | null;
  /**
   * The answer was cut short.
   *
   * A list capped without saying so looks exactly like a complete one, and
   * because the order is most-recent-first the entities that disappear are the
   * oldest — so a reader is shown the wrong nothing and told nothing. The
   * surface must say when this is set.
   */
  truncated?: boolean;
}

/**
 * One link touching the item being viewed, as THIS viewer may see it.
 *
 * Only the far side is described — the near side is the item whose panel is
 * being rendered — and `direction` says which end of the stored relation the
 * viewed item sits on. Incoming rows exist because the server unions the
 * reverse direction; no inverse row is stored, so a "blocks" link used to be
 * invisible to the very item it blocked.
 *
 * Every far field is nullable, and they are null together. When `far_readable`
 * is false the far entity sits in a space this viewer cannot read, and the
 * server sends no id, type, title or status for it. The row still arrives so
 * the panel can show that a link exists — an item needs to know it is blocked —
 * but nothing identifies what it points at.
 */
export interface Relation {
  id: string;
  kind: string;
  direction: 'outgoing' | 'incoming';
  far_readable: boolean;
  far_id: string | null;
  far_type: string | null;
  far_title: string | null;
  /** null for an unreadable far side — and ALSO for a readable page, which
   *  has no status. Key on far_readable, never on a non-null status. */
  far_status: string | null;
  /** The far entity's own space, for building its URL — relations link across
   *  spaces, so the near entity's space is not a substitute. null exactly when
   *  the far side is unreadable. */
  far_space_id: string | null;
}

/** The entity kinds a relation endpoint may name — the server's
 *  ValidEntityTypes, verbatim. */
export type RelationEntityType = 'project_item' | 'ticket' | 'page';

/**
 * The relation-kind vocabulary, with the labels the kind select renders.
 *
 * This mirrors the server's ValidRelationKinds exactly — the exhaustiveness
 * test in RelationsSection.test.tsx fails in both directions if the two drift
 * — and it is the ONE place the vocabulary lives client-side: the select maps
 * over this list rather than carrying its own.
 */
export const RELATION_KINDS: ReadonlyArray<{ value: string; label: string }> = [
  { value: 'relates_to', label: 'relates to' },
  { value: 'blocks', label: 'blocks' },
  { value: 'is_blocked_by', label: 'is blocked by' },
  { value: 'duplicates', label: 'duplicates' },
  { value: 'wiki_link', label: 'wiki link' },
];

export interface RoadmapItem {
  item: ProjectItem;
  due_at: string;
  overdue: boolean;
}

export interface RoadmapSprint {
  sprint: Sprint;
  items: ProjectItem[];
}

// ---------------------------------------------------------------------------
// Auth types
// ---------------------------------------------------------------------------

interface LoginRequest {
  email: string;
  password: string;
}

interface RegisterRequest {
  email: string;
  password: string;
  display_name: string;
  org_name: string;
}

interface AuthResponse {
  access_token: string;
  refresh_token: string;
  user: User;
}

interface RefreshResponse {
  access_token: string;
  refresh_token: string;
}

// ---------------------------------------------------------------------------
// Auth API functions
// ---------------------------------------------------------------------------

export async function loginUser(req: LoginRequest): Promise<AuthResponse> {
  return apiFetch<AuthResponse>('/auth/login', {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

export async function registerUser(req: RegisterRequest): Promise<AuthResponse> {
  return apiFetch<AuthResponse>('/auth/register', {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

export async function refreshAccessToken(): Promise<RefreshResponse> {
  const refreshToken = getRefreshToken();
  return apiFetch<RefreshResponse>('/auth/refresh', {
    method: 'POST',
    body: JSON.stringify({ refresh_token: refreshToken }),
  });
}

/**
 * revokeSession is the server half of signing out THIS device: since B1 it
 * revokes the current session row (the one the token's sid names), which is
 * what actually ends this session — while other devices the same user is
 * signed in on stay signed in. (The org-wide sign-out is /auth/logout-all.)
 *
 * Clearing localStorage is not a sign-out. The tokens are stateless RS256
 * JWTs; a copy taken out of the browser — by a script on a wiki page, by
 * anything with access to the profile directory — keeps working until its
 * session is revoked or it expires, no matter what the tab forgets. Until the
 * v0.4.1 trust patch the frontend never called this route at all.
 *
 * `logout()` in `./auth` is what calls this. Do not call it directly and leave
 * the local clear to a caller: the two belong together, which is why they are
 * one function there. It passes skipAuthRedirect because `logout()` owns the
 * clear in its finally — a 401 here means the session is already gone, so
 * apiFetch must not clear and hard-redirect out from under that ordering.
 */
export async function revokeSession(): Promise<void> {
  await apiFetch<{ message: string }>('/auth/logout', { method: 'POST' }, undefined, {
    skipAuthRedirect: true,
  });
}

// ---------------------------------------------------------------------------
// Space API functions
// ---------------------------------------------------------------------------

async function fetchSpace(spaceId: string): Promise<Space> {
  return apiFetch<Space>(`${spaceBase(spaceId)}`);
}

async function fetchSpaces(orgId: string): Promise<Space[]> {
  // Audit ref: testing-audit.md §7.5 — list endpoints occasionally return
  // literal `null` instead of `[]`. Treat null/undefined as empty so list
  // pages do not crash on .filter/.map.
  const data = await apiFetch<Space[] | Space | null>(`/orgs/${orgId}/spaces`);
  if (data == null) return [];
  return Array.isArray(data) ? data : [data];
}

interface CreateSpaceRequest {
  name: string;
  slug: string;
  // Optional: when omitted (or empty) the backend derives a key from the name
  // and dedupes it past the per-org key index. Auto-created spaces omit it.
  key?: string;
  type: SpaceType;
  description?: string;
  owner_team_id?: string;
}

async function createSpace(
  orgId: string,
  req: CreateSpaceRequest,
  ticketRef?: string,
): Promise<Space> {
  return apiFetch<Space>(`/orgs/${orgId}/spaces${ticketRefQuery(ticketRef)}`, {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

/**
 * PUT semantics, not PATCH: the backend requires name and overwrites
 * description, icon, and is_private wholesale (key is kept when omitted).
 * Callers changing one governance field (visibility, owner_team_id) must
 * echo the space's current values for the rest.
 */
interface UpdateSpaceRequest {
  name: string;
  key?: string;
  description?: string | null;
  icon?: string | null;
  is_private?: boolean;
  owner_team_id?: string;
  visibility?: SpaceVisibility;
}

async function updateSpace(
  orgId: string,
  spaceId: string,
  req: UpdateSpaceRequest,
  ticketRef?: string,
): Promise<Space> {
  return apiFetch<Space>(`/orgs/${orgId}/spaces/${spaceId}${ticketRefQuery(ticketRef)}`, {
    method: 'PUT',
    body: JSON.stringify(req),
  });
}

// ---------------------------------------------------------------------------
// Team API functions (P2)
// ---------------------------------------------------------------------------

async function fetchTeams(orgId: string): Promise<Team[]> {
  // Audit ref: testing-audit.md §7.5 — null-instead-of-[] regression.
  const data = await apiFetch<Team[] | null>(`/orgs/${orgId}/teams`);
  return data ?? [];
}

async function fetchTeamMembers(orgId: string, teamId: string): Promise<TeamMember[]> {
  const data = await apiFetch<TeamMember[] | null>(`/orgs/${orgId}/teams/${teamId}/members`);
  return data ?? [];
}

interface CreateTeamRequest {
  slug: string;
  name: string;
  description?: string;
  parent_id?: string;
}

async function createTeam(
  orgId: string,
  req: CreateTeamRequest,
  ticketRef?: string,
): Promise<Team> {
  return apiFetch<Team>(`/orgs/${orgId}/teams${ticketRefQuery(ticketRef)}`, {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

/**
 * parent_id is tri-state on the wire: absent (undefined) leaves the parent
 * untouched, null moves the team to the root, a uuid moves it under that
 * parent. JSON.stringify drops undefined keys, which is exactly the
 * "absent" encoding.
 */
interface UpdateTeamRequest {
  name?: string;
  description?: string;
  parent_id?: string | null;
}

async function updateTeam(
  orgId: string,
  teamId: string,
  req: UpdateTeamRequest,
  ticketRef?: string,
): Promise<Team> {
  return apiFetch<Team>(`/orgs/${orgId}/teams/${teamId}${ticketRefQuery(ticketRef)}`, {
    method: 'PATCH',
    body: JSON.stringify(req),
  });
}

async function deleteTeam(orgId: string, teamId: string, ticketRef?: string): Promise<void> {
  return apiFetch<void>(`/orgs/${orgId}/teams/${teamId}${ticketRefQuery(ticketRef)}`, {
    method: 'DELETE',
  });
}

interface PutTeamMemberRequest {
  role: TeamRole;
  is_primary?: boolean;
}

async function putTeamMember(
  orgId: string,
  teamId: string,
  userId: string,
  req: PutTeamMemberRequest,
  ticketRef?: string,
): Promise<TeamMember> {
  return apiFetch<TeamMember>(
    `/orgs/${orgId}/teams/${teamId}/members/${userId}${ticketRefQuery(ticketRef)}`,
    {
      method: 'PUT',
      body: JSON.stringify(req),
    },
  );
}

async function removeTeamMember(
  orgId: string,
  teamId: string,
  userId: string,
  ticketRef?: string,
): Promise<void> {
  return apiFetch<void>(
    `/orgs/${orgId}/teams/${teamId}/members/${userId}${ticketRefQuery(ticketRef)}`,
    {
      method: 'DELETE',
    },
  );
}

// ---------------------------------------------------------------------------
// Grant API functions (P2)
// ---------------------------------------------------------------------------

async function fetchSpaceGrants(orgId: string, spaceId: string): Promise<SpaceGrant[]> {
  const data = await apiFetch<SpaceGrant[] | null>(`/orgs/${orgId}/spaces/${spaceId}/grants`);
  return data ?? [];
}

interface CreateGrantRequest {
  subject_type: GrantSubjectType;
  subject_id: string;
  role: GrantRole;
}

async function createGrant(
  orgId: string,
  spaceId: string,
  req: CreateGrantRequest,
  ticketRef?: string,
): Promise<SpaceGrant> {
  return apiFetch<SpaceGrant>(`/orgs/${orgId}/spaces/${spaceId}/grants${ticketRefQuery(ticketRef)}`, {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

async function updateGrant(
  orgId: string,
  spaceId: string,
  grantId: string,
  role: GrantRole,
  ticketRef?: string,
): Promise<SpaceGrant> {
  return apiFetch<SpaceGrant>(
    `/orgs/${orgId}/spaces/${spaceId}/grants/${grantId}${ticketRefQuery(ticketRef)}`,
    {
      method: 'PATCH',
      body: JSON.stringify({ role }),
    },
  );
}

async function revokeGrant(
  orgId: string,
  spaceId: string,
  grantId: string,
  ticketRef?: string,
): Promise<void> {
  return apiFetch<void>(
    `/orgs/${orgId}/spaces/${spaceId}/grants/${grantId}${ticketRefQuery(ticketRef)}`,
    { method: 'DELETE' },
  );
}

async function fetchEffectiveAccess(
  orgId: string,
  spaceId: string,
  userId?: string,
): Promise<EffectiveAccess> {
  const qs = userId ? `?user_id=${encodeURIComponent(userId)}` : '';
  return apiFetch<EffectiveAccess>(`/orgs/${orgId}/spaces/${spaceId}/effective-access${qs}`);
}

// ---------------------------------------------------------------------------
// Administration types and API functions (P2.5)
// ---------------------------------------------------------------------------

export type PersonStatus = 'active' | 'invited' | 'deactivated';

/** One row of the admin People directory (GET /orgs/{o}/users). */
export interface Person {
  user_id: string;
  email: string;
  display_name: string;
  avatar_url?: string | null;
  org_role: string;
  status: 'active' | 'deactivated';
  last_login_at?: string | null;
  joined_at: string;
  primary_team_id?: string | null;
  primary_team_name?: string | null;
}

/** A picker search result (GET /orgs/{o}/members/search). */
export interface PersonRef {
  id: string;
  email: string;
  display_name: string;
  avatar_url?: string | null;
}

/**
 * One typeahead result for the ticket_ref field
 * (GET /orgs/{o}/tickets/suggest).
 *
 * `ref` is the string the picker writes into ticket_ref; everything else is
 * context for choosing between rows, since two spaces can each hold a ticket
 * numbered 42. The endpoint is org-member scoped and already cut to the
 * caller's readable spaces server-side. Free text stays valid in ticket_ref —
 * this only assists.
 */
export interface TicketRefSuggestion {
  ref: string;
  ticket_id: string;
  number: number;
  title: string;
  space_id: string;
  space_key: string;
  status: string;
  assigned_to_me: boolean;
}

export interface Invite {
  id: string;
  email: string;
  org_role: string;
  team_id?: string | null;
  team_name?: string;
  invited_by: string;
  invited_by_name?: string;
  expires_at: string;
  created_at: string;
  expired: boolean;
}

/** A freshly created or resent invite — invite_url is shown exactly once. */
export interface CreatedInvite extends Invite {
  invite_url: string;
  delivered: boolean;
}

export interface InviteOutcome {
  email: string;
  status: 'created' | 'invalid_email' | 'already_member' | 'already_invited' | 'error';
  error?: string;
  invite?: CreatedInvite;
}

export interface MatrixTeam {
  id: string;
  parent_id?: string | null;
  path: string[];
  name: string;
  is_default: boolean;
  member_count: number;
}

export interface MatrixSpace {
  id: string;
  name: string;
  type: SpaceType;
  visibility: SpaceVisibility;
}

export interface MatrixGrant {
  id: string;
  team_id: string;
  space_id: string;
  role: GrantRole;
}

export interface AccessMatrix {
  teams: MatrixTeam[];
  spaces: MatrixSpace[];
  grants: MatrixGrant[];
}

/** One requested cell state; role null revokes. */
export interface BulkChange {
  team_id: string;
  space_id: string;
  role: GrantRole | null;
}

export interface BulkAction {
  team_id: string;
  space_id: string;
  action: 'create' | 'update' | 'revoke' | 'noop';
  from_role?: GrantRole;
  to_role?: GrantRole;
}

export interface BulkResult {
  batch_id?: string;
  ticket_ref?: string;
  creates: number;
  updates: number;
  revokes: number;
  noops: number;
  actions: BulkAction[];
}

export interface AuditEntry {
  id: string;
  actor_id?: string | null;
  actor_name?: string;
  action: string;
  entity_kind: string;
  entity_id: string;
  payload: Record<string, string>;
  batch_id?: string | null;
  ticket_ref?: string | null;
  created_at: string;
  batch_size: number;
}

export interface AuditPage {
  entries: AuditEntry[];
  next_cursor?: string;
}

export interface AuditFilter {
  actor_id?: string;
  entity_kind?: string;
  action?: string;
  from?: string;
  to?: string;
  cursor?: string;
  limit?: number;
}

async function fetchOrgPeople(orgId: string): Promise<Person[]> {
  const data = await apiFetch<Person[] | null>(`/orgs/${orgId}/users`);
  return data ?? [];
}

async function searchOrgMembers(orgId: string, q: string): Promise<PersonRef[]> {
  const data = await apiFetch<PersonRef[] | null>(
    `/orgs/${orgId}/members/search?q=${encodeURIComponent(q)}`,
  );
  return data ?? [];
}

async function suggestTicketRefs(orgId: string, q: string): Promise<TicketRefSuggestion[]> {
  const data = await apiFetch<TicketRefSuggestion[] | null>(
    `/orgs/${orgId}/tickets/suggest?q=${encodeURIComponent(q)}`,
  );
  return data ?? [];
}

/** One row of the page-picker typeahead (A4). page_id is what the picker
 *  submits; the space key and name are how a human tells apart two pages
 *  with the same title in different spaces. */
export interface PageSuggestion {
  page_id: string;
  title: string;
  space_id: string;
  space_key: string;
  space_name: string;
}

async function suggestPages(orgId: string, q: string): Promise<PageSuggestion[]> {
  const data = await apiFetch<PageSuggestion[] | null>(
    `/orgs/${orgId}/pages/suggest?q=${encodeURIComponent(q)}`,
  );
  return data ?? [];
}

async function fetchInvites(orgId: string): Promise<Invite[]> {
  const data = await apiFetch<Invite[] | null>(`/orgs/${orgId}/invites`);
  return data ?? [];
}

interface CreateInvitesRequest {
  emails: string[];
  org_role?: string;
  team_id?: string | null;
}

async function createInvites(
  orgId: string,
  req: CreateInvitesRequest,
  ticketRef?: string,
): Promise<InviteOutcome[]> {
  const data = await apiFetch<InviteOutcome[] | null>(
    `/orgs/${orgId}/invites${ticketRefQuery(ticketRef)}`,
    {
      method: 'POST',
      body: JSON.stringify(req),
    },
  );
  return data ?? [];
}

async function revokeInvite(orgId: string, inviteId: string, ticketRef?: string): Promise<void> {
  return apiFetch<void>(`/orgs/${orgId}/invites/${inviteId}${ticketRefQuery(ticketRef)}`, {
    method: 'DELETE',
  });
}

async function resendInvite(
  orgId: string,
  inviteId: string,
  ticketRef?: string,
): Promise<CreatedInvite> {
  return apiFetch<CreatedInvite>(
    `/orgs/${orgId}/invites/${inviteId}/resend${ticketRefQuery(ticketRef)}`,
    { method: 'POST' },
  );
}

interface UpdatePersonRequest {
  org_role?: string;
  primary_team_id?: string;
  display_name?: string;
}

async function updatePerson(
  orgId: string,
  userId: string,
  req: UpdatePersonRequest,
  ticketRef?: string,
): Promise<void> {
  return apiFetch<void>(`/orgs/${orgId}/users/${userId}${ticketRefQuery(ticketRef)}`, {
    method: 'PATCH',
    body: JSON.stringify(req),
  });
}

/** AvatarUploadResult carries the serve URL of a freshly uploaded avatar. */
export interface AvatarUploadResult {
  avatar_url: string;
}

async function uploadOwnAvatar(file: File): Promise<AvatarUploadResult> {
  const body = new FormData();
  body.append('file', file);
  return apiFetch<AvatarUploadResult>(`/auth/me/avatar`, { method: 'PUT', body });
}

async function uploadUserAvatar(orgId: string, userId: string, file: File): Promise<AvatarUploadResult> {
  const body = new FormData();
  body.append('file', file);
  return apiFetch<AvatarUploadResult>(`/orgs/${orgId}/users/${userId}/avatar`, { method: 'PUT', body });
}

async function personLifecycle(orgId: string, userId: string, action: 'deactivate' | 'reactivate' | 'force-logout', ticketRef?: string): Promise<void> {
  return apiFetch<void>(`/orgs/${orgId}/users/${userId}/${action}${ticketRefQuery(ticketRef)}`, {
    method: 'POST',
  });
}

async function removePersonFromOrg(orgId: string, userId: string, ticketRef?: string): Promise<void> {
  return apiFetch<void>(`/orgs/${orgId}/users/${userId}${ticketRefQuery(ticketRef)}`, {
    method: 'DELETE',
  });
}

async function fetchAccessMatrix(orgId: string): Promise<AccessMatrix> {
  return apiFetch<AccessMatrix>(`/orgs/${orgId}/access-matrix`);
}

async function bulkPreviewGrants(orgId: string, changes: BulkChange[]): Promise<BulkResult> {
  return apiFetch<BulkResult>(`/orgs/${orgId}/grants/bulk-preview`, {
    method: 'POST',
    body: JSON.stringify({ changes }),
  });
}

async function bulkApplyGrants(orgId: string, changes: BulkChange[], ticketRef?: string): Promise<BulkResult> {
  return apiFetch<BulkResult>(`/orgs/${orgId}/grants/bulk-apply`, {
    method: 'POST',
    body: JSON.stringify({ changes, ticket_ref: ticketRef ?? '' }),
  });
}

async function fetchAuditLog(orgId: string, filter: AuditFilter): Promise<AuditPage> {
  const params = new URLSearchParams();
  if (filter.actor_id) params.set('actor_id', filter.actor_id);
  if (filter.entity_kind) params.set('entity_kind', filter.entity_kind);
  if (filter.action) params.set('action', filter.action);
  if (filter.from) params.set('from', filter.from);
  if (filter.to) params.set('to', filter.to);
  if (filter.cursor) params.set('cursor', filter.cursor);
  if (filter.limit) params.set('limit', String(filter.limit));
  const qs = params.toString();
  return apiFetch<AuditPage>(`/orgs/${orgId}/audit-log${qs ? `?${qs}` : ''}`);
}

async function fetchAuditBatch(orgId: string, batchId: string): Promise<AuditEntry[]> {
  const data = await apiFetch<AuditEntry[] | null>(`/orgs/${orgId}/audit-log/batches/${batchId}`);
  return data ?? [];
}

/** What a space contains — the delete confirmation names these counts. */
export interface SpaceContentsSummary {
  tickets: number;
  pages: number;
  items: number;
}

async function fetchSpaceContentsSummary(orgId: string, spaceId: string): Promise<SpaceContentsSummary> {
  return apiFetch<SpaceContentsSummary>(`/orgs/${orgId}/spaces/${spaceId}/summary`);
}

async function deleteSpace(orgId: string, spaceId: string, ticketRef?: string): Promise<void> {
  return apiFetch<void>(`/orgs/${orgId}/spaces/${spaceId}${ticketRefQuery(ticketRef)}`, {
    method: 'DELETE',
  });
}

/** The acceptance page's pre-submit view of an invite (public endpoint). */
export interface InviteInspection {
  email: string;
  org_name: string;
  state: 'active' | 'expired' | 'revoked' | 'accepted';
  existing_account: boolean;
}

export interface AcceptInviteResult {
  status: string;
  existing_account: boolean;
  org_id: string;
  org_slug: string;
  org_name: string;
  access_token?: string;
  refresh_token?: string;
  user_id?: string;
}

async function inspectInvite(token: string): Promise<InviteInspection> {
  return apiFetch<InviteInspection>(`/invites/${encodeURIComponent(token)}`);
}

interface AcceptInviteRequest {
  token: string;
  display_name?: string;
  password?: string;
}

async function acceptInvite(req: AcceptInviteRequest): Promise<AcceptInviteResult> {
  return apiFetch<AcceptInviteResult>('/invites/accept', {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

// ---------------------------------------------------------------------------
// Ticket API functions
// ---------------------------------------------------------------------------

async function fetchTickets(spaceId: string): Promise<Ticket[]> {
  // Audit ref: testing-audit.md §7.5 — null-instead-of-[] regression.
  const data = await apiFetch<Ticket[] | Ticket | null>(`${spaceBase(spaceId)}/tickets`);
  if (data == null) return [];
  return Array.isArray(data) ? data : [data];
}

async function fetchTicket(spaceId: string, ticketId: string): Promise<Ticket> {
  return apiFetch<Ticket>(`${spaceBase(spaceId)}/tickets/${ticketId}`);
}

interface CreateTicketRequest {
  title: string;
  description?: string;
  priority?: string;
  assignee_id?: string | null;
}

async function createTicket(spaceId: string, req: CreateTicketRequest): Promise<Ticket> {
  return apiFetch<Ticket>(`${spaceBase(spaceId)}/tickets`, {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

interface UpdateTicketRequest {
  title?: string;
  description?: string;
  priority?: string;
  assignee_id?: string | null;
  status?: string;
  /**
   * The ticket's due date, RFC3339. Same three states as the item PATCH: omit
   * to leave alone, `null` to clear, a timestamp to set.
   *
   * Note that `assignee_id` and `status` above are NOT accepted by the Go
   * handler — `updateTicketRequest` has never carried them and
   * `respond.DecodeJSON` rejects unknown fields, so sending either is a 400.
   * They are reachable through the dedicated `/assign` and `/status` routes,
   * which is what the two detail pages already call. Left declared rather than
   * removed because correcting them is not this change's to make; do not send
   * them.
   *
   * A bare `YYYY-MM-DD` is rejected with 400; use `toRFC3339Date`.
   */
  due_at?: string | null;
}

async function updateTicket(
  spaceId: string,
  ticketId: string,
  req: UpdateTicketRequest,
): Promise<Ticket> {
  return apiFetch<Ticket>(`${spaceBase(spaceId)}/tickets/${ticketId}`, {
    method: 'PATCH',
    body: JSON.stringify(req),
  });
}

async function transitionTicketStatus(
  spaceId: string,
  ticketId: string,
  status: TicketStatus,
): Promise<Ticket> {
  return apiFetch<Ticket>(`${spaceBase(spaceId)}/tickets/${ticketId}/status`, {
    method: 'POST',
    body: JSON.stringify({ status }),
  });
}

// ---------------------------------------------------------------------------
// Wiki API functions
// ---------------------------------------------------------------------------

async function fetchWikiPages(spaceId: string): Promise<WikiPage[]> {
  // Audit ref: testing-audit.md §7.5 — null-instead-of-[] regression.
  const data = await apiFetch<WikiPage[] | WikiPage | null>(`${spaceBase(spaceId)}/wiki`);
  if (data == null) return [];
  return Array.isArray(data) ? data : [data];
}

async function fetchWikiPage(spaceId: string, pageId: string): Promise<WikiPage> {
  return apiFetch<WikiPage>(`${spaceBase(spaceId)}/wiki/${pageId}`);
}

interface CreateWikiPageRequest {
  title: string;
  content: string;
  parent_id?: string | null;
  position?: number;
}

async function createWikiPage(spaceId: string, req: CreateWikiPageRequest): Promise<WikiPage> {
  return apiFetch<WikiPage>(`${spaceBase(spaceId)}/wiki`, {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

interface UpdateWikiPageRequest {
  title?: string;
  content?: string;
  expected_version?: number;
}

async function updateWikiPage(
  spaceId: string,
  pageId: string,
  req: UpdateWikiPageRequest,
): Promise<WikiPage> {
  return apiFetch<WikiPage>(`${spaceBase(spaceId)}/wiki/${pageId}`, {
    method: 'PUT',
    body: JSON.stringify(req),
  });
}

// ---------------------------------------------------------------------------
// Codex document API (issue #15 / ADR-0012)
//
// The document surface shipped in PR #73. Its contract, and the three shapes
// below, are fixed by `internal/core/wiki/document.go`; this is the client
// half and it invents nothing.
// ---------------------------------------------------------------------------

/**
 * CodexEditableDocument is what `GET …/wiki/{pageID}/document` returns: the
 * published document with every type outside the editor's schema replaced by
 * a preservation placeholder, plus the caller's own draft if they hold one.
 *
 * `doc` is safe to hand to ProseMirror; `WikiPage.doc` is not.
 */
export interface CodexEditableDocument {
  page_id: string;
  title: string;
  doc: CodexDoc;
  /** The published version `doc` belongs to; a publish must carry it back. */
  base_version: number;
  /** 'document' if the page already held one, 'markdown' if it was converted. */
  source_format: 'document' | 'markdown';
  /**
   * The placeholder ids in `doc`, in document order, taken from the server's
   * payload before ProseMirror parses it. If ProseMirror drops a placeholder
   * on load the id is here and absent from the editor's state, which is what
   * makes the loss detectable instead of invisible.
   */
  preserved_ids: string[];
  draft: CodexDraftDocument | null;
}

export interface CodexDraftDocument {
  title: string;
  doc: CodexDoc;
  base_version: number;
  updated_at: string;
  /** The page has been published past the version this draft started from. */
  stale: boolean;
}

/** One row of the Codex Drafts view, from `GET …/wiki/drafts`. */
export interface CodexDraftSummary {
  page_id: string;
  page_title: string;
  draft_title: string;
  base_version: number;
  page_version: number;
  path: string;
  updated_at: string;
  stale: boolean;
}

/** A stored page image. A document refers to images by id, never by URL. */
export interface CodexPageImage {
  attachment_id: string;
  filename: string;
  content_type: string;
  size_bytes: number;
}

/** The `ConflictDetail` 409 body: the page moved on under the author. */
export interface CodexPublishConflict {
  page_id: string;
  expected_version: number;
  current_page: WikiPage;
  message: string;
}

/** One piece of preserved content a publish would remove. */
export interface CodexLostContentItem {
  id: string;
  name: string;
  text: string;
}

/** The lost-content 409 body: the ADR-0012 catastrophe caught in the act. */
export interface CodexPublishLostContent {
  page_id: string;
  lost_ids: string[];
  lost: CodexLostContentItem[];
  message: string;
}

/**
 * PublishConflictError carries the 409 the version guard produced. The author
 * can resolve it — reload, or overwrite — so the detail travels with the error
 * rather than collapsing to a message.
 */
export class PublishConflictError extends Error {
  detail: CodexPublishConflict;
  constructor(detail: CodexPublishConflict) {
    super(detail.message);
    this.name = 'PublishConflictError';
    this.detail = detail;
  }
}

/**
 * PublishLostContentError carries the 409 that names preserved content the
 * publish would have removed. Republishing with those ids in
 * `acknowledged_lost_ids` is the author's explicit confirmation.
 */
export class PublishLostContentError extends Error {
  detail: CodexPublishLostContent;
  constructor(detail: CodexPublishLostContent) {
    super(detail.message);
    this.name = 'PublishLostContentError';
    this.detail = detail;
  }
}

/**
 * classifyPublishFailure recognises the publish route's two bare-object 409s.
 *
 * Both arrive as 409, so the discriminator is structural: a version conflict
 * names `current_page`, a lost-content refusal names `lost_ids`. Anything else
 * — including a 409 in the ordinary `{error:{…}}` envelope — is left to the
 * standard path.
 */
function classifyPublishFailure(status: number, body: unknown): Error | undefined {
  if (status !== 409 || body == null || typeof body !== 'object') return undefined;
  const candidate = body as Record<string, unknown>;
  if (Array.isArray(candidate.lost_ids)) {
    return new PublishLostContentError(body as CodexPublishLostContent);
  }
  if (candidate.current_page != null) {
    return new PublishConflictError(body as CodexPublishConflict);
  }
  return undefined;
}

async function fetchPageDocument(spaceId: string, pageId: string): Promise<CodexEditableDocument> {
  return apiFetch<CodexEditableDocument>(`${spaceBase(spaceId)}/wiki/${pageId}/document`);
}

export interface SavePageDraftRequest {
  title: string;
  doc: CodexDoc;
  base_version: number;
}

async function savePageDraft(
  spaceId: string,
  pageId: string,
  req: SavePageDraftRequest,
): Promise<CodexDraftDocument> {
  return apiFetch<CodexDraftDocument>(`${spaceBase(spaceId)}/wiki/${pageId}/draft`, {
    method: 'PUT',
    body: JSON.stringify(req),
  });
}

async function discardPageDraft(spaceId: string, pageId: string): Promise<void> {
  return apiFetch<void>(`${spaceBase(spaceId)}/wiki/${pageId}/draft`, { method: 'DELETE' });
}

async function fetchSpaceDrafts(spaceId: string): Promise<CodexDraftSummary[]> {
  // Audit ref: testing-audit.md §7.5 — null-instead-of-[] regression.
  const data = await apiFetch<CodexDraftSummary[] | null>(`${spaceBase(spaceId)}/wiki/drafts`);
  return data ?? [];
}

export interface PublishPageRequest {
  title: string;
  doc: CodexDoc;
  base_version: number;
  /** The preserved blocks the author has explicitly confirmed removing. */
  acknowledged_lost_ids?: string[];
  /** Publish over a page that moved on. Only after the conflict was reported. */
  overwrite?: boolean;
}

async function publishPage(
  spaceId: string,
  pageId: string,
  req: PublishPageRequest,
): Promise<WikiPage> {
  return apiFetch<WikiPage>(
    `${spaceBase(spaceId)}/wiki/${pageId}/publish`,
    { method: 'POST', body: JSON.stringify(req) },
    classifyPublishFailure,
  );
}

async function uploadPageImage(
  spaceId: string,
  pageId: string,
  file: File,
): Promise<CodexPageImage> {
  const form = new FormData();
  form.append('file', file);
  return apiFetch<CodexPageImage>(`${spaceBase(spaceId)}/wiki/${pageId}/images`, {
    method: 'POST',
    body: form,
  });
}

/**
 * fetchObjectURL fetches a binary route through the authenticated client and
 * hands back a blob URL an `<img src>` or an `<a href download>` can use. The
 * caller revokes it when done.
 *
 * This is the ONLY way this frontend puts server bytes in front of the
 * browser, and it is not a convenience. Every binary route authenticates from
 * the `Authorization` header or a `session` cookie, and this frontend holds a
 * bearer token in localStorage and sets no cookie — nothing in
 * `internal/core/api/auth` calls `http.SetCookie`. So a URL handed straight to
 * the browser, in an `<img src>` or an `<a href>`, is fetched with no
 * credential at all and answered 401. In an `<img>` that is a broken-image
 * icon and no error anywhere; in an `<a>` it is a saved file containing a JSON
 * error. Both are silent, which is how the shared page shipped with neither
 * its images nor its downloads working (S8).
 *
 * A route that streams bytes therefore gets a fetch-and-object-URL helper
 * here, never a URL builder.
 */
async function fetchObjectURL(path: string, unavailable: string): Promise<string> {
  const headers = new Headers();
  const token = getToken();
  if (token) headers.set('Authorization', `Bearer ${token}`);

  const response = await fetch(`${API_BASE_URL}${path}`, { headers });
  if (!response.ok) {
    throw new APIError(response.status, {
      error: { code: 'unknown', message: unavailable, request_id: '' },
    });
  }
  return URL.createObjectURL(await response.blob());
}

/**
 * fetchPageImageObjectURL resolves an image a document refers to by attachment
 * id into a blob URL an `<img src>` can use.
 *
 * The document deliberately stores no URL of its own: the address a reader
 * needs depends on whether they reached the page through the space or through
 * a share, so baking one in would make it wrong for one of them.
 */
export async function fetchPageImageObjectURL(
  spaceId: string,
  attachmentId: string,
): Promise<string> {
  return fetchObjectURL(
    `${spaceBase(spaceId)}/attachments/${attachmentId}`,
    'the image is unavailable',
  );
}

// ---------------------------------------------------------------------------
// Project item API functions
// ---------------------------------------------------------------------------

async function fetchProjectItems(spaceId: string): Promise<ProjectItem[]> {
  // Audit ref: testing-audit.md §7.5 — null-instead-of-[] regression.
  const data = await apiFetch<ProjectItem[] | ProjectItem | null>(`${spaceBase(spaceId)}/projects/items`);
  if (data == null) return [];
  return Array.isArray(data) ? data : [data];
}

export async function fetchProjectItem(spaceId: string, itemId: string): Promise<ProjectItem> {
  return apiFetch<ProjectItem>(`${spaceBase(spaceId)}/projects/items/${itemId}`);
}

interface CreateProjectItemRequest {
  title: string;
  description?: string;
  kind: string;
  priority: string;
  assignee_id?: string | null;
  sprint_id?: string | null;
}

async function createProjectItem(
  spaceId: string,
  req: CreateProjectItemRequest,
): Promise<ProjectItem> {
  return apiFetch<ProjectItem>(`${spaceBase(spaceId)}/projects/items`, {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

interface UpdateProjectItemRequest {
  title?: string;
  description?: string;
  priority?: string;
  assignee_id?: string | null;
  /**
   * The item's type slug. Optional in the strict sense the Go contract
   * requires (`kind *string`): omitting the key leaves the kind unchanged,
   * which is why JSON.stringify dropping undefined is the right encoding.
   * Sending it changes the type of an existing item in place.
   */
  kind?: string;
  /**
   * The item's due date, RFC3339. Three states, and all three are reachable
   * here because `JSON.stringify` drops `undefined` and keeps `null`:
   * omit the key to leave the stored date alone, send `null` to clear it, send
   * a timestamp to set it. The Go side reads it through
   * `respond.OptionalField[time.Time]`, which is what keeps absent and null
   * apart — collapsing them is what once wiped every item's due date.
   *
   * A bare `YYYY-MM-DD` is rejected with 400; use `toRFC3339Date`.
   */
  due_at?: string | null;
}

/**
 * Exported alongside the hook because a drag handler cannot use the hook.
 * `useUpdateProjectItem` binds its itemId at render time, and the board's drag
 * handler clears the active item on its first line; react-query then re-binds
 * the observer with an empty id, so the PATCH goes to
 * `.../projects/items/` — no id at all — and 404s. `transitionProjectItemStatus`
 * has always taken the id explicitly, which is exactly why the status half of a
 * drop persisted while the lane half silently did not.
 *
 * Callers holding an id should use this; callers rendering a form bound to one
 * item should use the hook.
 */
export async function updateProjectItem(
  spaceId: string,
  itemId: string,
  req: UpdateProjectItemRequest,
): Promise<ProjectItem> {
  return apiFetch<ProjectItem>(`${spaceBase(spaceId)}/projects/items/${itemId}`, {
    method: 'PATCH',
    body: JSON.stringify(req),
  });
}

export async function transitionProjectItemStatus(
  spaceId: string,
  itemId: string,
  status: string,
): Promise<ProjectItem> {
  return apiFetch<ProjectItem>(`${spaceBase(spaceId)}/projects/items/${itemId}/status`, {
    method: 'POST',
    body: JSON.stringify({ status }),
  });
}

// assignItemToSprint moves an item onto a sprint, or off every sprint back to
// the backlog when sprintId is null. The endpoint takes a nullable sprint_id
// rather than the /backlog/move-to-* pair so one call covers both directions.
export async function assignItemToSprint(
  spaceId: string,
  itemId: string,
  sprintId: string | null,
): Promise<void> {
  await apiFetch<{ message: string }>(`${spaceBase(spaceId)}/projects/items/${itemId}/sprint`, {
    method: 'POST',
    body: JSON.stringify({ sprint_id: sprintId }),
  });
}

// ---------------------------------------------------------------------------
// Board configuration API functions (W4)
// ---------------------------------------------------------------------------

async function fetchBoardConfig(spaceId: string): Promise<BoardConfig> {
  return apiFetch<BoardConfig>(`${spaceBase(spaceId)}/projects/board/config`);
}

export interface SaveBoardConfigRequest {
  columns: {
    /** Omit for a new column; supply to keep an existing column's identity. */
    id?: string;
    name: string;
    wip_limit: number | null;
    statuses: string[];
  }[];
}

async function saveBoardConfig(spaceId: string, req: SaveBoardConfigRequest): Promise<BoardConfig> {
  return apiFetch<BoardConfig>(`${spaceBase(spaceId)}/projects/board/config`, {
    method: 'PUT',
    body: JSON.stringify(req),
  });
}

async function resetBoardConfig(spaceId: string): Promise<BoardConfig> {
  return apiFetch<BoardConfig>(`${spaceBase(spaceId)}/projects/board/config/reset`, {
    method: 'POST',
  });
}

// deleteBoardColumn requires a re-mapping target: a removed column's statuses
// must go somewhere, so there is no variant that simply drops them.
async function deleteBoardColumn(spaceId: string, columnId: string, remapTo: string): Promise<BoardConfig> {
  return apiFetch<BoardConfig>(`${spaceBase(spaceId)}/projects/board/config/columns/${columnId}`, {
    method: 'DELETE',
    body: JSON.stringify({ remap_to: remapTo }),
  });
}

// ---------------------------------------------------------------------------
// Sprint API functions
// ---------------------------------------------------------------------------

async function fetchSprints(spaceId: string): Promise<Sprint[]> {
  return apiFetch<Sprint[]>(`${spaceBase(spaceId)}/projects/sprints`);
}

interface CreateSprintRequest {
  name: string;
  goal?: string;
  /** RFC3339 date-time — the API rejects bare YYYY-MM-DD dates with 400. */
  starts_at?: string;
  /** RFC3339 date-time — the API rejects bare YYYY-MM-DD dates with 400. */
  ends_at?: string;
}

async function createSprint(spaceId: string, req: CreateSprintRequest): Promise<Sprint> {
  return apiFetch<Sprint>(`${spaceBase(spaceId)}/projects/sprints`, {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

async function fetchActiveSprint(spaceId: string): Promise<Sprint | null> {
  try {
    return await apiFetch<Sprint>(`${spaceBase(spaceId)}/projects/sprints/active`);
  } catch (err) {
    if (err instanceof APIError && err.status === 404) return null;
    throw err;
  }
}

async function fetchSprintItems(spaceId: string, sprintId: string): Promise<ProjectItem[]> {
  const data = await apiFetch<ProjectItem[] | ProjectItem | null>(
    `${spaceBase(spaceId)}/projects/sprints/${sprintId}/items`,
  );
  if (data == null) return [];
  return Array.isArray(data) ? data : [data];
}

// ---------------------------------------------------------------------------
// Item type API functions
// ---------------------------------------------------------------------------

export interface ItemType {
  id: string;
  org_id: string;
  slug: string;
  name: string;
  position: number;
  archived_at: string | null;
  created_at: string;
  updated_at: string;
}

interface CreateItemTypeRequest {
  name: string;
}

interface UpdateItemTypeRequest {
  name?: string;
  archived?: boolean;
}

async function fetchItemTypes(orgId: string): Promise<ItemType[]> {
  const data = await apiFetch<ItemType[] | null>(`/orgs/${orgId}/item-types`);
  return Array.isArray(data) ? data : [];
}

async function createItemType(orgId: string, req: CreateItemTypeRequest): Promise<ItemType> {
  return apiFetch<ItemType>(`/orgs/${orgId}/item-types`, {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

async function updateItemType(orgId: string, typeId: string, req: UpdateItemTypeRequest): Promise<ItemType> {
  return apiFetch<ItemType>(`/orgs/${orgId}/item-types/${typeId}`, {
    method: 'PATCH',
    body: JSON.stringify(req),
  });
}

async function deleteItemType(orgId: string, typeId: string): Promise<void> {
  await apiFetch<void>(`/orgs/${orgId}/item-types/${typeId}`, { method: 'DELETE' });
}

// ---------------------------------------------------------------------------
// Custom field API functions
// ---------------------------------------------------------------------------

export type CustomFieldType = 'text' | 'number' | 'date' | 'single_select';

export interface CustomFieldDef {
  id: string;
  org_id: string;
  slug: string;
  name: string;
  field_type: CustomFieldType;
  options: string[];
  position: number;
  archived_at: string | null;
  created_at: string;
  updated_at: string;
}

/** A custom field as rendered on an entity's form: a definition attached to
 * this space's form with its current value and required flag, or a read-only
 * value whose definition is gone or not attached here (legacy). */
export interface EntityCustomField {
  slug: string;
  name: string;
  field_type: CustomFieldType;
  options: string[];
  value: string;
  required: boolean;
  legacy: boolean;
}

/** The two entity kinds with a field surface. Wire entity_type values are
 * 'project_item' and 'ticket'; the kind here picks the URL family. */
export type FieldEntityKind = 'item' | 'ticket';

/** One attachment of a custom field to a (space, entity type) form. */
export interface CustomFieldScope {
  field_id: string;
  space_id: string;
  entity_type: 'project_item' | 'ticket' | 'page';
  required: boolean;
  position: number;
}

interface CreateCustomFieldRequest {
  name: string;
  field_type: CustomFieldType;
  options?: string[];
}

interface UpdateCustomFieldRequest {
  name?: string;
  options?: string[];
  archived?: boolean;
}

async function fetchCustomFields(orgId: string): Promise<CustomFieldDef[]> {
  const data = await apiFetch<CustomFieldDef[] | null>(`/orgs/${orgId}/custom-fields`);
  return Array.isArray(data) ? data : [];
}

async function createCustomField(orgId: string, req: CreateCustomFieldRequest): Promise<CustomFieldDef> {
  return apiFetch<CustomFieldDef>(`/orgs/${orgId}/custom-fields`, { method: 'POST', body: JSON.stringify(req) });
}

async function updateCustomField(orgId: string, fieldId: string, req: UpdateCustomFieldRequest): Promise<CustomFieldDef> {
  return apiFetch<CustomFieldDef>(`/orgs/${orgId}/custom-fields/${fieldId}`, { method: 'PATCH', body: JSON.stringify(req) });
}

async function deleteCustomField(orgId: string, fieldId: string): Promise<void> {
  await apiFetch<void>(`/orgs/${orgId}/custom-fields/${fieldId}`, { method: 'DELETE' });
}

async function fetchFieldScopes(orgId: string, fieldId: string): Promise<CustomFieldScope[]> {
  const data = await apiFetch<CustomFieldScope[] | null>(`/orgs/${orgId}/custom-fields/${fieldId}/scopes`);
  return Array.isArray(data) ? data : [];
}

async function putFieldScope(
  orgId: string,
  fieldId: string,
  spaceId: string,
  entityType: 'project_item' | 'ticket',
  required: boolean,
): Promise<CustomFieldScope> {
  return apiFetch<CustomFieldScope>(`/orgs/${orgId}/custom-fields/${fieldId}/scopes/${spaceId}/${entityType}`, {
    method: 'PUT',
    body: JSON.stringify({ required }),
  });
}

async function deleteFieldScope(
  orgId: string,
  fieldId: string,
  spaceId: string,
  entityType: 'project_item' | 'ticket',
): Promise<void> {
  await apiFetch<void>(`/orgs/${orgId}/custom-fields/${fieldId}/scopes/${spaceId}/${entityType}`, {
    method: 'DELETE',
  });
}

async function fetchFormFieldScopes(
  orgId: string,
  spaceId: string,
  entityType: 'project_item' | 'ticket',
): Promise<CustomFieldScope[]> {
  const data = await apiFetch<CustomFieldScope[] | null>(
    `/orgs/${orgId}/custom-fields/forms/${spaceId}/${entityType}`,
  );
  return Array.isArray(data) ? data : [];
}

async function putFormFieldOrder(
  orgId: string,
  spaceId: string,
  entityType: 'project_item' | 'ticket',
  fieldIds: string[],
): Promise<CustomFieldScope[]> {
  const data = await apiFetch<CustomFieldScope[] | null>(
    `/orgs/${orgId}/custom-fields/forms/${spaceId}/${entityType}/order`,
    { method: 'PUT', body: JSON.stringify({ field_ids: fieldIds }) },
  );
  return Array.isArray(data) ? data : [];
}

function entityFieldsBase(spaceId: string, kind: FieldEntityKind, entityId: string): string {
  return kind === 'ticket'
    ? `${spaceBase(spaceId)}/tickets/${entityId}/fields`
    : `${spaceBase(spaceId)}/projects/items/${entityId}/fields`;
}

async function fetchEntityFields(spaceId: string, kind: FieldEntityKind, entityId: string): Promise<EntityCustomField[]> {
  const data = await apiFetch<EntityCustomField[] | null>(entityFieldsBase(spaceId, kind, entityId));
  return Array.isArray(data) ? data : [];
}

async function setEntityField(
  spaceId: string,
  kind: FieldEntityKind,
  entityId: string,
  slug: string,
  value: string,
): Promise<void> {
  await apiFetch<void>(`${entityFieldsBase(spaceId, kind, entityId)}/${slug}`, {
    method: 'PUT',
    body: JSON.stringify({ value }),
  });
}

// ---------------------------------------------------------------------------
// Organization API functions
// ---------------------------------------------------------------------------

async function fetchOrganization(orgId: string): Promise<Organization> {
  return apiFetch<Organization>(`/orgs/${orgId}`);
}

interface UpdateOrganizationRequest {
  name: string;
  description?: string;
}

async function updateOrganization(
  orgId: string,
  req: UpdateOrganizationRequest,
): Promise<Organization> {
  return apiFetch<Organization>(`/orgs/${orgId}`, {
    method: 'PATCH',
    body: JSON.stringify(req),
  });
}

// ---------------------------------------------------------------------------
// Current user API function
// ---------------------------------------------------------------------------

async function fetchMe(): Promise<User> {
  return apiFetch<User>('/auth/me');
}

// ---------------------------------------------------------------------------
// Member API functions
// ---------------------------------------------------------------------------

async function fetchMembers(orgId: string, spaceId: string): Promise<Member[]> {
  const data = await apiFetch<Member[] | Member>(`/orgs/${orgId}/spaces/${spaceId}/members`);
  return Array.isArray(data) ? data : [data];
}

// ---------------------------------------------------------------------------
// Comment API functions
// ---------------------------------------------------------------------------

function entityTypeToPath(entityType: string): string {
  switch (entityType) {
    case 'ticket': return 'tickets';
    case 'project_item': return 'projects/items';
    case 'page': return 'wiki';
    default: return entityType;
  }
}

async function fetchComments(orgId: string, spaceId: string, entityType: string, entityId: string): Promise<Comment[]> {
  const path = entityTypeToPath(entityType);
  const data = await apiFetch<Comment[] | Comment>(`/orgs/${orgId}/spaces/${spaceId}/${path}/${entityId}/comments`);
  return Array.isArray(data) ? data : [data];
}

interface CreateCommentRequest {
  content: string;
  /**
   * Optional on the wire — the server treats absent and `''` alike and stores
   * `internal`, so a client written before the portal existed keeps posting
   * private notes. Optional here for the same reason and no other: call sites
   * that offer the choice should send it explicitly rather than leaning on the
   * default, because "I meant internal" and "I forgot" then look identical in
   * the request.
   */
  visibility?: CommentVisibility;
}

async function createComment(orgId: string, spaceId: string, entityType: string, entityId: string, req: CreateCommentRequest): Promise<Comment> {
  const path = entityTypeToPath(entityType);
  return apiFetch<Comment>(`/orgs/${orgId}/spaces/${spaceId}/${path}/${entityId}/comments`, {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

// ---------------------------------------------------------------------------
// Notification API functions
// ---------------------------------------------------------------------------

async function listNotifications(): Promise<NotificationListResponse> {
  return apiFetch<NotificationListResponse>('/notifications');
}

async function markNotificationRead(id: string): Promise<void> {
  return apiFetch<void>(`/notifications/${id}/read`, { method: 'POST' });
}

async function markAllNotificationsRead(): Promise<void> {
  return apiFetch<void>('/notifications/read-all', { method: 'POST' });
}

// ---------------------------------------------------------------------------
// Ticket assign API function
// ---------------------------------------------------------------------------

async function assignTicket(spaceId: string, ticketId: string, assigneeId: string | null): Promise<void> {
  return apiFetch<void>(`${spaceBase(spaceId)}/tickets/${ticketId}/assign`, {
    method: 'POST',
    body: JSON.stringify({ assignee_id: assigneeId }),
  });
}

// ---------------------------------------------------------------------------
// Wiki tree / search / revision / move API functions
// ---------------------------------------------------------------------------

async function fetchWikiTree(spaceId: string): Promise<WikiTreeNode[]> {
  const data = await apiFetch<WikiTreeNode[] | null>(`${spaceBase(spaceId)}/wiki/tree`);
  return data ?? [];
}

async function searchWikiPages(spaceId: string, q: string): Promise<WikiPage[]> {
  const data = await apiFetch<WikiPage[] | null>(`${spaceBase(spaceId)}/wiki/search?q=${encodeURIComponent(q)}`);
  return data ?? [];
}

async function fetchWikiRevisions(spaceId: string, pageId: string): Promise<WikiRevision[]> {
  const data = await apiFetch<WikiRevision[] | null>(`${spaceBase(spaceId)}/wiki/${pageId}/revisions`);
  return data ?? [];
}

async function fetchWikiRevision(spaceId: string, pageId: string, version: number): Promise<WikiPage> {
  return apiFetch<WikiPage>(`${spaceBase(spaceId)}/wiki/${pageId}/revisions/${version}`);
}

async function fetchWikiDiff(
  spaceId: string,
  pageId: string,
  from: number,
  to: number,
): Promise<WikiRevisionDiff> {
  return apiFetch<WikiRevisionDiff>(
    `${spaceBase(spaceId)}/wiki/${pageId}/diff?from=${from}&to=${to}`,
  );
}

/**
 * Restoring an earlier version.
 *
 * It republishes through the ordinary publish path, so it carries the ordinary
 * publish arguments: `base_version` for the version guard, and
 * `acknowledged_lost_ids` for the ADR-0012 confirmation. A restore is more
 * likely than an edit to trigger that confirmation, not less — an older version
 * usually lacks preserved content the current one has, which is part of what
 * makes it older.
 */
export interface RestoreRevisionRequest {
  base_version: number;
  acknowledged_lost_ids?: string[];
  overwrite?: boolean;
}

/**
 * The result of a restore.
 *
 * `restored: false` is a success, not a failure: it is the answer to restoring
 * the version the page already holds, which produces no new version and says
 * so rather than adding an entry to the history in which nothing changed.
 */
export interface RestoreRevisionResult {
  restored: boolean;
  message?: string;
  page?: WikiPage;
}

async function restoreWikiRevision(
  spaceId: string,
  pageId: string,
  version: number,
  req: RestoreRevisionRequest,
): Promise<RestoreRevisionResult> {
  return apiFetch<RestoreRevisionResult>(
    `${spaceBase(spaceId)}/wiki/${pageId}/revisions/${version}/restore`,
    { method: 'POST', body: JSON.stringify(req) },
    // The same classifier the publish call uses, and that is the point: a
    // restore hits the same two 409s, so it must produce the same two typed
    // errors and the same two dialogues rather than a second set that drifts.
    classifyPublishFailure,
  );
}

// ---------------------------------------------------------------------------
// Entity tags (migrations 040, 055)
// ---------------------------------------------------------------------------

async function fetchOrgTags(orgId: string): Promise<CodexTag[]> {
  const data = await apiFetch<CodexTag[] | null>(`/orgs/${orgId}/tags`);
  return data ?? [];
}

async function fetchEntitiesWithTag(orgId: string, label: string): Promise<TaggedEntities> {
  // The LABEL, not a slug. The server slugifies whatever it is given and
  // Slugify is idempotent, so both forms work — and the client never has to
  // reimplement a slug convention that has a database CHECK written against it.
  return apiFetch<TaggedEntities>(`/orgs/${orgId}/tags/${encodeURIComponent(label)}/entities`);
}

/**
 * The one place an entity kind maps to its tag route. All three kinds carry
 * the same GET/PUT pair under their own resource path — the polymorphic
 * comments routes follow the same convention.
 */
function entityTagsPath(entityType: TagEntityType, spaceId: string, entityId: string): string {
  switch (entityType) {
    case 'ticket':
      return `${spaceBase(spaceId)}/tickets/${entityId}/tags`;
    case 'project_item':
      return `${spaceBase(spaceId)}/projects/items/${entityId}/tags`;
    case 'page':
      return `${spaceBase(spaceId)}/wiki/${entityId}/tags`;
  }
}

async function fetchEntityTags(
  entityType: TagEntityType,
  spaceId: string,
  entityId: string,
): Promise<CodexTag[]> {
  const data = await apiFetch<CodexTag[] | null>(entityTagsPath(entityType, spaceId, entityId));
  return data ?? [];
}

async function setEntityTags(
  entityType: TagEntityType,
  spaceId: string,
  entityId: string,
  labels: string[],
): Promise<CodexTag[]> {
  const data = await apiFetch<CodexTag[] | null>(entityTagsPath(entityType, spaceId, entityId), {
    method: 'PUT',
    body: JSON.stringify({ tags: labels }),
  });
  return data ?? [];
}

interface MoveWikiPageRequest {
  parent_id: string | null;
  position: number;
  /** Move the page to another space (P3, ADR-0008): revokes the moved
   *  subtree's shares. Omitted or equal to the current space = in-space. */
  target_space_id?: string;
}

/** The move response reports whether the move crossed spaces and how many
 *  shares it revoked, so the UI can confirm the outcome. */
export interface MoveWikiPageResult {
  message: string;
  cross_space: boolean;
  revoked_shares: number;
}

async function moveWikiPage(spaceId: string, pageId: string, req: MoveWikiPageRequest): Promise<MoveWikiPageResult> {
  return apiFetch<MoveWikiPageResult>(`${spaceBase(spaceId)}/wiki/${pageId}/move`, {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

// ---------------------------------------------------------------------------
// Relations API functions
// ---------------------------------------------------------------------------

/** The subtree each entity type's relation routes hang off (A4): one
 *  entity-generic satellite, mounted per subtree the way comments are. */
function relationsBase(spaceId: string, entityType: RelationEntityType, entityId: string): string {
  switch (entityType) {
    case 'ticket':
      return `${spaceBase(spaceId)}/tickets/${entityId}/relations`;
    case 'page':
      return `${spaceBase(spaceId)}/wiki/${entityId}/relations`;
    case 'project_item':
      return `${spaceBase(spaceId)}/projects/items/${entityId}/relations`;
  }
}

async function fetchRelations(
  spaceId: string,
  entityType: RelationEntityType,
  entityId: string,
): Promise<Relation[]> {
  const data = await apiFetch<Relation[] | null>(relationsBase(spaceId, entityType, entityId));
  return data ?? [];
}

interface CreateRelationRequest {
  to_id: string;
  /** Target entity type. Omitted means project_item — the wire's pre-A4
   *  default, kept so old clients mean what they always meant. */
  to_type?: RelationEntityType;
  kind: string;
}

async function createRelation(
  spaceId: string,
  entityType: RelationEntityType,
  entityId: string,
  req: CreateRelationRequest,
): Promise<Relation> {
  return apiFetch<Relation>(relationsBase(spaceId, entityType, entityId), {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

async function deleteRelation(spaceId: string, relationId: string): Promise<void> {
  return apiFetch<void>(`${spaceBase(spaceId)}/projects/relations/${relationId}`, { method: 'DELETE' });
}

// ---------------------------------------------------------------------------
// Rank / search items API functions
// ---------------------------------------------------------------------------

interface RankItemRequest {
  before_id?: string;
  after_id?: string;
}

async function rankItem(spaceId: string, itemId: string, req: RankItemRequest): Promise<void> {
  return apiFetch<void>(`${spaceBase(spaceId)}/projects/items/${itemId}/rank`, {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

async function searchItems(spaceId: string, q: string): Promise<ProjectItem[]> {
  const data = await apiFetch<ProjectItem[] | null>(`${spaceBase(spaceId)}/projects/items/search?q=${encodeURIComponent(q)}`);
  return data ?? [];
}

// ---------------------------------------------------------------------------
// Sprint start / complete API functions
// ---------------------------------------------------------------------------

async function startSprint(spaceId: string, sprintId: string): Promise<Sprint> {
  return apiFetch<Sprint>(`${spaceBase(spaceId)}/projects/sprints/${sprintId}/start`, { method: 'POST' });
}

// completeSprint disposes of the sprint's incomplete items: passing a
// nextSprintId carries them over to that sprint, otherwise they return to the
// backlog. The body is omitted entirely for the backlog case so the endpoint's
// empty-body path is exercised.
async function completeSprint(spaceId: string, sprintId: string, nextSprintId?: string | null): Promise<Sprint> {
  const init: RequestInit = { method: 'POST' };
  if (nextSprintId) {
    init.body = JSON.stringify({ next_sprint_id: nextSprintId });
  }
  return apiFetch<Sprint>(`${spaceBase(spaceId)}/projects/sprints/${sprintId}/complete`, init);
}

// ---------------------------------------------------------------------------
// Roadmap API functions
// ---------------------------------------------------------------------------

async function fetchRoadmap(spaceId: string, from: string, to: string): Promise<RoadmapItem[]> {
  const params = new URLSearchParams({ from, to });
  const data = await apiFetch<RoadmapItem[] | null>(
    `${spaceBase(spaceId)}/projects/roadmap?${params.toString()}`,
  );
  return data ?? [];
}

async function fetchRoadmapOverdue(spaceId: string): Promise<RoadmapItem[]> {
  const data = await apiFetch<RoadmapItem[] | null>(`${spaceBase(spaceId)}/projects/roadmap/overdue`);
  return data ?? [];
}

async function fetchRoadmapSprints(spaceId: string): Promise<RoadmapSprint[]> {
  const data = await apiFetch<RoadmapSprint[] | null>(`${spaceBase(spaceId)}/projects/roadmap/sprints`);
  return data ?? [];
}

// ---------------------------------------------------------------------------
// Query key factories
// ---------------------------------------------------------------------------

export const queryKeys = {
  me: () => ['me'] as const,
  organization: (orgId: string) => ['organization', orgId] as const,
  space: (spaceId: string) => ['space', spaceId] as const,
  spaces: (orgId: string) => ['spaces', orgId] as const,
  tickets: (spaceId: string) => ['tickets', spaceId] as const,
  ticket: (spaceId: string, ticketId: string) => ['tickets', spaceId, ticketId] as const,
  wikiPages: (spaceId: string) => ['wikiPages', spaceId] as const,
  wikiPage: (spaceId: string, pageId: string) => ['wikiPages', spaceId, pageId] as const,
  projectItems: (spaceId: string) => ['projectItems', spaceId] as const,
  projectItem: (spaceId: string, itemId: string) => ['projectItems', spaceId, itemId] as const,
  sprints: (spaceId: string) => ['sprints', spaceId] as const,
  activeSprint: (spaceId: string) => ['sprints', spaceId, 'active'] as const,
  sprintItems: (spaceId: string, sprintId: string) => ['sprints', spaceId, sprintId, 'items'] as const,
  itemTypes: (orgId: string) => ['itemTypes', orgId] as const,
  customFields: (orgId: string) => ['customFields', orgId] as const,
  fieldScopes: (orgId: string, fieldId: string) => ['fieldScopes', orgId, fieldId] as const,
  // One form's scope rows across fields — the same data fieldScopes holds per
  // field, pivoted per (space, entity type). Its own root: nesting it under
  // fieldScopes would need a fieldId it does not have.
  formFieldScopes: (orgId: string, spaceId: string, entityType: string) =>
    ['formFieldScopes', orgId, spaceId, entityType] as const,
  entityFields: (spaceId: string, kind: FieldEntityKind, entityId: string) =>
    ['entityFields', spaceId, kind, entityId] as const,
  members: (orgId: string, spaceId: string) => ['members', orgId, spaceId] as const,
  comments: (spaceId: string, entityType: string, entityId: string) => ['comments', spaceId, entityType, entityId] as const,
  notifications: () => ['notifications'] as const,
  wikiTree: (spaceId: string) => ['wikiTree', spaceId] as const,
  wikiSearch: (spaceId: string, q: string) => ['wikiSearch', spaceId, q] as const,
  wikiRevisions: (spaceId: string, pageId: string) => ['wikiRevisions', spaceId, pageId] as const,
  wikiRevision: (spaceId: string, pageId: string, version: number) => ['wikiRevision', spaceId, pageId, version] as const,
  wikiDiff: (spaceId: string, pageId: string, from: number, to: number) => ['wikiDiff', spaceId, pageId, from, to] as const,
  orgTags: (orgId: string) => ['orgTags', orgId] as const,
  entitiesWithTag: (orgId: string, label: string) => ['entitiesWithTag', orgId, label] as const,
  // Cross-module search (P6). Every input that changes the ANSWER is in the
  // key: the same text with a different limit, cursor or snippet flag is a
  // different response, and sharing a cache entry between them is the
  // whichever-resolved-last bug queryKeys.test.ts exists to stop.
  search: (orgId: string, q: string, limit: number, cursor: string, snippet: boolean) =>
    ['search', orgId, q, limit, cursor, snippet] as const,
  entityTags: (spaceId: string, entityType: string, entityId: string) =>
    ['entityTags', spaceId, entityType, entityId] as const,
  /** The Codex document surface (issue #15). */
  pageDocument: (spaceId: string, pageId: string) => ['pageDocument', spaceId, pageId] as const,
  spaceDrafts: (spaceId: string) => ['spaceDrafts', spaceId] as const,
  relations: (spaceId: string, entityType: string, entityId: string) =>
    ['relations', spaceId, entityType, entityId] as const,
  pageSuggestions: (orgId: string, q: string) => ['pageSuggestions', orgId, q] as const,
  roadmap: (spaceId: string, from: string, to: string) => ['roadmap', spaceId, from, to] as const,
  roadmapOverdue: (spaceId: string) => ['roadmapOverdue', spaceId] as const,
  roadmapSprints: (spaceId: string) => ['roadmapSprints', spaceId] as const,
  boardConfig: (spaceId: string) => ['boardConfig', spaceId] as const,
  itemSearch: (spaceId: string, q: string) => ['itemSearch', spaceId, q] as const,
  workflowStates: (spaceId: string) => ['workflowStates', spaceId] as const,
  orgWorkflows: (orgId: string) => ['orgWorkflows', orgId] as const,
  orgWorkflowStates: (orgId: string, workflowId: string) =>
    ['orgWorkflowStates', orgId, workflowId] as const,
  // ADR-0011 tier surface. Each root is its own word rather than a nesting
  // under an existing one, because queryKeys.test.ts asserts the families are
  // pairwise disjoint — a key that prefixes another invalidates it by accident,
  // which is how Home once rendered an array where an object was expected.
  orgWorkflowTransitions: (orgId: string, workflowId: string) =>
    ['orgWorkflowTransitions', orgId, workflowId] as const,
  transitionGuards: (orgId: string, workflowId: string, transitionId: string) =>
    ['transitionGuards', orgId, workflowId, transitionId] as const,
  transitionPostFunctions: (orgId: string, workflowId: string, transitionId: string) =>
    ['transitionPostFunctions', orgId, workflowId, transitionId] as const,
  transitionApprovers: (orgId: string, workflowId: string, transitionId: string) =>
    ['transitionApprovers', orgId, workflowId, transitionId] as const,
  spacePendingApprovals: (spaceId: string) => ['spacePendingApprovals', spaceId] as const,
  entityApprovals: (spaceId: string, entityType: string, entityId: string) =>
    ['entityApprovals', spaceId, entityType, entityId] as const,
  // The offered moves depend on the entity's own state and on guards evaluated
  // against the CALLER, so the key carries the entity and is refetched after
  // every status change rather than cached across one.
  availableTransitions: (spaceId: string, entityType: string, entityId: string) =>
    ['availableTransitions', spaceId, entityType, entityId] as const,
  // Team keys nest members under ['teams', orgId] so one prefix invalidation
  // catches every membership side effect (default-team re-add, primary moves).
  teams: (orgId: string) => ['teams', orgId] as const,
  teamMembers: (orgId: string, teamId: string) => ['teams', orgId, teamId, 'members'] as const,
  spaceGrants: (orgId: string, spaceId: string) => ['spaceGrants', orgId, spaceId] as const,
  effectiveAccess: (spaceId: string, userId?: string) =>
    ['effectiveAccess', spaceId, userId ?? 'me'] as const,
  // P2.5 administration.
  orgPeople: (orgId: string) => ['orgPeople', orgId] as const,
  memberSearch: (orgId: string, q: string) => ['memberSearch', orgId, q] as const,
  ticketRefSuggestions: (orgId: string, q: string) =>
    ['ticketRefSuggestions', orgId, q] as const,
  deploymentConfig: (orgId: string) => ['deploymentConfig', orgId] as const,
  invites: (orgId: string) => ['invites', orgId] as const,
  accessMatrix: (orgId: string) => ['accessMatrix', orgId] as const,
  auditLog: (orgId: string, filter: AuditFilter) => ['auditLog', orgId, filter] as const,
  auditBatch: (orgId: string, batchId: string) => ['auditLog', orgId, 'batch', batchId] as const,
  // P3 entity shares. Share lists key by (entity_type, entity_id); the
  // standalone shared read keys by the same pair.
  entityShares: (orgId: string, entityType: ShareEntityType, entityId: string) =>
    ['entityShares', orgId, entityType, entityId] as const,
  sharedEntity: (orgId: string, entityType: ShareEntityType, entityId: string) =>
    ['sharedEntity', orgId, entityType, entityId] as const,
  sharedAttachments: (orgId: string, entityType: ShareEntityType, entityId: string) =>
    ['sharedAttachments', orgId, entityType, entityId] as const,
  entityAttachments: (orgId: string, spaceId: string, entityType: ShareEntityType, entityId: string) =>
    ['entityAttachments', orgId, spaceId, entityType, entityId] as const,
  moveShareImpact: (orgId: string, spaceId: string, pageId: string) =>
    ['moveShareImpact', orgId, spaceId, pageId] as const,
  // P4 saved views (ADR-0009, ADR-0010). One view and its result pages nest
  // under ['views', orgId] so a single prefix invalidation after a create,
  // edit or delete catches the list, the definition and every cached page —
  // an edited query whose old results stayed on screen would be showing rows
  // the view no longer selects.
  views: (orgId: string) => ['views', orgId] as const,
  view: (orgId: string, viewId: string) => ['views', orgId, viewId] as const,
  viewResults: (orgId: string, viewId: string, cursor?: string, limit?: number) =>
    ['views', orgId, viewId, 'results', cursor ?? '', limit ?? 0] as const,
  // P4 Beacon queues. Space-scoped rather than org-scoped: a queue belongs to
  // one space and its ORDER is a property of that space, so the list and every
  // queue's result pages nest under ['queues', orgId, spaceId] and one prefix
  // invalidation after a create, edit, reorder or delete catches all of them.
  // A reorder changes no query, but it does change the list this key holds.
  queues: (orgId: string, spaceId: string) => ['queues', orgId, spaceId] as const,
  queueResults: (
    orgId: string,
    spaceId: string,
    queueId: string,
    cursor?: string,
    limit?: number,
  ) => ['queues', orgId, spaceId, queueId, 'results', cursor ?? '', limit ?? 0] as const,
  // P5 dashboards (ADR-0009). One dashboard and the list nest under
  // ['dashboards', orgId] so a single prefix invalidation after a create,
  // rename, layout save or delete catches every cached copy — a stale gadget
  // list on screen is a layout somebody thinks they saved and did not.
  //
  // The third element DISCRIMINATES THE FAMILY, and it is load-bearing rather
  // than decorative. Without it `dashboards(orgId, 'home')` — the sidebar's
  // list of Home dashboards — and `homeDashboard(orgId)` — the resolved Home
  // dashboard with its gadgets — produced the identical key, so whichever
  // resolved last overwrote the other in the cache and Home rendered an ARRAY
  // where it expected an object. It cost a blank page. queryKeys.test.ts now
  // asserts the three families are pairwise disjoint.
  dashboards: (orgId: string, module?: string) =>
    ['dashboards', orgId, 'list', module ?? ''] as const,
  dashboard: (orgId: string, dashboardId: string) =>
    ['dashboards', orgId, 'one', dashboardId] as const,
  homeDashboard: (orgId: string) => ['dashboards', orgId, 'home'] as const,
  // A gadget's data is keyed by the DOCUMENT it resolves, not by the gadget
  // id: two gadgets showing the same view share one cache entry, and a gadget
  // repointed at another view gets a new one without an explicit invalidation.
  gadgetResults: (orgId: string, queryJSON: string, limit: number) =>
    ['gadget', orgId, 'results', queryJSON, limit] as const,
  gadgetAggregate: (orgId: string, queryJSON: string, groupBy: string) =>
    ['gadget', orgId, 'aggregate', queryJSON, groupBy] as const,
  // Customer portal (migration 044). Everything nests under
  // ['portal', portalKey] so one prefix invalidation clears a portal on sign
  // out, and the describe key sits beside the requester-scoped ones rather
  // than in its own family — it is the same portal.
  //
  // THE REQUESTER'S EMAIL IS IN EVERY AUTHENTICATED KEY, and it is not
  // decoration. One QueryClient serves the whole app and survives a portal
  // sign-out, so without the email requester A's cached list is handed
  // straight to requester B after a sign-out and sign-in on the same machine —
  // a shared terminal, or simply two people on one household laptop. It fails
  // closed nowhere: the data is already in memory, so no request is made and
  // no 401 corrects it. `describe` carries no email because it is the
  // portal's public face and identical for everyone.
  /**
   * The invalidation prefix the other three nest under. Not a query of its
   * own — it exists so sign-out and redeem can drop a whole portal's cache
   * without a literal `['portal', portalKey]` at the call site.
   */
  portal: (portalKey: string) => ['portal', portalKey] as const,
  portalDescribe: (portalKey: string) => ['portal', portalKey, 'describe'] as const,
  portalRequests: (portalKey: string, requesterEmail: string) =>
    ['portal', portalKey, requesterEmail, 'requests', 'list'] as const,
  portalRequest: (portalKey: string, requesterEmail: string, reference: string) =>
    ['portal', portalKey, requesterEmail, 'requests', 'one', reference] as const,
  // The AGENT-side portal configuration (v0.4.2 A1). Its own root,
  // deliberately NOT nested under ['portal', …]: that family is the CUSTOMER
  // cache, keyed by portal key and removed wholesale on portal sign-out —
  // nesting the agent's view under it would let a customer-side sign-out (or
  // any customer prefix invalidation) blow away agent state, and vice versa.
  // Keyed by org and space because that is how the agent addresses the
  // resource; the portal key is something this query LEARNS, so it cannot be
  // in the key. queryKeys.test.ts pins the isolation.
  portalAdmin: (orgId: string, spaceId: string) => ['portalAdmin', orgId, spaceId] as const,
} as const;

// ---------------------------------------------------------------------------
// Query hooks
// ---------------------------------------------------------------------------

type QueryOpts<T> = Omit<UseQueryOptions<T, APIError>, 'queryKey' | 'queryFn'>;

export function useMe(opts?: QueryOpts<User>) {
  return useQuery<User, APIError>({
    queryKey: queryKeys.me(),
    queryFn: fetchMe,
    ...opts,
  });
}

export function useUpdateProfile() {
  const queryClient = useQueryClient();
  return useMutation<User, APIError, { display_name?: string; email?: string }>({
    mutationFn: (body) => apiFetch<User>('/auth/me', { method: 'PATCH', body: JSON.stringify(body) }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.me() }),
  });
}

export function useOrganization(orgId: string, opts?: QueryOpts<Organization>) {
  return useQuery<Organization, APIError>({
    queryKey: queryKeys.organization(orgId),
    queryFn: () => fetchOrganization(orgId),
    enabled: !!orgId,
    ...opts,
  });
}

export function useSpaces(orgId: string, opts?: QueryOpts<Space[]>) {
  return useQuery<Space[], APIError>({
    queryKey: queryKeys.spaces(orgId),
    queryFn: () => fetchSpaces(orgId),
    enabled: !!orgId,
    ...opts,
  });
}

export function useSpace(spaceId: string, opts?: QueryOpts<Space>) {
  return useQuery<Space, APIError>({
    queryKey: queryKeys.space(spaceId),
    queryFn: () => fetchSpace(spaceId),
    enabled: !!spaceId,
    ...opts,
  });
}

export function useTickets(spaceId: string, opts?: QueryOpts<Ticket[]>) {
  return useQuery<Ticket[], APIError>({
    queryKey: queryKeys.tickets(spaceId),
    queryFn: () => fetchTickets(spaceId),
    enabled: !!spaceId,
    ...opts,
  });
}

export function useTicket(spaceId: string, ticketId: string, opts?: QueryOpts<Ticket>) {
  return useQuery<Ticket, APIError>({
    queryKey: queryKeys.ticket(spaceId, ticketId),
    queryFn: () => fetchTicket(spaceId, ticketId),
    enabled: !!spaceId && !!ticketId,
    ...opts,
  });
}

export function useWikiPages(spaceId: string, opts?: QueryOpts<WikiPage[]>) {
  return useQuery<WikiPage[], APIError>({
    queryKey: queryKeys.wikiPages(spaceId),
    queryFn: () => fetchWikiPages(spaceId),
    enabled: !!spaceId,
    ...opts,
  });
}

export function useWikiPage(spaceId: string, pageId: string, opts?: QueryOpts<WikiPage>) {
  return useQuery<WikiPage, APIError>({
    queryKey: queryKeys.wikiPage(spaceId, pageId),
    queryFn: () => fetchWikiPage(spaceId, pageId),
    enabled: !!spaceId && !!pageId,
    ...opts,
  });
}

export function useProjectItems(spaceId: string, opts?: QueryOpts<ProjectItem[]>) {
  return useQuery<ProjectItem[], APIError>({
    queryKey: queryKeys.projectItems(spaceId),
    queryFn: () => fetchProjectItems(spaceId),
    enabled: !!spaceId,
    ...opts,
  });
}

export function useProjectItem(spaceId: string, itemId: string, opts?: QueryOpts<ProjectItem>) {
  return useQuery<ProjectItem, APIError>({
    queryKey: queryKeys.projectItem(spaceId, itemId),
    queryFn: () => fetchProjectItem(spaceId, itemId),
    enabled: !!spaceId && !!itemId,
    ...opts,
  });
}

export function useSprints(spaceId: string, opts?: QueryOpts<Sprint[]>) {
  return useQuery<Sprint[], APIError>({
    queryKey: queryKeys.sprints(spaceId),
    queryFn: () => fetchSprints(spaceId),
    enabled: !!spaceId,
    ...opts,
  });
}

export function useActiveSprint(spaceId: string, opts?: QueryOpts<Sprint | null>) {
  return useQuery<Sprint | null, APIError>({
    queryKey: queryKeys.activeSprint(spaceId),
    queryFn: () => fetchActiveSprint(spaceId),
    enabled: !!spaceId,
    ...opts,
  });
}

export function useSprintItems(spaceId: string, sprintId: string, opts?: QueryOpts<ProjectItem[]>) {
  return useQuery<ProjectItem[], APIError>({
    queryKey: queryKeys.sprintItems(spaceId, sprintId),
    queryFn: () => fetchSprintItems(spaceId, sprintId),
    enabled: !!spaceId && !!sprintId,
    ...opts,
  });
}

export function useItemTypes(orgId: string, opts?: QueryOpts<ItemType[]>) {
  return useQuery<ItemType[], APIError>({
    queryKey: queryKeys.itemTypes(orgId),
    queryFn: () => fetchItemTypes(orgId),
    enabled: !!orgId,
    ...opts,
  });
}

export function useCreateItemType(orgId: string) {
  const queryClient = useQueryClient();
  return useMutation<ItemType, APIError, CreateItemTypeRequest>({
    mutationFn: (req) => createItemType(orgId, req),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.itemTypes(orgId) });
    },
  });
}

export function useUpdateItemType(orgId: string) {
  const queryClient = useQueryClient();
  return useMutation<ItemType, APIError, { typeId: string; req: UpdateItemTypeRequest }>({
    mutationFn: ({ typeId, req }) => updateItemType(orgId, typeId, req),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.itemTypes(orgId) });
    },
  });
}

export function useDeleteItemType(orgId: string) {
  const queryClient = useQueryClient();
  return useMutation<void, APIError, string>({
    mutationFn: (typeId) => deleteItemType(orgId, typeId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.itemTypes(orgId) });
    },
  });
}

export function useCustomFields(orgId: string, opts?: QueryOpts<CustomFieldDef[]>) {
  return useQuery<CustomFieldDef[], APIError>({
    queryKey: queryKeys.customFields(orgId),
    queryFn: () => fetchCustomFields(orgId),
    enabled: !!orgId,
    ...opts,
  });
}

export function useCreateCustomField(orgId: string) {
  const queryClient = useQueryClient();
  return useMutation<CustomFieldDef, APIError, CreateCustomFieldRequest>({
    mutationFn: (req) => createCustomField(orgId, req),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.customFields(orgId) }),
  });
}

export function useUpdateCustomField(orgId: string) {
  const queryClient = useQueryClient();
  return useMutation<CustomFieldDef, APIError, { fieldId: string; req: UpdateCustomFieldRequest }>({
    mutationFn: ({ fieldId, req }) => updateCustomField(orgId, fieldId, req),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.customFields(orgId) }),
  });
}

export function useDeleteCustomField(orgId: string) {
  const queryClient = useQueryClient();
  return useMutation<void, APIError, string>({
    mutationFn: (fieldId) => deleteCustomField(orgId, fieldId),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.customFields(orgId) }),
  });
}

export function useFieldScopes(orgId: string, fieldId: string, opts?: QueryOpts<CustomFieldScope[]>) {
  return useQuery<CustomFieldScope[], APIError>({
    queryKey: queryKeys.fieldScopes(orgId, fieldId),
    queryFn: () => fetchFieldScopes(orgId, fieldId),
    enabled: !!orgId && !!fieldId,
    ...opts,
  });
}

export function useSetFieldScope(orgId: string, fieldId: string) {
  const queryClient = useQueryClient();
  return useMutation<CustomFieldScope, APIError, { spaceId: string; entityType: 'project_item' | 'ticket'; required: boolean }>({
    mutationFn: ({ spaceId, entityType, required }) => putFieldScope(orgId, fieldId, spaceId, entityType, required),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.fieldScopes(orgId, fieldId) }),
  });
}

export function useRemoveFieldScope(orgId: string, fieldId: string) {
  const queryClient = useQueryClient();
  return useMutation<void, APIError, { spaceId: string; entityType: 'project_item' | 'ticket' }>({
    mutationFn: ({ spaceId, entityType }) => deleteFieldScope(orgId, fieldId, spaceId, entityType),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.fieldScopes(orgId, fieldId) }),
  });
}

export function useFormFieldScopes(
  orgId: string,
  spaceId: string,
  entityType: 'project_item' | 'ticket',
  opts?: QueryOpts<CustomFieldScope[]>,
) {
  return useQuery<CustomFieldScope[], APIError>({
    queryKey: queryKeys.formFieldScopes(orgId, spaceId, entityType),
    queryFn: () => fetchFormFieldScopes(orgId, spaceId, entityType),
    enabled: !!orgId && !!spaceId,
    ...opts,
  });
}

export function useReorderFormFields(
  orgId: string,
  spaceId: string,
  entityType: 'project_item' | 'ticket',
) {
  const queryClient = useQueryClient();
  return useMutation<CustomFieldScope[], APIError, string[]>({
    mutationFn: (fieldIds) => putFormFieldOrder(orgId, spaceId, entityType, fieldIds),
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: queryKeys.formFieldScopes(orgId, spaceId, entityType),
      }),
  });
}

export function useEntityFields(
  spaceId: string,
  kind: FieldEntityKind,
  entityId: string,
  opts?: QueryOpts<EntityCustomField[]>,
) {
  return useQuery<EntityCustomField[], APIError>({
    queryKey: queryKeys.entityFields(spaceId, kind, entityId),
    queryFn: () => fetchEntityFields(spaceId, kind, entityId),
    enabled: !!spaceId && !!entityId,
    ...opts,
  });
}

export function useSetEntityField(spaceId: string, kind: FieldEntityKind, entityId: string) {
  const queryClient = useQueryClient();
  return useMutation<void, APIError, { slug: string; value: string }>({
    mutationFn: ({ slug, value }) => setEntityField(spaceId, kind, entityId, slug, value),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.entityFields(spaceId, kind, entityId) }),
  });
}

export function useMembers(orgId: string, spaceId: string, opts?: QueryOpts<Member[]>) {
  return useQuery<Member[], APIError>({
    queryKey: queryKeys.members(orgId, spaceId),
    queryFn: () => fetchMembers(orgId, spaceId),
    enabled: !!orgId && !!spaceId,
    retry: (failureCount, error) => {
      if (error?.status === 404) return false;
      return failureCount < 2;
    },
    staleTime: 30000,
    ...opts,
  });
}

export function useComments(orgId: string, spaceId: string, entityType: string, entityId: string, opts?: QueryOpts<Comment[]>) {
  return useQuery<Comment[], APIError>({
    queryKey: queryKeys.comments(spaceId, entityType, entityId),
    queryFn: () => fetchComments(orgId, spaceId, entityType, entityId),
    enabled: !!orgId && !!spaceId && !!entityType && !!entityId,
    retry: (failureCount, error) => {
      if (error?.status === 404) return false;
      return failureCount < 2;
    },
    ...opts,
  });
}


export function useNotifications(opts?: QueryOpts<NotificationListResponse>) {
  return useQuery<NotificationListResponse, APIError>({
    queryKey: queryKeys.notifications(),
    queryFn: listNotifications,
    refetchInterval: 30_000,
    ...opts,
  });
}

// ---------------------------------------------------------------------------
// Mutation hooks
// ---------------------------------------------------------------------------

export function useLogin() {
  return useMutation<AuthResponse, APIError, LoginRequest>({
    mutationFn: loginUser,
    onSuccess: (data) => {
      setToken(data.access_token);
      setRefreshToken(data.refresh_token);
    },
  });
}

export function useRegister() {
  return useMutation<AuthResponse, APIError, RegisterRequest>({
    mutationFn: registerUser,
    onSuccess: (data) => {
      setToken(data.access_token);
      setRefreshToken(data.refresh_token);
    },
  });
}

export function useUpdateOrganization(orgId: string) {
  const queryClient = useQueryClient();
  return useMutation<Organization, APIError, UpdateOrganizationRequest>({
    mutationFn: (req) => updateOrganization(orgId, req),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.organization(orgId) });
    },
  });
}

export function useCreateSpace(orgId: string) {
  const queryClient = useQueryClient();
  return useMutation<Space, APIError, CreateSpaceRequest & { ticketRef?: string }>({
    mutationFn: ({ ticketRef, ...req }) => createSpace(orgId, req, ticketRef),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.spaces(orgId) });
    },
  });
}

export function useCreateTicket(spaceId: string) {
  const queryClient = useQueryClient();
  return useMutation<Ticket, APIError, CreateTicketRequest>({
    mutationFn: (req) => createTicket(spaceId, req),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.tickets(spaceId) });
    },
  });
}

export function useUpdateTicket(spaceId: string, ticketId: string) {
  const queryClient = useQueryClient();
  return useMutation<Ticket, APIError, UpdateTicketRequest>({
    mutationFn: (req) => updateTicket(spaceId, ticketId, req),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.tickets(spaceId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.ticket(spaceId, ticketId) });
    },
  });
}

export function useTransitionTicketStatus(spaceId: string, ticketId: string) {
  const queryClient = useQueryClient();
  return useMutation<Ticket, APIError, TicketStatus>({
    mutationFn: (status) => transitionTicketStatus(spaceId, ticketId, status),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.tickets(spaceId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.ticket(spaceId, ticketId) });
      // A gated transition CREATES an approval request, so the block that
      // renders it must re-read. This lives HERE rather than in the page for a
      // reason worth keeping: a component-level useQueryClient throws "No
      // QueryClient set" in every page test that replaces lib/api wholesale —
      // which is all of them, including the customer portal's — and those
      // harnesses deliberately mount no provider. Invalidating where the
      // mutation already holds a client costs the page nothing and keeps the
      // sibling test suites working.
      queryClient.invalidateQueries({
        queryKey: queryKeys.entityApprovals(spaceId, 'ticket', ticketId),
      });
    },
  });
}

/**
 * Status transition for boards where the ticket varies per call (kanban drag).
 * Optimistically moves the ticket in the cached list so the card lands in its
 * new column immediately, and rolls back if the API rejects the transition.
 */
export function useTicketStatusTransition(spaceId: string) {
  const queryClient = useQueryClient();
  return useMutation<Ticket, APIError, { ticketId: string; status: TicketStatus }, { previous?: Ticket[] }>({
    mutationFn: ({ ticketId, status }) => transitionTicketStatus(spaceId, ticketId, status),
    onMutate: async ({ ticketId, status }) => {
      await queryClient.cancelQueries({ queryKey: queryKeys.tickets(spaceId) });
      const previous = queryClient.getQueryData<Ticket[]>(queryKeys.tickets(spaceId));
      if (previous) {
        queryClient.setQueryData<Ticket[]>(
          queryKeys.tickets(spaceId),
          previous.map((t) => (t.id === ticketId ? { ...t, status } : t)),
        );
      }
      return { previous };
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.previous) {
        queryClient.setQueryData(queryKeys.tickets(spaceId), ctx.previous);
      }
    },
    onSettled: (_data, _err, { ticketId }) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.tickets(spaceId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.ticket(spaceId, ticketId) });
    },
  });
}

export function useCreateWikiPage(spaceId: string) {
  const queryClient = useQueryClient();
  return useMutation<WikiPage, APIError, CreateWikiPageRequest>({
    mutationFn: (req) => createWikiPage(spaceId, req),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.wikiPages(spaceId) });
    },
  });
}

export function useUpdateWikiPage(spaceId: string, pageId: string) {
  const queryClient = useQueryClient();
  return useMutation<WikiPage, APIError, UpdateWikiPageRequest>({
    mutationFn: (req) => updateWikiPage(spaceId, pageId, req),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.wikiPages(spaceId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.wikiPage(spaceId, pageId) });
    },
  });
}

// ---------------------------------------------------------------------------
// Codex document hooks (issue #15 / ADR-0012)
// ---------------------------------------------------------------------------

/**
 * usePageDocument reads a page's editable document.
 *
 * `staleTime: Infinity` is load-bearing rather than a tuning choice. The
 * response carries `base_version` and the preservation ids that were minted
 * against it, and the editor's unsaved state is keyed to both. A background
 * refetch that swapped them under an open editor would make the next publish
 * resolve placeholders against a document the author never saw. The page view
 * invalidates this key deliberately after a publish, which is the only moment
 * a new base exists.
 */
export function usePageDocument(
  spaceId: string,
  pageId: string,
  opts?: QueryOpts<CodexEditableDocument>,
) {
  return useQuery<CodexEditableDocument, APIError>({
    queryKey: queryKeys.pageDocument(spaceId, pageId),
    queryFn: () => fetchPageDocument(spaceId, pageId),
    enabled: !!spaceId && !!pageId,
    staleTime: Infinity,
    ...opts,
  });
}

/** The pages in this space on which the caller holds an unpublished draft. */
export function useSpaceDrafts(spaceId: string, opts?: QueryOpts<CodexDraftSummary[]>) {
  return useQuery<CodexDraftSummary[], APIError>({
    queryKey: queryKeys.spaceDrafts(spaceId),
    queryFn: () => fetchSpaceDrafts(spaceId),
    enabled: !!spaceId,
    ...opts,
  });
}

/**
 * useSavePageDraft is the autosave.
 *
 * It deliberately invalidates only the space's draft list — the badge that
 * says "you have unpublished changes here". Invalidating the document key
 * would refetch the editor's own base out from under it on every keystroke
 * batch.
 */
export function useSavePageDraft(spaceId: string, pageId: string) {
  const queryClient = useQueryClient();
  return useMutation<CodexDraftDocument, APIError, SavePageDraftRequest>({
    mutationFn: (req) => savePageDraft(spaceId, pageId, req),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.spaceDrafts(spaceId) });
    },
  });
}

export function useDiscardPageDraft(spaceId: string, pageId: string) {
  const queryClient = useQueryClient();
  return useMutation<void, APIError, void>({
    mutationFn: () => discardPageDraft(spaceId, pageId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.spaceDrafts(spaceId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.pageDocument(spaceId, pageId) });
    },
  });
}

/**
 * usePublishPage publishes a document.
 *
 * The error type is `Error`, not `APIError`, because two of this route's
 * failures are not APIErrors: {@link PublishConflictError} and
 * {@link PublishLostContentError} each carry a body the UI has to render
 * rather than a message it can show.
 */
export function usePublishPage(spaceId: string, pageId: string) {
  const queryClient = useQueryClient();
  return useMutation<WikiPage, Error, PublishPageRequest>({
    mutationFn: (req) => publishPage(spaceId, pageId, req),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.wikiPages(spaceId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.wikiPage(spaceId, pageId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.wikiTree(spaceId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.pageDocument(spaceId, pageId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.spaceDrafts(spaceId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.wikiRevisions(spaceId, pageId) });
      // Publishing can CREATE tags: the server walks the document and adds a
      // tag for every inline `#tag` in the body. Without this the chip a
      // publish just produced does not appear until the next reload, because
      // the reading surface remounts against the cached pre-publish list —
      // which is empty, so it renders nothing at all and looks like the
      // aggregation never ran.
      queryClient.invalidateQueries({ queryKey: queryKeys.entityTags(spaceId, 'page', pageId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.orgTags(getCurrentOrgId()) });
    },
  });
}

export function useUploadPageImage(spaceId: string, pageId: string) {
  return useMutation<CodexPageImage, APIError, File>({
    mutationFn: (file) => uploadPageImage(spaceId, pageId, file),
  });
}

export function useCreateProjectItem(spaceId: string) {
  const queryClient = useQueryClient();
  return useMutation<ProjectItem, APIError, CreateProjectItemRequest>({
    mutationFn: (req) => createProjectItem(spaceId, req),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.projectItems(spaceId) });
    },
  });
}

export function useUpdateProjectItem(spaceId: string, itemId: string) {
  const queryClient = useQueryClient();
  return useMutation<ProjectItem, APIError, UpdateProjectItemRequest>({
    mutationFn: (req) => updateProjectItem(spaceId, itemId, req),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.projectItems(spaceId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.projectItem(spaceId, itemId) });
    },
  });
}

export function useTransitionProjectItemStatus(spaceId: string, itemId: string) {
  const queryClient = useQueryClient();
  return useMutation<ProjectItem, APIError, string>({
    mutationFn: (status) => transitionProjectItemStatus(spaceId, itemId, status),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.projectItems(spaceId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.projectItem(spaceId, itemId) });
      // A gated transition CREATES an approval request, so the block that
      // renders it must re-read. This lives HERE rather than in the page for a
      // reason worth keeping: a component-level useQueryClient throws "No
      // QueryClient set" in every page test that replaces lib/api wholesale —
      // which is all of them, including the customer portal's — and those
      // harnesses deliberately mount no provider. Invalidating where the
      // mutation already holds a client costs the page nothing and keeps the
      // sibling test suites working.
      queryClient.invalidateQueries({
        queryKey: queryKeys.entityApprovals(spaceId, 'item', itemId),
      });
    },
  });
}

export function useCreateComment(orgId: string, spaceId: string, entityType: string, entityId: string) {
  const queryClient = useQueryClient();
  return useMutation<Comment, APIError, CreateCommentRequest>({
    mutationFn: (req) => createComment(orgId, spaceId, entityType, entityId, req),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.comments(spaceId, entityType, entityId) });
    },
  });
}

export function useMarkNotificationRead() {
  const queryClient = useQueryClient();
  return useMutation<void, APIError, string>({
    mutationFn: (id) => markNotificationRead(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.notifications() });
    },
  });
}

export function useMarkAllNotificationsRead() {
  const queryClient = useQueryClient();
  return useMutation<void, APIError, void>({
    mutationFn: () => markAllNotificationsRead(),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.notifications() });
    },
  });
}

export function useWikiTree(spaceId: string, opts?: QueryOpts<WikiTreeNode[]>) {
  return useQuery<WikiTreeNode[], APIError>({
    queryKey: queryKeys.wikiTree(spaceId),
    queryFn: () => fetchWikiTree(spaceId),
    enabled: !!spaceId,
    ...opts,
  });
}

export function useWikiSearch(spaceId: string, q: string, opts?: QueryOpts<WikiPage[]>) {
  return useQuery<WikiPage[], APIError>({
    queryKey: queryKeys.wikiSearch(spaceId, q),
    queryFn: () => searchWikiPages(spaceId, q),
    enabled: !!spaceId && q.length > 0,
    ...opts,
  });
}

export function useWikiRevisions(spaceId: string, pageId: string, opts?: QueryOpts<WikiRevision[]>) {
  return useQuery<WikiRevision[], APIError>({
    queryKey: queryKeys.wikiRevisions(spaceId, pageId),
    queryFn: () => fetchWikiRevisions(spaceId, pageId),
    enabled: !!spaceId && !!pageId,
    ...opts,
  });
}

export function useWikiRevision(spaceId: string, pageId: string, version: number, opts?: QueryOpts<WikiPage>) {
  return useQuery<WikiPage, APIError>({
    queryKey: queryKeys.wikiRevision(spaceId, pageId, version),
    queryFn: () => fetchWikiRevision(spaceId, pageId, version),
    enabled: !!spaceId && !!pageId && version > 0,
    ...opts,
  });
}

export function useWikiDiff(
  spaceId: string,
  pageId: string,
  from: number,
  to: number,
  opts?: QueryOpts<WikiRevisionDiff>,
) {
  return useQuery<WikiRevisionDiff, APIError>({
    queryKey: queryKeys.wikiDiff(spaceId, pageId, from, to),
    queryFn: () => fetchWikiDiff(spaceId, pageId, from, to),
    // Two DIFFERENT versions, and both real. `from === to` would ask the server
    // to diff a revision against itself, which is a request with no answer
    // worth rendering.
    enabled: !!spaceId && !!pageId && from > 0 && to > 0 && from !== to,
    ...opts,
  });
}

/**
 * Restoring an earlier version.
 *
 * The invalidation list is the publish list, because a restore IS a publish:
 * it bumps the version, writes a revision and changes what a reader sees. A
 * shorter list would leave the page's own cache showing the version it had
 * before the restore.
 */
export function useRestoreWikiRevision(spaceId: string, pageId: string) {
  const queryClient = useQueryClient();
  return useMutation<
    RestoreRevisionResult,
    Error,
    { version: number } & RestoreRevisionRequest
  >({
    mutationFn: ({ version, ...req }) => restoreWikiRevision(spaceId, pageId, version, req),
    onSuccess: (result) => {
      // A no-op restore changed nothing, so there is nothing to invalidate and
      // refetching would only make the panel flicker.
      if (!result.restored) return;
      queryClient.invalidateQueries({ queryKey: queryKeys.wikiPages(spaceId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.wikiPage(spaceId, pageId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.wikiTree(spaceId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.pageDocument(spaceId, pageId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.wikiRevisions(spaceId, pageId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.entityTags(spaceId, 'page', pageId) });
    },
  });
}

// ---------------------------------------------------------------------------
// Entity tags (migrations 040, 055)
// ---------------------------------------------------------------------------

export function useOrgTags(orgId: string, opts?: QueryOpts<CodexTag[]>) {
  return useQuery<CodexTag[], APIError>({
    queryKey: queryKeys.orgTags(orgId),
    queryFn: () => fetchOrgTags(orgId),
    enabled: !!orgId,
    ...opts,
  });
}

export function useEntitiesWithTag(orgId: string, label: string, opts?: QueryOpts<TaggedEntities>) {
  return useQuery<TaggedEntities, APIError>({
    queryKey: queryKeys.entitiesWithTag(orgId, label),
    queryFn: () => fetchEntitiesWithTag(orgId, label),
    enabled: !!orgId && !!label,
    ...opts,
  });
}

export function useEntityTags(
  entityType: TagEntityType,
  spaceId: string,
  entityId: string,
  opts?: QueryOpts<CodexTag[]>,
) {
  return useQuery<CodexTag[], APIError>({
    queryKey: queryKeys.entityTags(spaceId, entityType, entityId),
    queryFn: () => fetchEntityTags(entityType, spaceId, entityId),
    enabled: !!spaceId && !!entityId,
    ...opts,
  });
}

/**
 * Setting an entity's tags — the authoritative path, which can remove.
 *
 * The org tag list is invalidated too, because setting a tag nobody has used
 * before creates it: tags have no administration surface and come into
 * existence by use, so this mutation is also the only constructor there is.
 */
export function useSetEntityTags(entityType: TagEntityType, spaceId: string, entityId: string) {
  const queryClient = useQueryClient();
  return useMutation<CodexTag[], APIError, string[]>({
    mutationFn: (labels) => setEntityTags(entityType, spaceId, entityId, labels),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: queryKeys.entityTags(spaceId, entityType, entityId),
      });
      queryClient.invalidateQueries({ queryKey: queryKeys.orgTags(getCurrentOrgId()) });
    },
  });
}

export function useRelations(
  spaceId: string,
  entityType: RelationEntityType,
  entityId: string,
  opts?: QueryOpts<Relation[]>,
) {
  return useQuery<Relation[], APIError>({
    queryKey: queryKeys.relations(spaceId, entityType, entityId),
    queryFn: () => fetchRelations(spaceId, entityType, entityId),
    enabled: !!spaceId && !!entityId,
    ...opts,
  });
}

export function useRoadmap(
  spaceId: string,
  from: string,
  to: string,
  opts?: QueryOpts<RoadmapItem[]>,
) {
  return useQuery<RoadmapItem[], APIError>({
    queryKey: queryKeys.roadmap(spaceId, from, to),
    queryFn: () => fetchRoadmap(spaceId, from, to),
    enabled: !!spaceId && !!from && !!to,
    ...opts,
  });
}

export function useRoadmapOverdue(spaceId: string, opts?: QueryOpts<RoadmapItem[]>) {
  return useQuery<RoadmapItem[], APIError>({
    queryKey: queryKeys.roadmapOverdue(spaceId),
    queryFn: () => fetchRoadmapOverdue(spaceId),
    enabled: !!spaceId,
    ...opts,
  });
}

export function useRoadmapSprints(spaceId: string, opts?: QueryOpts<RoadmapSprint[]>) {
  return useQuery<RoadmapSprint[], APIError>({
    queryKey: queryKeys.roadmapSprints(spaceId),
    queryFn: () => fetchRoadmapSprints(spaceId),
    enabled: !!spaceId,
    ...opts,
  });
}

export function useItemSearch(spaceId: string, q: string, opts?: QueryOpts<ProjectItem[]>) {
  return useQuery<ProjectItem[], APIError>({
    queryKey: queryKeys.itemSearch(spaceId, q),
    queryFn: () => searchItems(spaceId, q),
    enabled: !!spaceId && q.length > 0,
    ...opts,
  });
}

export function useAssignTicket(spaceId: string, ticketId: string) {
  const queryClient = useQueryClient();
  return useMutation<void, APIError, string | null>({
    mutationFn: (assigneeId) => assignTicket(spaceId, ticketId, assigneeId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.ticket(spaceId, ticketId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.tickets(spaceId) });
    },
  });
}

export function useMoveWikiPage(spaceId: string, pageId: string) {
  const queryClient = useQueryClient();
  return useMutation<MoveWikiPageResult, APIError, MoveWikiPageRequest>({
    mutationFn: (req) => moveWikiPage(spaceId, pageId, req),
    onSuccess: (_res, req) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.wikiTree(spaceId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.wikiPages(spaceId) });
      // A cross-space move revokes shares — refresh both spaces' badges.
      queryClient.invalidateQueries({ queryKey: ['spacePageShares'] });
      if (req.target_space_id) {
        queryClient.invalidateQueries({ queryKey: queryKeys.wikiTree(req.target_space_id) });
        queryClient.invalidateQueries({ queryKey: queryKeys.wikiPages(req.target_space_id) });
      }
    },
  });
}

export function useCreateRelation(spaceId: string, entityType: RelationEntityType, entityId: string) {
  const queryClient = useQueryClient();
  return useMutation<Relation, APIError, CreateRelationRequest>({
    mutationFn: (req) => createRelation(spaceId, entityType, entityId, req),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.relations(spaceId, entityType, entityId) });
    },
  });
}

export function useDeleteRelation(spaceId: string, entityType: RelationEntityType, entityId: string) {
  const queryClient = useQueryClient();
  return useMutation<void, APIError, string>({
    mutationFn: (relationId) => deleteRelation(spaceId, relationId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.relations(spaceId, entityType, entityId) });
    },
  });
}

export function useRankItem(spaceId: string) {
  const queryClient = useQueryClient();
  return useMutation<void, APIError, { itemId: string } & RankItemRequest>({
    mutationFn: ({ itemId, ...req }) => rankItem(spaceId, itemId, req),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.projectItems(spaceId) });
    },
  });
}


// --- Board configuration hooks (W4) ---

export function useBoardConfig(spaceId: string, opts?: QueryOpts<BoardConfig>) {
  return useQuery<BoardConfig, APIError>({
    queryKey: queryKeys.boardConfig(spaceId),
    queryFn: () => fetchBoardConfig(spaceId),
    enabled: !!spaceId && (opts?.enabled ?? true),
  });
}

export function useSaveBoardConfig(spaceId: string) {
  const queryClient = useQueryClient();
  return useMutation<BoardConfig, APIError, SaveBoardConfigRequest>({
    mutationFn: (req) => saveBoardConfig(spaceId, req),
    onSuccess: (cfg) => {
      // Write through rather than only invalidating: the settings surface
      // re-renders from this immediately, and the board picks it up on its
      // next read.
      queryClient.setQueryData(queryKeys.boardConfig(spaceId), cfg);
      queryClient.invalidateQueries({ queryKey: queryKeys.boardConfig(spaceId) });
    },
  });
}

export function useResetBoardConfig(spaceId: string) {
  const queryClient = useQueryClient();
  return useMutation<BoardConfig, APIError, void>({
    mutationFn: () => resetBoardConfig(spaceId),
    onSuccess: (cfg) => {
      queryClient.setQueryData(queryKeys.boardConfig(spaceId), cfg);
      queryClient.invalidateQueries({ queryKey: queryKeys.boardConfig(spaceId) });
    },
  });
}

export function useDeleteBoardColumn(spaceId: string) {
  const queryClient = useQueryClient();
  return useMutation<BoardConfig, APIError, { columnId: string; remapTo: string }>({
    mutationFn: ({ columnId, remapTo }) => deleteBoardColumn(spaceId, columnId, remapTo),
    onSuccess: (cfg) => {
      queryClient.setQueryData(queryKeys.boardConfig(spaceId), cfg);
      queryClient.invalidateQueries({ queryKey: queryKeys.boardConfig(spaceId) });
    },
  });
}

export interface AssignSprintVars {
  itemId: string;
  /** null returns the item to the backlog. */
  sprintId: string | null;
}

// useAssignItemSprint re-sprints one item. Re-sprinting changes which sprint
// owns the item, so every list keyed on sprint membership is stale afterwards:
// the backlog grouping, the board's active-sprint items, the per-sprint lists,
// and the roadmap's sprint spans.
export function useAssignItemSprint(spaceId: string) {
  const queryClient = useQueryClient();
  return useMutation<void, APIError, AssignSprintVars>({
    mutationFn: ({ itemId, sprintId }) => assignItemToSprint(spaceId, itemId, sprintId),
    onSuccess: () => {
      // projectItems is a key prefix of projectItem, so this one invalidation
      // covers the list and the single-item detail query together.
      queryClient.invalidateQueries({ queryKey: queryKeys.projectItems(spaceId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.sprints(spaceId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.roadmapSprints(spaceId) });
    },
  });
}

export function useStartSprint(spaceId: string) {
  const queryClient = useQueryClient();
  return useMutation<Sprint, APIError, string>({
    mutationFn: (sprintId) => startSprint(spaceId, sprintId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.sprints(spaceId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.activeSprint(spaceId) });
    },
  });
}

export interface CompleteSprintVars {
  sprintId: string;
  // Carry-over target for incomplete items; omit/null to return them to the backlog.
  nextSprintId?: string | null;
}

export function useCompleteSprint(spaceId: string) {
  const queryClient = useQueryClient();
  return useMutation<Sprint, APIError, CompleteSprintVars>({
    mutationFn: ({ sprintId, nextSprintId }) => completeSprint(spaceId, sprintId, nextSprintId),
    onSuccess: () => {
      // Completion moves items off the sprint, so the item lists (backlog,
      // board, per-sprint) and the sprint lists all refresh. The 'sprints'
      // prefix covers activeSprint and sprintItems too.
      queryClient.invalidateQueries({ queryKey: queryKeys.sprints(spaceId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.projectItems(spaceId) });
    },
  });
}

export function useCreateSprint(spaceId: string) {
  const queryClient = useQueryClient();
  return useMutation<Sprint, APIError, CreateSprintRequest>({
    mutationFn: (req) => createSprint(spaceId, req),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.sprints(spaceId) });
    },
  });
}

async function fetchWorkflowStates(spaceId: string): Promise<WorkflowState[]> {
  return apiFetch<WorkflowState[]>(`${spaceBase(spaceId)}/workflow/states`);
}

export function useWorkflowStates(spaceId: string, opts?: QueryOpts<WorkflowState[]>) {
  return useQuery<WorkflowState[], APIError>({
    queryKey: queryKeys.workflowStates(spaceId),
    queryFn: () => fetchWorkflowStates(spaceId),
    enabled: !!spaceId,
    staleTime: 5 * 60 * 1000, // workflow states rarely change
    ...opts,
  });
}

// Org-scoped workflow admin surface (/orgs/{orgId}/workflows/...) — distinct
// from useWorkflowStates above, which reads the resolved workflow of one space.
async function fetchOrgWorkflows(orgId: string): Promise<Workflow[]> {
  return apiFetch<Workflow[]>(`/orgs/${orgId}/workflows`);
}

export function useOrgWorkflows(orgId: string, opts?: QueryOpts<Workflow[]>) {
  return useQuery<Workflow[], APIError>({
    queryKey: queryKeys.orgWorkflows(orgId),
    queryFn: () => fetchOrgWorkflows(orgId),
    enabled: !!orgId,
    ...opts,
  });
}

async function fetchOrgWorkflowStates(
  orgId: string,
  workflowId: string,
): Promise<WorkflowState[]> {
  return apiFetch<WorkflowState[]>(`/orgs/${orgId}/workflows/${workflowId}/states`);
}

export function useOrgWorkflowStates(
  orgId: string,
  workflowId: string,
  opts?: QueryOpts<WorkflowState[]>,
) {
  return useQuery<WorkflowState[], APIError>({
    queryKey: queryKeys.orgWorkflowStates(orgId, workflowId),
    queryFn: () => fetchOrgWorkflowStates(orgId, workflowId),
    enabled: !!orgId && !!workflowId,
    staleTime: 5 * 60 * 1000, // workflow definitions rarely change
    ...opts,
  });
}

// ---------------------------------------------------------------------------
// Workflow tiers — ADR-0011 conditions, validators, approvals, post-functions
//
// The configuration half is org-scoped and org-admin; the approval half is
// space-scoped, and who may DECIDE is data rather than a capability, so the
// server answers `can_decide` per row and the UI must not compute its own.
//
// Every vocabulary these hooks carry is closed and mirrored in
// lib/workflow/vocabulary.ts, held equal to Go by guards.test.ts. Nothing here
// accepts a free-text predicate and nothing may be added that does (ADR-0011).
// ---------------------------------------------------------------------------

export interface WorkflowTransition {
  id: string;
  workflow_id: string;
  from_state_id: string;
  to_state_id: string;
  name: string;
}

export interface WorkflowGuard {
  id: string;
  transition_id: string;
  /** NOTE the wire name: `guard_class`, not `class`. */
  guard_class: GuardClass;
  kind: GuardKind;
  position: number;
  capability?: GuardCapability;
  /**
   * Absent on a DEGRADED guard — migration 046 sets it NULL when the team is
   * deleted, so the restriction survives as unsatisfiable rather than silently
   * disappearing. Such a row must be re-scoped, never resubmitted: ValidateGuard
   * refuses a null team on write even though the database holds one at rest.
   */
  team_id?: string;
  field_key?: GuardFieldKey;
}

export interface WorkflowPostFunction {
  id: string;
  transition_id: string;
  kind: PostFunctionKind;
  position: number;
  assignee_user_id?: string;
  field_key?: PostFieldKey;
  field_value?: string;
}

export interface WorkflowApprover {
  id: string;
  transition_id: string;
  subject_type: ApproverSubjectType;
  subject_id: string;
  /** Resolved at read time; empty when the subject no longer exists. */
  subject_name?: string;
  /** True when the named user or team has been deleted. */
  subject_missing?: boolean;
}

export interface WorkflowApproval {
  id: string;
  /** Null when the edge was deleted under the request, which makes it stuck. */
  transition_id: string | null;
  entity_type: ApprovalEntityType;
  entity_id: string;
  space_id: string;
  from_state_id: string | null;
  to_state_id: string | null;
  from_status: string;
  to_status: string;
  requested_by: string;
  requested_at: string;
  decided_by?: string;
  decided_at?: string;
  decision?: Decision;
  /** The approver's sentence. Required on a decline (migration 050). */
  reason?: string;
  requested_by_name?: string;
  decided_by_name?: string;
  /**
   * Whether the CALLING user may decide. Server-resolved, because approval
   * authority is being NAMED on the transition — there is no capability to
   * check and nothing on the row a client could compute it from.
   */
  can_decide: boolean;
}

/**
 * The 202 body a gated transition answers with.
 *
 * This is NOT an error, which is exactly what makes it dangerous: apiFetch
 * treats every ok status as success and casts the body to the declared type, so
 * a status mutation that ignores it resolves with this object wearing a Ticket's
 * type and the UI reports a move that did not happen. Call sites use
 * `pendingApprovalOf` below rather than checking by hand.
 */
export interface PendingApprovalResponse {
  status: 'pending_approval';
  message: string;
  approval_id: string;
  from_status: string;
  to_status: string;
  requested_at: string;
  transition_id?: string;
}

/**
 * Recognises the 202 pending-approval body inside a status-mutation result.
 *
 * Every status call site must run its result through this before treating the
 * transition as applied. Both detail pages and both boards previously assumed
 * any resolved promise meant success, which turned "waiting on an approver"
 * into a silent false success — the card stayed in the target column and the
 * item had never moved.
 */
export function pendingApprovalOf(result: unknown): PendingApprovalResponse | null {
  if (
    result &&
    typeof result === 'object' &&
    (result as { status?: unknown }).status === 'pending_approval' &&
    typeof (result as { approval_id?: unknown }).approval_id === 'string'
  ) {
    return result as PendingApprovalResponse;
  }
  return null;
}

function transitionBase(orgId: string, workflowId: string, transitionId: string): string {
  return `/orgs/${orgId}/workflows/${workflowId}/transitions/${transitionId}`;
}

// ─── Transitions ──────────────────────────────────────────────────────────────

export function useOrgWorkflowTransitions(
  orgId: string,
  workflowId: string,
  opts?: QueryOpts<WorkflowTransition[]>,
) {
  return useQuery<WorkflowTransition[], APIError>({
    queryKey: queryKeys.orgWorkflowTransitions(orgId, workflowId),
    queryFn: () =>
      apiFetch<WorkflowTransition[]>(`/orgs/${orgId}/workflows/${workflowId}/transitions`),
    enabled: !!orgId && !!workflowId,
    staleTime: 5 * 60 * 1000, // workflow definitions rarely change
    ...opts,
  });
}

// ─── Guards (conditions and validators share one table and one route) ─────────

export function useTransitionGuards(
  orgId: string,
  workflowId: string,
  transitionId: string,
  opts?: QueryOpts<WorkflowGuard[]>,
) {
  return useQuery<WorkflowGuard[], APIError>({
    queryKey: queryKeys.transitionGuards(orgId, workflowId, transitionId),
    queryFn: () => apiFetch<WorkflowGuard[]>(`${transitionBase(orgId, workflowId, transitionId)}/guards`),
    enabled: !!orgId && !!workflowId && !!transitionId,
    ...opts,
  });
}

export interface CreateGuardRequest {
  guard_class: GuardClass;
  kind: GuardKind;
  position: number;
  capability?: GuardCapability;
  team_id?: string;
  field_key?: GuardFieldKey;
}

export function useCreateTransitionGuard(orgId: string, workflowId: string, transitionId: string) {
  const qc = useQueryClient();
  return useMutation<WorkflowGuard, APIError, CreateGuardRequest>({
    mutationFn: (body) =>
      apiFetch<WorkflowGuard>(`${transitionBase(orgId, workflowId, transitionId)}/guards`, {
        method: 'POST',
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({
        queryKey: queryKeys.transitionGuards(orgId, workflowId, transitionId),
      });
    },
  });
}

export function useDeleteTransitionGuard(orgId: string, workflowId: string, transitionId: string) {
  const qc = useQueryClient();
  return useMutation<void, APIError, string>({
    mutationFn: (guardId) =>
      apiFetch<void>(`${transitionBase(orgId, workflowId, transitionId)}/guards/${guardId}`, {
        method: 'DELETE',
      }),
    onSuccess: () => {
      void qc.invalidateQueries({
        queryKey: queryKeys.transitionGuards(orgId, workflowId, transitionId),
      });
    },
  });
}

// ─── Post-functions ───────────────────────────────────────────────────────────

export function useTransitionPostFunctions(
  orgId: string,
  workflowId: string,
  transitionId: string,
  opts?: QueryOpts<WorkflowPostFunction[]>,
) {
  return useQuery<WorkflowPostFunction[], APIError>({
    queryKey: queryKeys.transitionPostFunctions(orgId, workflowId, transitionId),
    queryFn: () =>
      apiFetch<WorkflowPostFunction[]>(`${transitionBase(orgId, workflowId, transitionId)}/post-functions`),
    enabled: !!orgId && !!workflowId && !!transitionId,
    ...opts,
  });
}

export interface CreatePostFunctionRequest {
  kind: PostFunctionKind;
  position: number;
  assignee_user_id?: string;
  field_key?: PostFieldKey;
  /**
   * Encoding is per field and is parsed at WRITE time, so a bad value is
   * refused on this form rather than at transition time: `due_at` must be
   * RFC3339, and `tags` is a comma-separated label list, NOT a JSON array.
   */
  field_value?: string;
}

export function useCreateTransitionPostFunction(
  orgId: string,
  workflowId: string,
  transitionId: string,
) {
  const qc = useQueryClient();
  return useMutation<WorkflowPostFunction, APIError, CreatePostFunctionRequest>({
    mutationFn: (body) =>
      apiFetch<WorkflowPostFunction>(`${transitionBase(orgId, workflowId, transitionId)}/post-functions`, {
        method: 'POST',
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({
        queryKey: queryKeys.transitionPostFunctions(orgId, workflowId, transitionId),
      });
    },
  });
}

export function useDeleteTransitionPostFunction(
  orgId: string,
  workflowId: string,
  transitionId: string,
) {
  const qc = useQueryClient();
  return useMutation<void, APIError, string>({
    mutationFn: (postFunctionId) =>
      apiFetch<void>(
        `${transitionBase(orgId, workflowId, transitionId)}/post-functions/${postFunctionId}`,
        { method: 'DELETE' },
      ),
    onSuccess: () => {
      void qc.invalidateQueries({
        queryKey: queryKeys.transitionPostFunctions(orgId, workflowId, transitionId),
      });
    },
  });
}

// ─── Approvers ────────────────────────────────────────────────────────────────

export function useTransitionApprovers(
  orgId: string,
  workflowId: string,
  transitionId: string,
  opts?: QueryOpts<WorkflowApprover[]>,
) {
  return useQuery<WorkflowApprover[], APIError>({
    queryKey: queryKeys.transitionApprovers(orgId, workflowId, transitionId),
    queryFn: () =>
      apiFetch<WorkflowApprover[]>(`${transitionBase(orgId, workflowId, transitionId)}/approvers`),
    enabled: !!orgId && !!workflowId && !!transitionId,
    ...opts,
  });
}

export interface CreateApproverRequest {
  subject_type: ApproverSubjectType;
  subject_id: string;
}

export function useCreateTransitionApprover(
  orgId: string,
  workflowId: string,
  transitionId: string,
) {
  const qc = useQueryClient();
  return useMutation<WorkflowApprover, APIError, CreateApproverRequest>({
    mutationFn: (body) =>
      apiFetch<WorkflowApprover>(`${transitionBase(orgId, workflowId, transitionId)}/approvers`, {
        method: 'POST',
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({
        queryKey: queryKeys.transitionApprovers(orgId, workflowId, transitionId),
      });
    },
  });
}

export function useDeleteTransitionApprover(
  orgId: string,
  workflowId: string,
  transitionId: string,
) {
  const qc = useQueryClient();
  return useMutation<void, APIError, string>({
    mutationFn: (approverId) =>
      apiFetch<void>(`${transitionBase(orgId, workflowId, transitionId)}/approvers/${approverId}`, {
        method: 'DELETE',
      }),
    onSuccess: () => {
      void qc.invalidateQueries({
        queryKey: queryKeys.transitionApprovers(orgId, workflowId, transitionId),
      });
    },
  });
}

// ─── Approvals (space-scoped) ─────────────────────────────────────────────────

/**
 * Everything awaiting a decision in a space. Backs the board's blocked markers.
 *
 * Space-read by design: anyone who can see the space's items can see that one
 * is waiting. Do not narrow it to approvers — that would hide the block from
 * the person it affects.
 */
export function useSpacePendingApprovals(spaceId: string, opts?: QueryOpts<WorkflowApproval[]>) {
  return useQuery<WorkflowApproval[], APIError>({
    queryKey: queryKeys.spacePendingApprovals(spaceId),
    queryFn: () => apiFetch<WorkflowApproval[]>(`${spaceBase(spaceId)}/workflow/approvals`),
    enabled: !!spaceId,
    ...opts,
  });
}

/**
 * One item's approval history, pending and decided alike.
 *
 * The detail surface needs this rather than the space list because a DECIDED
 * approval leaves the pending set: the moment an approver declines, the request
 * and its reason vanish from `/approvals`, and a block that simply disappeared
 * tells the requester nothing.
 *
 * entityType is required and is not inferable from the id — tickets and project
 * items are separate tables (ADR-0003) and their ids are not unique across the
 * pair.
 */
export function useEntityApprovals(
  spaceId: string,
  entityType: ApprovalEntityType,
  entityId: string,
  opts?: QueryOpts<WorkflowApproval[]>,
) {
  return useQuery<WorkflowApproval[], APIError>({
    queryKey: queryKeys.entityApprovals(spaceId, entityType, entityId),
    queryFn: () =>
      apiFetch<WorkflowApproval[]>(
        `${spaceBase(spaceId)}/workflow/entities/${entityType}/${entityId}/approvals`,
      ),
    enabled: !!spaceId && !!entityId,
    ...opts,
  });
}

/** One transition the workflow is willing to offer this actor for this entity. */
export interface WorkflowOffer {
  transition_id: string;
  name: string;
  to_state_id: string;
  to_status: string;
  /**
   * Taking this move creates an approval request rather than moving the entity.
   * Saying so before the click matters: without it the only honest control is
   * one that sometimes answers "pending" for reasons the user cannot see.
   */
  requires_approval: boolean;
}

/** What the workflow will offer for one entity. */
export interface WorkflowOffering {
  /**
   * The space has no workflow, so the server has no opinion and the client
   * keeps its own vocabulary.
   *
   * This is NOT the same as an empty `transitions` list, and collapsing the two
   * strands the picker: "use your own statuses" and "the workflow offers you
   * nothing" are opposite instructions.
   */
  no_workflow: boolean;
  /** Where the workflow considers the entity to be. */
  current_status: string;
  /**
   * The entity's stored status text, which can differ from `current_status` on
   * a row written before the two position columns were kept in step.
   */
  entity_status: string;
  transitions: WorkflowOffer[];
}

/**
 * The status changes this entity may be offered, with ADR-0011 conditions
 * applied.
 *
 * This is what makes a configured condition mean anything to a user: a condition
 * HIDES a transition, and until this endpoint existed nothing offered
 * transitions, so it hid nothing from anybody.
 *
 * It is NOT the authority. The server refuses an illegal move whether or not the
 * client asked first; this only stops the UI presenting doors that do not open.
 * A picker built on it must still handle a refusal on submit.
 */
export function useAvailableTransitions(
  spaceId: string,
  entityType: ApprovalEntityType,
  entityId: string,
  opts?: QueryOpts<WorkflowOffering>,
) {
  return useQuery<WorkflowOffering, APIError>({
    queryKey: queryKeys.availableTransitions(spaceId, entityType, entityId),
    queryFn: () =>
      apiFetch<WorkflowOffering>(
        `${spaceBase(spaceId)}/workflow/entities/${entityType}/${entityId}/transitions`,
      ),
    enabled: !!spaceId && !!entityId,
    ...opts,
  });
}

export interface DecideApprovalRequest {
  approvalId: string;
  decision: Decision;
  /** Required when declining; the server refuses a bare or blank decline. */
  reason?: string;
}

/**
 * Approve or decline a pending transition.
 *
 * On approval the server applies the captured transition, so the item's own
 * queries go stale too — hence the entity and space invalidations alongside the
 * approval ones. The ticket and item lists are invalidated by the caller, which
 * knows which of the two it is looking at.
 */
export function useDecideApproval(
  spaceId: string,
  entityType: ApprovalEntityType,
  entityId: string,
) {
  const qc = useQueryClient();
  return useMutation<WorkflowApproval, APIError, DecideApprovalRequest>({
    mutationFn: ({ approvalId, decision, reason }) =>
      apiFetch<WorkflowApproval>(
        `${spaceBase(spaceId)}/workflow/approvals/${approvalId}/decide`,
        { method: 'POST', body: JSON.stringify({ decision, reason: reason ?? '' }) },
      ),
    onSuccess: () => {
      void qc.invalidateQueries({
        queryKey: queryKeys.entityApprovals(spaceId, entityType, entityId),
      });
      void qc.invalidateQueries({ queryKey: queryKeys.spacePendingApprovals(spaceId) });
    },
  });
}

// ---------------------------------------------------------------------------
// Team hooks (P2)
// ---------------------------------------------------------------------------

export function useTeams(orgId: string, opts?: QueryOpts<Team[]>) {
  return useQuery<Team[], APIError>({
    queryKey: queryKeys.teams(orgId),
    queryFn: () => fetchTeams(orgId),
    enabled: !!orgId,
    ...opts,
  });
}

export function useTeamMembers(orgId: string, teamId: string, opts?: QueryOpts<TeamMember[]>) {
  return useQuery<TeamMember[], APIError>({
    queryKey: queryKeys.teamMembers(orgId, teamId),
    queryFn: () => fetchTeamMembers(orgId, teamId),
    enabled: !!orgId && !!teamId,
    ...opts,
  });
}

export function useCreateTeam(orgId: string) {
  const queryClient = useQueryClient();
  return useMutation<Team, APIError, CreateTeamRequest & { ticketRef?: string }>({
    mutationFn: ({ ticketRef, ...req }) => createTeam(orgId, req, ticketRef),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.teams(orgId) });
    },
  });
}

/** The outcome of creating a team plus any opt-in per-module spaces. */
export interface CreateTeamWithSpacesResult {
  team: Team;
  spaces: Space[];
}

/**
 * Creates a team, then — for each opted-in module — a space named for the team
 * owned by it, granting the team access via the existing space_grants path
 * (subject_type=team). Reuses the single POST /spaces + POST /grants
 * implementations rather than a second server-side space-creation loop.
 * The space key is omitted so the backend derives and dedupes it (space keys
 * are unique per org, unlike slugs which are per module).
 */
export function useCreateTeamWithSpaces(orgId: string) {
  const queryClient = useQueryClient();
  return useMutation<
    CreateTeamWithSpacesResult,
    APIError,
    CreateTeamRequest & { modules: SpaceType[]; ticketRef?: string }
  >({
    // One operator action, so one reference: it rides EVERY request this
    // composite makes — one team create plus two per module (the space, then
    // its grant). Since POST /grants gained ticket_ref, no step is exempt.
    //
    // That matters more than tidiness. This mutationFn has no rollback: it
    // awaits team, then space, then grant, in sequence. An exempt step would
    // be either silently unaudited or, under AZIMUTHAL_TICKET_REF_REQUIRED, a
    // 400 halfway through an orchestration that has already created a team and
    // a space — leaving a half-built module behind a retry that then 409s on
    // the duplicate slug. With the reference threaded, the only
    // reference-less failure is the FIRST request, before anything exists.
    //
    // api.ticket-ref.test.ts pins all 1+2N URLs; the backend proof that the
    // whole orchestration survives required mode is
    // TestTicketRefRequired_TeamWithAutoSpaces_OneReferenceCoversTheWholeOrchestration.
    mutationFn: async ({ modules, ticketRef, ...teamReq }) => {
      const team = await createTeam(orgId, teamReq, ticketRef);
      const spaces: Space[] = [];
      for (const module of modules) {
        const space = await createSpace(orgId, {
          name: team.name,
          slug: team.slug,
          type: module,
          owner_team_id: team.id,
        }, ticketRef);
        await createGrant(
          orgId,
          space.id,
          {
            subject_type: 'team',
            subject_id: team.id,
            role: 'contributor',
          },
          ticketRef,
        );
        spaces.push(space);
      }
      return { team, spaces };
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.teams(orgId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.spaces(orgId) });
    },
  });
}

export function useUpdateTeam(orgId: string) {
  const queryClient = useQueryClient();
  return useMutation<Team, APIError, { teamId: string; ticketRef?: string } & UpdateTeamRequest>({
    mutationFn: ({ teamId, ticketRef, ...req }) => updateTeam(orgId, teamId, req, ticketRef),
    onSuccess: () => {
      // Reparenting rewrites the paths of the whole subtree — refetch all.
      queryClient.invalidateQueries({ queryKey: queryKeys.teams(orgId) });
    },
  });
}

export function useDeleteTeam(orgId: string) {
  const queryClient = useQueryClient();
  // The variable is the team id, or { id, ticketRef } — see IdWithTicketRef.
  return useMutation<void, APIError, IdWithTicketRef>({
    mutationFn: (v) => {
      const { id, ticketRef } = splitIdRef(v);
      return deleteTeam(orgId, id, ticketRef);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.teams(orgId) });
    },
  });
}

export function usePutTeamMember(orgId: string, teamId: string) {
  const queryClient = useQueryClient();
  return useMutation<
    TeamMember,
    APIError,
    { userId: string; ticketRef?: string } & PutTeamMemberRequest
  >({
    mutationFn: ({ userId, ticketRef, ...req }) =>
      putTeamMember(orgId, teamId, userId, req, ticketRef),
    onSuccess: () => {
      // is_primary: true clears the user's primary flag elsewhere — the
      // ['teams', orgId] prefix reaches every team's member list.
      queryClient.invalidateQueries({ queryKey: queryKeys.teams(orgId) });
    },
  });
}

export function useRemoveTeamMember(orgId: string, teamId: string) {
  const queryClient = useQueryClient();
  // The variable is the user id, or { id, ticketRef } — see IdWithTicketRef.
  return useMutation<void, APIError, IdWithTicketRef>({
    mutationFn: (v) => {
      const { id, ticketRef } = splitIdRef(v);
      return removeTeamMember(orgId, teamId, id, ticketRef);
    },
    onSuccess: () => {
      // A user removed from their last team is re-added to the org default
      // team — another team's member list just changed too.
      queryClient.invalidateQueries({ queryKey: queryKeys.teams(orgId) });
    },
  });
}

// ---------------------------------------------------------------------------
// Grant hooks (P2)
// ---------------------------------------------------------------------------

export function useSpaceGrants(orgId: string, spaceId: string, opts?: QueryOpts<SpaceGrant[]>) {
  return useQuery<SpaceGrant[], APIError>({
    queryKey: queryKeys.spaceGrants(orgId, spaceId),
    queryFn: () => fetchSpaceGrants(orgId, spaceId),
    enabled: !!orgId && !!spaceId,
    retry: (failureCount, error) => {
      // 403 is a stable answer (manage_grants missing), not a flake.
      if (error?.status === 403 || error?.status === 404) return false;
      return failureCount < 2;
    },
    ...opts,
  });
}

export function useCreateGrant(orgId: string, spaceId: string) {
  const queryClient = useQueryClient();
  return useMutation<SpaceGrant, APIError, CreateGrantRequest & { ticketRef?: string }>({
    mutationFn: ({ ticketRef, ...req }) => createGrant(orgId, spaceId, req, ticketRef),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.spaceGrants(orgId, spaceId) });
    },
  });
}

export function useUpdateGrant(orgId: string, spaceId: string) {
  const queryClient = useQueryClient();
  return useMutation<
    SpaceGrant,
    APIError,
    { grantId: string; role: GrantRole; ticketRef?: string }
  >({
    mutationFn: ({ grantId, role, ticketRef }) =>
      updateGrant(orgId, spaceId, grantId, role, ticketRef),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.spaceGrants(orgId, spaceId) });
    },
  });
}

export function useRevokeGrant(orgId: string, spaceId: string) {
  const queryClient = useQueryClient();
  // The variable is the grant id, or { id, ticketRef } — see IdWithTicketRef.
  return useMutation<void, APIError, IdWithTicketRef>({
    mutationFn: (v) => {
      const { id, ticketRef } = splitIdRef(v);
      return revokeGrant(orgId, spaceId, id, ticketRef);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.spaceGrants(orgId, spaceId) });
    },
  });
}

export function useEffectiveAccess(
  orgId: string,
  spaceId: string,
  userId?: string,
  opts?: QueryOpts<EffectiveAccess>,
) {
  return useQuery<EffectiveAccess, APIError>({
    queryKey: queryKeys.effectiveAccess(spaceId, userId),
    queryFn: () => fetchEffectiveAccess(orgId, spaceId, userId),
    enabled: !!orgId && !!spaceId,
    ...opts,
  });
}

export function useUpdateSpace(orgId: string, spaceId: string) {
  const queryClient = useQueryClient();
  return useMutation<Space, APIError, UpdateSpaceRequest & { ticketRef?: string }>({
    mutationFn: ({ ticketRef, ...req }) => updateSpace(orgId, spaceId, req, ticketRef),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.space(spaceId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.spaces(orgId) });
    },
  });
}

// ---------------------------------------------------------------------------
// Administration hooks (P2.5)
// ---------------------------------------------------------------------------

export function useOrgPeople(orgId: string, opts?: QueryOpts<Person[]>) {
  return useQuery<Person[], APIError>({
    queryKey: queryKeys.orgPeople(orgId),
    queryFn: () => fetchOrgPeople(orgId),
    enabled: !!orgId,
    ...opts,
  });
}

/** Person picker search; disabled until the query has content. */
export function useMemberSearch(orgId: string, q: string, opts?: QueryOpts<PersonRef[]>) {
  return useQuery<PersonRef[], APIError>({
    queryKey: queryKeys.memberSearch(orgId, q),
    queryFn: () => searchOrgMembers(orgId, q),
    enabled: !!orgId && q.trim().length > 0,
    ...opts,
  });
}

/**
 * ticket_ref picker typeahead. Same shape as useMemberSearch, deliberately:
 * an empty orgId disables it (that is how a picker turns itself off), the
 * query is disabled until q has content, and debouncing is the caller's job.
 * The endpoint would answer an empty q with a default ordering; this hook
 * does not ask, so the two pickers behave identically from the operator's
 * side.
 */
/**
 * DeploymentConfig is GET /orgs/{orgId}/config — the boot-time flags the
 * server publishes to org members. The Go type is spaces.BootConfig; the name
 * differs here on purpose, because `BoardConfig` already exists in this file
 * and one letter of difference in a 4000-line module is a bug waiting to
 * happen.
 *
 * The server decides what may appear here through an explicit allowlist in
 * code. Do not widen this interface speculatively — a field the server does
 * not send is a lie about the contract.
 */
export interface DeploymentConfig {
  ticket_ref_required: boolean;
}

async function fetchDeploymentConfig(orgId: string): Promise<DeploymentConfig> {
  return apiFetch<DeploymentConfig>(`/orgs/${orgId}/config`);
}

/**
 * useDeploymentConfig reads the deployment's boot-time flags.
 *
 * `staleTime: Infinity` is load-bearing rather than tuning. These values are
 * read once at server start and cannot change while the server is running —
 * that is the design of AZIMUTHAL_TICKET_REF_REQUIRED, not an accident — so a
 * refetch can never return anything new. Unexported: every consumer wants one
 * flag, and going through a selector keeps the fail-safe in exactly one place.
 */
function useDeploymentConfig(orgId: string) {
  return useQuery<DeploymentConfig, APIError>({
    queryKey: queryKeys.deploymentConfig(orgId),
    queryFn: () => fetchDeploymentConfig(orgId),
    enabled: !!orgId,
    staleTime: Infinity,
  });
}

/**
 * useTicketRefRequired reports whether this deployment demands a ticket
 * reference on administrative changes. Callers pass it to TicketRefField's
 * `required` prop and use it to gate their own submit button.
 *
 * It fails safe to FALSE while loading or on error, and the direction matters.
 * The server enforces the requirement either way and is the authority; a
 * client that guessed `true` on a failed fetch would lock every administrative
 * dialog on the instance behind a field the operator may not even need.
 * Guessing `false` costs one 400 with a message that says exactly what to do.
 */
export function useTicketRefRequired(orgId: string): boolean {
  return useDeploymentConfig(orgId).data?.ticket_ref_required ?? false;
}

export function useTicketRefSuggestions(
  orgId: string,
  q: string,
  opts?: QueryOpts<TicketRefSuggestion[]>,
) {
  return useQuery<TicketRefSuggestion[], APIError>({
    queryKey: queryKeys.ticketRefSuggestions(orgId, q),
    queryFn: () => suggestTicketRefs(orgId, q),
    enabled: !!orgId && q.trim().length > 0,
    ...opts,
  });
}

export function usePageSuggestions(
  orgId: string,
  q: string,
  opts?: QueryOpts<PageSuggestion[]>,
) {
  return useQuery<PageSuggestion[], APIError>({
    queryKey: queryKeys.pageSuggestions(orgId, q),
    queryFn: () => suggestPages(orgId, q),
    enabled: !!orgId && q.trim().length > 0,
    ...opts,
  });
}

export function useInvites(orgId: string, opts?: QueryOpts<Invite[]>) {
  return useQuery<Invite[], APIError>({
    queryKey: queryKeys.invites(orgId),
    queryFn: () => fetchInvites(orgId),
    enabled: !!orgId,
    ...opts,
  });
}

export function useCreateInvites(orgId: string) {
  const queryClient = useQueryClient();
  return useMutation<InviteOutcome[], APIError, CreateInvitesRequest & { ticketRef?: string }>({
    mutationFn: ({ ticketRef, ...req }) => createInvites(orgId, req, ticketRef),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.invites(orgId) });
    },
  });
}

export function useRevokeInvite(orgId: string) {
  const queryClient = useQueryClient();
  // The variable is the invite id, or { id, ticketRef } — see IdWithTicketRef.
  return useMutation<void, APIError, IdWithTicketRef>({
    mutationFn: (v) => {
      const { id, ticketRef } = splitIdRef(v);
      return revokeInvite(orgId, id, ticketRef);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.invites(orgId) });
    },
  });
}

export function useResendInvite(orgId: string) {
  const queryClient = useQueryClient();
  // The variable is the invite id, or { id, ticketRef } — see IdWithTicketRef.
  return useMutation<CreatedInvite, APIError, IdWithTicketRef>({
    mutationFn: (v) => {
      const { id, ticketRef } = splitIdRef(v);
      return resendInvite(orgId, id, ticketRef);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.invites(orgId) });
    },
  });
}

export function useUploadOwnAvatar() {
  return useMutation<AvatarUploadResult, APIError, File>({
    mutationFn: (file) => uploadOwnAvatar(file),
  });
}

export function useUploadUserAvatar(orgId: string) {
  const queryClient = useQueryClient();
  return useMutation<AvatarUploadResult, APIError, { userId: string; file: File }>({
    mutationFn: ({ userId, file }) => uploadUserAvatar(orgId, userId, file),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.orgPeople(orgId) });
    },
  });
}

export function useUpdatePerson(orgId: string) {
  const queryClient = useQueryClient();
  return useMutation<void, APIError, { userId: string; ticketRef?: string } & UpdatePersonRequest>({
    mutationFn: ({ userId, ticketRef, ...req }) => updatePerson(orgId, userId, req, ticketRef),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.orgPeople(orgId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.teams(orgId) });
    },
  });
}

export function usePersonLifecycle(orgId: string) {
  const queryClient = useQueryClient();
  return useMutation<void, APIError, { userId: string; action: 'deactivate' | 'reactivate' | 'force-logout'; ticketRef?: string }>({
    mutationFn: ({ userId, action, ticketRef }) => personLifecycle(orgId, userId, action, ticketRef),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.orgPeople(orgId) });
    },
  });
}

export function useRemovePerson(orgId: string) {
  const queryClient = useQueryClient();
  // The variable is the user id, or { id, ticketRef } — see IdWithTicketRef.
  return useMutation<void, APIError, IdWithTicketRef>({
    mutationFn: (v) => {
      const { id, ticketRef } = splitIdRef(v);
      return removePersonFromOrg(orgId, id, ticketRef);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.orgPeople(orgId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.teams(orgId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.accessMatrix(orgId) });
    },
  });
}

// ── Credential links (D1) ────────────────────────────────────────────────────
// One mechanism for three internal-user credential handoffs. The public
// endpoints (forgot-password / inspect / consume) carry `credential: 'none'`:
// they authenticate by possession of the raw token, and a stray internal 401
// must not clear a signed-out visitor's tokens or bounce them to /login (the
// portal-redemption precedent).

export type CredentialPurpose = 'signin' | 'password_reset' | 'email_change';

export interface CredentialInspection {
  purpose: CredentialPurpose;
  /** Present only for the email_change purpose. */
  new_email?: string;
}

export interface CredentialConsumeResult {
  status: string;
  purpose: CredentialPurpose;
  /** Present only for the signin purpose — a redeemed sign-in link signs in. */
  access_token?: string;
  refresh_token?: string;
}

/** A one-time link returned to an authorised caller (an admin, or the
 * reauthenticated requester in the no-relay email-change case). */
export interface CredentialLinkResult {
  status: string;
  url?: string;
  expires_at?: string;
  user_id?: string;
  delivered?: boolean;
}

async function requestForgotPassword(email: string): Promise<void> {
  await apiFetch<{ status: string }>(
    '/credential-links/forgot-password',
    { method: 'POST', body: JSON.stringify({ email }) },
    undefined,
    { credential: 'none' },
  );
}

async function inspectCredentialLink(token: string): Promise<CredentialInspection> {
  return apiFetch<CredentialInspection>(
    '/credential-links/inspect',
    { method: 'POST', body: JSON.stringify({ token }) },
    undefined,
    { credential: 'none' },
  );
}

async function consumeCredentialLink(token: string, password?: string): Promise<CredentialConsumeResult> {
  return apiFetch<CredentialConsumeResult>(
    '/credential-links/consume',
    { method: 'POST', body: JSON.stringify(password ? { token, password } : { token }) },
    undefined,
    { credential: 'none' },
  );
}

async function requestEmailChange(body: { new_email: string; current_password: string }): Promise<CredentialLinkResult> {
  return apiFetch<CredentialLinkResult>('/auth/me/email-change', {
    method: 'POST',
    body: JSON.stringify(body),
  });
}

async function adminCreateUserWithLink(
  orgId: string,
  body: { email: string; name: string; role?: string },
): Promise<CredentialLinkResult> {
  return apiFetch<CredentialLinkResult>(`/orgs/${orgId}/credential-links/users`, {
    method: 'POST',
    body: JSON.stringify(body),
  });
}

async function adminIssueResetLink(orgId: string, email: string): Promise<CredentialLinkResult> {
  return apiFetch<CredentialLinkResult>(`/orgs/${orgId}/credential-links/reset`, {
    method: 'POST',
    body: JSON.stringify({ email }),
  });
}

export function useForgotPassword() {
  return useMutation<void, APIError, string>({ mutationFn: requestForgotPassword });
}

export function useInspectCredentialLink(token: string, opts?: QueryOpts<CredentialInspection>) {
  return useQuery<CredentialInspection, APIError>({
    queryKey: ['credentialLink', token] as const,
    queryFn: () => inspectCredentialLink(token),
    enabled: !!token,
    retry: false,
    ...opts,
  });
}

export function useConsumeCredentialLink() {
  return useMutation<CredentialConsumeResult, APIError, { token: string; password?: string }>({
    mutationFn: ({ token, password }) => consumeCredentialLink(token, password),
    onSuccess: (data) => {
      // A redeemed sign-in link carries a session; store it so the app is
      // authenticated on the redirect. A reset or an email change mints none.
      if (data.access_token && data.refresh_token) {
        setToken(data.access_token);
        setRefreshToken(data.refresh_token);
      }
    },
  });
}

export function useRequestEmailChange() {
  return useMutation<CredentialLinkResult, APIError, { new_email: string; current_password: string }>({
    mutationFn: requestEmailChange,
  });
}

export function useAdminCreateUserWithLink(orgId: string) {
  const queryClient = useQueryClient();
  return useMutation<CredentialLinkResult, APIError, { email: string; name: string; role?: string }>({
    mutationFn: (body) => adminCreateUserWithLink(orgId, body),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.orgPeople(orgId) }),
  });
}

export function useAdminIssueResetLink(orgId: string) {
  return useMutation<CredentialLinkResult, APIError, string>({
    mutationFn: (email) => adminIssueResetLink(orgId, email),
  });
}

export function useAccessMatrix(orgId: string, opts?: QueryOpts<AccessMatrix>) {
  return useQuery<AccessMatrix, APIError>({
    queryKey: queryKeys.accessMatrix(orgId),
    queryFn: () => fetchAccessMatrix(orgId),
    enabled: !!orgId,
    ...opts,
  });
}

export function useBulkPreviewGrants(orgId: string) {
  return useMutation<BulkResult, APIError, BulkChange[]>({
    mutationFn: (changes) => bulkPreviewGrants(orgId, changes),
  });
}

export function useBulkApplyGrants(orgId: string) {
  const queryClient = useQueryClient();
  return useMutation<BulkResult, APIError, { changes: BulkChange[]; ticketRef?: string }>({
    mutationFn: ({ changes, ticketRef }) => bulkApplyGrants(orgId, changes, ticketRef),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.accessMatrix(orgId) });
      queryClient.invalidateQueries({ queryKey: ['auditLog', orgId] });
    },
  });
}

export function useAuditLog(orgId: string, filter: AuditFilter, opts?: QueryOpts<AuditPage>) {
  return useQuery<AuditPage, APIError>({
    queryKey: queryKeys.auditLog(orgId, filter),
    queryFn: () => fetchAuditLog(orgId, filter),
    enabled: !!orgId,
    ...opts,
  });
}

export function useAuditBatch(orgId: string, batchId: string, opts?: QueryOpts<AuditEntry[]>) {
  return useQuery<AuditEntry[], APIError>({
    queryKey: queryKeys.auditBatch(orgId, batchId),
    queryFn: () => fetchAuditBatch(orgId, batchId),
    enabled: !!orgId && !!batchId,
    ...opts,
  });
}

export function useSpaceContentsSummary(orgId: string, spaceId: string, opts?: QueryOpts<SpaceContentsSummary>) {
  return useQuery<SpaceContentsSummary, APIError>({
    queryKey: ['spaceSummary', orgId, spaceId] as const,
    queryFn: () => fetchSpaceContentsSummary(orgId, spaceId),
    enabled: !!orgId && !!spaceId,
    ...opts,
  });
}

export function useDeleteSpace(orgId: string) {
  const queryClient = useQueryClient();
  // The variable is the space id, or { id, ticketRef } — see IdWithTicketRef.
  return useMutation<void, APIError, IdWithTicketRef>({
    mutationFn: (v) => {
      const { id, ticketRef } = splitIdRef(v);
      return deleteSpace(orgId, id, ticketRef);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.spaces(orgId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.accessMatrix(orgId) });
    },
  });
}

/** Public: the acceptance page's invite lookup — the token is the credential. */
export function useInviteInspection(token: string, opts?: QueryOpts<InviteInspection>) {
  return useQuery<InviteInspection, APIError>({
    queryKey: ['inviteInspection', token] as const,
    queryFn: () => inspectInvite(token),
    enabled: !!token,
    retry: false,
    ...opts,
  });
}

export function useAcceptInvite() {
  return useMutation<AcceptInviteResult, APIError, AcceptInviteRequest>({
    mutationFn: acceptInvite,
    onSuccess: (res) => {
      // A freshly created account is signed in on the spot.
      if (res.access_token) {
        setToken(res.access_token);
      }
      if (res.refresh_token) {
        setRefreshToken(res.refresh_token);
      }
    },
  });
}

// ---------------------------------------------------------------------------
// Entity shares (P3, ADR-0008). Shares widen visibility, never narrow it: a
// shared entity may be read by an audience with no access to its space. The
// team audience is schema-ready but only org is surfaced here (P7 adds team).
// ---------------------------------------------------------------------------

export type ShareEntityType = 'page' | 'ticket' | 'project_item';
export type ShareAudience = 'org' | 'team';

export interface Share {
  id: string;
  entity_type: ShareEntityType;
  entity_id: string;
  audience: ShareAudience;
  audience_id?: string;
  cascade: boolean;
  expires_at?: string;
  expired: boolean;
  created_at: string;
  created_by: string;
}

export interface SharesListResponse {
  shares: Share[];
  /** Present for pages: how many pages a cascade share would cover. */
  cascade_page_count?: number;
}

export interface CreateShareRequest {
  entity_type: ShareEntityType;
  entity_id: string;
  audience: ShareAudience;
  audience_id?: string;
  cascade?: boolean;
  expires_at?: string;
}

/** The container-stripped view returned by the standalone shared read route. */
export interface SharedEntityView {
  id: string;
  entity_type: ShareEntityType;
  title: string;
  body: string;
  rendered_html?: string;
  status?: string;
  priority?: string;
  version?: number;
  updated_at?: string;
  shared: boolean;
}

export interface AttachmentMeta {
  id: string;
  entity_type: ShareEntityType;
  entity_id: string;
  filename: string;
  content_type: string;
  size_bytes: number;
  created_by: string;
  created_at: string;
}

async function fetchEntityShares(
  orgId: string,
  entityType: ShareEntityType,
  entityId: string,
): Promise<SharesListResponse> {
  return apiFetch<SharesListResponse>(
    `/orgs/${orgId}/shares?entity_type=${entityType}&entity_id=${entityId}`,
  );
}

async function createShare(
  orgId: string,
  req: CreateShareRequest,
  ticketRef?: string,
): Promise<Share> {
  return apiFetch<Share>(`/orgs/${orgId}/shares${ticketRefQuery(ticketRef)}`, {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

async function revokeShare(orgId: string, shareId: string, ticketRef?: string): Promise<void> {
  return apiFetch<void>(`/orgs/${orgId}/shares/${shareId}${ticketRefQuery(ticketRef)}`, {
    method: 'DELETE',
  });
}

async function fetchSharedEntity(
  orgId: string,
  entityType: ShareEntityType,
  entityId: string,
): Promise<SharedEntityView> {
  return apiFetch<SharedEntityView>(`/orgs/${orgId}/shared/${entityType}/${entityId}`);
}

async function fetchSharedAttachments(
  orgId: string,
  entityType: ShareEntityType,
  entityId: string,
): Promise<AttachmentMeta[]> {
  const data = await apiFetch<AttachmentMeta[] | null>(
    `/orgs/${orgId}/shared/${entityType}/${entityId}/attachments`,
  );
  return data ?? [];
}

/** One active page share in a space, for annotating the tree/page with a
 *  ShareBadge (ADR-0008 rule 5). */
export interface SpacePageShare {
  entity_id: string;
  cascade: boolean;
  root_path: string;
}

async function fetchSpacePageShares(orgId: string, spaceId: string): Promise<SpacePageShare[]> {
  const data = await apiFetch<SpacePageShare[] | null>(
    `/orgs/${orgId}/spaces/${spaceId}/wiki/shares`,
  );
  return data ?? [];
}

export function useSpacePageShares(orgId: string, spaceId: string, opts?: QueryOpts<SpacePageShare[]>) {
  return useQuery<SpacePageShare[], APIError>({
    queryKey: ['spacePageShares', orgId, spaceId] as const,
    queryFn: () => fetchSpacePageShares(orgId, spaceId),
    enabled: !!orgId && !!spaceId,
    ...opts,
  });
}

/** Whether a page is shared: a direct share on it, or a cascade share on an
 *  ancestor. Subtree membership is an exact-segment prefix check on the
 *  dot-separated path — "a.b" covers "a.b.c" but never the sibling "a.bc". */
export function pageShareState(
  shares: SpacePageShare[] | undefined,
  pageId: string,
  pagePath: string,
): { shared: boolean; viaCascade: boolean } {
  if (!shares) return { shared: false, viaCascade: false };
  for (const s of shares) {
    if (!s.cascade && s.entity_id === pageId) return { shared: true, viaCascade: false };
  }
  for (const s of shares) {
    if (!s.cascade) continue;
    if (pagePath === s.root_path || pagePath.startsWith(s.root_path + '.')) {
      return { shared: true, viaCascade: s.entity_id !== pageId };
    }
  }
  return { shared: false, viaCascade: false };
}

/** The move-confirmation warning count (ADR-0008 rule 9), served by the API. */
export interface MoveShareImpact {
  active_share_count: number;
}

async function fetchMoveShareImpact(
  orgId: string,
  spaceId: string,
  pageId: string,
): Promise<MoveShareImpact> {
  return apiFetch<MoveShareImpact>(`/orgs/${orgId}/spaces/${spaceId}/wiki/${pageId}/share-impact`);
}

export function useEntityShares(
  orgId: string,
  entityType: ShareEntityType,
  entityId: string,
  opts?: QueryOpts<SharesListResponse>,
) {
  return useQuery<SharesListResponse, APIError>({
    queryKey: queryKeys.entityShares(orgId, entityType, entityId),
    queryFn: () => fetchEntityShares(orgId, entityType, entityId),
    enabled: !!orgId && !!entityId,
    ...opts,
  });
}

export function useCreateShare(orgId: string, entityType: ShareEntityType, entityId: string) {
  const queryClient = useQueryClient();
  return useMutation<Share, APIError, CreateShareRequest & { ticketRef?: string }>({
    mutationFn: ({ ticketRef, ...req }) => createShare(orgId, req, ticketRef),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.entityShares(orgId, entityType, entityId) });
      // The page ShareBadge reads the space-wide badge list; refresh it so a
      // new share (incl. cascade coverage) shows immediately.
      queryClient.invalidateQueries({ queryKey: ['spacePageShares'] });
    },
  });
}

export function useRevokeShare(orgId: string, entityType: ShareEntityType, entityId: string) {
  const queryClient = useQueryClient();
  // The variable is the share id, or { id, ticketRef } — see IdWithTicketRef.
  return useMutation<void, APIError, IdWithTicketRef>({
    mutationFn: (v) => {
      const { id, ticketRef } = splitIdRef(v);
      return revokeShare(orgId, id, ticketRef);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.entityShares(orgId, entityType, entityId) });
      queryClient.invalidateQueries({ queryKey: ['spacePageShares'] });
    },
  });
}

export function useSharedEntity(
  orgId: string,
  entityType: ShareEntityType,
  entityId: string,
  opts?: QueryOpts<SharedEntityView>,
) {
  return useQuery<SharedEntityView, APIError>({
    queryKey: queryKeys.sharedEntity(orgId, entityType, entityId),
    queryFn: () => fetchSharedEntity(orgId, entityType, entityId),
    enabled: !!orgId && !!entityId,
    retry: false,
    ...opts,
  });
}

export function useSharedAttachments(
  orgId: string,
  entityType: ShareEntityType,
  entityId: string,
  opts?: QueryOpts<AttachmentMeta[]>,
) {
  return useQuery<AttachmentMeta[], APIError>({
    queryKey: queryKeys.sharedAttachments(orgId, entityType, entityId),
    queryFn: () => fetchSharedAttachments(orgId, entityType, entityId),
    enabled: !!orgId && !!entityId,
    ...opts,
  });
}

export function useMoveShareImpact(
  orgId: string,
  spaceId: string,
  pageId: string,
  opts?: QueryOpts<MoveShareImpact>,
) {
  return useQuery<MoveShareImpact, APIError>({
    queryKey: queryKeys.moveShareImpact(orgId, spaceId, pageId),
    queryFn: () => fetchMoveShareImpact(orgId, spaceId, pageId),
    enabled: !!orgId && !!spaceId && !!pageId,
    ...opts,
  });
}

/**
 * fetchSharedAttachmentObjectURL resolves a shared entity's attachment into a
 * blob URL, through the authenticated client.
 *
 * It replaces a `sharedAttachmentURL` builder whose comment claimed "the
 * browser fetches it with the session cookie". There is no session cookie —
 * this frontend is bearer-only — so every image on a shared page 401'd and
 * every download link saved an error body (S8). See fetchObjectURL.
 */
export async function fetchSharedAttachmentObjectURL(
  orgId: string,
  entityType: ShareEntityType,
  entityId: string,
  attachmentId: string,
): Promise<string> {
  return fetchObjectURL(
    `/orgs/${orgId}/shared/${entityType}/${entityId}/attachments/${attachmentId}`,
    'this attachment is unavailable',
  );
}

// ---------------------------------------------------------------------------
// Saved views (P4, ADR-0009 + ADR-0010). A saved view stores a QUERY, never
// results, and every read resolves it against the CALLING viewer's access.
//
// Three consequences the UI must not treat as faults:
//
//  1. Two people opening one shared view see different rows. That is the
//     feature working. It is never a sync problem and never an error.
//  2. `is_valid: false` means the view's scope is gone (its spaces or its
//     audience team were deleted). The view still lists, still opens, and
//     renders "scope unavailable" with a prompt to re-scope — an EmptyState
//     with an action, never an error panel and never friendlyErrorMessage.
//  3. The routes are org-scoped and org-member. There is no admin gate and no
//     space capability to mirror client-side; who may edit a view is decided
//     by ownership, which arrives pre-computed as `is_owner`.
//
// The filter vocabulary itself — QueryDoc and the helpers that decide what a
// module selection permits — lives in ./views/query, mirroring how the Codex
// document vocabulary lives in ./codex/schema. Import it from there.
// ---------------------------------------------------------------------------

export type ViewVisibility = 'private' | 'team' | 'org';

export interface SavedView {
  id: string;
  owner_id: string;
  owner_name?: string;
  name: string;
  description: string;
  /** The stored filter document. See lib/views/query. */
  query: QueryDoc;
  visibility: ViewVisibility;
  /** Non-null only for team visibility; null once that team is deleted. */
  visibility_team_id: string | null;
  team_name?: string;
  /** Pre-computed server-side: the client never compares ids to the session. */
  is_owner: boolean;
  /** ADR-0009 case C1. False means "scope unavailable", not "broken". */
  is_valid: boolean;
  invalid_reason?: string;
  created_at: string;
  updated_at: string;
}

/** The create and update body. PATCH replaces the whole mutable surface. */
export interface ViewRequest {
  name: string;
  description: string;
  query: QueryDoc;
  visibility: ViewVisibility;
  /** Required for team visibility; send null otherwise. */
  visibility_team_id: string | null;
}

/**
 * One row of a resolved view, from either module.
 *
 * `origin` decides what may be SAID about the row, exactly as it does for a
 * search hit. A saved view is the one listing allowed to union entity shares
 * into a cross-space result set (ADR-0008 §13) — that widens which rows are
 * visible, not what may be disclosed about them. For `share` rows the viewer
 * cannot enter the space, so the server sends no space_id/space_key/space_name
 * and no key (the key embeds the space key), and the surface must render
 * provenance rather than invent a container.
 */
export interface ViewResult {
  module: ViewModule;
  id: string;
  /** Absent on a share-only row: the key is composed from the space key. */
  key?: string;
  title: string;
  origin: 'space' | 'share';
  /** Absent on a share-only row. */
  space_id?: string;
  space_key?: string;
  space_name?: string;
  status: string;
  priority: string;
  assignee_id: string | null;
  /**
   * Joined in the fan-out, not looked up per row — a per-row lookup is the
   * shape spec §2.5 case 23 forbids inside a list handler. Null when
   * unassigned, and also when the id names no user.
   */
  assignee_name: string | null;
  /** Vector only. */
  kind?: string;
  /** Vector only. */
  sprint_id?: string;
  created_at: string;
  updated_at: string;
  due_at?: string;
  resolved_at?: string;
}

/** One keyset-paginated page of results. */
export interface ViewResultPage {
  results: ViewResult[];
  next_cursor: string;
  has_more: boolean;
}

/**
 * The names the API contract uses. Exported as aliases so a call site may spell
 * either; they are the same types, not copies.
 */
export type { ViewResult as Result, ViewResultPage as ResultPage };

/** The wire shape before null-coalescing. Go serialises an empty slice as null. */
interface RawResultPage {
  results: ViewResult[] | null;
  next_cursor: string;
  has_more: boolean;
}

/**
 * toResultPage fills in what Go may serialise as null — the results array
 * itself, since a page with no rows arrives as null and `.map` on null takes
 * the whole page down, which is the failure class
 * web/e2e/null-collections.spec.ts exists for. (It used to normalise a
 * per-row `labels` array too; that field died with the entity-tags
 * convergence.)
 */
function toResultPage(raw: RawResultPage | null | undefined): ViewResultPage {
  return {
    results: raw?.results ?? [],
    next_cursor: raw?.next_cursor ?? '',
    has_more: raw?.has_more ?? false,
  };
}

/** The shared `?cursor=&limit=` suffix. Absent parameters produce no query. */
function viewPageQuery(cursor?: string, limit?: number): string {
  const params = new URLSearchParams();
  if (cursor) params.set('cursor', cursor);
  if (limit) params.set('limit', String(limit));
  const qs = params.toString();
  return qs ? `?${qs}` : '';
}

async function fetchSavedViews(orgId: string): Promise<SavedView[]> {
  const data = await apiFetch<{ views: SavedView[] | null }>(`/orgs/${orgId}/views`);
  return data?.views ?? [];
}

async function fetchSavedView(orgId: string, viewId: string): Promise<SavedView> {
  return apiFetch<SavedView>(`/orgs/${orgId}/views/${viewId}`);
}

async function createSavedView(orgId: string, req: ViewRequest): Promise<SavedView> {
  return apiFetch<SavedView>(`/orgs/${orgId}/views`, {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

async function updateSavedView(
  orgId: string,
  viewId: string,
  req: ViewRequest,
): Promise<SavedView> {
  return apiFetch<SavedView>(`/orgs/${orgId}/views/${viewId}`, {
    method: 'PATCH',
    body: JSON.stringify(req),
  });
}

async function deleteSavedView(orgId: string, viewId: string): Promise<void> {
  return apiFetch<void>(`/orgs/${orgId}/views/${viewId}`, { method: 'DELETE' });
}

async function fetchViewResults(
  orgId: string,
  viewId: string,
  cursor?: string,
  limit?: number,
): Promise<ViewResultPage> {
  const raw = await apiFetch<RawResultPage>(
    `/orgs/${orgId}/views/${viewId}/results${viewPageQuery(cursor, limit)}`,
  );
  return toResultPage(raw);
}

async function previewViewResults(
  orgId: string,
  query: QueryDoc,
  cursor?: string,
  limit?: number,
): Promise<ViewResultPage> {
  const raw = await apiFetch<RawResultPage>(
    `/orgs/${orgId}/views/preview${viewPageQuery(cursor, limit)}`,
    { method: 'POST', body: JSON.stringify({ query }) },
  );
  return toResultPage(raw);
}

/** Every view whose definition reaches the caller: their own plus shared. */
export function useSavedViews(orgId: string, opts?: QueryOpts<SavedView[]>) {
  return useQuery<SavedView[], APIError>({
    queryKey: queryKeys.views(orgId),
    queryFn: () => fetchSavedViews(orgId),
    enabled: !!orgId,
    ...opts,
  });
}

/**
 * One saved view's definition. An invalid view resolves normally here — the
 * degradation is reported in `is_valid`, not as a failed request — so this
 * hook's error state means the view is genuinely missing or out of reach.
 */
export function useSavedView(orgId: string, viewId: string, opts?: QueryOpts<SavedView>) {
  return useQuery<SavedView, APIError>({
    queryKey: queryKeys.view(orgId, viewId),
    queryFn: () => fetchSavedView(orgId, viewId),
    enabled: !!orgId && !!viewId,
    ...opts,
  });
}

/**
 * One page of a view's results, resolved for the calling viewer.
 *
 * Paging is keyset, exactly as the audit log's is: the caller owns the cursor
 * and the stack of cursors that led to it, and passes the current one here.
 * Pass `{ placeholderData: (prev) => prev }` to keep the visible page on screen
 * while the next one loads instead of flashing an empty state.
 */
export function useViewResults(
  orgId: string,
  viewId: string,
  cursor?: string,
  limit?: number,
  opts?: QueryOpts<ViewResultPage>,
) {
  return useQuery<ViewResultPage, APIError>({
    queryKey: queryKeys.viewResults(orgId, viewId, cursor, limit),
    queryFn: () => fetchViewResults(orgId, viewId, cursor, limit),
    enabled: !!orgId && !!viewId,
    ...opts,
  });
}

export function useCreateView(orgId: string) {
  const queryClient = useQueryClient();
  return useMutation<SavedView, APIError, ViewRequest>({
    mutationFn: (req) => createSavedView(orgId, req),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.views(orgId) });
    },
  });
}

/** PATCH replaces the whole mutable surface, so the body is a full ViewRequest. */
export interface UpdateViewVars {
  viewId: string;
  req: ViewRequest;
}

export function useUpdateView(orgId: string) {
  const queryClient = useQueryClient();
  return useMutation<SavedView, APIError, UpdateViewVars>({
    mutationFn: ({ viewId, req }) => updateSavedView(orgId, viewId, req),
    onSuccess: () => {
      // The prefix key covers the list, this view and its cached result pages.
      // Dropping the pages is the point: the edited query selects other rows.
      queryClient.invalidateQueries({ queryKey: queryKeys.views(orgId) });
    },
  });
}

export function useDeleteView(orgId: string) {
  const queryClient = useQueryClient();
  return useMutation<void, APIError, string>({
    mutationFn: (viewId) => deleteSavedView(orgId, viewId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.views(orgId) });
    },
  });
}

export interface PreviewViewVars {
  query: QueryDoc;
  cursor?: string;
  limit?: number;
}

/**
 * The builder's live results, through the identical path a saved view uses —
 * so what the builder shows is what the saved view will return.
 *
 * A mutation rather than a query, and deliberately uncached: the document
 * changes on every keystroke, so a stable key would either collide across
 * edits or accumulate one cache entry per intermediate state. It invalidates
 * nothing — a preview changes no server state.
 *
 * A rejected document arrives as a 422 whose message is written server-side
 * for people; pass it through friendlyErrorMessage and render it as-is rather
 * than re-wording it.
 */
export function usePreviewResults(orgId: string) {
  return useMutation<ViewResultPage, APIError, PreviewViewVars>({
    mutationFn: ({ query, cursor, limit }) => previewViewResults(orgId, query, cursor, limit),
  });
}

// ---------------------------------------------------------------------------
// Beacon queues (P4). A queue IS a saved view — the same QueryDoc, the same
// resolution path, the same ResultPage — bound to one Beacon space and ordered
// among that space's queues. It is not a second kind of object, and nothing
// here re-implements the view family's shapes; `toResultPage` and
// `viewPageQuery` above are shared deliberately.
//
// Three things about this family the UI must get right:
//
//  1. `can_manage` IS THE ANSWER, and it arrives on the wire. The server gates
//     every mutation on `manage_queue` (ADR-0007 puts it at the agent role), so
//     a contributor lists queues perfectly well and gets `can_manage: false`.
//     Render the create, edit, reorder and delete controls from that flag; do
//     not reconstruct the capability rule client-side from a role.
//  2. REORDER SENDS THE WHOLE ORDER. The body must be a permutation of the
//     space's live queues — every one exactly once, nothing else. A partial or
//     duplicated list is refused with 422 and changes nothing, which is the
//     point: a half-order would silently interleave the queues nobody named.
//  3. Results resolve PER VIEWER. An "Assigned to me" queue shows each agent
//     their own work. Two agents seeing different rows is the feature; it is
//     never stale data and never a sync problem.
// ---------------------------------------------------------------------------

/** One queue: a space-bound, ordered saved view. */
export interface Queue {
  id: string;
  space_id: string;
  /** Its slot in the space's order. Changed by reorder alone, never by update. */
  position: number;
  name: string;
  description: string;
  /** The stored filter document — the same one the view builder edits. */
  query: QueryDoc;
  owner_id: string;
  owner_name?: string;
  /**
   * Pre-computed server-side from `manage_queue` on the space. The client never
   * derives this from a role: see note 1 above.
   */
  can_manage: boolean;
}

/**
 * The list response. `can_manage` is repeated at the top level so an EMPTY
 * space still answers "may this reader add queues?" — a per-row flag cannot,
 * because there are no rows.
 */
export interface QueueList {
  queues: Queue[];
  can_manage: boolean;
}

/**
 * The create and update body. Deliberately narrower than `ViewRequest`: a
 * queue's visibility is always the space it belongs to, and its position is
 * moved by the reorder endpoint alone.
 */
export interface QueueRequest {
  name: string;
  description: string;
  query: QueryDoc;
}

function queueBase(orgId: string, spaceId: string): string {
  return `/orgs/${orgId}/spaces/${spaceId}/queues`;
}

/**
 * Go serialises an empty slice as `null`, so both fields are coalesced rather
 * than trusted — a component mapping over null takes the whole page down, which
 * is the failure class web/e2e/null-collections.spec.ts exists for. The
 * `can_manage` fallback is FALSE: a missing capability answer must hide the
 * management controls, never offer them.
 */
interface RawQueueList {
  queues: Queue[] | null;
  can_manage: boolean | null;
}

async function fetchQueues(orgId: string, spaceId: string): Promise<QueueList> {
  const data = await apiFetch<RawQueueList | null>(queueBase(orgId, spaceId));
  return { queues: data?.queues ?? [], can_manage: data?.can_manage ?? false };
}

async function createQueue(orgId: string, spaceId: string, req: QueueRequest): Promise<Queue> {
  return apiFetch<Queue>(queueBase(orgId, spaceId), {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

async function createDefaultQueues(orgId: string, spaceId: string): Promise<number> {
  const data = await apiFetch<{ created: number | null }>(`${queueBase(orgId, spaceId)}/defaults`, {
    method: 'POST',
  });
  return data?.created ?? 0;
}

/**
 * Sets the whole order at once. `queueIds` must name every live queue in the
 * space exactly once — see note 2 at the top of this section.
 */
async function reorderQueues(orgId: string, spaceId: string, queueIds: string[]): Promise<void> {
  return apiFetch<void>(`${queueBase(orgId, spaceId)}/order`, {
    method: 'PUT',
    body: JSON.stringify({ queue_ids: queueIds }),
  });
}

async function updateQueue(
  orgId: string,
  spaceId: string,
  queueId: string,
  req: QueueRequest,
): Promise<Queue> {
  return apiFetch<Queue>(`${queueBase(orgId, spaceId)}/${queueId}`, {
    method: 'PATCH',
    body: JSON.stringify(req),
  });
}

async function deleteQueue(orgId: string, spaceId: string, queueId: string): Promise<void> {
  return apiFetch<void>(`${queueBase(orgId, spaceId)}/${queueId}`, { method: 'DELETE' });
}

async function fetchQueueResults(
  orgId: string,
  spaceId: string,
  queueId: string,
  cursor?: string,
  limit?: number,
): Promise<ViewResultPage> {
  const raw = await apiFetch<RawResultPage>(
    `${queueBase(orgId, spaceId)}/${queueId}/results${viewPageQuery(cursor, limit)}`,
  );
  return toResultPage(raw);
}

/**
 * A space's queues in display order, with the reader's management answer.
 *
 * Reading needs only space-readability, so this succeeds for a viewer as
 * readily as for an agent — the difference lands in `can_manage`.
 */
export function useQueues(orgId: string, spaceId: string, opts?: QueryOpts<QueueList>) {
  return useQuery<QueueList, APIError>({
    queryKey: queryKeys.queues(orgId, spaceId),
    queryFn: () => fetchQueues(orgId, spaceId),
    enabled: !!orgId && !!spaceId,
    ...opts,
  });
}

/**
 * One page of a queue's results, resolved for the calling viewer.
 *
 * Paging is keyset, exactly as `useViewResults` is: the caller owns the cursor
 * and the stack of cursors that led to it. Pass
 * `{ placeholderData: (prev) => prev }` to keep the visible page on screen
 * while the next one loads instead of flashing an empty state.
 */
export function useQueueResults(
  orgId: string,
  spaceId: string,
  queueId: string,
  cursor?: string,
  limit?: number,
  opts?: QueryOpts<ViewResultPage>,
) {
  return useQuery<ViewResultPage, APIError>({
    queryKey: queryKeys.queueResults(orgId, spaceId, queueId, cursor, limit),
    queryFn: () => fetchQueueResults(orgId, spaceId, queueId, cursor, limit),
    enabled: !!orgId && !!spaceId && !!queueId,
    ...opts,
  });
}

export function useCreateQueue(orgId: string, spaceId: string) {
  const queryClient = useQueryClient();
  return useMutation<Queue, APIError, QueueRequest>({
    mutationFn: (req) => createQueue(orgId, spaceId, req),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.queues(orgId, spaceId) });
    },
  });
}

/**
 * The one-click starting set. Idempotent server-side (ON CONFLICT DO NOTHING
 * per name), and it reports how many it actually created — which is how the UI
 * can say "added 2" rather than claiming four every time.
 */
export function useCreateDefaultQueues(orgId: string, spaceId: string) {
  const queryClient = useQueryClient();
  return useMutation<number, APIError, void>({
    mutationFn: () => createDefaultQueues(orgId, spaceId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.queues(orgId, spaceId) });
    },
  });
}

/**
 * Writes the space's whole queue order in one transaction.
 *
 * The variable is the COMPLETE ordered list of queue ids. Callers build it from
 * the full list they are showing and move one entry within it; sending only the
 * pair that swapped is a 422 that changes nothing.
 */
export function useReorderQueues(orgId: string, spaceId: string) {
  const queryClient = useQueryClient();
  return useMutation<void, APIError, string[]>({
    mutationFn: (queueIds) => reorderQueues(orgId, spaceId, queueIds),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.queues(orgId, spaceId) });
    },
  });
}

/** PATCH replaces the whole mutable surface, so the body is a full QueueRequest. */
export interface UpdateQueueVars {
  queueId: string;
  req: QueueRequest;
}

export function useUpdateQueue(orgId: string, spaceId: string) {
  const queryClient = useQueryClient();
  return useMutation<Queue, APIError, UpdateQueueVars>({
    mutationFn: ({ queueId, req }) => updateQueue(orgId, spaceId, queueId, req),
    onSuccess: () => {
      // The prefix key covers the list and every cached result page. Dropping
      // the pages is the point: the edited query selects other rows.
      queryClient.invalidateQueries({ queryKey: queryKeys.queues(orgId, spaceId) });
    },
  });
}

export function useDeleteQueue(orgId: string, spaceId: string) {
  const queryClient = useQueryClient();
  return useMutation<void, APIError, string>({
    mutationFn: (queueId) => deleteQueue(orgId, spaceId, queueId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.queues(orgId, spaceId) });
    },
  });
}

// Re-export create helpers for direct use
// ---------------------------------------------------------------------------
// Dashboards and gadgets (P5, ADR-0009)
// ---------------------------------------------------------------------------
//
// A dashboard owns LAYOUT. Every gadget's data comes from the saved-view
// layer: the dashboard response hands out the filter document each tile should
// resolve, and the tile posts it to /views/preview or /views/aggregate — the
// same two endpoints the filter builder uses. There is no second results path,
// and none should be added.

/** Which product surface a dashboard belongs to. There is no Codex module. */
export type DashboardModule = 'home' | 'beacon' | 'vector';

/**
 * What a tile should render. Computed SERVER-SIDE, covering every ADR-0009
 * degradation rule, so no client re-derives an audience rule to decide whether
 * to draw a gadget.
 */
export type GadgetState =
  | 'ready'
  | 'unknown_gadget'
  | 'view_required'
  | 'view_unreadable'
  | 'scope_unavailable';

/** How the client draws a gadget. The registry dispatches on it. */
export type GadgetRender = 'list' | 'stat' | 'breakdown' | 'note';

/** A gadget's configuration. Every key is optional and validated server-side. */
export interface GadgetConfig {
  title?: string;
  limit?: number;
  group_by?: string;
  body?: string;
}

/** One resolved tile. */
export interface DashboardGadget {
  id: string;
  gadget_key: string;
  position: number;
  col_span: number;
  saved_view_id: string | null;
  config: GadgetConfig;
  state: GadgetState;
  /** The heading: the config override, else the view's name, else the kind's. */
  title: string;
  render?: GadgetRender;
  /**
   * The document this tile resolves for its data. Present only when the state
   * is `ready` and the gadget has a query at all — a note has none, and an
   * unreadable view's document is deliberately withheld.
   */
  query?: QueryDoc;
  view_name?: string;
  invalid_reason?: string;
}

export interface Dashboard {
  id: string;
  owner_id: string;
  owner_name?: string;
  name: string;
  description: string;
  module: DashboardModule;
  is_default: boolean;
  /** True when this row came from the starter layout rather than from a person. */
  is_seeded: boolean;
  visibility: ViewVisibility;
  visibility_team_id: string | null;
  team_name?: string;
  /** Pre-computed server-side: the client never compares ids to the session. */
  is_owner: boolean;
  /** False means "audience unavailable", not "broken". Never an error state. */
  is_valid: boolean;
  invalid_reason?: string;
  created_at: string;
  updated_at: string;
}

/** A dashboard with its gadgets. The detail routes return this shape. */
export interface DashboardDetail extends Dashboard {
  gadgets: DashboardGadget[];
}

/** The create and update body. PATCH replaces the whole mutable surface. */
export interface DashboardRequest {
  name: string;
  description: string;
  module: DashboardModule;
  visibility: ViewVisibility;
  /** Required for team visibility; send null otherwise. */
  visibility_team_id: string | null;
  /** Omit to leave the flag alone — sending false clears somebody's default. */
  is_default?: boolean;
}

/**
 * One tile in a layout write. There is no position: the ORDER of the array is
 * the display order and the server assigns positions from it, so a client
 * cannot produce a gap or a duplicate.
 */
export interface GadgetRequest {
  gadget_key: string;
  col_span?: number;
  saved_view_id?: string | null;
  config?: GadgetConfig;
}

/** One group of a breakdown. */
export interface AggregateBucket {
  key: string;
  label: string;
  count: number;
  /** The rollup carrying everything past the server's bucket cap. */
  other?: boolean;
  other_buckets?: number;
}

export interface AggregateResult {
  total: number;
  buckets: AggregateBucket[];
  truncated: boolean;
}

interface RawDashboardDetail extends Dashboard {
  gadgets: DashboardGadget[] | null;
}

interface RawAggregate {
  total: number | null;
  buckets: AggregateBucket[] | null;
  truncated: boolean | null;
}

function dashboardBase(orgId: string): string {
  return `/orgs/${orgId}/dashboards`;
}

function toDashboardDetail(raw: RawDashboardDetail | null | undefined): DashboardDetail {
  return {
    ...(raw as DashboardDetail),
    gadgets: (raw?.gadgets ?? []).map((g) => ({ ...g, config: g.config ?? {} })),
  };
}

async function fetchDashboards(orgId: string, module?: DashboardModule): Promise<Dashboard[]> {
  const qs = module ? `?module=${encodeURIComponent(module)}` : '';
  const data = await apiFetch<{ dashboards: Dashboard[] | null }>(`${dashboardBase(orgId)}${qs}`);
  return data?.dashboards ?? [];
}

async function fetchDashboard(orgId: string, dashboardId: string): Promise<DashboardDetail> {
  return toDashboardDetail(
    await apiFetch<RawDashboardDetail>(`${dashboardBase(orgId)}/${dashboardId}`),
  );
}

/**
 * The Home dashboard. A GET that seeds a starter layout on a first visit —
 * idempotently, by a database constraint rather than by a check, so two tabs
 * opening Home cannot produce two dashboards.
 */
async function fetchHomeDashboard(orgId: string): Promise<DashboardDetail> {
  return toDashboardDetail(await apiFetch<RawDashboardDetail>(`${dashboardBase(orgId)}/home`));
}

async function createDashboard(orgId: string, req: DashboardRequest): Promise<Dashboard> {
  return apiFetch<Dashboard>(dashboardBase(orgId), {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

async function updateDashboard(
  orgId: string,
  dashboardId: string,
  req: DashboardRequest,
): Promise<Dashboard> {
  return apiFetch<Dashboard>(`${dashboardBase(orgId)}/${dashboardId}`, {
    method: 'PATCH',
    body: JSON.stringify(req),
  });
}

async function deleteDashboard(orgId: string, dashboardId: string): Promise<void> {
  return apiFetch<void>(`${dashboardBase(orgId)}/${dashboardId}`, { method: 'DELETE' });
}

/** Replaces the WHOLE gadget collection. Never send a partial layout. */
async function saveDashboardGadgets(
  orgId: string,
  dashboardId: string,
  gadgets: GadgetRequest[],
): Promise<DashboardDetail> {
  return toDashboardDetail(
    await apiFetch<RawDashboardDetail>(`${dashboardBase(orgId)}/${dashboardId}/gadgets`, {
      method: 'PUT',
      body: JSON.stringify({ gadgets }),
    }),
  );
}

/**
 * Counts a query's results for the CALLER, optionally grouped. The grouping
 * happens in the database: a count gadget must never fetch pages and count
 * them, which would stop at the page size and under-report the busy view
 * somebody put the count on.
 */
async function aggregateQuery(
  orgId: string,
  query: QueryDoc,
  groupBy?: string,
): Promise<AggregateResult> {
  const raw = await apiFetch<RawAggregate>(`/orgs/${orgId}/views/aggregate`, {
    method: 'POST',
    body: JSON.stringify(groupBy ? { query, group_by: groupBy } : { query }),
  });
  return {
    total: raw?.total ?? 0,
    buckets: raw?.buckets ?? [],
    truncated: raw?.truncated ?? false,
  };
}

/** Every dashboard whose definition reaches the caller. */
export function useDashboards(
  orgId: string,
  module?: DashboardModule,
  opts?: QueryOpts<Dashboard[]>,
) {
  return useQuery<Dashboard[], APIError>({
    queryKey: queryKeys.dashboards(orgId, module),
    queryFn: () => fetchDashboards(orgId, module),
    enabled: !!orgId,
    ...opts,
  });
}

/** One dashboard with its gadgets, resolved for the calling viewer. */
export function useDashboard(
  orgId: string,
  dashboardId: string,
  opts?: QueryOpts<DashboardDetail>,
) {
  return useQuery<DashboardDetail, APIError>({
    queryKey: queryKeys.dashboard(orgId, dashboardId),
    queryFn: () => fetchDashboard(orgId, dashboardId),
    enabled: !!orgId && !!dashboardId,
    ...opts,
  });
}

/** The caller's Home dashboard, seeded on a first visit. */
export function useHomeDashboard(orgId: string, opts?: QueryOpts<DashboardDetail>) {
  return useQuery<DashboardDetail, APIError>({
    queryKey: queryKeys.homeDashboard(orgId),
    queryFn: () => fetchHomeDashboard(orgId),
    enabled: !!orgId,
    ...opts,
  });
}

/**
 * A gadget's rows. Keyed by the DOCUMENT rather than the gadget, so two tiles
 * showing one view share a cache entry and a repointed tile gets a fresh one.
 */
export function useGadgetResults(
  orgId: string,
  query: QueryDoc | undefined,
  limit: number,
  opts?: QueryOpts<ViewResultPage>,
) {
  const queryJSON = query ? JSON.stringify(query) : '';
  return useQuery<ViewResultPage, APIError>({
    queryKey: queryKeys.gadgetResults(orgId, queryJSON, limit),
    queryFn: () => previewViewResults(orgId, JSON.parse(queryJSON) as QueryDoc, undefined, limit),
    enabled: !!orgId && !!queryJSON,
    ...opts,
  });
}

/** A gadget's count, optionally grouped. */
export function useGadgetAggregate(
  orgId: string,
  query: QueryDoc | undefined,
  groupBy?: string,
  opts?: QueryOpts<AggregateResult>,
) {
  const queryJSON = query ? JSON.stringify(query) : '';
  return useQuery<AggregateResult, APIError>({
    queryKey: queryKeys.gadgetAggregate(orgId, queryJSON, groupBy ?? ''),
    queryFn: () => aggregateQuery(orgId, JSON.parse(queryJSON) as QueryDoc, groupBy),
    enabled: !!orgId && !!queryJSON,
    ...opts,
  });
}

export function useCreateDashboard(orgId: string) {
  const queryClient = useQueryClient();
  return useMutation<Dashboard, APIError, DashboardRequest>({
    mutationFn: (req) => createDashboard(orgId, req),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['dashboards', orgId] });
    },
  });
}

export interface UpdateDashboardVars {
  dashboardId: string;
  req: DashboardRequest;
}

export function useUpdateDashboard(orgId: string) {
  const queryClient = useQueryClient();
  return useMutation<Dashboard, APIError, UpdateDashboardVars>({
    mutationFn: ({ dashboardId, req }) => updateDashboard(orgId, dashboardId, req),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['dashboards', orgId] });
    },
  });
}

export function useDeleteDashboard(orgId: string) {
  const queryClient = useQueryClient();
  return useMutation<void, APIError, string>({
    mutationFn: (dashboardId) => deleteDashboard(orgId, dashboardId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['dashboards', orgId] });
    },
  });
}

export interface SaveGadgetsVars {
  dashboardId: string;
  gadgets: GadgetRequest[];
}

export function useSaveDashboardGadgets(orgId: string) {
  const queryClient = useQueryClient();
  return useMutation<DashboardDetail, APIError, SaveGadgetsVars>({
    mutationFn: ({ dashboardId, gadgets }) => saveDashboardGadgets(orgId, dashboardId, gadgets),
    onSuccess: () => {
      // The prefix, not the one dashboard: a layout write changes the detail,
      // and a gadget that gained or lost a view changes what the list's
      // provenance chips should say.
      queryClient.invalidateQueries({ queryKey: ['dashboards', orgId] });
    },
  });
}

export {
  createSpace,
  createTicket,
  createWikiPage,
  createProjectItem,
  createSprint,
  updateOrganization,
  type UpdateOrganizationRequest,
  type CreateSpaceRequest,
  type CreateTicketRequest,
  type UpdateTicketRequest,
  type CreateWikiPageRequest,
  type UpdateWikiPageRequest,
  type CreateProjectItemRequest,
  type UpdateProjectItemRequest,
  type CreateSprintRequest,
  type LoginRequest,
  type RegisterRequest,
  type AuthResponse,
  type CreateCommentRequest,
  type UpdateSpaceRequest,
  type CreateTeamRequest,
  type UpdateTeamRequest,
  type PutTeamMemberRequest,
  type CreateGrantRequest,
  type CreateInvitesRequest,
  type UpdatePersonRequest,
};

// ---------------------------------------------------------------------------
// Cross-module search (P6, spec §5/§7)
// ---------------------------------------------------------------------------

export type SearchModule = 'codex' | 'beacon' | 'vector';

/**
 * How a hit became visible, which decides what may be SAID about it.
 *
 * `share` means the entity reached this viewer through a share on the entity
 * itself; they cannot enter its space. The server therefore sends no
 * space_id/space_key/space_name for such a hit (matrix case 16), so the surface
 * must render provenance rather than a container — and must not invent one.
 */
export type SearchOrigin = 'space' | 'share';

/**
 * The three ways a search comes back with nothing. They are NOT the same
 * answer and must not render alike:
 *   ok                   — it ran; empty results mean nothing matched
 *   no_searchable_terms  — the query reduced to an empty tsquery (stopwords,
 *                          punctuation), so nothing COULD match
 *   no_readable_scope    — this viewer can read no space and holds no share
 */
export type SearchState = 'ok' | 'no_searchable_terms' | 'no_readable_scope';

export interface SearchHit {
  module: SearchModule;
  id: string;
  title: string;
  origin: SearchOrigin;
  /** Absent on a share-only hit. */
  space_id?: string;
  space_key?: string;
  space_name?: string;
  number?: number;
  item_key?: string;
  kind?: string;
  status?: string;
  priority?: string;
  path?: string;
  /**
   * ts_headline excerpt, present only when requested. Delimited by U+0002 and
   * U+0003, NOT markup — render it with splitSnippet, never with
   * dangerouslySetInnerHTML.
   */
  snippet?: string;
  updated_at: string;
}

export interface SearchResults {
  results: SearchHit[];
  next_cursor?: string;
  /** The EFFECTIVE module set, which a tag: filter can narrow implicitly. */
  modules: SearchModule[];
  tag?: string;
  state: SearchState;
}

export interface SearchOptions {
  limit?: number;
  cursor?: string;
  /** Ask for ts_headline excerpts. Costs one query per module in the page. */
  snippet?: boolean;
}

async function fetchSearch(orgId: string, q: string, opts: SearchOptions): Promise<SearchResults> {
  const params = new URLSearchParams({ q });
  if (opts.limit) params.set('limit', String(opts.limit));
  if (opts.cursor) params.set('cursor', opts.cursor);
  if (opts.snippet) params.set('snippet', 'true');
  return apiFetch<SearchResults>(`/orgs/${orgId}/search?${params.toString()}`);
}

/**
 * Cross-module search.
 *
 * Disabled on an empty query rather than fired with one: the server answers
 * no_searchable_terms, which is a correct answer to a question nobody asked,
 * and issuing it on every keystroke of an empty box wastes the per-request
 * query budget that is already at its ceiling.
 */
export function useSearch(
  orgId: string,
  q: string,
  opts: SearchOptions = {},
  queryOpts?: QueryOpts<SearchResults>,
) {
  const limit = opts.limit ?? 0;
  const cursor = opts.cursor ?? '';
  const snippet = opts.snippet ?? false;
  return useQuery<SearchResults, APIError>({
    queryKey: queryKeys.search(orgId, q, limit, cursor, snippet),
    queryFn: () => fetchSearch(orgId, q, opts),
    enabled: !!orgId && q.trim().length > 0,
    ...queryOpts,
  });
}

/**
 * Splits a ts_headline snippet into plain and highlighted runs.
 *
 * The server delimits matches with U+0002 / U+0003 rather than markup, because
 * ts_headline escapes NOTHING — it returns the source text with the delimiters
 * inserted, so HTML delimiters would hand the client a fragment built out of
 * stored page content. Splitting here and wrapping the pieces in real elements
 * highlights without ever interpreting stored content as markup, which is why
 * no caller should reach for dangerouslySetInnerHTML.
 */
export function splitSnippet(snippet: string): Array<{ text: string; match: boolean }> {
  const out: Array<{ text: string; match: boolean }> = [];
  let rest = snippet;
  while (rest.length > 0) {
    const start = rest.indexOf('\u0002');
    if (start === -1) {
      out.push({ text: rest, match: false });
      break;
    }
    if (start > 0) out.push({ text: rest.slice(0, start), match: false });
    const end = rest.indexOf('\u0003', start + 1);
    if (end === -1) {
      // Unterminated: treat the remainder as plain text rather than dropping
      // it. A truncated fragment is a display nuisance; losing it is data.
      out.push({ text: rest.slice(start + 1), match: false });
      break;
    }
    out.push({ text: rest.slice(start + 1, end), match: true });
    rest = rest.slice(end + 1);
  }
  return out.filter((p) => p.text.length > 0);
}

// ---------------------------------------------------------------------------
// Customer portal — the agent-side configuration surface (v0.4.2 A1)
// ---------------------------------------------------------------------------
//
// These are ORDINARY INTERNAL routes (/orgs/{orgId}/spaces/{spaceId}/portal)
// on the default internal credential — nothing here touches the portal
// session machinery in the requester section below, and the two cache
// families are deliberately disjoint (see queryKeys.portalAdmin).
//
// manage_space is enforced in the handler, so 403 is a stable answer for a
// member without it. GET answers 404 for a space that simply has no portal
// yet — that 404 is the "offer to create one" signal, not an error state.

/** The agent's view of a portal: unlike the customer's, it carries the key. */
export interface AdminPortalConfig {
  portal_key: string;
  name: string;
  intro: string;
  enabled: boolean;
  portal_id: string;
}

export interface CreatePortalRequest {
  name: string;
  intro?: string;
}

/**
 * A partial update: omit a field to leave it unchanged. The server treats
 * every field as three-state (absent / explicit null / value), and
 * JSON.stringify dropping undefined keys is exactly that wire contract.
 * `intro: null` clears the introduction; name and enabled are never null —
 * the server answers 400 rather than guessing.
 */
export interface UpdatePortalRequest {
  enabled?: boolean;
  name?: string;
  intro?: string | null;
}

function portalAdminBase(orgId: string, spaceId: string): string {
  return `/orgs/${orgId}/spaces/${spaceId}/portal`;
}

async function fetchPortalConfig(orgId: string, spaceId: string): Promise<AdminPortalConfig> {
  return apiFetch<AdminPortalConfig>(portalAdminBase(orgId, spaceId));
}

async function createPortal(
  orgId: string,
  spaceId: string,
  req: CreatePortalRequest,
): Promise<AdminPortalConfig> {
  return apiFetch<AdminPortalConfig>(portalAdminBase(orgId, spaceId), {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

async function updatePortal(
  orgId: string,
  spaceId: string,
  req: UpdatePortalRequest,
): Promise<AdminPortalConfig> {
  return apiFetch<AdminPortalConfig>(portalAdminBase(orgId, spaceId), {
    method: 'PATCH',
    body: JSON.stringify(req),
  });
}

export function usePortalConfig(
  orgId: string,
  spaceId: string,
  opts?: QueryOpts<AdminPortalConfig>,
) {
  return useQuery<AdminPortalConfig, APIError>({
    queryKey: queryKeys.portalAdmin(orgId, spaceId),
    queryFn: () => fetchPortalConfig(orgId, spaceId),
    enabled: !!orgId && !!spaceId,
    retry: (failureCount, error) => {
      // Both are stable answers, not flakes: 404 is "no portal yet" (the
      // create affordance) and 403 is a caller without manage_space.
      if (error?.status === 403 || error?.status === 404) return false;
      return failureCount < 2;
    },
    ...opts,
  });
}

export function useCreatePortal(orgId: string, spaceId: string) {
  const queryClient = useQueryClient();
  return useMutation<AdminPortalConfig, APIError, CreatePortalRequest>({
    mutationFn: (req) => createPortal(orgId, spaceId, req),
    onSuccess: (cfg) => {
      // Write through rather than only invalidating: the settings section
      // re-renders from this immediately — the board-config shape.
      queryClient.setQueryData(queryKeys.portalAdmin(orgId, spaceId), cfg);
      queryClient.invalidateQueries({ queryKey: queryKeys.portalAdmin(orgId, spaceId) });
    },
  });
}

/** One hook for the whole partial update — enabled, name and intro together. */
export function useUpdatePortal(orgId: string, spaceId: string) {
  const queryClient = useQueryClient();
  return useMutation<AdminPortalConfig, APIError, UpdatePortalRequest>({
    mutationFn: (req) => updatePortal(orgId, spaceId, req),
    onSuccess: (cfg) => {
      queryClient.setQueryData(queryKeys.portalAdmin(orgId, spaceId), cfg);
      queryClient.invalidateQueries({ queryKey: queryKeys.portalAdmin(orgId, spaceId) });
    },
  });
}

// ---------------------------------------------------------------------------
// Customer portal — the requester surface (migration 044)
// ---------------------------------------------------------------------------
//
// Every type below is a SEPARATE shape, never a reuse of Ticket or Comment,
// and that is the point rather than an accident of ordering. The server's wire
// types (internal/core/api/portal/handler.go) carry no space id, space key,
// space name, ticket number, assignee, reporter, priority, label or workflow
// state, because a field that does not exist cannot be leaked by a serialiser
// change. Mirroring them exactly here means a client that reaches for
// `ticket.space_id` on a portal response does not compile, instead of reading
// undefined and rendering nothing while somebody assumes the field is coming.
//
// Two rules govern this whole section:
//
//  1. `status` is DISPLAY TEXT, already translated server-side into requester
//     language ('Received' | 'In progress' | 'Resolved' | 'Closed', with
//     anything unrecognised falling through to 'In progress'). Do not map it,
//     re-label it, switch on it, or type it as a union — tickets.status has no
//     database CHECK and the workflow route writes arbitrary state names into
//     it, so a client-side mapping would eventually print an internal workflow
//     state to a customer, which is exactly the leak the server-side
//     translation exists to prevent.
//  2. `reference` is a bare ticket UUID BY DESIGN. It is not the 'BEA-42' form
//     the internal product shows, because that is composed from the space key
//     and would tell an external customer what the internal space is called.
//     Never prettify it, never derive a key from it.
//
// Credentials: the three sign-in routes take `'none'` (a stale internal token
// must never attach an org member's identity to a stranger's request) and
// everything under /my takes `'portal'`. See the `Credential` type above.

/** A portal's public face: all an anonymous visitor learns before signing in. */
export interface PortalDescribe {
  name: string;
  intro: string;
}

/** A request as it appears in the requester's own list. */
export interface PortalRequestSummary {
  /** Opaque handle — a ticket UUID. Never rendered as a key. */
  reference: string;
  summary: string;
  /** Requester-facing display text, already translated. Never re-map it. */
  status: string;
  created_at: string;
  updated_at: string;
}

/**
 * One request in full. `description` is omitempty server-side and only the
 * detail route sets it, so it is optional here and absent on every list row —
 * a list that appears to have lost the body has not lost anything.
 */
export interface PortalRequestDetail extends PortalRequestSummary {
  description?: string;
}

/**
 * One message in a request's thread.
 *
 * There is no `visibility` field, unlike {@link Comment}: the portal query
 * returns public rows exclusively, so every message here is public by
 * construction and there is nothing to disambiguate. `author` is a display
 * label, not an id — an agent's identity reaches a customer as a name and
 * nothing else.
 */
export interface PortalMessage {
  id: string;
  author: string;
  from_requester: boolean;
  body: string;
  created_at: string;
}

export interface PortalRequestDetailResponse {
  request: PortalRequestDetail;
  messages: PortalMessage[];
}

/**
 * What redeeming a sign-in link yields.
 *
 * `expires_in` is in SECONDS. Store it through `sessionFromRedeem` in
 * `lib/portalSession.ts`, which converts to an absolute ms epoch — nothing
 * re-serves this response, because there is no `GET /my/me` and no refresh
 * endpoint, so a session that is mis-stored is a session lost.
 */
export interface PortalRedeemResponse {
  session_token: string;
  expires_in: number;
  requester: { email: string; name: string };
  portal: { name: string; intro: string };
}

/**
 * The answer to asking for a sign-in link — deliberately uninformative.
 *
 * It says the same thing for a known address, an unknown one and a deactivated
 * requester, because any difference would be a free oracle for testing whether
 * an address has ever contacted this service desk. `delivered` reports whether
 * mail was dispatched, not whether the address exists, and `magic_link_url` is
 * populated only where an operator has explicitly opted into disclosure on a
 * non-production deployment — absent by default, and absent in production
 * whatever the configuration says. Do not build a "we don't know that address"
 * state out of either field.
 */
export interface PortalLinkResponse {
  status: string;
  delivered: boolean;
  magic_link_url?: string;
}

// portalBase builds the unauthenticated prefix; portalMyBase the session one.
// A portalKey is server-generated [a-z0-9]{16,32}, but it arrives here off a
// URL segment the user can type, so both encode it.
function portalBase(portalKey: string): string {
  return `/portal/${encodeURIComponent(portalKey)}`;
}

function portalMyBase(portalKey: string): string {
  return `${portalBase(portalKey)}/my`;
}

async function fetchPortalDescribe(portalKey: string): Promise<PortalDescribe> {
  return apiFetch<PortalDescribe>(portalBase(portalKey), {}, undefined, { credential: 'none' });
}

async function requestPortalLink(
  portalKey: string,
  email: string,
  name?: string,
): Promise<PortalLinkResponse> {
  // The body is assembled key by key rather than spread from the caller's
  // object: respond.DecodeJSON sets DisallowUnknownFields, so one stray key —
  // a form's `remember`, a page's `portalKey` — is a 400 with a message that
  // says nothing about which field caused it.
  const body: { email: string; name?: string } = { email };
  if (name !== undefined && name !== '') body.name = name;
  return apiFetch<PortalLinkResponse>(
    `${portalBase(portalKey)}/auth/request-link`,
    { method: 'POST', body: JSON.stringify(body) },
    undefined,
    { credential: 'none' },
  );
}

async function redeemPortalLink(token: string): Promise<PortalRedeemResponse> {
  // Redeem is NOT under {portalKey} — the single-use token identifies the
  // portal by itself, which is why this path takes no key. The hook still
  // takes one, because the caller needs it to store the session under.
  return apiFetch<PortalRedeemResponse>(
    '/portal/auth/redeem',
    { method: 'POST', body: JSON.stringify({ token }) },
    undefined,
    { credential: 'none' },
  );
}

async function submitPortalRequest(
  portalKey: string,
  summary: string,
  description: string,
): Promise<PortalRequestSummary> {
  return apiFetch<PortalRequestSummary>(
    `${portalMyBase(portalKey)}/requests`,
    { method: 'POST', body: JSON.stringify({ summary, description }) },
    undefined,
    { credential: 'portal', portalKey },
  );
}

async function fetchPortalRequests(portalKey: string): Promise<PortalRequestSummary[]> {
  // A bare array, no envelope. The server sends `[]` rather than null, but the
  // `?? []` matches every other list fetcher here and costs nothing.
  const data = await apiFetch<PortalRequestSummary[] | null>(
    `${portalMyBase(portalKey)}/requests`,
    {},
    undefined,
    { credential: 'portal', portalKey },
  );
  return data ?? [];
}

async function fetchPortalRequest(
  portalKey: string,
  reference: string,
): Promise<PortalRequestDetailResponse> {
  return apiFetch<PortalRequestDetailResponse>(
    `${portalMyBase(portalKey)}/requests/${encodeURIComponent(reference)}`,
    {},
    undefined,
    { credential: 'portal', portalKey },
  );
}

async function replyToPortalRequest(
  portalKey: string,
  reference: string,
  body: string,
): Promise<PortalMessage> {
  return apiFetch<PortalMessage>(
    `${portalMyBase(portalKey)}/requests/${encodeURIComponent(reference)}/replies`,
    { method: 'POST', body: JSON.stringify({ body }) },
    undefined,
    { credential: 'portal', portalKey },
  );
}

async function signOutOfPortal(portalKey: string): Promise<void> {
  // NO BODY AT ALL, not even '{}'. respond.DecodeJSON rejects an empty body,
  // and it would reject '{}' too on the unknown-fields rule the moment the
  // route grew a struct — but the operative reason is simpler: this route
  // decodes nothing, so anything sent is a 400 waiting to happen. Passing no
  // `body` also keeps apiFetch from setting a Content-Type for content that
  // does not exist.
  return apiFetch<void>(
    `${portalMyBase(portalKey)}/auth/sign-out`,
    { method: 'POST' },
    undefined,
    { credential: 'portal', portalKey },
  );
}

/**
 * The portal's public name and blurb.
 *
 * Unauthenticated, so this is the one portal read that works before sign-in —
 * it is what the sign-in page has to render, and a 404 from it is the honest
 * answer to "no such portal".
 */
export function usePortalDescribe(portalKey: string, opts?: QueryOpts<PortalDescribe>) {
  return useQuery<PortalDescribe, APIError>({
    queryKey: queryKeys.portalDescribe(portalKey),
    queryFn: () => fetchPortalDescribe(portalKey),
    enabled: !!portalKey,
    ...opts,
  });
}

/**
 * The requester's own requests, newest first as the server orders them.
 *
 * `requesterEmail` comes from the stored session and is part of the CACHE KEY,
 * not the request — the server scopes to the session's requester and would
 * ignore it. Gating on it therefore does double duty: it keeps the query from
 * firing before a session exists, and it keeps two requesters who used the
 * same browser from sharing one cache entry.
 */
export function usePortalRequests(
  portalKey: string,
  requesterEmail: string,
  opts?: QueryOpts<PortalRequestSummary[]>,
) {
  return useQuery<PortalRequestSummary[], APIError>({
    queryKey: queryKeys.portalRequests(portalKey, requesterEmail),
    queryFn: () => fetchPortalRequests(portalKey),
    enabled: !!portalKey && !!requesterEmail,
    ...opts,
  });
}

/**
 * One request and its public thread.
 *
 * A request belonging to somebody else answers 404, never 403 — so does a
 * malformed reference. Render both the same way; distinguishing them in the UI
 * would rebuild the oracle the server declined to give.
 */
export function usePortalRequest(
  portalKey: string,
  requesterEmail: string,
  reference: string,
  opts?: QueryOpts<PortalRequestDetailResponse>,
) {
  return useQuery<PortalRequestDetailResponse, APIError>({
    queryKey: queryKeys.portalRequest(portalKey, requesterEmail, reference),
    queryFn: () => fetchPortalRequest(portalKey, reference),
    enabled: !!portalKey && !!requesterEmail && !!reference,
    ...opts,
  });
}

/**
 * Ask for a sign-in link. Always 202 — see {@link PortalLinkResponse}.
 *
 * Nothing is invalidated because nothing cached changed: no session exists yet
 * and the portal's description did not move.
 */
export function useRequestPortalLink(portalKey: string) {
  return useMutation<PortalLinkResponse, APIError, { email: string; name?: string }>({
    mutationFn: ({ email, name }) => requestPortalLink(portalKey, email, name),
  });
}

/**
 * Exchange a magic-link token for a session.
 *
 * The portalKey is a mutation variable rather than a hook parameter because
 * the redemption page reads it from the same URL as the token, and there is no
 * earlier point at which the hook could have been given it.
 *
 * The caller stores the result — `setPortalSession(portalKey,
 * sessionFromRedeem(res))` — because storage belongs to `portalSession.ts` and
 * this module owns exactly one thing, the network. Every cached answer for
 * this portal is dropped first: the previous requester's list is still in
 * memory at this moment, and while the email in the query key means it can
 * never be SERVED to the new one, not keeping a stranger's support history in
 * a shared browser's memory is the cheaper guarantee.
 */
export function useRedeemPortalLink() {
  const queryClient = useQueryClient();
  return useMutation<PortalRedeemResponse, APIError, { portalKey: string; token: string }>({
    mutationFn: ({ token }) => redeemPortalLink(token),
    onSuccess: (_res, { portalKey }) => {
      queryClient.removeQueries({ queryKey: queryKeys.portal(portalKey) });
    },
  });
}

/**
 * Raise a request. 201 with the new row, WITHOUT its description — the create
 * response is a summary, so a caller that wants the body back must read the
 * detail route. Invalidates the list, which is the only cached thing it changed.
 */
export function useSubmitPortalRequest(portalKey: string, requesterEmail: string) {
  const queryClient = useQueryClient();
  return useMutation<
    PortalRequestSummary,
    APIError,
    { summary: string; description: string }
  >({
    mutationFn: ({ summary, description }) =>
      submitPortalRequest(portalKey, summary, description),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: queryKeys.portalRequests(portalKey, requesterEmail),
      });
    },
  });
}

/**
 * Append the requester's message to their own request.
 *
 * Only the detail is invalidated, and the list deliberately is not: a reply
 * inserts a comment and touches no column of the request itself, so the list's
 * `status` and `updated_at` are unchanged and refetching it would be a request
 * that can only return what is already on screen.
 */
export function useReplyToPortalRequest(
  portalKey: string,
  requesterEmail: string,
  reference: string,
) {
  const queryClient = useQueryClient();
  return useMutation<PortalMessage, APIError, { body: string }>({
    mutationFn: ({ body }) => replyToPortalRequest(portalKey, reference, body),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: queryKeys.portalRequest(portalKey, requesterEmail, reference),
      });
    },
  });
}

/**
 * Sign out, revoking EVERY session this requester holds — the server bumps
 * `session_generation`, so the token stops working on its next use rather than
 * lingering until it expires.
 *
 * Cached answers for the portal are removed rather than invalidated: an
 * invalidation would refetch them, and there is no longer a session to refetch
 * with. The caller still owns `clearPortalSession(portalKey)` and the
 * navigation, because a failed sign-out must not leave the UI claiming a
 * session was ended that was not.
 */
export function usePortalSignOut(portalKey: string) {
  const queryClient = useQueryClient();
  return useMutation<void, APIError, void>({
    mutationFn: () => signOutOfPortal(portalKey),
    onSuccess: () => {
      queryClient.removeQueries({ queryKey: queryKeys.portal(portalKey) });
    },
  });
}
