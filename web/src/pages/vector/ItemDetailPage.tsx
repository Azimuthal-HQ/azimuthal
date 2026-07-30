import { useState } from 'react';
import { useParams, Link } from 'react-router-dom';
import { ArrowLeft, Clock, AlertCircle, Trash2, Link2 } from 'lucide-react';
import { Badge, type BadgeProps } from '../../components/ui/badge';
import { Button } from '../../components/ui/button';
import { Input } from '../../components/ui/input';
import {
  DetailLayout,
  DetailMain,
  DetailSide,
  DetailField,
  DetailDivider,
} from '../../components/layout/DetailLayout';
import { EntityShareControl } from '../../components/EntityShareControl';
import { ModuleChip } from '../../shell/ModuleChip';
import { ItemKeyChip, itemKeyLabel } from '../../components/ItemKeyChip';
import { CustomFieldsSection } from '../../components/CustomFieldsSection';
import { PriorityPill, normalizePriority } from '../../components/priority';
import { cn } from '../../lib/utils';
import { Markdown } from '../../components/Markdown';
import {
  useProjectItem,
  useUpdateProjectItem,
  useTransitionProjectItemStatus,
  useMembers,
  useComments,
  useCreateComment,
  useMe,
  useRelations,
  useCreateRelation,
  useDeleteRelation,
  useItemSearch,
  useSpace,
  useItemTypes,
  useSprints,
  useAssignItemSprint,
  friendlyErrorMessage,
} from '../../lib/api';

// Sentinel option value for "no sprint" — a <select> option cannot carry null.
const NO_SPRINT = '__backlog__';

// ---------------------------------------------------------------------------
// Status vocabulary
// ---------------------------------------------------------------------------

const STATUS_VARIANT: Record<string, BadgeProps['variant']> = {
  open: 'default', todo: 'secondary', in_progress: 'warning', in_review: 'default', done: 'success', closed: 'secondary',
};
const STATUS_LABEL: Record<string, string> = {
  open: 'Open', todo: 'To Do', in_progress: 'In Progress', in_review: 'In Review', done: 'Done', closed: 'Closed',
};

const ALL_STATUSES = ['open', 'in_progress', 'in_review', 'done', 'closed'];

const sideSelectClass = cn(
  'h-8 w-full rounded-[var(--radius-lg)] border border-[var(--color-border)]',
  'bg-[var(--color-input)] px-2 text-[var(--text-xs)] text-[var(--color-text)]',
  'focus-visible:outline-none focus-visible:border-[var(--color-primary)] focus-visible:ring-1 focus-visible:ring-[var(--color-primary)]',
);

function InitialAvatar({ name, className }: { name?: string | null; className?: string }) {
  return (
    <span
      className={cn(
        'flex h-[22px] w-[22px] shrink-0 items-center justify-center rounded-full',
        'bg-[var(--color-primary-muted)] text-[9px] font-medium text-[var(--color-primary)]',
        className,
      )}
    >
      {name?.[0]?.toUpperCase() ?? '?'}
    </span>
  );
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

/** Detail view for a project item. */
export function ItemDetailPage() {
  const { spaceId = '', itemKey } = useParams<{ spaceId: string; itemKey: string }>();
  const itemId = itemKey ?? '';

  const { data: space } = useSpace(spaceId);
  const { data: item, isLoading, error, refetch: refetchItem } = useProjectItem(spaceId, itemId);
  const updateMutation = useUpdateProjectItem(spaceId, itemId);
  const statusMutation = useTransitionProjectItemStatus(spaceId, itemId);
  const { data: me } = useMe();
  const orgId = me?.org_id ?? '';
  const { data: members } = useMembers(orgId, spaceId);
  const { data: itemTypes } = useItemTypes(orgId);
  const { data: comments, refetch: refetchComments } = useComments(orgId, spaceId, 'project_item', itemId);
  const createCommentMutation = useCreateComment(orgId, spaceId, 'project_item', itemId);

  // Sprint membership (W2): completed sprints are not offered as targets, but
  // one stays listed while it owns this item so the control shows the truth.
  const { data: sprints = [] } = useSprints(spaceId);
  const assignSprintMutation = useAssignItemSprint(spaceId);
  const [sprintError, setSprintError] = useState<string | null>(null);

  const [newComment, setNewComment] = useState('');

  // Edit mode for title + description
  const [isEditing, setIsEditing] = useState(false);
  const [editTitle, setEditTitle] = useState('');
  const [editDescription, setEditDescription] = useState('');

  // Relations state
  const { data: relations = [] } = useRelations(spaceId, itemId);
  const createRelationMutation = useCreateRelation(spaceId, itemId);
  const deleteRelationMutation = useDeleteRelation(spaceId, itemId);
  const [relKind, setRelKind] = useState('relates_to');
  const [relSearch, setRelSearch] = useState('');
  const [relSearchDebounced, setRelSearchDebounced] = useState('');
  const { data: searchResults = [] } = useItemSearch(spaceId, relSearchDebounced);

  // The debounce timer hangs off the function object itself. Typed here rather
  // than cast to `any` so the property has a name and a type; the runtime is
  // unchanged.
  type DebouncedRelSearch = typeof handleRelSearchChange & {
    _t?: ReturnType<typeof setTimeout>;
  };

  function handleRelSearchChange(v: string) {
    setRelSearch(v);
    clearTimeout((handleRelSearchChange as DebouncedRelSearch)._t);
    (handleRelSearchChange as DebouncedRelSearch)._t = setTimeout(
      () => setRelSearchDebounced(v),
      300,
    );
  }

  const backlogPath = `/vector/${spaceId}/backlog`;

  async function handleStatusChange(newStatus: string) {
    await statusMutation.mutateAsync(newStatus);
    refetchItem();
  }

  async function handleAssigneeChange(assigneeId: string) {
    await updateMutation.mutateAsync({ assignee_id: assigneeId || null });
    refetchItem();
  }

  async function handleSprintChange(value: string) {
    setSprintError(null);
    try {
      await assignSprintMutation.mutateAsync({
        itemId,
        sprintId: value === NO_SPRINT ? null : value,
      });
      refetchItem();
    } catch (e) {
      setSprintError(friendlyErrorMessage(e, 'The sprint could not be changed.'));
    }
  }

  async function handleAddComment() {
    if (!newComment.trim()) return;
    await createCommentMutation.mutateAsync({ content: newComment.trim() });
    setNewComment('');
    refetchComments();
  }

  function startEditing() {
    setEditTitle(item?.title ?? '');
    setEditDescription(item?.description ?? '');
    setIsEditing(true);
  }

  async function handleSaveEdit() {
    if (!editTitle.trim()) return;
    await updateMutation.mutateAsync({
      title: editTitle.trim(),
      description: editDescription,
      priority: item?.priority,
    });
    setIsEditing(false);
    refetchItem();
  }

  if (isLoading) {
    return (
      <div className="flex h-64 items-center justify-center text-[var(--color-text-muted)]">
        Loading item...
      </div>
    );
  }

  if (error) {
    return (
      <div className="space-y-4">
        <Link to={backlogPath} className="flex items-center gap-1 text-[var(--text-sm)] text-[var(--color-text-muted)] hover:text-[var(--color-text)]">
          <ArrowLeft className="h-4 w-4" />
          Backlog
        </Link>
        <div className="flex items-center gap-3 rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[color-mix(in_srgb,var(--color-danger)_10%,transparent)] p-4">
          <AlertCircle className="h-5 w-5 text-[var(--color-danger)]" />
          <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
            {error.status === 404
              ? 'Item not found.'
              : friendlyErrorMessage(error, 'The item could not be loaded.')}
          </p>
        </div>
      </div>
    );
  }

  if (!item) {
    return (
      <div className="flex flex-col items-center justify-center py-20 text-[var(--color-text-muted)]">
        <p className="text-lg font-medium">Item not found</p>
        <Link to={backlogPath} className="mt-2 text-[var(--color-primary)] hover:underline">
          Back to backlog
        </Link>
      </div>
    );
  }

  const keyLabel = itemKeyLabel(item, space?.key);
  // Resolve the item's type slug (item.kind) to its display name; fall back to
  // a humanized slug if the type list hasn't loaded or the type was removed.
  const typeName = item.kind
    ? (itemTypes ?? []).find((t) => t.slug === item.kind)?.name ??
      item.kind.charAt(0).toUpperCase() + item.kind.slice(1)
    : '';
  const reporter = (members ?? []).find((m) => m.user_id === item.reporter_id);

  return (
    <div className="space-y-4">
      {/* Breadcrumb */}
      <div className="flex items-center gap-2 text-[var(--text-sm)] text-[var(--color-text-muted)]">
        <Link to={backlogPath} className="flex items-center gap-1 hover:text-[var(--color-text)]">
          <ArrowLeft className="h-4 w-4" />
          Backlog
        </Link>
        <span>/</span>
        <span className="text-[var(--color-text)]" style={{ fontFamily: 'var(--font-mono)' }}>
          {keyLabel}
        </span>
      </div>

      <DetailLayout>
        <DetailMain>
          <div className="mb-2">
            <ItemKeyChip item={item} spaceKey={space?.key} />
          </div>

          {isEditing ? (
            <div className="space-y-3">
              <input
                id="edit-item-title"
                value={editTitle}
                onChange={(e) => setEditTitle(e.target.value)}
                className="w-full rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-input)] px-3 py-2 text-[19px] font-semibold text-[var(--color-text)] focus:outline-none focus:border-[var(--color-primary)] focus:ring-1 focus:ring-[var(--color-primary)]"
              />
              <textarea
                id="edit-item-description"
                value={editDescription}
                onChange={(e) => setEditDescription(e.target.value)}
                rows={6}
                placeholder="Description (markdown supported)"
                className="w-full rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-input)] px-3 py-2 text-[var(--text-sm)] text-[var(--color-text)] focus:outline-none focus:border-[var(--color-primary)] focus:ring-1 focus:ring-[var(--color-primary)]"
              />
              <div className="flex gap-2">
                <Button onClick={handleSaveEdit} disabled={!editTitle.trim() || updateMutation.isPending}>
                  {updateMutation.isPending ? 'Saving...' : 'Save'}
                </Button>
                <Button variant="secondary" onClick={() => setIsEditing(false)}>Cancel</Button>
              </div>
            </div>
          ) : (
            <>
              <div className="flex items-start justify-between gap-3">
                <h1 className="mb-3.5 text-[19px] font-semibold leading-[1.3] tracking-[-.01em] text-[var(--color-text)]">
                  {item.title}
                </h1>
                <div className="flex items-center gap-2">
                  <EntityShareControl
                    orgId={orgId}
                    spaceId={spaceId}
                    entityType="project_item"
                    entityId={item.id}
                    entityLabel={item.title}
                  />
                  <Button variant="secondary" onClick={startEditing}>Edit</Button>
                </div>
              </div>

              {/* Meta row: type, status, priority, module — the one vocabulary. */}
              <div className="mb-5 flex flex-wrap items-center gap-2">
                {typeName && <Badge variant="secondary" data-testid="item-type-chip">{typeName}</Badge>}
                <Badge variant={STATUS_VARIANT[item.status] ?? 'secondary'}>
                  {STATUS_LABEL[item.status] ?? item.status}
                </Badge>
                <PriorityPill priority={normalizePriority(item.priority)} />
                <ModuleChip module="vector" />
              </div>

              {/* The shared renderer (P5). */}
              <Markdown
                fallback={
                  <span className="italic text-[var(--color-text-muted)] text-[var(--text-sm)]">
                    No description provided.
                  </span>
                }
              >
                {item.description ?? ''}
              </Markdown>
            </>
          )}

          {/* Relations section */}
          <div className="mt-6 border-t border-[var(--color-border)] pt-5">
            <h3 className="mb-3 flex items-center gap-2 text-[var(--text-sm)] font-semibold text-[var(--color-text)]">
              <Link2 className="h-4 w-4" />Relations
            </h3>
            {relations.length > 0 && (
              <div className="mb-4 space-y-1.5">
                {relations.map(rel => (
                  <div key={rel.id} className="flex items-center gap-2 rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-[var(--text-sm)]">
                    <span className="shrink-0 rounded-full bg-[var(--color-surface-hover)] px-2 py-0.5 text-[var(--text-xs)] capitalize text-[var(--color-text-muted)]">
                      {rel.kind.replace(/_/g, ' ')}
                    </span>
                    <span className="flex-1 truncate text-[var(--color-text)]">{rel.to_title}</span>
                    <span className="shrink-0 text-[var(--text-xs)] text-[var(--color-text-muted)]">{rel.to_status}</span>
                    <button
                      onClick={() => deleteRelationMutation.mutate(rel.id)}
                      className="ml-1 rounded p-0.5 text-[var(--color-text-muted)] transition-colors hover:text-[var(--color-danger)]"
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </button>
                  </div>
                ))}
              </div>
            )}
            <div className="flex gap-2">
              <select
                value={relKind}
                onChange={e => setRelKind(e.target.value)}
                className={cn(
                  'rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-input)] px-2 py-1.5 text-[var(--text-sm)] text-[var(--color-text)]',
                  'focus:outline-none focus:border-[var(--color-primary)] focus:ring-1 focus:ring-[var(--color-primary)]',
                )}
              >
                {['relates_to', 'blocks', 'is_blocked_by', 'duplicates'].map(k => (
                  <option key={k} value={k}>{k.replace(/_/g, ' ')}</option>
                ))}
              </select>
              <div className="relative flex-1">
                <Input
                  placeholder="Search items…"
                  value={relSearch}
                  onChange={e => handleRelSearchChange(e.target.value)}
                />
                {searchResults.length > 0 && relSearch && (
                  <div className="absolute left-0 top-full z-50 mt-1 w-full rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-surface)] shadow-[var(--shadow-md)]">
                    {searchResults.filter(r => r.id !== itemId).slice(0, 8).map(r => (
                      <button
                        key={r.id}
                        type="button"
                        className="flex w-full items-center gap-2 px-3 py-2 text-left text-[var(--text-sm)] text-[var(--color-text)] hover:bg-[var(--color-surface-hover)]"
                        onClick={async () => {
                          await createRelationMutation.mutateAsync({ to_id: r.id, kind: relKind });
                          setRelSearch('');
                          setRelSearchDebounced('');
                        }}
                      >
                        <span className="truncate">{r.title}</span>
                        <span className="ml-auto shrink-0 text-[var(--text-xs)] text-[var(--color-text-muted)]">{r.status}</span>
                      </button>
                    ))}
                  </div>
                )}
              </div>
            </div>
          </div>

          {/* Comments section */}
          <div className="mt-6 border-t border-[var(--color-border)] pt-5">
            <h3 className="mb-4 text-[var(--text-sm)] font-semibold text-[var(--color-text)]">Activity</h3>

            <div className="mb-6 space-y-4">
              {(comments ?? []).length === 0 && (
                <p className="text-[var(--text-sm)] italic text-[var(--color-text-muted)]">No comments yet.</p>
              )}
              {(comments ?? []).map((comment) => (
                <div key={comment.id} className="flex gap-3">
                  <InitialAvatar name={comment.author_name} className="h-8 w-8 text-[var(--text-sm)]" />
                  <div className="min-w-0 flex-1">
                    <div className="mb-1 flex items-center gap-2">
                      <span className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                        {comment.author_name ?? 'Unknown'}
                      </span>
                      <span className="text-[var(--text-xs)] text-[var(--color-text-muted)]">
                        {new Date(comment.created_at).toLocaleDateString()}
                      </span>
                    </div>
                    <p className="whitespace-pre-wrap text-[var(--text-sm)] text-[var(--color-text-muted)]">
                      {comment.content ?? comment.body}
                    </p>
                  </div>
                </div>
              ))}
            </div>

            <div className="flex gap-3">
              <InitialAvatar name={me?.display_name} className="h-8 w-8 text-[var(--text-sm)]" />
              <div className="flex-1">
                <textarea
                  value={newComment}
                  onChange={(e) => setNewComment(e.target.value)}
                  placeholder="Add a comment..."
                  className={cn(
                    'w-full resize-none rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-input)] px-3 py-2 text-[var(--text-sm)] text-[var(--color-text)]',
                    'placeholder:text-[var(--color-text-muted)]',
                    'focus:outline-none focus:border-[var(--color-primary)] focus:ring-1 focus:ring-[var(--color-primary)]',
                  )}
                  rows={3}
                />
                <button
                  onClick={handleAddComment}
                  disabled={!newComment.trim() || createCommentMutation.isPending}
                  className="mt-2 rounded-[var(--radius-lg)] bg-[var(--color-primary)] px-4 py-1.5 text-[var(--text-sm)] font-medium text-white transition-colors hover:bg-[var(--color-primary-hover)] disabled:opacity-50"
                >
                  {createCommentMutation.isPending ? 'Posting...' : 'Comment'}
                </button>
              </div>
            </div>
          </div>
        </DetailMain>

        <DetailSide>
          <DetailField label="Status">
            <div className="space-y-1.5">
              <Badge variant={STATUS_VARIANT[item.status] ?? 'secondary'}>
                {STATUS_LABEL[item.status] ?? item.status}
              </Badge>
              <select
                aria-label="Status"
                value={item.status}
                onChange={(e) => handleStatusChange(e.target.value)}
                className={sideSelectClass}
              >
                {ALL_STATUSES.map((s) => (
                  <option key={s} value={s}>{STATUS_LABEL[s] ?? s}</option>
                ))}
              </select>
            </div>
          </DetailField>

          <DetailField label="Priority">
            <PriorityPill priority={normalizePriority(item.priority)} />
          </DetailField>

          <DetailDivider />

          <DetailField label="Assignee">
            <select
              aria-label="Assignee"
              value={item.assignee_id ?? ''}
              onChange={(e) => handleAssigneeChange(e.target.value)}
              className={sideSelectClass}
            >
              <option value="">Unassigned</option>
              {(members ?? []).map((m) => (
                <option key={m.user_id} value={m.user_id}>{m.display_name}</option>
              ))}
            </select>
          </DetailField>

          <DetailField label="Sprint">
            <select
              aria-label="Sprint"
              value={item.sprint_id ?? NO_SPRINT}
              disabled={assignSprintMutation.isPending}
              onChange={(e) => handleSprintChange(e.target.value)}
              className={sideSelectClass}
            >
              <option value={NO_SPRINT}>Backlog</option>
              {sprints.filter((s) => s.status !== 'completed').map((s) => (
                <option key={s.id} value={s.id}>{s.name}</option>
              ))}
              {item.sprint_id && !sprints.some((s) => s.id === item.sprint_id && s.status !== 'completed') && (
                <option value={item.sprint_id}>
                  {sprints.find((s) => s.id === item.sprint_id)?.name ?? 'Unknown sprint'}
                </option>
              )}
            </select>
            {sprintError && (
              <p className="mt-1 text-[var(--text-xs)] text-[var(--color-danger)]">{sprintError}</p>
            )}
          </DetailField>

          <DetailField label="Reporter">
            <div className="flex items-center gap-2" data-testid="item-reporter">
              <InitialAvatar name={reporter?.display_name} />
              <span className="text-[var(--text-sm)] text-[var(--color-text)]">
                {reporter?.display_name ?? 'Unknown'}
              </span>
            </div>
          </DetailField>

          <DetailDivider />

          <DetailField label="Created">
            <div className="flex items-center gap-1 text-[var(--text-xs)] text-[var(--color-text-muted)]">
              <Clock className="h-3 w-3" /> {item.created_at.slice(0, 10)}
            </div>
          </DetailField>
          <DetailField label="Updated">
            <div className="flex items-center gap-1 text-[var(--text-xs)] text-[var(--color-text-muted)]">
              <Clock className="h-3 w-3" /> {item.updated_at.slice(0, 10)}
            </div>
          </DetailField>

          <DetailDivider />
          <CustomFieldsSection spaceId={spaceId} itemId={itemId} />
        </DetailSide>
      </DetailLayout>
    </div>
  );
}
