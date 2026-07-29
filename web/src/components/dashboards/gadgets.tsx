import { AlertCircle, BarChart3, Filter, Hash, StickyNote, Clock, User } from 'lucide-react';
import { Markdown } from '../Markdown';
import { ViewResultList } from '../views/ViewResultList';
import { friendlyErrorMessage, useGadgetAggregate, useGadgetResults } from '../../lib/api';
import type { AggregateBucket } from '../../lib/api';
import { GADGET_LIMITS, registerGadget, type GadgetProps } from '../../lib/dashboards/registry';
import { cn } from '../../lib/utils';

/**
 * The v1 gadget set, drawn.
 *
 * Six definitions, each registered through registerGadget. Nothing outside
 * this file knows any gadget key: GadgetTile looks a definition up and renders
 * whatever Body it finds, so adding a seventh gadget is one call here and one
 * const in internal/core/dashboards/registry.go.
 *
 * Every one of these resolves PER VIEWER. The tile is handed a filter document
 * by the dashboard response and posts it to /views/preview or
 * /views/aggregate, which resolve against whoever is asking. Two people
 * opening one shared dashboard legitimately see different rows and different
 * numbers, and none of these components may present that as a failure.
 */

const ALL_MODULES = ['home', 'beacon', 'vector'] as const;

// The bodies below are exported so this file exports only components, which is
// what react-refresh/only-export-components asks of a module that defines any.
// Nothing imports them by name: GadgetTile reaches them through the registry,
// which is the whole point of ADR-0009 decision 5.

/** A gadget's own loading line. Deliberately not the word "loading" alone —
 * a tile that says nothing while it waits reads as an empty tile. */
export function GadgetLoading({ label }: { label: string }) {
  return (
    <div className="flex h-20 items-center justify-center text-[var(--text-xs)] text-[var(--color-text-muted)]">
      {label}
    </div>
  );
}

/**
 * A gadget's own error line.
 *
 * It says "this gadget" rather than borrowing a page-level phrase, because
 * assertNoErrors in the E2E harness hunts "could not be loaded" and a gadget
 * that fails is not a page that failed. The fallback carries no backend
 * string; friendlyErrorMessage passes through only the human-written ones.
 */
export function GadgetError({ error, fallback }: { error: unknown; fallback: string }) {
  return (
    <div
      data-testid="gadget-error"
      className="flex items-start gap-2 text-[var(--text-xs)] text-[var(--color-danger)]"
    >
      <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
      <span>{friendlyErrorMessage(error, fallback)}</span>
    </div>
  );
}

// ── List gadgets ────────────────────────────────────────────────────────────

/**
 * Rows from a resolved query. Used by three gadget kinds — a saved view's
 * results, My work and Recently updated — because they differ only in where
 * the document came from, which is the registry's business and not the
 * renderer's.
 *
 * It renders through ViewResultList rather than a second row component: that
 * one already handles the module chip, the priority translation, the "You"
 * assignee case and the hard-deleted-account case, and a gadget-local copy
 * would drift from all four.
 */
export function ResultsBody({ gadget, orgId, meId }: GadgetProps) {
  const limit = gadget.config.limit ?? GADGET_LIMITS.defaultLimit;
  const q = useGadgetResults(orgId, gadget.query, limit);
  return (
    <ViewResultList
      page={q.data}
      isLoading={q.isLoading}
      error={q.error}
      errorFallback="This gadget's results are unavailable right now."
      emptyTitle="Nothing here"
      emptyDescription="Nothing matches this gadget for you right now."
      meId={meId}
      testId="gadget-results"
    />
  );
}

// ── Stat gadget ─────────────────────────────────────────────────────────────

/**
 * One number.
 *
 * The count is computed in the database over the same fan-out the results path
 * uses. It is never derived from a fetched page: that would stop at the page
 * size and under-report exactly the busy view somebody put a count on.
 *
 * DELIBERATELY NO COMPARISON WINDOW. The prototype's stat tiles carry a delta
 * line ("+1 since Monday"). The filter vocabulary has no time dimension, so a
 * previous-period count is not expressible without extending it — which is a
 * change to a locked decision rather than a render choice. The sub-line
 * carries the view's own name instead, and the deviation is recorded in the
 * phase report.
 */
export function StatBody({ gadget, orgId }: GadgetProps) {
  const q = useGadgetAggregate(orgId, gadget.query);
  if (q.isLoading) return <GadgetLoading label="Counting…" />;
  if (q.error) return <GadgetError error={q.error} fallback="This count is unavailable right now." />;
  return (
    <div data-testid="gadget-stat">
      <p className="text-[25px] font-medium leading-[1.15] tracking-[-.01em] text-[var(--color-text)]">
        {q.data?.total ?? 0}
      </p>
      <p className="mt-1.5 text-[var(--text-xs)] text-[var(--color-text-muted)]">
        {gadget.view_name || 'Matching items'}
      </p>
    </div>
  );
}

// ── Breakdown gadget ────────────────────────────────────────────────────────

function bucketLabel(b: AggregateBucket): string {
  if (b.other) return `Other (${b.other_buckets ?? 0})`;
  // An empty key is a real bucket, not a missing one: unassigned work, or an
  // item with no type. Naming it is the point.
  if (!b.key) return 'Unassigned';
  return b.label || b.key;
}

/**
 * Counts grouped by one field, as horizontal bars.
 *
 * Bars rather than the prototype's donut: a donut needs a legend beside it to
 * be readable at tile size, which costs the same width the bars use for their
 * labels, and a bar chart degrades gracefully as the bucket count grows.
 * Recorded as a deviation in the phase report.
 */
export function BreakdownBody({ gadget, orgId }: GadgetProps) {
  const q = useGadgetAggregate(orgId, gadget.query, gadget.config.group_by);
  if (q.isLoading) return <GadgetLoading label="Grouping…" />;
  if (q.error) {
    return <GadgetError error={q.error} fallback="This breakdown is unavailable right now." />;
  }
  const buckets = q.data?.buckets ?? [];
  if (buckets.length === 0) {
    return (
      <p className="py-4 text-center text-[var(--text-xs)] text-[var(--color-text-muted)]">
        Nothing to group yet.
      </p>
    );
  }
  const max = Math.max(...buckets.map((b) => b.count), 1);
  return (
    <div data-testid="gadget-breakdown" className="flex flex-col gap-2">
      {buckets.map((b) => (
        <div key={b.other ? '__other' : b.key} className="flex items-center gap-2">
          <span
            className="w-[38%] shrink-0 truncate text-[var(--text-xs)] text-[var(--color-text-muted)]"
            title={bucketLabel(b)}
          >
            {bucketLabel(b)}
          </span>
          <span className="h-2 flex-1 overflow-hidden rounded-[var(--radius-full)] bg-[var(--color-surface-hover)]">
            <span
              className={cn(
                'block h-full rounded-[var(--radius-full)]',
                b.other ? 'bg-[var(--color-text-muted)]' : 'bg-[var(--color-primary)]',
              )}
              style={{ width: `${Math.max((b.count / max) * 100, 4)}%` }}
            />
          </span>
          <span
            className="w-8 shrink-0 text-right text-[var(--text-xs)] text-[var(--color-text)]"
            style={{ fontFamily: 'var(--font-mono)' }}
          >
            {b.count}
          </span>
        </div>
      ))}
      {q.data?.truncated && (
        <p className="text-[var(--text-xs)] text-[var(--color-text-muted)]">
          The smallest groups are rolled up into Other.
        </p>
      )}
    </div>
  );
}

// ── Note gadget ─────────────────────────────────────────────────────────────

/**
 * A markdown annotation. No query, no fetch, no editor.
 *
 * It renders through the shared Markdown component, which escapes raw HTML —
 * a note lands on somebody else's screen the moment the dashboard is shared.
 * The Codex TipTap editor is deliberately NOT used here: it wants a space id,
 * a page id and the full page list, and a dashboard note is a string.
 */
export function NoteBody({ gadget }: GadgetProps) {
  return (
    <Markdown
      testId="gadget-note"
      fallback={
        <p className="text-[var(--text-xs)] italic text-[var(--color-text-muted)]">
          This note is empty. Configure it to add some text.
        </p>
      }
    >
      {gadget.config.body ?? ''}
    </Markdown>
  );
}

// ── Registration ────────────────────────────────────────────────────────────

registerGadget({
  key: 'view_results',
  name: 'View results',
  description: "The first few rows of a saved view, resolved against your own access.",
  icon: Filter,
  defaultSpan: 2,
  modules: [...ALL_MODULES],
  requiresSavedView: true,
  configKeys: ['title', 'limit'],
  render: 'list',
  Body: ResultsBody,
});

registerGadget({
  key: 'view_count',
  name: 'View count',
  description: 'How many things a saved view matches for you, counted in the database.',
  icon: Hash,
  defaultSpan: 1,
  modules: [...ALL_MODULES],
  requiresSavedView: true,
  configKeys: ['title'],
  render: 'stat',
  Body: StatBody,
});

registerGadget({
  key: 'breakdown',
  name: 'Breakdown',
  description: "A saved view's results grouped by status, priority, assignee or type.",
  icon: BarChart3,
  defaultSpan: 2,
  modules: [...ALL_MODULES],
  requiresSavedView: true,
  configKeys: ['title', 'group_by'],
  render: 'breakdown',
  Body: BreakdownBody,
});

registerGadget({
  key: 'my_work',
  name: 'My work',
  description: 'Everything assigned to you across Beacon and Vector.',
  icon: User,
  defaultSpan: 2,
  modules: [...ALL_MODULES],
  requiresSavedView: false,
  configKeys: ['title', 'limit'],
  render: 'list',
  Body: ResultsBody,
});

registerGadget({
  key: 'recent_work',
  name: 'Recently updated',
  description: 'The most recently touched work in the spaces you can read.',
  icon: Clock,
  defaultSpan: 2,
  modules: [...ALL_MODULES],
  requiresSavedView: false,
  configKeys: ['title', 'limit'],
  render: 'list',
  Body: ResultsBody,
});

registerGadget({
  key: 'note',
  name: 'Note',
  description: 'A markdown annotation for the people reading this dashboard.',
  icon: StickyNote,
  defaultSpan: 4,
  modules: [...ALL_MODULES],
  requiresSavedView: false,
  configKeys: ['title', 'body'],
  render: 'note',
  Body: NoteBody,
});
