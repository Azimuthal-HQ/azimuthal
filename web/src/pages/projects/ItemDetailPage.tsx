import { useState } from 'react';
import { useParams, Link } from 'react-router-dom';
import { ArrowLeft, Clock, AlertCircle } from 'lucide-react';
import ReactMarkdown from 'react-markdown';
import { Badge, type BadgeProps } from '../../components/ui/badge';
import { Card, CardContent, CardHeader, CardTitle } from '../../components/ui/card';
import { cn } from '../../lib/utils';
import {
  useProjectItem,
  useUpdateProjectItem,
  useMembers,
  useComments,
  useCreateComment,
  useMe,
} from '../../lib/api';

// ---------------------------------------------------------------------------
// Badge helpers
// ---------------------------------------------------------------------------

const PRIORITY_VARIANT: Record<string, BadgeProps['variant']> = {
  critical: 'danger', urgent: 'danger', high: 'warning', medium: 'secondary', low: 'outline',
};
const PRIORITY_LABEL: Record<string, string> = {
  critical: 'Critical', urgent: 'Critical', high: 'High', medium: 'Medium', low: 'Low',
};
const STATUS_VARIANT: Record<string, BadgeProps['variant']> = {
  open: 'default', todo: 'secondary', in_progress: 'warning', in_review: 'default', done: 'success', closed: 'secondary',
};
const STATUS_LABEL: Record<string, string> = {
  open: 'Open', todo: 'To Do', in_progress: 'In Progress', in_review: 'In Review', done: 'Done', closed: 'Closed',
};

const ALL_STATUSES = ['open', 'in_progress', 'in_review', 'done', 'closed'];

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

/** Detail view for a project item. */
export function ItemDetailPage() {
  const { spaceId = '', itemKey } = useParams<{ spaceId: string; itemKey: string }>();
  const itemId = itemKey ?? '';

  const { data: item, isLoading, error, refetch: refetchItem } = useProjectItem(spaceId, itemId);
  const updateMutation = useUpdateProjectItem(spaceId, itemId);
  const { data: me } = useMe();
  const orgId = me?.org_id ?? '';
  const { data: members } = useMembers(orgId);
  const { data: comments, refetch: refetchComments } = useComments(spaceId, itemId);
  const createCommentMutation = useCreateComment(spaceId, itemId);

  const [newComment, setNewComment] = useState('');

  const backlogPath = `/spaces/${spaceId}/backlog`;

  async function handleStatusChange(newStatus: string) {
    await updateMutation.mutateAsync({ status: newStatus });
    refetchItem();
  }

  async function handleAssigneeChange(assigneeId: string) {
    await updateMutation.mutateAsync({ assignee_id: assigneeId || null });
    refetchItem();
  }

  async function handleAddComment() {
    if (!newComment.trim()) return;
    await createCommentMutation.mutateAsync({ content: newComment.trim() });
    setNewComment('');
    refetchComments();
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
        <div className="flex items-center gap-3 rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[var(--color-danger)]/10 p-4">
          <AlertCircle className="h-5 w-5 text-[var(--color-danger)]" />
          <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
            {error.status === 404 ? 'Item not found.' : `Failed to load item: ${error.message}`}
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

  const priorityKey = String(item.priority ?? '').toLowerCase();
  const reporter = (members ?? []).find((m) => m.user_id === item.reporter_id);

  return (
    <div className="space-y-6">
      {/* Breadcrumb */}
      <div className="flex items-center gap-2 text-[var(--text-sm)] text-[var(--color-text-muted)]">
        <Link to={backlogPath} className="flex items-center gap-1 hover:text-[var(--color-text)]">
          <ArrowLeft className="h-4 w-4" />
          Backlog
        </Link>
        <span>/</span>
        <span className="text-[var(--color-text)]" style={{ fontFamily: 'var(--font-mono)' }}>
          {item.number ? `PROJ-${item.number}` : item.id.slice(0, 8)}
        </span>
      </div>

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
        {/* Main content */}
        <div className="space-y-6 lg:col-span-2">
          <h1 className="text-[var(--text-2xl)] font-bold text-[var(--color-text)]">{item.title}</h1>

          <Card>
            <CardHeader><CardTitle>Description</CardTitle></CardHeader>
            <CardContent>
              <div className="prose prose-sm dark:prose-invert max-w-none">
                {item.description ? (
                  <ReactMarkdown>{item.description}</ReactMarkdown>
                ) : (
                  <span className="italic text-[var(--color-text-muted)] text-[var(--text-sm)]">
                    No description provided.
                  </span>
                )}
              </div>
            </CardContent>
          </Card>

          {/* Comments section */}
          <div className="border-t border-[var(--color-border)] pt-6">
            <h3 className="text-[var(--text-sm)] font-semibold mb-4 text-[var(--color-text)]">Activity</h3>

            <div className="space-y-4 mb-6">
              {(comments ?? []).length === 0 && (
                <p className="text-[var(--text-sm)] italic text-[var(--color-text-muted)]">No comments yet.</p>
              )}
              {(comments ?? []).map((comment) => (
                <div key={comment.id} className="flex gap-3">
                  <div className="h-8 w-8 rounded-full bg-[var(--color-primary)] flex items-center justify-center text-[var(--text-sm)] text-white font-medium flex-shrink-0">
                    {comment.author_name?.[0]?.toUpperCase() ?? '?'}
                  </div>
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2 mb-1">
                      <span className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                        {comment.author_name ?? 'Unknown'}
                      </span>
                      <span className="text-[var(--text-xs)] text-[var(--color-text-muted)]">
                        {new Date(comment.created_at).toLocaleDateString()}
                      </span>
                    </div>
                    <p className="text-[var(--text-sm)] text-[var(--color-text-muted)] whitespace-pre-wrap">
                      {comment.content ?? comment.body}
                    </p>
                  </div>
                </div>
              ))}
            </div>

            <div className="flex gap-3">
              <div className="h-8 w-8 rounded-full bg-[var(--color-primary)] flex items-center justify-center text-[var(--text-sm)] text-white font-medium flex-shrink-0">
                {me?.display_name?.[0]?.toUpperCase() ?? 'U'}
              </div>
              <div className="flex-1">
                <textarea
                  value={newComment}
                  onChange={(e) => setNewComment(e.target.value)}
                  placeholder="Add a comment..."
                  className={cn(
                    'w-full rounded-[var(--radius-md)] border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-[var(--text-sm)] text-[var(--color-text)] resize-none',
                    'focus:outline-none focus:ring-1 focus:ring-[var(--color-primary)]',
                    'placeholder:text-[var(--color-text-muted)]',
                  )}
                  rows={3}
                />
                <button
                  onClick={handleAddComment}
                  disabled={!newComment.trim() || createCommentMutation.isPending}
                  className="mt-2 px-4 py-1.5 bg-[var(--color-primary)] text-white rounded-[var(--radius-md)] text-[var(--text-sm)] font-medium disabled:opacity-50 hover:opacity-90 transition-colors"
                >
                  {createCommentMutation.isPending ? 'Posting...' : 'Comment'}
                </button>
              </div>
            </div>
          </div>
        </div>

        {/* Sidebar */}
        <div className="space-y-4">
          <Card>
            <CardContent className="space-y-4 p-4">
              {/* Status */}
              <div>
                <label className="mb-1 block text-[var(--text-xs)] font-medium uppercase tracking-wider text-[var(--color-text-muted)]">
                  Status
                </label>
                <div className="flex items-center gap-2">
                  <Badge variant={STATUS_VARIANT[item.status] ?? 'secondary'}>
                    {STATUS_LABEL[item.status] ?? item.status}
                  </Badge>
                  <select
                    value={item.status}
                    onChange={(e) => handleStatusChange(e.target.value)}
                    className={cn(
                      'h-9 flex-1 rounded-[var(--radius-md)] border border-[var(--color-border)]',
                      'bg-[var(--color-surface)] px-3 text-[var(--text-sm)] text-[var(--color-text)]',
                      'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)]',
                    )}
                  >
                    {ALL_STATUSES.map((s) => (
                      <option key={s} value={s}>{STATUS_LABEL[s] ?? s}</option>
                    ))}
                  </select>
                </div>
              </div>

              {/* Priority */}
              <div>
                <label className="mb-1 block text-[var(--text-xs)] font-medium uppercase tracking-wider text-[var(--color-text-muted)]">
                  Priority
                </label>
                <Badge variant={PRIORITY_VARIANT[priorityKey] ?? 'secondary'}>
                  {PRIORITY_LABEL[priorityKey] ?? 'Medium'}
                </Badge>
              </div>

              {/* Assignee */}
              <div className="space-y-1">
                <label className="text-[var(--text-xs)] font-medium uppercase tracking-wider text-[var(--color-text-muted)]">
                  Assignee
                </label>
                <select
                  value={item.assignee_id ?? ''}
                  onChange={(e) => handleAssigneeChange(e.target.value)}
                  className={cn(
                    'w-full rounded-[var(--radius-md)] border border-[var(--color-border)]',
                    'bg-[var(--color-surface)] px-2 py-1.5 text-[var(--text-sm)] text-[var(--color-text)]',
                    'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)]',
                  )}
                >
                  <option value="">Unassigned</option>
                  {(members ?? []).map((m) => (
                    <option key={m.user_id} value={m.user_id}>{m.display_name}</option>
                  ))}
                </select>
              </div>

              {/* Reporter */}
              <div className="space-y-1">
                <label className="text-[var(--text-xs)] font-medium uppercase tracking-wider text-[var(--color-text-muted)]">
                  Reporter
                </label>
                <div className="flex items-center gap-2">
                  <div className="h-6 w-6 rounded-full bg-[var(--color-primary)] flex items-center justify-center text-[var(--text-xs)] text-white font-medium">
                    {reporter?.display_name?.[0]?.toUpperCase() ?? '?'}
                  </div>
                  <span className="text-[var(--text-sm)] text-[var(--color-text)]">
                    {reporter?.display_name ?? 'Unknown'}
                  </span>
                </div>
              </div>

              {/* Dates */}
              <div className="border-t border-[var(--color-border)] pt-3 space-y-1">
                <div className="flex items-center gap-1 text-[var(--text-xs)] text-[var(--color-text-muted)]">
                  <Clock className="h-3 w-3" /> Created {item.created_at.slice(0, 10)}
                </div>
                <div className="flex items-center gap-1 text-[var(--text-xs)] text-[var(--color-text-muted)]">
                  <Clock className="h-3 w-3" /> Updated {item.updated_at.slice(0, 10)}
                </div>
              </div>
            </CardContent>
          </Card>
        </div>
      </div>
    </div>
  );
}
