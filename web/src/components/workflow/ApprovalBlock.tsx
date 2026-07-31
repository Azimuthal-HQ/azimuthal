import { useState } from 'react';
import { Clock, ShieldCheck, XCircle } from 'lucide-react';
import { cn } from '../../lib/utils';
import {
  friendlyErrorMessage,
  useDecideApproval,
  useEntityApprovals,
  type ApprovalEntityType,
  type WorkflowApproval,
} from '../../lib/api';

/**
 * The approval surface for one ticket or project item (ADR-0011 tier 2).
 *
 * # Why this reads the ENTITY route rather than the space's pending list
 *
 * The board reads the space's pending approvals, and it would have been cheaper
 * to reuse that here. It cannot be: the moment an approver declines, the request
 * stops being pending and leaves that list, taking the reason with it. A block
 * built on the pending set would show the requester a blocked item and then
 * show them nothing at all — the item silently not having moved, which is the
 * exact failure this tier exists to prevent, arriving one step later.
 *
 * So the per-entity read returns the whole history and this renders the latest
 * decision when it is a decline.
 *
 * # Nothing is shown when nothing is happening
 *
 * An item with no approval history renders null — not an empty panel, not a
 * "no approvals" line. An item in a space nobody has configured must look
 * exactly as it did before this feature shipped.
 *
 * # Authority is DATA, so the server decides who sees the buttons
 *
 * `can_decide` is resolved server-side from the transition's approver rows and
 * the caller's ADR-0007 effective teams. There is no capability to check and
 * nothing on the approval row a client could compute it from, so a
 * capability-shaped gate here would show the buttons to the wrong people. The
 * server refuses regardless — this only decides whether to offer.
 */

interface ApprovalBlockProps {
  spaceId: string;
  entityType: ApprovalEntityType;
  entityId: string;
  /** Called after a decision lands, so the page can refetch the item. */
  onDecided?: () => void;
}

export function ApprovalBlock({ spaceId, entityType, entityId, onDecided }: ApprovalBlockProps) {
  const { data } = useEntityApprovals(spaceId, entityType, entityId);
  const approvals = data ?? [];

  // Newest first from the server. The pending one, if any, is what matters
  // most; otherwise the most recent decision is worth showing only while it
  // still explains why the item is where it is.
  const pending = approvals.find((a) => !a.decided_at);
  const lastDecision = approvals.find((a) => a.decided_at);

  if (pending) {
    return (
      <PendingApproval
        approval={pending}
        spaceId={spaceId}
        entityType={entityType}
        entityId={entityId}
        onDecided={onDecided}
      />
    );
  }

  // An approval that went through needs no notice: the item moved, and the
  // status itself is the report. A DECLINE does need one — the item did not
  // move, and without this the requester has no way to learn why.
  if (lastDecision?.decision === 'declined') {
    return <DeclinedNotice approval={lastDecision} />;
  }

  return null;
}

function PendingApproval({
  approval,
  spaceId,
  entityType,
  entityId,
  onDecided,
}: {
  approval: WorkflowApproval;
  spaceId: string;
  entityType: ApprovalEntityType;
  entityId: string;
  onDecided?: () => void;
}) {
  const decide = useDecideApproval(spaceId, entityType, entityId);
  const [declining, setDeclining] = useState(false);
  const [reason, setReason] = useState('');

  const stuck = approval.transition_id === null;

  function submit(decision: 'approved' | 'declined') {
    decide.mutate(
      { approvalId: approval.id, decision, reason: decision === 'declined' ? reason : undefined },
      { onSuccess: () => { setDeclining(false); setReason(''); onDecided?.(); } },
    );
  }

  return (
    <section
      data-testid="approval-pending"
      className={cn(
        'rounded-[var(--radius-lg)] border border-[var(--color-warning)]',
        'bg-[color-mix(in_srgb,var(--color-warning)_8%,transparent)] p-[var(--space-4)]',
      )}
    >
      <div className="flex items-start gap-[var(--space-3)]">
        <Clock className="mt-0.5 h-5 w-5 shrink-0 text-[var(--color-warning)]" />
        <div className="min-w-0 flex-1">
          <h3 className="text-[var(--text-sm)] font-semibold text-[var(--color-text)]">
            Waiting for approval
          </h3>
          <p className="mt-1 text-[var(--text-sm)] text-[var(--color-text-muted)]">
            {/* Who asked, and since when — both required by the brief. The name
                is resolved server-side at read time; the id is the fallback,
                because a deleted requester must not render a blank sentence. */}
            <span data-testid="approval-requester">
              {approval.requested_by_name || approval.requested_by}
            </span>{' '}
            asked to move this from{' '}
            <strong className="text-[var(--color-text)]">{approval.from_status}</strong> to{' '}
            <strong className="text-[var(--color-text)]">{approval.to_status}</strong>{' '}
            <Timestamp iso={approval.requested_at} />.
          </p>
          <p className="mt-1 text-[var(--text-sm)] text-[var(--color-text-muted)]">
            The item has not moved and will not until somebody decides.
          </p>

          {stuck && (
            // migration 047 keeps the row when the edge is deleted (ON DELETE
            // SET NULL) so the request does not vanish along with the process
            // state. There is nothing left to traverse, so nobody can decide it
            // — say so rather than showing buttons that will 409.
            <p
              data-testid="approval-stuck"
              className="mt-[var(--space-2)] text-[var(--text-sm)] text-[var(--color-danger)]"
            >
              The transition this was requested for no longer exists, so it can no longer be
              decided. An administrator will need to recreate it.
            </p>
          )}

          {approval.can_decide && !stuck && (
            <div className="mt-[var(--space-3)]">
              {!declining ? (
                <div className="flex flex-wrap gap-[var(--space-2)]">
                  <button
                    type="button"
                    data-testid="approval-approve"
                    disabled={decide.isPending}
                    onClick={() => submit('approved')}
                    className={cn(
                      'inline-flex h-8 items-center gap-1 rounded-[var(--radius-md)]',
                      'bg-[var(--color-primary)] px-[var(--space-3)] text-[var(--text-sm)]',
                      'font-medium text-[var(--color-primary-contrast)] disabled:opacity-50',
                    )}
                  >
                    <ShieldCheck className="h-3.5 w-3.5" />
                    {decide.isPending ? 'Working…' : 'Approve'}
                  </button>
                  <button
                    type="button"
                    data-testid="approval-decline"
                    disabled={decide.isPending}
                    onClick={() => setDeclining(true)}
                    className={cn(
                      'inline-flex h-8 items-center gap-1 rounded-[var(--radius-md)]',
                      'border border-[var(--color-border)] px-[var(--space-3)]',
                      'text-[var(--text-sm)] text-[var(--color-text)] disabled:opacity-50',
                    )}
                  >
                    <XCircle className="h-3.5 w-3.5" />
                    Decline
                  </button>
                </div>
              ) : (
                <div className="space-y-[var(--space-2)]">
                  {/* A reason is REQUIRED on a decline (migration 050). The
                      server refuses a blank or whitespace-only one, and the
                      button is disabled to say so before the round trip rather
                      than after it. */}
                  <label
                    htmlFor="approval-decline-reason"
                    className="block text-[var(--text-sm)] text-[var(--color-text)]"
                  >
                    Why are you declining? The requester will see this.
                  </label>
                  <textarea
                    id="approval-decline-reason"
                    data-testid="approval-decline-reason"
                    rows={2}
                    value={reason}
                    onChange={(e) => setReason(e.target.value)}
                    className={cn(
                      'w-full rounded-[var(--radius-md)] border border-[var(--color-border)]',
                      'bg-[var(--color-surface)] p-[var(--space-2)] text-[var(--text-sm)]',
                      'text-[var(--color-text)]',
                    )}
                  />
                  <div className="flex gap-[var(--space-2)]">
                    <button
                      type="button"
                      data-testid="approval-decline-submit"
                      disabled={decide.isPending || reason.trim() === ''}
                      onClick={() => submit('declined')}
                      className={cn(
                        'h-8 rounded-[var(--radius-md)] bg-[var(--color-danger)] px-[var(--space-3)]',
                        'text-[var(--text-sm)] font-medium text-white disabled:opacity-50',
                      )}
                    >
                      {decide.isPending ? 'Working…' : 'Decline'}
                    </button>
                    <button
                      type="button"
                      onClick={() => setDeclining(false)}
                      className="h-8 rounded-[var(--radius-md)] px-[var(--space-3)] text-[var(--text-sm)] text-[var(--color-text-muted)]"
                    >
                      Cancel
                    </button>
                  </div>
                </div>
              )}

              {decide.error && (
                <p
                  data-testid="approval-error"
                  className="mt-[var(--space-2)] text-[var(--text-sm)] text-[var(--color-danger)]"
                >
                  {friendlyErrorMessage(decide.error, 'The decision could not be recorded.')}
                </p>
              )}
            </div>
          )}
        </div>
      </div>
    </section>
  );
}

/**
 * The decline notice.
 *
 * "Decline returns the item to the source status" is satisfied by the item
 * never having left it — the gate blocks rather than moves — so this does NOT
 * say "returned to open", which would describe a rollback that did not happen.
 * It says where the item still is, and why it is still there.
 */
function DeclinedNotice({ approval }: { approval: WorkflowApproval }) {
  return (
    <section
      data-testid="approval-declined"
      className={cn(
        'rounded-[var(--radius-lg)] border border-[var(--color-border)]',
        'bg-[var(--color-surface-hover)] p-[var(--space-4)]',
      )}
    >
      <div className="flex items-start gap-[var(--space-3)]">
        <XCircle className="mt-0.5 h-5 w-5 shrink-0 text-[var(--color-danger)]" />
        <div className="min-w-0 flex-1">
          <h3 className="text-[var(--text-sm)] font-semibold text-[var(--color-text)]">
            The move to “{approval.to_status}” was declined
          </h3>
          <p className="mt-1 text-[var(--text-sm)] text-[var(--color-text-muted)]">
            <span data-testid="approval-decider">
              {approval.decided_by_name || approval.decided_by}
            </span>{' '}
            declined it <Timestamp iso={approval.decided_at ?? approval.requested_at} />. This
            stayed in <strong className="text-[var(--color-text)]">{approval.from_status}</strong>.
          </p>
          {approval.reason && (
            <blockquote
              data-testid="approval-decline-reason-text"
              className={cn(
                'mt-[var(--space-2)] border-l-2 border-[var(--color-border)] pl-[var(--space-3)]',
                'text-[var(--text-sm)] text-[var(--color-text)]',
              )}
            >
              {approval.reason}
            </blockquote>
          )}
        </div>
      </div>
    </section>
  );
}

/**
 * When something happened, absolutely.
 *
 * Deliberately not "3 hours ago". A relative rendering has to read the clock
 * during render, which react-hooks/purity refuses — correctly, since the output
 * would then depend on when React happened to re-render rather than on the
 * data. The surrounding pages already format timestamps this way, and "since
 * when" is answered as well by a date somebody can quote back.
 */
function Timestamp({ iso }: { iso: string }) {
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return <span>at an unknown time</span>;

  return (
    <time dateTime={iso} title={at.toISOString()}>
      on {at.toLocaleDateString()} at {at.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
    </time>
  );
}
