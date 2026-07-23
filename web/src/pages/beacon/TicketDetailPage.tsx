import { useState } from 'react';
import { useParams, Link } from 'react-router-dom';
import { ChevronRight, Clock, AlertCircle } from 'lucide-react';
import ReactMarkdown from 'react-markdown';
import { Badge, type BadgeProps } from '../../components/ui/badge';
import {
  DetailLayout,
  DetailMain,
  DetailSide,
  DetailField,
  DetailDivider,
} from '../../components/layout/DetailLayout';
import { EntityShareControl } from '../../components/EntityShareControl';
import { ModuleChip } from '../../shell/ModuleChip';
import { PriorityPill, normalizePriority } from '../../components/priority';
import { cn } from '../../lib/utils';
import {
  useTicket,
  useTransitionTicketStatus,
  useAssignTicket,
  useMembers,
  useComments,
  useCreateComment,
  useMe,
  useSpace,
  friendlyErrorMessage,
  type TicketStatus,
} from '../../lib/api';

// ---------------------------------------------------------------------------
// Status vocabulary
// ---------------------------------------------------------------------------

const STATUS_VARIANT: Record<TicketStatus, BadgeProps['variant']> = {
  open: 'default',
  in_progress: 'warning',
  resolved: 'success',
  closed: 'secondary',
};

const STATUS_LABEL: Record<TicketStatus, string> = {
  open: 'Open',
  in_progress: 'In Progress',
  resolved: 'Resolved',
  closed: 'Closed',
};

const ALL_STATUSES: TicketStatus[] = ['open', 'in_progress', 'resolved', 'closed'];

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

/** Detail page for a single service desk ticket. */
export function TicketDetailPage() {
  const { spaceId = '', ticketId } = useParams<{ spaceId: string; ticketId: string }>();
  const { data: space } = useSpace(spaceId);
  const { data: ticket, isLoading, error, refetch: refetchTicket } = useTicket(spaceId, ticketId ?? '');
  const transitionMutation = useTransitionTicketStatus(spaceId, ticketId ?? '');
  const assignMutation = useAssignTicket(spaceId, ticketId ?? '');
  const { data: me } = useMe();
  const orgId = me?.org_id ?? '';
  const { data: members } = useMembers(orgId, spaceId);
  const { data: comments, refetch: refetchComments } = useComments(orgId, spaceId, 'ticket', ticketId ?? '');
  const createCommentMutation = useCreateComment(orgId, spaceId, 'ticket', ticketId ?? '');

  const [newComment, setNewComment] = useState('');

  async function handleStatusChange(newStatus: TicketStatus) {
    await transitionMutation.mutateAsync(newStatus);
    refetchTicket();
  }

  async function handleAssigneeChange(assigneeId: string) {
    await assignMutation.mutateAsync(assigneeId || null);
    refetchTicket();
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
        Loading ticket...
      </div>
    );
  }

  if (error) {
    return (
      <div className="flex items-center gap-3 rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[color-mix(in_srgb,var(--color-danger)_10%,transparent)] p-4">
        <AlertCircle className="h-5 w-5 text-[var(--color-danger)]" />
        <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
          {error.status === 404
            ? 'Ticket not found.'
            : friendlyErrorMessage(error, 'The ticket could not be loaded.')}
        </p>
      </div>
    );
  }

  if (!ticket) {
    return (
      <div className="flex h-64 items-center justify-center text-[var(--color-text-muted)]">
        Ticket not found.
      </div>
    );
  }

  const ticketsPath = spaceId ? `/beacon/${spaceId}/tickets` : '/beacon';
  const ticketKey = ticket.number
    ? `${space?.key ?? 'SD'}-${ticket.number}`
    : ticket.id.slice(0, 8);
  const reporter = (members ?? []).find((m) => m.user_id === ticket.reporter_id);
  const assignee = (members ?? []).find((m) => m.user_id === ticket.assignee_id);

  return (
    <div className="space-y-4">
      {/* Breadcrumb */}
      <nav className="flex items-center gap-1 text-[var(--text-sm)] text-[var(--color-text-muted)]">
        <Link to={ticketsPath} className="hover:text-[var(--color-text)]">
          Tickets
        </Link>
        <ChevronRight className="h-4 w-4" />
        <span className="text-[var(--color-text)]" style={{ fontFamily: 'var(--font-mono)' }}>
          {ticketKey}
        </span>
      </nav>

      <DetailLayout>
        <DetailMain>
          <div
            className="mb-2 text-[var(--text-xs)] text-[var(--color-text-muted)]"
            style={{ fontFamily: 'var(--font-mono)' }}
          >
            {ticketKey}
          </div>
          <div className="flex items-start justify-between gap-3">
            <h1 className="mb-3.5 text-[19px] font-semibold leading-[1.3] tracking-[-.01em] text-[var(--color-text)]">
              {ticket.title}
            </h1>
            <EntityShareControl
              orgId={orgId}
              spaceId={spaceId}
              entityType="ticket"
              entityId={ticket.id}
              entityLabel={ticket.title}
            />
          </div>

          {/* Meta row: status, priority, module — the one vocabulary. */}
          <div className="mb-5 flex flex-wrap items-center gap-2">
            <Badge variant={STATUS_VARIANT[ticket.status]}>
              {STATUS_LABEL[ticket.status]}
            </Badge>
            <PriorityPill priority={normalizePriority(ticket.priority)} />
            <ModuleChip module="beacon" />
          </div>

          {/* Description — prose colors pinned to the theme tokens: the app's
              theme is the .dark class, while prose-invert keys off the OS
              media query, so the two can desync. */}
          <div
            className={cn(
              'prose prose-sm dark:prose-invert max-w-none leading-[1.7]',
              'prose-headings:text-[var(--color-text)] prose-headings:font-semibold',
              'prose-p:text-[var(--color-text)] prose-li:text-[var(--color-text)] prose-strong:text-[var(--color-text)]',
              'prose-a:text-[var(--color-primary)]',
              'prose-code:font-[var(--font-mono)] prose-code:text-[var(--color-text)] prose-code:bg-[var(--color-input)] prose-code:rounded prose-code:px-1.5 prose-code:py-0.5',
              'prose-pre:bg-[var(--color-input)] prose-pre:border prose-pre:border-[var(--color-border)]',
            )}
          >
            {ticket.description ? (
              <ReactMarkdown>{ticket.description}</ReactMarkdown>
            ) : (
              <span className="italic text-[var(--color-text-muted)] text-[var(--text-sm)]">
                No description provided.
              </span>
            )}
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
          <DetailField label="Assignee">
            <div className="space-y-1.5">
              {assignee ? (
                <div className="flex items-center gap-2">
                  <InitialAvatar name={assignee.display_name} />
                  <span className="text-[var(--text-sm)] text-[var(--color-text)]">
                    {assignee.display_name}
                  </span>
                </div>
              ) : (
                <div className="text-[var(--text-sm)] text-[var(--color-text-muted)]">Unassigned</div>
              )}
              <select
                value={ticket.assignee_id ?? ''}
                onChange={(e) => handleAssigneeChange(e.target.value)}
                aria-label="Change assignee"
                className={sideSelectClass}
              >
                <option value="">Unassigned</option>
                {(members ?? []).map((m) => (
                  <option key={m.user_id} value={m.user_id}>{m.display_name}</option>
                ))}
              </select>
              <div className="flex gap-[var(--space-2)]">
                {ticket.assignee_id !== me?.id && (
                  <button
                    type="button"
                    onClick={() => handleAssigneeChange(me?.id ?? '')}
                    className="text-[var(--text-xs)] text-[var(--color-primary)] hover:underline"
                  >
                    Assign to me
                  </button>
                )}
                {ticket.assignee_id && (
                  <button
                    type="button"
                    onClick={() => handleAssigneeChange('')}
                    className="text-[var(--text-xs)] text-[var(--color-text-muted)] hover:text-[var(--color-danger)] hover:underline"
                  >
                    Unassign
                  </button>
                )}
              </div>
            </div>
          </DetailField>

          <DetailField label="Reporter">
            <div className="flex items-center gap-2" data-testid="ticket-reporter">
              <InitialAvatar name={reporter?.display_name} />
              <span className="text-[var(--text-sm)] text-[var(--color-text)]">
                {reporter?.display_name ?? 'Unknown'}
              </span>
            </div>
          </DetailField>

          <DetailDivider />

          <DetailField label="Priority">
            <PriorityPill priority={normalizePriority(ticket.priority)} />
          </DetailField>

          <DetailField label="Status">
            <div className="space-y-1.5">
              <Badge variant={STATUS_VARIANT[ticket.status]}>
                {STATUS_LABEL[ticket.status]}
              </Badge>
              <select
                value={ticket.status}
                onChange={(e) => handleStatusChange(e.target.value as TicketStatus)}
                aria-label="Change status"
                className={sideSelectClass}
              >
                {ALL_STATUSES.map((s) => (
                  <option key={s} value={s}>{STATUS_LABEL[s]}</option>
                ))}
              </select>
            </div>
          </DetailField>

          <DetailDivider />

          <DetailField label="Created">
            <div className="flex items-center gap-2 text-[var(--text-xs)] text-[var(--color-text-muted)]">
              <Clock className="h-3 w-3" />
              {ticket.created_at.slice(0, 10)}
            </div>
          </DetailField>
          <DetailField label="Updated">
            <div className="flex items-center gap-2 text-[var(--text-xs)] text-[var(--color-text-muted)]">
              <Clock className="h-3 w-3" />
              {ticket.updated_at.slice(0, 10)}
            </div>
          </DetailField>
        </DetailSide>
      </DetailLayout>
    </div>
  );
}
