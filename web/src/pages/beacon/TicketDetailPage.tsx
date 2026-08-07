import { useState, type CSSProperties } from 'react';
import { useParams, Link } from 'react-router-dom';
import { ChevronRight, Clock, AlertCircle } from 'lucide-react';
import { Badge, type BadgeProps } from '../../components/ui/badge';
import { SegmentedControl, type SegmentedOption } from '../../components/ui/segmented';
import {
  DetailLayout,
  DetailMain,
  DetailSide,
  DetailField,
  DetailDivider,
} from '../../components/layout/DetailLayout';
import { EntityShareControl } from '../../components/EntityShareControl';
import { CustomFieldsSection } from '../../components/CustomFieldsSection';
import { RelationsSection } from '../../components/RelationsSection';
import { EntityTags } from '../../components/tags/EntityTags';
import { ModuleChip } from '../../shell/ModuleChip';
import { PriorityPill, normalizePriority } from '../../components/priority';
import { Input } from '../../components/ui/input';
import { cn, formatUTCDate, toRFC3339Date } from '../../lib/utils';
import { Markdown } from '../../components/Markdown';
import { ApprovalBlock } from '../../components/workflow/ApprovalBlock';
import { statusOptionsFor, statusOptionLabel } from '../../lib/workflow/statusOptions';
import {
  runStatusChange,
  statusOutcomeMessage,
  type StatusOutcome,
} from '../../components/workflow/statusOutcome';
import {
  useAvailableTransitions,
  useTicket,
  useTransitionTicketStatus,
  useAssignTicket,
  useUpdateTicket,
  useMembers,
  useComments,
  useCreateComment,
  useMe,
  useSpace,
  friendlyErrorMessage,
  type TicketStatus,
  type CommentVisibility,
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

/**
 * The picker's vocabulary for a space with NO workflow assigned.
 *
 * Where a workflow governs, the options come from the server's offering — the
 * only thing that can know which moves this actor may take from this ticket's
 * current state, since ADR-0011 conditions are evaluated against both.
 *
 * These four remain the fallback because that is exactly what an unworkflowed
 * beacon space still runs: tickets.validTransitions, the hardcoded map, which
 * knows these four and no others.
 */
const FALLBACK_STATUSES: TicketStatus[] = ['open', 'in_progress', 'resolved', 'closed'];

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
// Comment visibility vocabulary (customer portal, migrations 044/045)
// ---------------------------------------------------------------------------

/**
 * Visibility is STATE, so its markers are a tinted ground with a MATCHING
 * foreground — tokens.css: "Hue with matching text means state. Hue with
 * neutral text means provenance." The tint formula is the priority selector's
 * (`PRIORITY_SEGMENT_CLASS` / `PRIORITY_PILL_CLASS` in components/priority.tsx),
 * reused verbatim so the two selectors read as one control family.
 *
 * Internal borrows warning-yellow (caution: this stays inside), public borrows
 * info-blue (this leaves the building).
 */
const VISIBILITY_SEGMENT_CLASS: Record<CommentVisibility, string> = {
  internal:
    'bg-[color-mix(in_srgb,var(--color-warning)_22%,transparent)] text-[var(--color-warning)]',
  public: 'bg-[color-mix(in_srgb,var(--color-info)_26%,transparent)] text-[var(--color-info)]',
};

const VISIBILITY_CHIP_CLASS: Record<CommentVisibility, string> = {
  internal:
    'bg-[color-mix(in_srgb,var(--color-warning)_16%,transparent)] text-[var(--color-warning)]',
  public: 'bg-[color-mix(in_srgb,var(--color-info)_18%,transparent)] text-[var(--color-info)]',
};

/**
 * A full sentence, not a word. The two mistakes are not symmetric: a public
 * note that should have been internal is a disclosure to an external customer
 * that cannot be recalled, while the reverse is a delay. So the composer says
 * who reads this in plain language, next to where the typing happens, rather
 * than relying on the operator decoding a selected segment.
 */
const VISIBILITY_SENTENCE: Record<CommentVisibility, string> = {
  internal: 'Only your team can see this.',
  public: 'The customer will see this.',
};

const VISIBILITY_OPTIONS: SegmentedOption<CommentVisibility>[] = [
  {
    value: 'internal',
    label: 'Internal note',
    dotColor: 'var(--color-warning)',
    selectedClassName: VISIBILITY_SEGMENT_CLASS.internal,
  },
  {
    value: 'public',
    label: 'Reply to customer',
    dotColor: 'var(--color-info)',
    selectedClassName: VISIBILITY_SEGMENT_CLASS.public,
  },
];

const markerChipClass = 'inline-flex shrink-0 items-center rounded-[5px] px-2 py-[3px] text-[11px] font-medium';

/**
 * Where a ticket or a message came from is PROVENANCE, so it is the module hue
 * as background at --module-chip-alpha (the `.module-chip` rule in
 * globals.css) with neutral --module-chip-fg text — never the hue as text,
 * which would read as state. Same shape as `shell/ModuleChip` and
 * `components/ItemKeyChip`; it is a distinct label rather than a reuse of
 * either because neither is keyed on origin.
 */
function ProvenanceChip({ label, testId }: { label: string; testId: string }) {
  return (
    <span
      data-testid={testId}
      className={cn(
        'module-chip inline-flex shrink-0 items-center rounded-[5px] px-[7px] py-[2px]',
        'text-[10px] font-medium leading-4',
      )}
      style={{ '--chip-hue': 'var(--module-beacon)', color: 'var(--module-chip-fg)' } as CSSProperties}
    >
      {label}
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
  // The moves this actor may be offered, with ADR-0011 conditions applied. The
  // server refuses an illegal move regardless — this only stops the picker
  // showing doors that do not open.
  const { data: transitions, refetch: refetchTransitions } =
    useAvailableTransitions(spaceId, 'ticket', ticketId ?? '');
  const transitionMutation = useTransitionTicketStatus(spaceId, ticketId ?? '');
  const assignMutation = useAssignTicket(spaceId, ticketId ?? '');
  // The first caller of the ticket PATCH from anywhere in the product. Assignee
  // and status have their own routes; due_at has none, so it goes through the
  // general update — which until this change could not carry it.
  const updateMutation = useUpdateTicket(spaceId, ticketId ?? '');
  const { data: me } = useMe();
  const orgId = me?.org_id ?? '';
  const { data: members } = useMembers(orgId, spaceId);
  const { data: comments, refetch: refetchComments } = useComments(orgId, spaceId, 'ticket', ticketId ?? '');
  const createCommentMutation = useCreateComment(orgId, spaceId, 'ticket', ticketId ?? '');

  // Always includes the ticket's CURRENT status, which the workflow never
  // offers (no state has an edge to itself) and a <select> renders blank
  // without.
  const statusOptions = statusOptionsFor(ticket?.status ?? '', transitions, FALLBACK_STATUSES);

  const [newComment, setNewComment] = useState('');
  // Internal is the default and the safe direction: a note that stays inside
  // when it should have gone out costs a delay; the reverse cannot be undone.
  const [commentVisibility, setCommentVisibility] = useState<CommentVisibility>('internal');
  const [statusOutcome, setStatusOutcome] = useState<StatusOutcome>({ kind: 'idle' });
  const [dueDateError, setDueDateError] = useState<string | null>(null);

  // A status change has THREE outcomes and this page used to handle one. The
  // await had no try/catch and never read `.error`, so a guard refusal was an
  // unhandled rejection: the <select> kept its new value and the refetch never
  // ran. Worse, a 202 pending-approval body is not an error at all, so it
  // resolved as success wearing a Ticket's type — the page reported a move that
  // had not happened. runStatusChange tells the three apart.
  async function handleStatusChange(newStatus: TicketStatus) {
    setStatusOutcome({ kind: 'idle' });
    const outcome = await runStatusChange(
      () => transitionMutation.mutateAsync(newStatus),
      'The status could not be changed.',
    );
    setStatusOutcome(outcome);
    // Refetch on every outcome, not just success: the select is bound to
    // ticket.status, so re-reading the server's truth is what snaps it back
    // when the transition was refused or is merely pending. The OFFERED set
    // goes stale with it, because what is legal depends on where the ticket now
    // is.
    refetchTicket();
    refetchTransitions();
  }

  async function handleAssigneeChange(assigneeId: string) {
    await assignMutation.mutateAsync(assigneeId || null);
    refetchTicket();
  }

  async function handleDueDateChange(value: string) {
    setDueDateError(null);
    try {
      // An emptied input must send an explicit null. toRFC3339Date returns
      // undefined for "", which JSON.stringify drops from the body — and an
      // absent due_at means "leave it alone", so relying on that default would
      // make the field impossible to clear.
      //
      // Nothing else goes in this body. The ticket PATCH is a true partial
      // update, so resending title/description/priority to "be safe" would be
      // the race, not the safeguard.
      await updateMutation.mutateAsync({ due_at: value ? toRFC3339Date(value) : null });
      refetchTicket();
    } catch (e) {
      setDueDateError(friendlyErrorMessage(e, 'The due date could not be changed.'));
    }
  }

  async function handleAddComment() {
    if (!newComment.trim()) return;
    // `visibility` goes on EVERY create, never left to the server default.
    // Absent and '' both mean internal server-side, so an omission and a
    // deliberate internal note would be indistinguishable on the wire; sending
    // it explicitly keeps the request self-describing.
    await createCommentMutation.mutateAsync({
      content: newComment.trim(),
      visibility: commentVisibility,
    });
    setNewComment('');
    // Public does not stick. Leaving the toggle where it was would turn one
    // deliberate customer reply into a default for the next note.
    setCommentVisibility('internal');
    // The create response literal always reports from_requester:false — only
    // the list path populates it — so the thread is refetched rather than
    // rendered optimistically from the POST echo.
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
  // Migration 044's `tickets_origin_identity` XOR: a ticket carries EITHER a
  // reporter or a requester, never both and never neither. So this predicate is
  // the origin — there is no `origin` field to add, and nothing for one to
  // disagree with.
  const fromPortal = ticket.requester_id !== null;
  // A requester has no `users` row by design, so it must never be looked up in
  // the org member list; that lookup is what rendered portal tickets as
  // "Unknown".
  const reporter = fromPortal
    ? undefined
    : (members ?? []).find((m) => m.user_id === ticket.reporter_id);
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
            {fromPortal && <ProvenanceChip label="Portal" testId="portal-origin-chip" />}
          </div>

          {/* The shared renderer (P5). It was four copies of the same prose
              block until a fifth was about to ship with the note gadget. */}
          <Markdown
            fallback={
              <span className="italic text-[var(--color-text-muted)] text-[var(--text-sm)]">
                No description provided.
              </span>
            }
          >
            {ticket.description ?? ''}
          </Markdown>

          {/* Entity tags (migration 055): the same org vocabulary pages carry,
              editable in place like the rest of the detail surface. The server
              enforces the edit permission; a refused write surfaces its words. */}
          <EntityTags entityType="ticket" spaceId={spaceId} entityId={ticket.id} editable />

          {/* Approvals (ADR-0011 tier 2). Above the Activity block and outside
              it: the comment area and its visibility toggle belong to the
              customer-portal track (#98) and are deliberately untouched here. */}
          <div className="mt-6">
            <ApprovalBlock
              spaceId={spaceId}
              entityType="ticket"
              entityId={ticketId ?? ''}
              onDecided={() => refetchTicket()}
            />
          </div>

          {/* Relations (A4): the same shared surface project items carry —
              tickets are a from-side now, not only a target. */}
          <RelationsSection
            orgId={orgId}
            spaceId={spaceId}
            entityType="ticket"
            entityId={ticket.id}
          />

          {/* Comments section */}
          <div className="mt-6 border-t border-[var(--color-border)] pt-5">
            <h3 className="mb-4 text-[var(--text-sm)] font-semibold text-[var(--color-text)]">Activity</h3>

            <div className="mb-6 space-y-4">
              {(comments ?? []).length === 0 && (
                <p className="text-[var(--text-sm)] italic text-[var(--color-text-muted)]">No comments yet.</p>
              )}
              {(comments ?? []).map((comment) => (
                <div
                  key={comment.id}
                  data-testid="comment-row"
                  data-visibility={comment.visibility}
                  className={cn(
                    'flex gap-3',
                    // Provenance again: a customer's own message sits on the
                    // module hue with ordinary text, never hue-coloured text.
                    comment.from_requester &&
                      'rounded-[var(--radius-lg)] bg-[color-mix(in_srgb,var(--module-beacon)_8%,transparent)] p-2',
                  )}
                >
                  <InitialAvatar name={comment.author_name} className="h-8 w-8 text-[var(--text-sm)]" />
                  <div className="min-w-0 flex-1">
                    <div className="mb-1 flex flex-wrap items-center gap-2">
                      <span className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                        {comment.author_name ?? 'Unknown'}
                      </span>
                      {comment.from_requester && (
                        <ProvenanceChip label="Customer" testId="comment-requester-chip" />
                      )}
                      {comment.visibility === 'public' && (
                        <span
                          data-testid="comment-public-marker"
                          className={cn(markerChipClass, VISIBILITY_CHIP_CLASS.public)}
                        >
                          Visible to customer
                        </span>
                      )}
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
                {/* The audience, stated twice: as the selected segment, and as
                    a full sentence beside it. The composer's own border picks
                    up the public hue so the state is unmistakable while typing
                    and not only at the toggle. */}
                <div className="mb-2 flex flex-wrap items-center gap-2">
                  <SegmentedControl
                    options={VISIBILITY_OPTIONS}
                    value={commentVisibility}
                    onChange={setCommentVisibility}
                    aria-label="Comment visibility"
                    fullWidth={false}
                    testId="comment-visibility"
                  />
                  <span
                    data-testid="comment-visibility-state"
                    className={cn(markerChipClass, VISIBILITY_CHIP_CLASS[commentVisibility])}
                  >
                    {VISIBILITY_SENTENCE[commentVisibility]}
                  </span>
                </div>
                <textarea
                  value={newComment}
                  onChange={(e) => setNewComment(e.target.value)}
                  placeholder={
                    commentVisibility === 'public'
                      ? 'Reply to the customer...'
                      : 'Add an internal note...'
                  }
                  data-testid="comment-composer"
                  className={cn(
                    'w-full resize-none rounded-[var(--radius-lg)] border bg-[var(--color-input)] px-3 py-2 text-[var(--text-sm)] text-[var(--color-text)]',
                    'placeholder:text-[var(--color-text-muted)]',
                    'focus:outline-none focus:ring-1',
                    commentVisibility === 'public'
                      ? 'border-[var(--color-info)] focus:border-[var(--color-info)] focus:ring-[var(--color-info)]'
                      : 'border-[var(--color-border)] focus:border-[var(--color-primary)] focus:ring-[var(--color-primary)]',
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

          {/* Requester or Reporter — never both, per the 044 XOR. The field is
              relabelled rather than reused: an external requester is not a
              member of this org and must not be presented as one. */}
          {fromPortal ? (
            <DetailField label="Requester">
              <div className="flex items-center gap-2" data-testid="ticket-requester">
                <InitialAvatar name={ticket.requester?.display_name} />
                <div className="min-w-0">
                  <div className="truncate text-[var(--text-sm)] text-[var(--color-text)]">
                    {ticket.requester?.display_name ?? 'Portal requester'}
                  </div>
                  {ticket.requester?.email && (
                    <div className="truncate text-[var(--text-xs)] text-[var(--color-text-muted)]">
                      {ticket.requester.email}
                    </div>
                  )}
                </div>
              </div>
            </DetailField>
          ) : (
            <DetailField label="Reporter">
              <div className="flex items-center gap-2" data-testid="ticket-reporter">
                <InitialAvatar name={reporter?.display_name} />
                <span className="text-[var(--text-sm)] text-[var(--color-text)]">
                  {reporter?.display_name ?? 'Unknown'}
                </span>
              </div>
            </DetailField>
          )}

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
                {statusOptions.map((o) => (
                  <option key={o.value} value={o.value}>
                    {statusOptionLabel(
                      o,
                      // A workflow state can be named anything an administrator
                      // likes, so the label map is a lookup with a fallback
                      // rather than an exhaustive Record.
                      STATUS_LABEL[o.value as TicketStatus] ?? o.value,
                    )}
                  </option>
                ))}
              </select>
              {/* The reason, beside the control that produced it. A refusal
                  carries the guard's own sentence (422 VALIDATION_ERROR passes
                  through friendlyErrorMessage unchanged); a pending approval
                  carries the server's wording, which already says the item has
                  not moved. Neither is a toast: this page mounts no toast
                  provider, and a message that disappears is the wrong shape for
                  an explanation the user may need to act on. */}
              {statusOutcomeMessage(statusOutcome) && (
                <p
                  data-testid="status-outcome"
                  className={
                    statusOutcome.kind === 'pending'
                      ? 'text-[var(--text-xs)] text-[var(--color-warning)]'
                      : 'text-[var(--text-xs)] text-[var(--color-danger)]'
                  }
                >
                  {statusOutcomeMessage(statusOutcome)}
                </p>
              )}
            </div>
          </DetailField>

          <DetailDivider />

          <DetailField label="Due date">
            <Input
              type="date"
              aria-label="Due date"
              data-testid="ticket-due-date"
              value={ticket.due_at ? formatUTCDate(ticket.due_at) : ''}
              disabled={updateMutation.isPending}
              onChange={(e) => handleDueDateChange(e.target.value)}
              className={sideSelectClass}
            />
            {dueDateError && (
              <p className="mt-1 text-[var(--text-xs)] text-[var(--color-danger)]">{dueDateError}</p>
            )}
          </DetailField>

          {/* Live, editable, sometimes required data — above the Created/
              Updated metadata footer, not below it. The section renders its
              own trailing divider only when it has content, so an empty form
              leaves this rail exactly as it was before custom fields. */}
          <CustomFieldsSection spaceId={spaceId} entityKind="ticket" entityId={ticket.id} />

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
