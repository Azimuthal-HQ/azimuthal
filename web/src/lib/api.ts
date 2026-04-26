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
  body: string;
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
  sort_order: number;
  label_ids: string[];
  created_at: string;
  updated_at: string;
}

export interface Sprint {
  id: string;
  space_id: string;
  name: string;
  goal: string;
  status: SprintStatus;
  start_date: string | null;
  end_date: string | null;
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

export type NotificationKind = 'assigned' | 'mentioned' | 'commented';
export type NotificationEntityKind = 'ticket' | 'item' | 'page' | 'comment' | '';

export interface Notification {
  id: string;
  user_id: string;
  kind: NotificationKind;
  title: string;
  body?: string;
  entity_kind?: NotificationEntityKind;
  entity_id?: string;
  is_read: boolean;
  created_at: string;
  read_at?: string;
}

export interface NotificationListResponse {
  notifications: Notification[];
  unread_count: number;
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
  status?: string;
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
  start_date?: string;
  end_date?: string;
}

async function createSprint(spaceId: string, req: CreateSprintRequest): Promise<Sprint> {
  return apiFetch<Sprint>(`/spaces/${spaceId}/projects/sprints`, {
    method: 'POST',
    body: JSON.stringify(req),
  });
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

async function fetchComments(orgId: string, spaceId: string, itemId: string): Promise<Comment[]> {
  const data = await apiFetch<Comment[] | Comment>(`/orgs/${orgId}/spaces/${spaceId}/items/${itemId}/comments`);
  return Array.isArray(data) ? data : [data];
}

interface CreateCommentRequest {
  content: string;
}

async function createComment(orgId: string, spaceId: string, itemId: string, req: CreateCommentRequest): Promise<Comment> {
  return apiFetch<Comment>(`/orgs/${orgId}/spaces/${spaceId}/items/${itemId}/comments`, {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

// ---------------------------------------------------------------------------
// Notification API functions
// ---------------------------------------------------------------------------

async function fetchNotifications(): Promise<NotificationListResponse> {
  const data = await apiFetch<NotificationListResponse | null>('/notifications');
  if (data == null) return { notifications: [], unread_count: 0 };
  // Defensive: backend should always send a non-null array, but guard anyway.
  return {
    notifications: Array.isArray(data.notifications) ? data.notifications : [],
    unread_count: typeof data.unread_count === 'number' ? data.unread_count : 0,
  };
}

async function markNotificationRead(notificationId: string): Promise<void> {
  await apiFetch<void>(`/notifications/${notificationId}/read`, { method: 'POST' });
}

async function markAllNotificationsRead(): Promise<void> {
  await apiFetch<void>('/notifications/read-all', { method: 'POST' });
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
  labels: (orgId: string) => ['labels', orgId] as const,
  members: (orgId: string, spaceId: string) => ['members', orgId, spaceId] as const,
  comments: (spaceId: string, itemId: string) => ['comments', spaceId, itemId] as const,
  notifications: () => ['notifications'] as const,
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

export function useComments(orgId: string, spaceId: string, itemId: string, opts?: QueryOpts<Comment[]>) {
  return useQuery<Comment[], APIError>({
    queryKey: queryKeys.comments(spaceId, itemId),
    queryFn: () => fetchComments(orgId, spaceId, itemId),
    enabled: !!orgId && !!spaceId && !!itemId,
    retry: (failureCount, error) => {
      if (error?.status === 404) return false;
      return failureCount < 2;
    },
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

export function useNotifications(opts?: QueryOpts<NotificationListResponse>) {
  return useQuery<NotificationListResponse, APIError>({
    queryKey: queryKeys.notifications(),
    queryFn: fetchNotifications,
    refetchInterval: 30000,
    refetchOnWindowFocus: true,
    ...opts,
  });
}

export function useMarkNotificationRead() {
  const queryClient = useQueryClient();
  return useMutation<void, APIError, string>({
    mutationFn: markNotificationRead,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.notifications() });
    },
  });
}

export function useMarkAllNotificationsRead() {
  const queryClient = useQueryClient();
  return useMutation<void, APIError, void>({
    mutationFn: markAllNotificationsRead,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.notifications() });
    },
  });
}

export function useCreateComment(orgId: string, spaceId: string, itemId: string) {
  const queryClient = useQueryClient();
  return useMutation<Comment, APIError, CreateCommentRequest>({
    mutationFn: (req) => createComment(orgId, spaceId, itemId, req),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.comments(spaceId, itemId) });
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
