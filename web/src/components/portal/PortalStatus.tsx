import { cn } from '../../lib/utils';

/**
 * A request's status, rendered exactly as the server sent it.
 *
 * THERE IS NO MAP IN THIS FILE, AND THERE MUST NEVER BE ONE. The translation
 * from internal vocabulary to requester-facing language already happened
 * server-side in `requesterStatus` (`internal/core/api/portal/handler.go`), and
 * it is not a lookup the client could repeat: `tickets.status` has no database
 * CHECK, the workflow transition route writes arbitrary state names into it,
 * and anything unrecognised deliberately falls through to "In progress". A Go
 * test pins that fall-through with "Awaiting legal sign-off" → "In progress".
 *
 * A client-side `status → label` map would reconstruct precisely what the
 * server stripped: it would have to enumerate the internal states to key on
 * them, and any value it failed to recognise would either render raw — leaking
 * an internal workflow state name to an external customer — or render as
 * "Unknown", which tells the customer their request is in a state nobody can
 * name. Passing the string through has neither failure mode.
 *
 * The styling is uniform for the same reason. A colour keyed on the status
 * string is a map wearing different clothes, and it breaks in the same place:
 * on the fall-through value it has never seen.
 */
export function PortalStatus({ status, className }: { status: string; className?: string }) {
  const label = status?.trim();
  if (!label) return null;
  return (
    <span
      data-testid="portal-status"
      className={cn(
        'inline-flex shrink-0 items-center rounded-[var(--radius-full)]',
        'border border-[var(--color-border)] bg-[var(--color-surface-hover)]',
        'px-[var(--space-2)] py-[var(--space-1)]',
        'text-[var(--text-xs)] font-medium text-[var(--color-text)]',
        className,
      )}
    >
      {label}
    </span>
  );
}
