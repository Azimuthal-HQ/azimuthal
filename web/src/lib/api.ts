import {
  useQuery,
  useMutation,
  useQueryClient,
} from '@tanstack/react-query';
import type { UseQueryOptions } from '@tanstack/react-query';
import { getToken, setToken, setRefreshToken, getRefreshToken, removeToken, removeRefreshToken } from './auth';

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

// ---------------------------------------------------------------------------
// Base fetch helper
// ---------------------------------------------------------------------------

async function apiFetch<T>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const headers = new Headers(options.headers);

  if (!headers.has('Content-Type') && options.body) {
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

export type SpaceType = 'service_desk' | 'wiki' | 'project';
export type TicketStatus = 'open' | 'in_progress' | 'resolved' | 'closed';
export type SprintStatus = 'planning' | 'active' | 'completed';

export interface Organization {
  id: string;
  name: string;
  slug: string;
  description: string | null;
  plan: string;
  created_at: string;
  updated_at: string;
}

export interface Space {
  id: string;
  org_id: string;
  name: string;
  slug: string;
  type: SpaceType;
  description: string;
  created_at: string;
  updated_at: string;
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
  created_at: string;
  updated_at: string;
}

export interface ProjectItem {
  id: string;
  space_id: string;
  number: number | null;
  title: string;
  description: string;
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
  type: SpaceType;
  description?: string;
}

async function createSpace(orgId: string, req: CreateSpaceRequest): Promise<Space> {
  return apiFetch<Space>(`/orgs/${orgId}/spaces`, {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

// ---------------------------------------------------------------------------
// Ticket API functions
// ---------------------------------------------------------------------------

async function fetchTickets(spaceId: string): Promise<Ticket[]> {
  // Audit ref: testing-audit.md §7.5 — null-instead-of-[] regression.
  const data = await apiFetch<Ticket[] | Ticket | null>(`/spaces/${spaceId}/tickets`);
  if (data == null) return [];
  return Array.isArray(data) ? data : [data];
}

async function fetchTicket(spaceId: string, ticketId: string): Promise<Ticket> {
  return apiFetch<Ticket>(`/spaces/${spaceId}/tickets/${ticketId}`);
}

interface CreateTicketRequest {
  title: string;
  description?: string;
  priority?: string;
  assignee_id?: string | null;
  labels?: string[];
}

async function createTicket(spaceId: string, req: CreateTicketRequest): Promise<Ticket> {
  return apiFetch<Ticket>(`/spaces/${spaceId}/tickets`, {
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
  return apiFetch<Ticket>(`/spaces/${spaceId}/tickets/${ticketId}`, {
    method: 'PATCH',
    body: JSON.stringify(req),
  });
}

async function transitionTicketStatus(
  spaceId: string,
  ticketId: string,
  status: TicketStatus,
): Promise<Ticket> {
  return apiFetch<Ticket>(`/spaces/${spaceId}/tickets/${ticketId}/status`, {
    method: 'POST',
    body: JSON.stringify({ status }),
  });
}

// ---------------------------------------------------------------------------
// Wiki API functions
// ---------------------------------------------------------------------------

async function fetchWikiPages(spaceId: string): Promise<WikiPage[]> {
  // Audit ref: testing-audit.md §7.5 — null-instead-of-[] regression.
  const data = await apiFetch<WikiPage[] | WikiPage | null>(`/spaces/${spaceId}/wiki`);
  if (data == null) return [];
  return Array.isArray(data) ? data : [data];
}

async function fetchWikiPage(spaceId: string, pageId: string): Promise<WikiPage> {
  return apiFetch<WikiPage>(`/spaces/${spaceId}/wiki/${pageId}`);
}

interface CreateWikiPageRequest {
  title: string;
  content: string;
  parent_id?: string | null;
  position?: number;
}

async function createWikiPage(spaceId: string, req: CreateWikiPageRequest): Promise<WikiPage> {
  return apiFetch<WikiPage>(`/spaces/${spaceId}/wiki`, {
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
  return apiFetch<WikiPage>(`/spaces/${spaceId}/wiki/${pageId}`, {
    method: 'PUT',
    body: JSON.stringify(req),
  });
}

// ---------------------------------------------------------------------------
// Project item API functions
// ---------------------------------------------------------------------------

async function fetchProjectItems(spaceId: string): Promise<ProjectItem[]> {
  // Audit ref: testing-audit.md §7.5 — null-instead-of-[] regression.
  const data = await apiFetch<ProjectItem[] | ProjectItem | null>(`/spaces/${spaceId}/projects/items`);
  if (data == null) return [];
  return Array.isArray(data) ? data : [data];
}

export async function fetchProjectItem(spaceId: string, itemId: string): Promise<ProjectItem> {
  return apiFetch<ProjectItem>(`/spaces/${spaceId}/projects/items/${itemId}`);
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
  return apiFetch<ProjectItem>(`/spaces/${spaceId}/projects/items`, {
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
  return apiFetch<ProjectItem>(`/spaces/${spaceId}/projects/items/${itemId}`, {
    method: 'PATCH',
    body: JSON.stringify(req),
  });
}

async function transitionProjectItemStatus(
  spaceId: string,
  itemId: string,
  status: string,
): Promise<ProjectItem> {
  return apiFetch<ProjectItem>(`/spaces/${spaceId}/projects/items/${itemId}/status`, {
    method: 'POST',
    body: JSON.stringify({ status }),
  });
}

// ---------------------------------------------------------------------------
// Sprint API functions
// ---------------------------------------------------------------------------

async function fetchSprints(spaceId: string): Promise<Sprint[]> {
  return apiFetch<Sprint[]>(`/spaces/${spaceId}/projects/sprints`);
}

interface CreateSprintRequest {
  name: string;
  goal?: string;
  starts_at?: string;
  ends_at?: string;
}

async function createSprint(spaceId: string, req: CreateSprintRequest): Promise<Sprint> {
  return apiFetch<Sprint>(`/spaces/${spaceId}/projects/sprints`, {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

async function fetchActiveSprint(spaceId: string): Promise<Sprint | null> {
  try {
    return await apiFetch<Sprint>(`/spaces/${spaceId}/projects/sprints/active`);
  } catch (err) {
    if (err instanceof APIError && err.status === 404) return null;
    throw err;
  }
}

async function fetchSprintItems(spaceId: string, sprintId: string): Promise<ProjectItem[]> {
  const data = await apiFetch<ProjectItem[] | ProjectItem | null>(
    `/spaces/${spaceId}/projects/sprints/${sprintId}/items`,
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
    case 'project_item': return 'project-items';
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
  return apiFetch<void>(`/spaces/${spaceId}/tickets/${ticketId}/assign`, {
    method: 'POST',
    body: JSON.stringify({ assignee_id: assigneeId }),
  });
}

// ---------------------------------------------------------------------------
// Wiki tree / search / revision / move API functions
// ---------------------------------------------------------------------------

async function fetchWikiTree(spaceId: string): Promise<WikiTreeNode[]> {
  const data = await apiFetch<WikiTreeNode[] | null>(`/spaces/${spaceId}/wiki/tree`);
  return data ?? [];
}

async function searchWikiPages(spaceId: string, q: string): Promise<WikiPage[]> {
  const data = await apiFetch<WikiPage[] | null>(`/spaces/${spaceId}/wiki/search?q=${encodeURIComponent(q)}`);
  return data ?? [];
}

async function fetchWikiRevisions(spaceId: string, pageId: string): Promise<WikiRevision[]> {
  const data = await apiFetch<WikiRevision[] | null>(`/spaces/${spaceId}/wiki/${pageId}/revisions`);
  return data ?? [];
}

async function fetchWikiRevision(spaceId: string, pageId: string, version: number): Promise<WikiPage> {
  return apiFetch<WikiPage>(`/spaces/${spaceId}/wiki/${pageId}/revisions/${version}`);
}

async function fetchWikiDiff(spaceId: string, pageId: string, from: number, to: number): Promise<{ diff: string }> {
  return apiFetch<{ diff: string }>(`/spaces/${spaceId}/wiki/${pageId}/diff?from=${from}&to=${to}`);
}

interface MoveWikiPageRequest {
  parent_id: string | null;
  position: number;
}

async function moveWikiPage(spaceId: string, pageId: string, req: MoveWikiPageRequest): Promise<WikiPage> {
  return apiFetch<WikiPage>(`/spaces/${spaceId}/wiki/${pageId}/move`, {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

// ---------------------------------------------------------------------------
// Relations API functions
// ---------------------------------------------------------------------------

async function fetchRelations(spaceId: string, itemId: string): Promise<Relation[]> {
  const data = await apiFetch<Relation[] | null>(`/spaces/${spaceId}/projects/items/${itemId}/relations`);
  return data ?? [];
}

interface CreateRelationRequest {
  to_id: string;
  kind: string;
}

async function createRelation(spaceId: string, itemId: string, req: CreateRelationRequest): Promise<Relation> {
  return apiFetch<Relation>(`/spaces/${spaceId}/projects/items/${itemId}/relations`, {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

async function deleteRelation(spaceId: string, relationId: string): Promise<void> {
  return apiFetch<void>(`/spaces/${spaceId}/projects/relations/${relationId}`, { method: 'DELETE' });
}

// ---------------------------------------------------------------------------
// Rank / search items API functions
// ---------------------------------------------------------------------------

interface RankItemRequest {
  before_id?: string;
  after_id?: string;
}

async function rankItem(spaceId: string, itemId: string, req: RankItemRequest): Promise<void> {
  return apiFetch<void>(`/spaces/${spaceId}/projects/items/${itemId}/rank`, {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

async function searchItems(spaceId: string, q: string): Promise<ProjectItem[]> {
  const data = await apiFetch<ProjectItem[] | null>(`/spaces/${spaceId}/projects/items/search?q=${encodeURIComponent(q)}`);
  return data ?? [];
}

// ---------------------------------------------------------------------------
// Sprint start / complete API functions
// ---------------------------------------------------------------------------

async function startSprint(spaceId: string, sprintId: string): Promise<Sprint> {
  return apiFetch<Sprint>(`/spaces/${spaceId}/projects/sprints/${sprintId}/start`, { method: 'POST' });
}

async function completeSprint(spaceId: string, sprintId: string): Promise<Sprint> {
  return apiFetch<Sprint>(`/spaces/${spaceId}/projects/sprints/${sprintId}/complete`, { method: 'POST' });
}

// ---------------------------------------------------------------------------
// Roadmap API functions
// ---------------------------------------------------------------------------

async function fetchRoadmap(spaceId: string): Promise<RoadmapItem[]> {
  const data = await apiFetch<RoadmapItem[] | null>(`/spaces/${spaceId}/projects/roadmap`);
  return data ?? [];
}

async function fetchRoadmapOverdue(spaceId: string): Promise<RoadmapItem[]> {
  const data = await apiFetch<RoadmapItem[] | null>(`/spaces/${spaceId}/projects/roadmap/overdue`);
  return data ?? [];
}

async function fetchRoadmapSprints(spaceId: string): Promise<RoadmapSprint[]> {
  const data = await apiFetch<RoadmapSprint[] | null>(`/spaces/${spaceId}/projects/roadmap/sprints`);
  return data ?? [];
}

// ---------------------------------------------------------------------------
// Query key factories
// ---------------------------------------------------------------------------

export const queryKeys = {
  me: () => ['me'] as const,
  organization: (orgId: string) => ['organization', orgId] as const,
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
  roadmap: (spaceId: string) => ['roadmap', spaceId] as const,
  roadmapOverdue: (spaceId: string) => ['roadmapOverdue', spaceId] as const,
  roadmapSprints: (spaceId: string) => ['roadmapSprints', spaceId] as const,
  itemSearch: (spaceId: string, q: string) => ['itemSearch', spaceId, q] as const,
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
      const data = await apiFetch<PageLock | null>(`/spaces/${spaceId}/wiki/${pageId}/lock`);
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
    mutationFn: () => apiFetch<PageLock>(`/spaces/${spaceId}/wiki/${pageId}/lock`, { method: 'POST' }),
    onSuccess: (data) => queryClient.setQueryData(queryKeys.wikiLock(spaceId, pageId), data),
  });
}

export function useReleasePageLock(spaceId: string, pageId: string) {
  const queryClient = useQueryClient();
  return useMutation<void, APIError, void>({
    mutationFn: () => apiFetch<void>(`/spaces/${spaceId}/wiki/${pageId}/lock`, { method: 'DELETE' }),
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

export function useRoadmap(spaceId: string, opts?: QueryOpts<RoadmapItem[]>) {
  return useQuery<RoadmapItem[], APIError>({
    queryKey: queryKeys.roadmap(spaceId),
    queryFn: () => fetchRoadmap(spaceId),
    enabled: !!spaceId,
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
  return useMutation<WikiPage, APIError, MoveWikiPageRequest>({
    mutationFn: (req) => moveWikiPage(spaceId, pageId, req),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.wikiTree(spaceId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.wikiPages(spaceId) });
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

export function useCompleteSprint(spaceId: string) {
  const queryClient = useQueryClient();
  return useMutation<Sprint, APIError, string>({
    mutationFn: (sprintId) => completeSprint(spaceId, sprintId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.sprints(spaceId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.activeSprint(spaceId) });
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
};
