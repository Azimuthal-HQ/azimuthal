import { Fragment, useMemo, useState } from 'react';
import { ChevronDown, ChevronRight, X } from 'lucide-react';
import { Badge } from '../../components/ui/badge';
import { Button } from '../../components/ui/button';
import { Card, CardContent } from '../../components/ui/card';
import { Input } from '../../components/ui/input';
import { cn } from '../../lib/utils';
import { useAuth } from '../../lib/auth';
import {
  friendlyErrorMessage,
  useAuditBatch,
  useAuditLog,
  type AuditEntry,
  type AuditFilter,
} from '../../lib/api';

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Server default page size (backend caps at 100). */
const PAGE_SIZE = 50;

/** Entity kinds the audit writer emits (spec W7). */
const ENTITY_KINDS = [
  'user',
  'invite',
  'grant',
  'team',
  'team_member',
  'space',
  'ticket',
  'page',
  'item',
] as const;

const selectClass = cn(
  'h-9 rounded-[var(--radius-lg)] border border-[var(--color-border)]',
  'bg-[var(--color-input)] px-3 text-[var(--text-sm)] text-[var(--color-text)]',
  'focus-visible:outline-none focus-visible:border-[var(--color-primary)] focus-visible:ring-1 focus-visible:ring-[var(--color-primary)]',
);

const fieldLabelClass = 'text-[var(--text-xs)] font-medium text-[var(--color-text-muted)]';

/** localToRFC3339 converts a datetime-local value to RFC3339 for the API. */
function localToRFC3339(value: string): string | undefined {
  if (!value) return undefined;
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? undefined : d.toISOString();
}

function formatTime(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}

/** payloadSummary flattens payload key/values into one truncatable line. */
function payloadSummary(payload: Record<string, string> | null | undefined): string {
  return Object.entries(payload ?? {})
    .map(([k, v]) => `${k}=${v}`)
    .join(' · ');
}

/** EntityRef renders "kind" plus the first 8 chars of the entity id. */
function EntityRef({ kind, id }: { kind: string; id: string }) {
  return (
    <>
      {kind}{' '}
      <span
        className="font-mono text-[var(--text-xs)] text-[var(--color-text-muted)]"
        title={id}
      >
        {id.slice(0, 8)}
      </span>
    </>
  );
}

// ---------------------------------------------------------------------------
// Batch expansion
// ---------------------------------------------------------------------------

/** BatchEvents lists the constituent events of one batch, oldest first. */
function BatchEvents({ orgId, batchId }: { orgId: string; batchId: string }) {
  const batchQuery = useAuditBatch(orgId, batchId);

  return (
    <div
      className="mb-1 ml-9 border-l-2 border-[var(--color-border)] pl-3"
      data-testid={`audit-batch-events-${batchId}`}
    >
      {batchQuery.isLoading && (
        <p className="py-1.5 text-[var(--text-sm)] text-[var(--color-text-muted)]">Loading batch…</p>
      )}
      {batchQuery.error && (
        <p className="py-1.5 text-[var(--text-sm)] text-[var(--color-danger)]">
          {friendlyErrorMessage(batchQuery.error, 'The batch events could not be loaded.')}
        </p>
      )}
      {(batchQuery.data ?? []).map((ev) => (
        <div
          key={ev.id}
          data-testid="audit-batch-event"
          className="flex items-center gap-3 rounded-[var(--radius-md)] px-2 py-1"
        >
          <span
            className="w-40 shrink-0 truncate text-[var(--text-xs)] text-[var(--color-text-muted)]"
            title={ev.created_at}
          >
            {formatTime(ev.created_at)}
          </span>
          <Badge variant="secondary" className="shrink-0">
            {ev.action}
          </Badge>
          <span className="shrink-0 text-[var(--text-sm)] text-[var(--color-text)]">
            <EntityRef kind={ev.entity_kind} id={ev.entity_id} />
          </span>
          <span
            className="min-w-0 flex-1 truncate text-[var(--text-xs)] text-[var(--color-text-muted)]"
            title={payloadSummary(ev.payload)}
          >
            {payloadSummary(ev.payload)}
          </span>
        </div>
      ))}
    </div>
  );
}

/** PayloadList shows a single entry's payload as a muted definition list. */
function PayloadList({ payload }: { payload: Record<string, string> }) {
  const pairs = Object.entries(payload);
  if (pairs.length === 0) return null;
  return (
    <dl
      data-testid="audit-payload"
      className="mb-1 ml-9 grid grid-cols-[auto_1fr] gap-x-4 gap-y-0.5 border-l-2 border-[var(--color-border)] py-1 pl-3 text-[var(--text-xs)] text-[var(--color-text-muted)]"
    >
      {pairs.map(([k, v]) => (
        <Fragment key={k}>
          <dt className="font-mono">{k}</dt>
          <dd className="break-all">{v}</dd>
        </Fragment>
      ))}
    </dl>
  );
}

// ---------------------------------------------------------------------------
// Rows
// ---------------------------------------------------------------------------

/**
 * AuditRow renders one log line. An entry with batch_size > 1 is the batch
 * representative: one collapsed row for the whole bulk change, expandable
 * to its constituent events (the point of batch_id). Single entries expand
 * to their payload when there is one.
 */
function AuditRow({ orgId, entry }: { orgId: string; entry: AuditEntry }) {
  const [expanded, setExpanded] = useState(false);

  const payload = entry.payload ?? {};
  const batchId = entry.batch_size > 1 ? (entry.batch_id ?? '') : '';
  const isBatch = batchId !== '';
  const expandable = isBatch || Object.keys(payload).length > 0;

  return (
    <div>
      <div
        data-testid={isBatch ? `audit-batch-row-${batchId}` : 'audit-entry-row'}
        onClick={expandable ? () => setExpanded((p) => !p) : undefined}
        className={cn(
          'flex items-center gap-3 rounded-[var(--radius-md)] px-2 py-2',
          expandable && 'cursor-pointer hover:bg-[var(--color-surface-hover)]',
        )}
      >
        {expandable ? (
          // No onClick of its own: the click bubbles to the row handler, so
          // mouse and keyboard (Enter/Space on the button) share one path.
          <button
            type="button"
            aria-label={expanded ? 'Collapse entry' : 'Expand entry'}
            aria-expanded={expanded}
            className="shrink-0 text-[var(--color-text-muted)] hover:text-[var(--color-text)]"
          >
            {expanded ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
          </button>
        ) : (
          <span className="w-4 shrink-0" aria-hidden="true" />
        )}

        <span
          className="w-40 shrink-0 truncate text-[var(--text-xs)] text-[var(--color-text-muted)]"
          title={entry.created_at}
        >
          {formatTime(entry.created_at)}
        </span>

        <Badge variant="secondary" className="shrink-0">
          {entry.action}
        </Badge>

        {isBatch && (
          <span className="shrink-0 rounded-[var(--radius-full)] bg-[var(--color-surface-hover)] px-2 py-0.5 text-[var(--text-xs)] font-medium text-[var(--color-text-muted)]">
            ×{entry.batch_size}
          </span>
        )}

        <span className="min-w-0 flex-1 truncate text-[var(--text-sm)] text-[var(--color-text)]">
          <EntityRef kind={entry.entity_kind} id={entry.entity_id} />
        </span>

        <span className="shrink-0 text-[var(--text-sm)] text-[var(--color-text-muted)]">
          {entry.actor_name || '—'}
        </span>

        {entry.ticket_ref && (
          <Badge variant="outline" title="Ticket reference" className="shrink-0">
            {entry.ticket_ref}
          </Badge>
        )}
      </div>

      {expanded && isBatch && <BatchEvents orgId={orgId} batchId={batchId} />}
      {expanded && !isBatch && <PayloadList payload={payload} />}
    </div>
  );
}

// ---------------------------------------------------------------------------
// AuditLogPage
// ---------------------------------------------------------------------------

/**
 * Org-admin audit log viewer (P2.5 W7): filterable, keyset-paginated,
 * newest first. Bulk changes surface as one expandable batch row.
 * Renders inside AdminLayout, which supplies the page header and tabs.
 */
export function AuditLogPage() {
  const { user } = useAuth();
  const orgId = user?.orgId ?? '';

  // Filter fields. Date inputs keep the raw datetime-local value; the
  // conversion to RFC3339 happens when the filter is assembled.
  const [entityKind, setEntityKind] = useState('');
  const [actionText, setActionText] = useState('');
  const [actorText, setActorText] = useState('');
  const [fromLocal, setFromLocal] = useState('');
  const [toLocal, setToLocal] = useState('');

  // Keyset pagination: the cursor of the visible page plus a stack of the
  // cursors that led here (undefined = first page). Any filter change
  // drops both — the old cursors belong to a different result set.
  const [cursor, setCursor] = useState<string | undefined>(undefined);
  const [prevCursors, setPrevCursors] = useState<(string | undefined)[]>([]);

  function resetPaging() {
    setCursor(undefined);
    setPrevCursors([]);
  }

  function clearFilters() {
    setEntityKind('');
    setActionText('');
    setActorText('');
    setFromLocal('');
    setToLocal('');
    resetPaging();
  }

  const hasActiveFilters =
    entityKind !== '' || actionText !== '' || actorText !== '' || fromLocal !== '' || toLocal !== '';

  const filter = useMemo<AuditFilter>(() => {
    const f: AuditFilter = { limit: PAGE_SIZE };
    if (entityKind) f.entity_kind = entityKind;
    const action = actionText.trim();
    if (action) f.action = action;
    const actor = actorText.trim();
    if (actor) f.actor_id = actor;
    const from = localToRFC3339(fromLocal);
    if (from) f.from = from;
    const to = localToRFC3339(toLocal);
    if (to) f.to = to;
    if (cursor) f.cursor = cursor;
    return f;
  }, [entityKind, actionText, actorText, fromLocal, toLocal, cursor]);

  // Keep the previous page on screen while a filter keystroke or page
  // turn refetches, instead of flashing the loading state.
  const auditQuery = useAuditLog(orgId, filter, { placeholderData: (prev) => prev });

  const entries = auditQuery.data?.entries ?? [];
  const nextCursor = auditQuery.data?.next_cursor;

  function goNext() {
    if (!nextCursor) return;
    setPrevCursors((stack) => [...stack, cursor]);
    setCursor(nextCursor);
  }

  function goPrev() {
    if (prevCursors.length === 0) return;
    const stack = [...prevCursors];
    const prev = stack.pop();
    setPrevCursors(stack);
    setCursor(prev);
  }

  return (
    <div className="space-y-[var(--space-4)]" data-testid="admin-audit-log">
      <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
        Append-only record of administrative actions. Bulk changes appear as one expandable batch.
      </p>

      {/* Filters */}
      <Card>
        <CardContent className="p-[var(--space-4)]">
          <div className="flex flex-wrap items-end gap-3">
            <div className="space-y-1">
              <label htmlFor="audit-filter-entity" className={fieldLabelClass}>
                Entity
              </label>
              <select
                id="audit-filter-entity"
                data-testid="audit-filter-entity"
                value={entityKind}
                onChange={(e) => {
                  setEntityKind(e.target.value);
                  resetPaging();
                }}
                className={cn(selectClass, 'block w-36')}
              >
                <option value="">All kinds</option>
                {ENTITY_KINDS.map((kind) => (
                  <option key={kind} value={kind}>
                    {kind}
                  </option>
                ))}
              </select>
            </div>

            <div className="space-y-1">
              <label htmlFor="audit-filter-action" className={fieldLabelClass}>
                Action
              </label>
              <Input
                id="audit-filter-action"
                data-testid="audit-filter-action"
                placeholder="e.g. grant.created"
                value={actionText}
                onChange={(e) => {
                  setActionText(e.target.value);
                  resetPaging();
                }}
                className="w-44"
              />
            </div>

            <div className="space-y-1">
              <label htmlFor="audit-filter-actor" className={fieldLabelClass}>
                Actor
              </label>
              <Input
                id="audit-filter-actor"
                data-testid="audit-filter-actor"
                placeholder="Actor ID"
                value={actorText}
                onChange={(e) => {
                  setActorText(e.target.value);
                  resetPaging();
                }}
                className="w-44 font-mono text-[var(--text-xs)]"
              />
            </div>

            <div className="space-y-1">
              <label htmlFor="audit-filter-from" className={fieldLabelClass}>
                From
              </label>
              <Input
                id="audit-filter-from"
                data-testid="audit-filter-from"
                type="datetime-local"
                value={fromLocal}
                onChange={(e) => {
                  setFromLocal(e.target.value);
                  resetPaging();
                }}
                className="w-52"
              />
            </div>

            <div className="space-y-1">
              <label htmlFor="audit-filter-to" className={fieldLabelClass}>
                To
              </label>
              <Input
                id="audit-filter-to"
                data-testid="audit-filter-to"
                type="datetime-local"
                value={toLocal}
                onChange={(e) => {
                  setToLocal(e.target.value);
                  resetPaging();
                }}
                className="w-52"
              />
            </div>

            {hasActiveFilters && (
              <Button
                variant="ghost"
                size="sm"
                data-testid="audit-clear-filters"
                onClick={clearFilters}
                className="text-[var(--color-text-muted)] hover:text-[var(--color-text)]"
              >
                <X className="mr-1 h-3.5 w-3.5" />
                Clear filters
              </Button>
            )}
          </div>
        </CardContent>
      </Card>

      {/* Entries */}
      {auditQuery.isLoading ? (
        <div className="py-[var(--space-8)] text-center text-[var(--text-sm)] text-[var(--color-text-muted)]">
          Loading audit log…
        </div>
      ) : auditQuery.error ? (
        <div className="rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[var(--color-danger)]/10 p-4 text-[var(--text-sm)] text-[var(--color-danger)]">
          {friendlyErrorMessage(auditQuery.error, 'The audit log could not be loaded.')}
        </div>
      ) : (
        <Card>
          <CardContent className="p-3">
            <div className="mb-1 flex items-center justify-between border-b border-[var(--color-border)] px-2 pb-2">
              <span className="text-[var(--text-xs)] text-[var(--color-text-muted)]">
                Newest first
              </span>
              <div className="flex items-center gap-2">
                {prevCursors.length > 0 && (
                  <Button
                    variant="outline"
                    size="sm"
                    data-testid="audit-prev-page"
                    disabled={auditQuery.isFetching}
                    onClick={goPrev}
                  >
                    Previous
                  </Button>
                )}
                {nextCursor && (
                  <Button
                    variant="outline"
                    size="sm"
                    data-testid="audit-next-page"
                    disabled={auditQuery.isFetching}
                    onClick={goNext}
                  >
                    Next
                  </Button>
                )}
              </div>
            </div>

            {entries.length === 0 ? (
              <p className="py-[var(--space-6)] text-center text-[var(--text-sm)] text-[var(--color-text-muted)]">
                No audit events match these filters.
              </p>
            ) : (
              entries.map((entry) => <AuditRow key={entry.id} orgId={orgId} entry={entry} />)
            )}
          </CardContent>
        </Card>
      )}
    </div>
  );
}
