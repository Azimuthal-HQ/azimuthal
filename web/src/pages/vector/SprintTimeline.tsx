import { useMemo, useState } from 'react';
import { ChevronDown, ChevronRight } from 'lucide-react';
import { ItemKeyChip } from '../../components/ItemKeyChip';
import { PriorityDot, normalizePriority } from '../../components/priority';
import { formatUTCDate, cn } from '../../lib/utils';
import type { RoadmapSprint } from '../../lib/api';

// ---------------------------------------------------------------------------
// Zoom presets
// ---------------------------------------------------------------------------

// Preset windows rather than free pan/zoom — this is a timeline, not a Gantt
// engine (no dependencies, no drag-to-reschedule).
export type ZoomKey = 'quarter' | 'half' | 'year' | 'all';

export const ZOOM_OPTIONS: { value: ZoomKey; label: string }[] = [
  { value: 'quarter', label: '3 months' },
  { value: 'half', label: '6 months' },
  { value: 'year', label: '12 months' },
  { value: 'all', label: 'All' },
];

const ZOOM_MONTHS: Record<Exclude<ZoomKey, 'all'>, number> = {
  quarter: 3,
  half: 6,
  year: 12,
};

const DAY_MS = 86_400_000;

// A sprint with only one of the two dates still deserves a span; it gets a
// nominal two-week extent from the date it does have, which is the default
// iteration length and keeps the bar visible rather than zero-width.
const NOMINAL_SPRINT_MS = 14 * DAY_MS;

interface Span {
  start: number;
  end: number;
}

/** Resolves a sprint's date span, tolerating one-sided and absent dates. */
export function sprintSpan(starts: string | null, ends: string | null): Span | null {
  const s = starts ? Date.parse(starts) : NaN;
  const e = ends ? Date.parse(ends) : NaN;
  const hasS = !Number.isNaN(s);
  const hasE = !Number.isNaN(e);

  if (hasS && hasE) {
    // An inverted range (end before start) is data we cannot draw honestly;
    // normalise it rather than rendering a negative-width bar.
    return s <= e ? { start: s, end: e } : { start: e, end: s };
  }
  if (hasS) return { start: s, end: s + NOMINAL_SPRINT_MS };
  if (hasE) return { start: e - NOMINAL_SPRINT_MS, end: e };
  return null;
}

/**
 * Computes the visible window. A preset spans that many months forward from
 * one month back, so in-flight and upcoming work both show. 'all' fits every
 * sprint span, padded, and falls back to the 6-month preset when there is
 * nothing to fit.
 */
export function computeWindow(zoom: ZoomKey, spans: Span[], now: number): Span {
  if (zoom !== 'all') {
    const start = new Date(now);
    start.setUTCDate(1);
    start.setUTCHours(0, 0, 0, 0);
    start.setUTCMonth(start.getUTCMonth() - 1);
    const end = new Date(start);
    end.setUTCMonth(end.getUTCMonth() + ZOOM_MONTHS[zoom] + 1);
    return { start: start.getTime(), end: end.getTime() };
  }

  if (spans.length === 0) return computeWindow('half', [], now);

  const start = Math.min(...spans.map(s => s.start));
  const end = Math.max(...spans.map(s => s.end));
  // A single-day range would divide by zero downstream; pad both edges.
  const pad = Math.max((end - start) * 0.05, 3 * DAY_MS);
  return { start: start - pad, end: end + pad };
}

/** Month boundaries inside the window, for gridlines and axis labels. */
function monthTicks(win: Span): { at: number; label: string }[] {
  const ticks: { at: number; label: string }[] = [];
  const cursor = new Date(win.start);
  cursor.setUTCDate(1);
  cursor.setUTCHours(0, 0, 0, 0);
  if (cursor.getTime() < win.start) cursor.setUTCMonth(cursor.getUTCMonth() + 1);

  // Bounded: a pathological window cannot spin forever.
  for (let i = 0; i < 64 && cursor.getTime() <= win.end; i++) {
    ticks.push({
      at: cursor.getTime(),
      label: cursor.toLocaleDateString('en-US', { month: 'short', year: '2-digit', timeZone: 'UTC' }),
    });
    cursor.setUTCMonth(cursor.getUTCMonth() + 1);
  }
  return ticks;
}

const pct = (v: number) => `${(v * 100).toFixed(3)}%`;

// Status hue for the sprint bar, reusing the token set.
const STATUS_COLOR: Record<string, string> = {
  active: 'var(--color-primary)',
  planned: 'var(--color-text-muted)',
  completed: 'var(--color-success)',
};

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

interface SprintTimelineProps {
  sprints: RoadmapSprint[];
  zoom: ZoomKey;
  spaceKey?: string;
  /** Injectable clock so the "today" marker is testable. */
  now?: number;
}

/**
 * Sprint timeline: each sprint is a span on a shared date axis, and expanding a
 * row places its items against that span. Sprints that fall entirely outside
 * the visible window are counted, not drawn — a silently shortened list would
 * read as "no such work".
 */
export function SprintTimeline({ sprints, zoom, spaceKey, now = Date.now() }: SprintTimelineProps) {
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  function toggle(id: string) {
    setExpanded(prev => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  const rows = useMemo(
    () =>
      sprints
        .map(rs => ({ rs, span: sprintSpan(rs.sprint.starts_at, rs.sprint.ends_at) }))
        .filter((r): r is { rs: RoadmapSprint; span: Span } => r.span !== null)
        .sort((a, b) => a.span.start - b.span.start),
    [sprints],
  );

  const win = useMemo(() => computeWindow(zoom, rows.map(r => r.span), now), [zoom, rows, now]);
  const winSpan = Math.max(win.end - win.start, 1);
  const ticks = useMemo(() => monthTicks(win), [win]);

  const visible = rows.filter(r => r.span.end >= win.start && r.span.start <= win.end);
  const hiddenCount = rows.length - visible.length;

  const nowOffset = (now - win.start) / winSpan;
  const showNow = nowOffset >= 0 && nowOffset <= 1;

  if (rows.length === 0) {
    return (
      <div className="flex h-48 items-center justify-center rounded-[var(--radius-lg)] border-2 border-dashed border-[var(--color-border)]">
        <p className="text-[var(--color-text-muted)]">
          No sprints with dates. Give a sprint a start or end date to see it here.
        </p>
      </div>
    );
  }

  return (
    <div data-testid="sprint-timeline" className="space-y-2">
      {/* Axis */}
      <div className="flex items-end gap-3">
        <div className="w-44 shrink-0" />
        <div className="relative h-5 flex-1">
          {ticks.map(t => (
            <span
              key={t.at}
              className="absolute top-0 -translate-x-1/2 text-[var(--text-xs)] text-[var(--color-text-muted)]"
              style={{ left: pct((t.at - win.start) / winSpan) }}
            >
              {t.label}
            </span>
          ))}
        </div>
      </div>

      <div className="space-y-1.5">
        {visible.map(({ rs, span }) => {
          const isOpen = expanded.has(rs.sprint.id);
          const left = Math.max((span.start - win.start) / winSpan, 0);
          const right = Math.min((span.end - win.start) / winSpan, 1);
          // Clamped bars keep a hairline width so a sprint whose span barely
          // clips the window edge is still visible.
          const width = Math.max(right - left, 0.004);
          const items = rs.items ?? [];

          return (
            <div key={rs.sprint.id} data-testid="timeline-row">
              <div className="flex items-center gap-3">
                <button
                  type="button"
                  onClick={() => toggle(rs.sprint.id)}
                  aria-expanded={isOpen}
                  aria-label={`Toggle items for ${rs.sprint.name}`}
                  className="flex w-44 shrink-0 items-center gap-1 text-left text-[var(--text-sm)] text-[var(--color-text)] hover:text-[var(--color-primary)]"
                >
                  {isOpen
                    ? <ChevronDown className="h-3.5 w-3.5 shrink-0" />
                    : <ChevronRight className="h-3.5 w-3.5 shrink-0" />}
                  <span className="truncate">{rs.sprint.name}</span>
                  <span className="ml-auto shrink-0 text-[var(--text-xs)] text-[var(--color-text-muted)]">
                    {items.length}
                  </span>
                </button>

                {/* Track */}
                <div className="relative h-7 flex-1 rounded-[var(--radius-lg)] bg-[var(--color-bg)]">
                  {ticks.map(t => (
                    <span
                      key={t.at}
                      className="absolute inset-y-0 w-px bg-[var(--color-border)]"
                      style={{ left: pct((t.at - win.start) / winSpan) }}
                    />
                  ))}
                  {showNow && (
                    <span
                      data-testid="timeline-today"
                      className="absolute inset-y-0 z-10 w-px bg-[var(--color-danger)]"
                      style={{ left: pct(nowOffset) }}
                    />
                  )}
                  <div
                    data-testid="timeline-bar"
                    title={`${rs.sprint.starts_at ? formatUTCDate(rs.sprint.starts_at) : '—'} → ${rs.sprint.ends_at ? formatUTCDate(rs.sprint.ends_at) : '—'}`}
                    className="absolute top-1/2 flex h-5 -translate-y-1/2 items-center overflow-hidden rounded-[5px] px-2"
                    style={{
                      left: pct(left),
                      width: pct(width),
                      backgroundColor: STATUS_COLOR[rs.sprint.status] ?? 'var(--color-text-muted)',
                    }}
                  >
                    <span className="truncate text-[10px] font-medium text-white">
                      {rs.sprint.name}
                    </span>
                  </div>
                </div>
              </div>

              {/* Items, placed against their sprint's span. */}
              {isOpen && (
                <div className="mt-1 space-y-1">
                  {items.length === 0 ? (
                    <div className="flex items-center gap-3">
                      <div className="w-44 shrink-0" />
                      <p className="flex-1 text-[var(--text-xs)] text-[var(--color-text-muted)]">
                        No items in this sprint.
                      </p>
                    </div>
                  ) : (
                    items.map(item => (
                      <div key={item.id} className="flex items-center gap-3">
                        <div className="w-44 shrink-0" />
                        <div className="relative h-6 flex-1">
                          <div
                            className={cn(
                              'absolute top-1/2 flex h-5 min-w-0 -translate-y-1/2 items-center gap-1.5',
                              'overflow-hidden rounded-[5px] border border-[var(--color-border)]',
                              'bg-[var(--color-surface)] px-1.5',
                            )}
                            style={{ left: pct(left), width: pct(width) }}
                          >
                            <PriorityDot priority={normalizePriority(item.priority)} className="h-1.5 w-1.5 shrink-0" />
                            <ItemKeyChip item={item} spaceKey={spaceKey} />
                            <span className="truncate text-[var(--text-xs)] text-[var(--color-text)]">
                              {item.title}
                            </span>
                          </div>
                        </div>
                      </div>
                    ))
                  )}
                </div>
              )}
            </div>
          );
        })}
      </div>

      {hiddenCount > 0 && (
        <p data-testid="timeline-hidden-note" className="text-[var(--text-xs)] text-[var(--color-text-muted)]">
          {hiddenCount} sprint{hiddenCount !== 1 ? 's' : ''} outside this range. Widen the range to see
          {hiddenCount !== 1 ? ' them' : ' it'}.
        </p>
      )}
    </div>
  );
}
