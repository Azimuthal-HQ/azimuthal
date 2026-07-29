import { Link } from 'react-router-dom';
import { AlertCircle, ChevronLeft, ChevronRight, Inbox } from 'lucide-react';
import { Badge } from '../ui/badge';
import { Button } from '../ui/button';
import { PriorityPill, normalizePriority } from '../priority';
import { ModuleChip } from '../../shell/ModuleChip';
import { spacePath } from '../../shell/modules';
import { EmptyState } from '../../shell/EmptyState';
import { cn } from '../../lib/utils';
import { friendlyErrorMessage, type ViewResult, type ViewResultPage } from '../../lib/api';
import type { ViewModule } from '../../lib/views/query';

/**
 * The rendered results of a saved view — one list, both modules, in the merged
 * order the API returned (ADR-0009: fan out per module, merge in the API
 * layer). Presentational only: it never fetches. The saved-view page feeds it
 * `useViewResults`, the builder feeds it `usePreviewResults`, and because both
 * pass through this one component the builder shows what the saved view will
 * return rather than an approximation of it.
 *
 * It is a LIST, not a table. Rows cross modules and containers, so a column
 * grid would have to be the union of two different shapes — Vector's type and
 * sprint against Beacon's neither — and every Beacon row would carry blank
 * cells announcing fields that cannot exist for it.
 */

/** The detail route of a result row, by module. */
const MODULE_DETAIL_SUBPATH: Record<ViewModule, string> = {
  beacon: 'tickets',
  vector: 'backlog',
};

function resultPath(r: ViewResult): string {
  return spacePath(r.module, r.space_id, `${MODULE_DETAIL_SUBPATH[r.module]}/${r.id}`);
}

/** "in_progress" -> "In progress". Vector statuses are user-defined free text. */
function humanStatus(status: string): string {
  const spaced = status.replace(/_/g, ' ').trim();
  if (!spaced) return '—';
  return spaced.charAt(0).toUpperCase() + spaced.slice(1);
}

/**
 * The assignee cell.
 *
 * The name arrives on the row: the two fan-outs LEFT JOIN it once, so a page of
 * results costs the same whether it holds one row or fifty. Resolving it here
 * instead — one request per row — is precisely the shape spec §2.5 case 23
 * forbids inside a list handler, and this component must never grow one.
 *
 * `assignee_name` can still be null while `assignee_id` is not, when the id
 * names no user (a hard-deleted account). That is shown as the short id rather
 * than as "Unassigned", because the two mean different things and conflating
 * them would quietly under-report assigned work.
 */
function AssigneeCell({
  assigneeId,
  assigneeName,
  meId,
}: {
  assigneeId: string | null;
  assigneeName: string | null;
  meId?: string;
}) {
  if (!assigneeId) {
    return <span className="text-[var(--color-text-muted)]">Unassigned</span>;
  }
  if (meId && assigneeId === meId) {
    return <span className="text-[var(--color-text)]">You</span>;
  }
  if (assigneeName) {
    return <span className="text-[var(--color-text)]">{assigneeName}</span>;
  }
  return (
    <span
      className="text-[var(--color-text-muted)]"
      style={{ fontFamily: 'var(--font-mono)' }}
      title={assigneeId}
    >
      {assigneeId.slice(0, 8)}
    </span>
  );
}

export function ViewResultRow({ result, meId }: { result: ViewResult; meId?: string }) {
  const path = resultPath(result);
  return (
    <li
      data-testid="view-result-row"
      data-module={result.module}
      className={cn(
        'flex flex-col gap-1.5 border-b border-[var(--color-border)] px-3 py-3 last:border-b-0',
        'transition-colors hover:bg-[var(--color-surface-hover)]',
      )}
    >
      <div className="flex flex-wrap items-center gap-2">
        <ModuleChip module={result.module} />
        <Link
          to={path}
          className="text-[var(--text-xs)] text-[var(--color-primary)] hover:underline"
          style={{ fontFamily: 'var(--font-mono)' }}
        >
          {result.key || result.id.slice(0, 8)}
        </Link>
        <Link to={path} className="text-[13px] text-[var(--color-text)] hover:underline">
          {result.title}
        </Link>
      </div>
      <div className="flex flex-wrap items-center gap-2 text-[var(--text-xs)]">
        {result.kind && <Badge variant="outline">{humanStatus(result.kind)}</Badge>}
        <PriorityPill priority={normalizePriority(result.priority)} />
        <Badge variant="secondary">{humanStatus(result.status)}</Badge>
        <AssigneeCell
          assigneeId={result.assignee_id}
          assigneeName={result.assignee_name}
          meId={meId}
        />
        <span className="text-[var(--color-text-muted)]">
          {result.space_key ? `${result.space_key} · ` : ''}
          {result.space_name}
        </span>
      </div>
    </li>
  );
}

interface ViewResultListProps {
  page?: ViewResultPage;
  isLoading?: boolean;
  error?: unknown;
  /** Shown when `error` is set and the server sent nothing human. */
  errorFallback: string;
  emptyTitle: string;
  emptyDescription: string;
  /** Current user id, so a row assigned to the reader reads "You". */
  meId?: string;
  /** Paging. Omit both to render a single page with no controls. */
  onNext?: () => void;
  onPrev?: () => void;
  canPrev?: boolean;
  testId?: string;
}

export function ViewResultList({
  page,
  isLoading,
  error,
  errorFallback,
  emptyTitle,
  emptyDescription,
  meId,
  onNext,
  onPrev,
  canPrev = false,
  testId = 'view-results',
}: ViewResultListProps) {
  if (error) {
    return (
      <div
        data-testid={`${testId}-error`}
        className="flex items-center gap-3 rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[color-mix(in_srgb,var(--color-danger)_10%,transparent)] p-4"
      >
        <AlertCircle className="h-5 w-5 shrink-0 text-[var(--color-danger)]" />
        <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
          {friendlyErrorMessage(error, errorFallback)}
        </p>
      </div>
    );
  }

  if (isLoading && !page) {
    return (
      <div
        data-testid={`${testId}-loading`}
        className="flex h-32 items-center justify-center text-[var(--text-sm)] text-[var(--color-text-muted)]"
      >
        Resolving this view…
      </div>
    );
  }

  const results = page?.results ?? [];
  if (results.length === 0) {
    return <EmptyState icon={Inbox} title={emptyTitle} description={emptyDescription} />;
  }

  const showPaging = Boolean(onNext || onPrev) && (canPrev || page?.has_more);

  return (
    <div data-testid={testId} className="space-y-3">
      <ul className="overflow-hidden rounded-[var(--radius-xl)] border border-[var(--color-border)] bg-[var(--color-surface)]">
        {results.map((r) => (
          <ViewResultRow key={`${r.module}:${r.id}`} result={r} meId={meId} />
        ))}
      </ul>

      {showPaging && (
        <div className="flex items-center justify-end gap-2">
          <Button
            type="button"
            variant="outline"
            size="sm"
            data-testid={`${testId}-prev`}
            disabled={!canPrev || isLoading}
            onClick={onPrev}
          >
            <ChevronLeft className="mr-1 h-4 w-4" />
            Previous
          </Button>
          <Button
            type="button"
            variant="outline"
            size="sm"
            data-testid={`${testId}-next`}
            disabled={!page?.has_more || isLoading}
            onClick={onNext}
          >
            Next
            <ChevronRight className="ml-1 h-4 w-4" />
          </Button>
        </div>
      )}
    </div>
  );
}
