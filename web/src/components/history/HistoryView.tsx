import type { HistoryEvent } from '../../lib/api';
import { cn } from '../../lib/utils';

/**
 * The entity History feed (D5): the filtered audit trail — status changes,
 * assignment, field edits, creation — for one ticket or item, rendered as a
 * chronological list. A SIBLING of the comment thread (Activity), never
 * interleaved with it: the two are separate surfaces in the JSM model the
 * product follows, so this component only ever renders history events.
 *
 * Shared by the ticket and item detail pages — there is one implementation, not
 * one per module. The only thing that differs between them is the event
 * vocabulary the server returns for each entity kind.
 */

// Human phrasing for each action in the History vocabulary. A status change
// renders its old -> new separately (below); everything else is a plain verb.
const ACTION_VERB: Record<string, string> = {
  'ticket.created': 'created this ticket',
  'ticket.updated': 'edited fields',
  'ticket.status_changed': 'changed status',
  'ticket.assigned': 'assigned this ticket',
  'ticket.unassigned': 'unassigned this ticket',
  'item.created': 'created this item',
  'item.updated': 'edited fields',
  'item.status_changed': 'changed status',
};

/** Turn a raw status/state name ("in_progress") into a label ("In progress"). */
function humanizeStatus(s: string): string {
  const spaced = s.replace(/[_-]+/g, ' ').trim();
  return spaced ? spaced.charAt(0).toUpperCase() + spaced.slice(1) : s;
}

/** Fallback phrasing for an action not in ACTION_VERB. */
function describeAction(action: string): string {
  if (ACTION_VERB[action]) return ACTION_VERB[action];
  const tail = action.includes('.') ? action.slice(action.indexOf('.') + 1) : action;
  return tail.replace(/[_.]+/g, ' ');
}

function isStatusChange(action: string): boolean {
  return action === 'ticket.status_changed' || action === 'item.status_changed';
}

function InitialCircle({ name }: { name?: string | null }) {
  return (
    <span
      aria-hidden="true"
      className={cn(
        'flex h-8 w-8 shrink-0 items-center justify-center rounded-full',
        'bg-[var(--color-primary-muted)] text-[var(--text-sm)] font-medium text-[var(--color-primary)]',
      )}
    >
      {name?.[0]?.toUpperCase() ?? '?'}
    </span>
  );
}

function StatusChip({ label }: { label: string }) {
  return (
    <span className="rounded-[var(--radius-sm)] bg-[var(--color-input)] px-1.5 py-0.5 text-[var(--text-xs)] font-medium text-[var(--color-text)]">
      {label}
    </span>
  );
}

/** old -> new for a status change, drawn from the event payload. */
function StatusTransition({ payload }: { payload: Record<string, string> }) {
  const from = payload.from;
  const to = payload.to;
  if (!to) return null;
  return (
    <span className="inline-flex items-center gap-1.5" data-testid="history-status-transition">
      {from && (
        <>
          <StatusChip label={humanizeStatus(from)} />
          <span aria-hidden="true" className="text-[var(--color-text-muted)]">
            &rarr;
          </span>
        </>
      )}
      <StatusChip label={humanizeStatus(to)} />
    </span>
  );
}

function HistoryRow({ event }: { event: HistoryEvent }) {
  const actor = event.actor_name || 'Unknown';
  return (
    <li className="flex gap-3" data-testid="history-row" data-action={event.action}>
      <InitialCircle name={event.actor_name} />
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <span className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">{actor}</span>
          <span className="text-[var(--text-sm)] text-[var(--color-text-muted)]">{describeAction(event.action)}</span>
          {isStatusChange(event.action) && <StatusTransition payload={event.payload} />}
          <span className="text-[var(--text-xs)] text-[var(--color-text-muted)]">
            {new Date(event.created_at).toLocaleString()}
          </span>
        </div>
      </div>
    </li>
  );
}

export interface HistoryViewProps {
  events: HistoryEvent[] | undefined;
  isLoading?: boolean;
  error?: unknown;
}

export function HistoryView({ events, isLoading, error }: HistoryViewProps) {
  if (isLoading) {
    return (
      <p className="text-[var(--text-sm)] italic text-[var(--color-text-muted)]" data-testid="history-loading">
        Loading history&hellip;
      </p>
    );
  }
  if (error) {
    return (
      <p className="text-[var(--text-sm)] italic text-[var(--color-text-muted)]" data-testid="history-error">
        History could not be loaded.
      </p>
    );
  }
  if (!events || events.length === 0) {
    return (
      <p className="text-[var(--text-sm)] italic text-[var(--color-text-muted)]" data-testid="history-empty">
        No history yet.
      </p>
    );
  }
  return (
    <ul className="space-y-4" data-testid="history-list">
      {events.map((e) => (
        <HistoryRow key={e.id} event={e} />
      ))}
    </ul>
  );
}
