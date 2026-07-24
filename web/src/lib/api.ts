import {
  useQuery,
  useMutation,
  useQueryClient,
} from '@tanstack/react-query';
import type { UseQueryOptions } from '@tanstack/react-query';
import { getToken, setToken, setRefreshToken, getRefreshToken, removeToken, removeRefreshToken, getCurrentOrgId } from './auth';

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
    const humanCodes = ['VALIDATION_ERROR', 'CONFLICT', 'GONE'];
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

// ---------------------------------------------------------------------------
// Base fetch helper
// ---------------------------------------------------------------------------

async function apiFetch<T>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const headers = new Headers(options.headers);

  // Default to JSON, but never for FormData — the browser must set the
  // multipart Content-Type with its boundary itself.
  if (!headers.has('Content-Type') && options.body && !(options.body instanceof FormData)) {
    headers.set('Content-Type', 'application/json');
  }

  const token = getToken();
  if (token) {
    headers.set('Authorization', `Bearer ${token}`);
  }

  const response = await fetch(`${API_BASE_URL}${path}`, {
    ...options,
    headers,
  });

  if (!response.ok) {
    // On 401, only redirect to login for auth-critical endpoints.
    // Non-critical 401s (e.g. permission denied on a resource) should
    // throw an error without logging the user out.
    if (response.status === 401) {
      const url = response.url || '';
      const isCriticalAuthEndpoint = url.includes('/auth/login') ||
        url.includes('/auth/me') ||
        url.includes('/auth/refresh');
      if (isCriticalAuthEndpoint) {
        removeToken();
        removeRefreshToken();
        const currentPath = window.location.pathname;
        if (currentPath !== '/login') {
          window.location.href = `/login?redirect=${encodeURIComponent(currentPath)}`;
        }
      }
    }

    let body: APIErrorBody;
    try {
      body = (await response.json()) as APIErrorBody;
    } catch {
      body = {
        error: {
          code: 'unknown',
          message: response.statusText || 'Request failed',
          request_id: '',
        },
      };
    }
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

export interface Ticket {
  id: string;
  space_id: string;
  number: number | null;
  title: string;
  description: string;
  status: TicketStatus;
  priority: string;
  assignee_id: string | null;
  reporter_id: string;
  label_ids: string[];
  created_at: string;
  updated_at: string;
}

export interface WikiPage {
  id: string;
  space_id: string;
  title: string;
  content: string;
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
  labels: string[];
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

export interface Label {
  id: string;
  org_id: string;
  name: string;
  color: string;
  created_at: string;
  updated_at: string;
}

export interface Comment {
  id: string;
  entity_type: string;
  entity_id: string;
  author_id: string;
  author_name?: string;
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
  created_at: string;
}

export interface Relation {
  id: string;
  from_id: string;
  to_id: string;
  kind: string;
  created_by: string;
  to_title: string;
  to_status: string;
  to_kind: string;
}

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
  key: string;
  type: SpaceType;
  description?: string;
}

async function createSpace(orgId: string, req: CreateSpaceRequest): Promise<Space> {
  return apiFetch<Space>(`/orgs/${orgId}/spaces`, {
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
): Promise<Space> {
  return apiFetch<Space>(`/orgs/${orgId}/spaces/${spaceId}`, {
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

async function createTeam(orgId: string, req: CreateTeamRequest): Promise<Team> {
  return apiFetch<Team>(`/orgs/${orgId}/teams`, {
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

async function updateTeam(orgId: string, teamId: string, req: UpdateTeamRequest): Promise<Team> {
  return apiFetch<Team>(`/orgs/${orgId}/teams/${teamId}`, {
    method: 'PATCH',
    body: JSON.stringify(req),
  });
}

async function deleteTeam(orgId: string, teamId: string): Promise<void> {
  return apiFetch<void>(`/orgs/${orgId}/teams/${teamId}`, { method: 'DELETE' });
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
): Promise<TeamMember> {
  return apiFetch<TeamMember>(`/orgs/${orgId}/teams/${teamId}/members/${userId}`, {
    method: 'PUT',
    body: JSON.stringify(req),
  });
}

async function removeTeamMember(orgId: string, teamId: string, userId: string): Promise<void> {
  return apiFetch<void>(`/orgs/${orgId}/teams/${teamId}/members/${userId}`, {
    method: 'DELETE',
  });
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
): Promise<SpaceGrant> {
  return apiFetch<SpaceGrant>(`/orgs/${orgId}/spaces/${spaceId}/grants`, {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

async function updateGrant(
  orgId: string,
  spaceId: string,
  grantId: string,
  role: GrantRole,
): Promise<SpaceGrant> {
  return apiFetch<SpaceGrant>(`/orgs/${orgId}/spaces/${spaceId}/grants/${grantId}`, {
    method: 'PATCH',
    body: JSON.stringify({ role }),
  });
}

async function revokeGrant(orgId: string, spaceId: string, grantId: string): Promise<void> {
  return apiFetch<void>(`/orgs/${orgId}/spaces/${spaceId}/grants/${grantId}`, {
    method: 'DELETE',
  });
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

async function fetchInvites(orgId: string): Promise<Invite[]> {
  const data = await apiFetch<Invite[] | null>(`/orgs/${orgId}/invites`);
  return data ?? [];
}

interface CreateInvitesRequest {
  emails: string[];
  org_role?: string;
  team_id?: string | null;
}

async function createInvites(orgId: string, req: CreateInvitesRequest): Promise<InviteOutcome[]> {
  const data = await apiFetch<InviteOutcome[] | null>(`/orgs/${orgId}/invites`, {
    method: 'POST',
    body: JSON.stringify(req),
  });
  return data ?? [];
}

async function revokeInvite(orgId: string, inviteId: string): Promise<void> {
  return apiFetch<void>(`/orgs/${orgId}/invites/${inviteId}`, { method: 'DELETE' });
}

async function resendInvite(orgId: string, inviteId: string): Promise<CreatedInvite> {
  return apiFetch<CreatedInvite>(`/orgs/${orgId}/invites/${inviteId}/resend`, { method: 'POST' });
}

interface UpdatePersonRequest {
  org_role?: string;
  primary_team_id?: string;
  display_name?: string;
}

async function updatePerson(orgId: string, userId: string, req: UpdatePersonRequest): Promise<void> {
  return apiFetch<void>(`/orgs/${orgId}/users/${userId}`, {
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

async function personLifecycle(orgId: string, userId: string, action: 'deactivate' | 'reactivate' | 'force-logout'): Promise<void> {
  return apiFetch<void>(`/orgs/${orgId}/users/${userId}/${action}`, { method: 'POST' });
}

async function removePersonFromOrg(orgId: string, userId: string): Promise<void> {
  return apiFetch<void>(`/orgs/${orgId}/users/${userId}`, { method: 'DELETE' });
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

async function deleteSpace(orgId: string, spaceId: string): Promise<void> {
  return apiFetch<void>(`/orgs/${orgId}/spaces/${spaceId}`, { method: 'DELETE' });
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
  labels?: string[];
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
  labels?: string[];
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
  labels?: string[];
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
  labels?: string[];
}

async function updateProjectItem(
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
// Label API functions
// ---------------------------------------------------------------------------

async function fetchLabels(orgId: string): Promise<Label[]> {
  return apiFetch<Label[]>(`/orgs/${orgId}/labels`);
}

interface CreateLabelRequest {
  name: string;
  color: string;
}

async function createLabel(orgId: string, req: CreateLabelRequest): Promise<Label> {
  return apiFetch<Label>(`/orgs/${orgId}/labels`, {
    method: 'POST',
    body: JSON.stringify(req),
  });
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

/** A custom field as rendered on an item: an active definition with its current
 * value, or a legacy value whose definition is gone (read-only). */
export interface ItemCustomField {
  slug: string;
  name: string;
  field_type: CustomFieldType;
  options: string[];
  value: string;
  legacy: boolean;
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

async function fetchItemFields(spaceId: string, itemId: string): Promise<ItemCustomField[]> {
  const data = await apiFetch<ItemCustomField[] | null>(`${spaceBase(spaceId)}/projects/items/${itemId}/fields`);
  return Array.isArray(data) ? data : [];
}

async function setItemField(spaceId: string, itemId: string, slug: string, value: string): Promise<void> {
  await apiFetch<void>(`${spaceBase(spaceId)}/projects/items/${itemId}/fields/${slug}`, {
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

async function fetchWikiDiff(spaceId: string, pageId: string, from: number, to: number): Promise<{ diff: string }> {
  return apiFetch<{ diff: string }>(`${spaceBase(spaceId)}/wiki/${pageId}/diff?from=${from}&to=${to}`);
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

async function fetchRelations(spaceId: string, itemId: string): Promise<Relation[]> {
  const data = await apiFetch<Relation[] | null>(`${spaceBase(spaceId)}/projects/items/${itemId}/relations`);
  return data ?? [];
}

interface CreateRelationRequest {
  to_id: string;
  kind: string;
}

async function createRelation(spaceId: string, itemId: string, req: CreateRelationRequest): Promise<Relation> {
  return apiFetch<Relation>(`${spaceBase(spaceId)}/projects/items/${itemId}/relations`, {
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
  labels: (orgId: string) => ['labels', orgId] as const,
  itemTypes: (orgId: string) => ['itemTypes', orgId] as const,
  customFields: (orgId: string) => ['customFields', orgId] as const,
  itemFields: (spaceId: string, itemId: string) => ['itemFields', spaceId, itemId] as const,
  members: (orgId: string, spaceId: string) => ['members', orgId, spaceId] as const,
  comments: (spaceId: string, entityType: string, entityId: string) => ['comments', spaceId, entityType, entityId] as const,
  notifications: () => ['notifications'] as const,
  wikiTree: (spaceId: string) => ['wikiTree', spaceId] as const,
  wikiSearch: (spaceId: string, q: string) => ['wikiSearch', spaceId, q] as const,
  wikiLock: (spaceId: string, pageId: string) => ['wikiLock', spaceId, pageId] as const,
  wikiRevisions: (spaceId: string, pageId: string) => ['wikiRevisions', spaceId, pageId] as const,
  wikiRevision: (spaceId: string, pageId: string, version: number) => ['wikiRevision', spaceId, pageId, version] as const,
  wikiDiff: (spaceId: string, pageId: string, from: number, to: number) => ['wikiDiff', spaceId, pageId, from, to] as const,
  relations: (spaceId: string, itemId: string) => ['relations', spaceId, itemId] as const,
  roadmap: (spaceId: string, from: string, to: string) => ['roadmap', spaceId, from, to] as const,
  roadmapOverdue: (spaceId: string) => ['roadmapOverdue', spaceId] as const,
  roadmapSprints: (spaceId: string) => ['roadmapSprints', spaceId] as const,
  boardConfig: (spaceId: string) => ['boardConfig', spaceId] as const,
  itemSearch: (spaceId: string, q: string) => ['itemSearch', spaceId, q] as const,
  workflowStates: (spaceId: string) => ['workflowStates', spaceId] as const,
  orgWorkflows: (orgId: string) => ['orgWorkflows', orgId] as const,
  orgWorkflowStates: (orgId: string, workflowId: string) =>
    ['orgWorkflowStates', orgId, workflowId] as const,
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

export function useLabels(orgId: string, opts?: QueryOpts<Label[]>) {
  return useQuery<Label[], APIError>({
    queryKey: queryKeys.labels(orgId),
    queryFn: () => fetchLabels(orgId),
    enabled: !!orgId,
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

export function useItemFields(spaceId: string, itemId: string, opts?: QueryOpts<ItemCustomField[]>) {
  return useQuery<ItemCustomField[], APIError>({
    queryKey: queryKeys.itemFields(spaceId, itemId),
    queryFn: () => fetchItemFields(spaceId, itemId),
    enabled: !!spaceId && !!itemId,
    ...opts,
  });
}

export function useSetItemField(spaceId: string, itemId: string) {
  const queryClient = useQueryClient();
  return useMutation<void, APIError, { slug: string; value: string }>({
    mutationFn: ({ slug, value }) => setItemField(spaceId, itemId, slug, value),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.itemFields(spaceId, itemId) }),
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
  return useMutation<Space, APIError, CreateSpaceRequest>({
    mutationFn: (req) => createSpace(orgId, req),
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

export interface PageLock {
  page_id: string;
  user_id: string;
  user_name: string;
  expires_at: string;
}

export function usePageLock(spaceId: string, pageId: string, opts?: QueryOpts<PageLock | null>) {
  return useQuery<PageLock | null, APIError>({
    queryKey: queryKeys.wikiLock(spaceId, pageId),
    queryFn: async () => {
      const data = await apiFetch<PageLock | null>(`${spaceBase(spaceId)}/wiki/${pageId}/lock`);
      return data;
    },
    enabled: !!spaceId && !!pageId,
    refetchInterval: 15000,
    ...opts,
  });
}

export function useAcquirePageLock(spaceId: string, pageId: string) {
  const queryClient = useQueryClient();
  return useMutation<PageLock, APIError, void>({
    mutationFn: () => apiFetch<PageLock>(`${spaceBase(spaceId)}/wiki/${pageId}/lock`, { method: 'POST' }),
    onSuccess: (data) => queryClient.setQueryData(queryKeys.wikiLock(spaceId, pageId), data),
  });
}

export function useReleasePageLock(spaceId: string, pageId: string) {
  const queryClient = useQueryClient();
  return useMutation<void, APIError, void>({
    mutationFn: () => apiFetch<void>(`${spaceBase(spaceId)}/wiki/${pageId}/lock`, { method: 'DELETE' }),
    onSuccess: () => queryClient.setQueryData(queryKeys.wikiLock(spaceId, pageId), null),
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

export function useWikiDiff(spaceId: string, pageId: string, from: number, to: number, opts?: QueryOpts<{ diff: string }>) {
  return useQuery<{ diff: string }, APIError>({
    queryKey: queryKeys.wikiDiff(spaceId, pageId, from, to),
    queryFn: () => fetchWikiDiff(spaceId, pageId, from, to),
    enabled: !!spaceId && !!pageId && from > 0 && to > 0,
    ...opts,
  });
}

export function useRelations(spaceId: string, itemId: string, opts?: QueryOpts<Relation[]>) {
  return useQuery<Relation[], APIError>({
    queryKey: queryKeys.relations(spaceId, itemId),
    queryFn: () => fetchRelations(spaceId, itemId),
    enabled: !!spaceId && !!itemId,
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

export function useCreateRelation(spaceId: string, itemId: string) {
  const queryClient = useQueryClient();
  return useMutation<Relation, APIError, CreateRelationRequest>({
    mutationFn: (req) => createRelation(spaceId, itemId, req),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.relations(spaceId, itemId) });
    },
  });
}

export function useDeleteRelation(spaceId: string, itemId: string) {
  const queryClient = useQueryClient();
  return useMutation<void, APIError, string>({
    mutationFn: (relationId) => deleteRelation(spaceId, relationId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.relations(spaceId, itemId) });
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
  return useMutation<Team, APIError, CreateTeamRequest>({
    mutationFn: (req) => createTeam(orgId, req),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.teams(orgId) });
    },
  });
}

export function useUpdateTeam(orgId: string) {
  const queryClient = useQueryClient();
  return useMutation<Team, APIError, { teamId: string } & UpdateTeamRequest>({
    mutationFn: ({ teamId, ...req }) => updateTeam(orgId, teamId, req),
    onSuccess: () => {
      // Reparenting rewrites the paths of the whole subtree — refetch all.
      queryClient.invalidateQueries({ queryKey: queryKeys.teams(orgId) });
    },
  });
}

export function useDeleteTeam(orgId: string) {
  const queryClient = useQueryClient();
  return useMutation<void, APIError, string>({
    mutationFn: (teamId) => deleteTeam(orgId, teamId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.teams(orgId) });
    },
  });
}

export function usePutTeamMember(orgId: string, teamId: string) {
  const queryClient = useQueryClient();
  return useMutation<TeamMember, APIError, { userId: string } & PutTeamMemberRequest>({
    mutationFn: ({ userId, ...req }) => putTeamMember(orgId, teamId, userId, req),
    onSuccess: () => {
      // is_primary: true clears the user's primary flag elsewhere — the
      // ['teams', orgId] prefix reaches every team's member list.
      queryClient.invalidateQueries({ queryKey: queryKeys.teams(orgId) });
    },
  });
}

export function useRemoveTeamMember(orgId: string, teamId: string) {
  const queryClient = useQueryClient();
  return useMutation<void, APIError, string>({
    mutationFn: (userId) => removeTeamMember(orgId, teamId, userId),
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
  return useMutation<SpaceGrant, APIError, CreateGrantRequest>({
    mutationFn: (req) => createGrant(orgId, spaceId, req),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.spaceGrants(orgId, spaceId) });
    },
  });
}

export function useUpdateGrant(orgId: string, spaceId: string) {
  const queryClient = useQueryClient();
  return useMutation<SpaceGrant, APIError, { grantId: string; role: GrantRole }>({
    mutationFn: ({ grantId, role }) => updateGrant(orgId, spaceId, grantId, role),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.spaceGrants(orgId, spaceId) });
    },
  });
}

export function useRevokeGrant(orgId: string, spaceId: string) {
  const queryClient = useQueryClient();
  return useMutation<void, APIError, string>({
    mutationFn: (grantId) => revokeGrant(orgId, spaceId, grantId),
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
  return useMutation<Space, APIError, UpdateSpaceRequest>({
    mutationFn: (req) => updateSpace(orgId, spaceId, req),
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
  return useMutation<InviteOutcome[], APIError, CreateInvitesRequest>({
    mutationFn: (req) => createInvites(orgId, req),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.invites(orgId) });
    },
  });
}

export function useRevokeInvite(orgId: string) {
  const queryClient = useQueryClient();
  return useMutation<void, APIError, string>({
    mutationFn: (inviteId) => revokeInvite(orgId, inviteId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.invites(orgId) });
    },
  });
}

export function useResendInvite(orgId: string) {
  const queryClient = useQueryClient();
  return useMutation<CreatedInvite, APIError, string>({
    mutationFn: (inviteId) => resendInvite(orgId, inviteId),
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
  return useMutation<void, APIError, { userId: string } & UpdatePersonRequest>({
    mutationFn: ({ userId, ...req }) => updatePerson(orgId, userId, req),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.orgPeople(orgId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.teams(orgId) });
    },
  });
}

export function usePersonLifecycle(orgId: string) {
  const queryClient = useQueryClient();
  return useMutation<void, APIError, { userId: string; action: 'deactivate' | 'reactivate' | 'force-logout' }>({
    mutationFn: ({ userId, action }) => personLifecycle(orgId, userId, action),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.orgPeople(orgId) });
    },
  });
}

export function useRemovePerson(orgId: string) {
  const queryClient = useQueryClient();
  return useMutation<void, APIError, string>({
    mutationFn: (userId) => removePersonFromOrg(orgId, userId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.orgPeople(orgId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.teams(orgId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.accessMatrix(orgId) });
    },
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
  return useMutation<void, APIError, string>({
    mutationFn: (spaceId) => deleteSpace(orgId, spaceId),
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

async function createShare(orgId: string, req: CreateShareRequest): Promise<Share> {
  return apiFetch<Share>(`/orgs/${orgId}/shares`, {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

async function revokeShare(orgId: string, shareId: string): Promise<void> {
  return apiFetch<void>(`/orgs/${orgId}/shares/${shareId}`, { method: 'DELETE' });
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
  return useMutation<Share, APIError, CreateShareRequest>({
    mutationFn: (req) => createShare(orgId, req),
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
  return useMutation<void, APIError, string>({
    mutationFn: (shareId) => revokeShare(orgId, shareId),
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

/** The absolute URL of a shared entity's attachment (for <img src>), so the
 *  browser fetches it with the session cookie. Bearer-only deployments should
 *  proxy through fetch; here the object streams from a same-origin route. */
export function sharedAttachmentURL(
  orgId: string,
  entityType: ShareEntityType,
  entityId: string,
  attachmentId: string,
): string {
  return `${API_BASE_URL}/orgs/${orgId}/shared/${entityType}/${entityId}/attachments/${attachmentId}`;
}

// Re-export create helpers for direct use
export {
  createSpace,
  createTicket,
  createWikiPage,
  createProjectItem,
  createSprint,
  createLabel,
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
  type CreateLabelRequest,
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
