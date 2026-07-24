import { useMemo, useState } from 'react';
import { useParams } from 'react-router-dom';
import { AlertTriangle } from 'lucide-react';
import { SegmentedControl } from '../../components/ui/segmented';
import { PriorityDot, normalizePriority } from '../../components/priority';
import { formatUTCDate } from '../../lib/utils';
import { ItemKeyChip } from '../../components/ItemKeyChip';
import { SprintTimeline, ZOOM_OPTIONS, type ZoomKey } from './SprintTimeline';
import {
  useRoadmap,
  useRoadmapSprints,
  useSprints,
  useSpace,
  friendlyErrorMessage,
  type RoadmapItem,
  type RoadmapSprint,
  type Sprint,
} from '../../lib/api';

type ViewMode = 'items' | 'sprints';

const DAY_MS = 86_400_000;

function toYMD(ms: number): string {
  return new Date(ms).toISOString().slice(0, 10);
}

/**
 * The "All Items" roadmap endpoint requires a from/to window. We derive a
 * default that (a) spans every open/active sprint and (b) floors ~90 days
 * into the past so client-derived overdue items (past due dates) still
 * surface, with a ~180-day forward ceiling for near-term planned work.
 * The backend treats `to` as inclusive at midnight UTC (due <= to), so the
 * ceiling is pushed one day out to include items due any time on that day.
 */
function computeRoadmapWindow(sprints: Sprint[]): { from: string; to: string } {
  const now = Date.now();
  let earliest = now - 90 * DAY_MS;
  let latest = now + 180 * DAY_MS;
  for (const s of sprints) {
    if (s.status === 'completed') continue;
    if (s.starts_at) {
      const t = Date.parse(s.starts_at);
      if (!Number.isNaN(t)) earliest = Math.min(earliest, t);
    }
    if (s.ends_at) {
      const t = Date.parse(s.ends_at);
      if (!Number.isNaN(t)) latest = Math.max(latest, t);
    }
  }
  return { from: toYMD(earliest), to: toYMD(latest + DAY_MS) };
}

function groupByMonth(items: RoadmapItem[]): Record<string, RoadmapItem[]> {
  return items.reduce((acc, ri) => {
    const month = ri.due_at.slice(0, 7); // YYYY-MM
    if (!acc[month]) acc[month] = [];
    acc[month].push(ri);
    return acc;
  }, {} as Record<string, RoadmapItem[]>);
}

function formatMonth(ym: string) {
  const [y, m] = ym.split('-');
  return new Date(Number(y), Number(m) - 1).toLocaleDateString('en-US', { month: 'long', year: 'numeric' });
}

function ItemRow({ ri, spaceKey }: { ri: RoadmapItem; spaceKey?: string }) {
  return (
    <div className="flex items-center gap-3 rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2">
      <PriorityDot priority={normalizePriority(ri.item.priority)} />
      <ItemKeyChip item={ri.item} spaceKey={spaceKey} />
      <span className="flex-1 truncate text-[var(--text-sm)] text-[var(--color-text)]">{ri.item.title}</span>
      <span className="shrink-0 text-[var(--text-xs)] text-[var(--color-text-muted)]">{ri.item.status}</span>
      {ri.overdue && (
        <span className="flex items-center gap-1 rounded-full bg-[color-mix(in_srgb,var(--color-danger)_15%,transparent)] px-2 py-0.5 text-[var(--text-xs)] font-medium text-[var(--color-danger)]">
          <AlertTriangle className="h-3 w-3" />Overdue
        </span>
      )}
      <span className="shrink-0 text-[var(--text-xs)] text-[var(--color-text-muted)]">{ri.due_at.slice(0, 10)}</span>
    </div>
  );
}

function SprintCard({ rs, spaceKey }: { rs: RoadmapSprint; spaceKey?: string }) {
  return (
    <div className="rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-surface)] p-4">
      <div className="flex items-center justify-between mb-3">
        <div>
          <h3 className="font-semibold text-[var(--color-text)]">{rs.sprint.name}</h3>
          {(rs.sprint.starts_at || rs.sprint.ends_at) && (
            <p className="text-[var(--text-xs)] text-[var(--color-text-muted)]">
              {rs.sprint.starts_at ? formatUTCDate(rs.sprint.starts_at) : '—'} → {rs.sprint.ends_at ? formatUTCDate(rs.sprint.ends_at) : '—'}
            </p>
          )}
        </div>
        <span className="rounded-full bg-[var(--color-surface-hover)] px-2 py-0.5 text-[var(--text-xs)] text-[var(--color-text-muted)]">
          {rs.items?.length ?? 0} items
        </span>
      </div>
      {(rs.items ?? []).length === 0 ? (
        <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">No items in this sprint.</p>
      ) : (
        <div className="space-y-1.5">
          {(rs.items ?? []).map(item => (
            <div key={item.id} className="flex items-center gap-2 text-[var(--text-sm)] text-[var(--color-text)]">
              <PriorityDot priority={normalizePriority(item.priority)} className="h-1.5 w-1.5" />
              <ItemKeyChip item={item} spaceKey={spaceKey} />
              <span className="truncate">{item.title}</span>
              <span className="ml-auto text-[var(--text-xs)] text-[var(--color-text-muted)] shrink-0">{item.status}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

export function RoadmapPage() {
  const { spaceId = '' } = useParams<{ spaceId: string }>();
  const [view, setView] = useState<ViewMode>('items');
  const [zoom, setZoom] = useState<ZoomKey>('half');

  const { data: space } = useSpace(spaceId);
  const { data: sprints = [] } = useSprints(spaceId);
  const { from, to } = useMemo(() => computeRoadmapWindow(sprints), [sprints]);

  const {
    data: roadmapItems = [],
    isLoading: loadingItems,
    isError: itemsError,
    error: itemsErr,
  } = useRoadmap(spaceId, from, to);
  const {
    data: sprintRoadmap = [],
    isLoading: loadingSprints,
    isError: sprintsError,
    error: sprintsErr,
  } = useRoadmapSprints(spaceId, { enabled: view === 'sprints' });

  const isLoading = view === 'items' ? loadingItems : loadingSprints;
  const isError = view === 'items' ? itemsError : sprintsError;
  const error = view === 'items' ? itemsErr : sprintsErr;

  const grouped = groupByMonth(roadmapItems);
  const months = Object.keys(grouped).sort();

  const overdueItems = roadmapItems.filter(ri => ri.overdue);

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-[var(--text-lg)] font-semibold tracking-[-.01em] text-[var(--color-text)]">Roadmap</h1>
          {overdueItems.length > 0 && (
            <p className="mt-0.5 flex items-center gap-1 text-[var(--text-sm)] text-[var(--color-danger)]">
              <AlertTriangle className="h-4 w-4" />
              {overdueItems.length} overdue item{overdueItems.length !== 1 ? 's' : ''}
            </p>
          )}
        </div>
        <div className="flex items-center gap-2">
          {/* Zoom applies to the timeline only; hidden in the item list so it
              never reads as a filter on data it does not affect. */}
          {view === 'sprints' && (
            <SegmentedControl
              options={ZOOM_OPTIONS}
              value={zoom}
              onChange={setZoom}
              aria-label="Timeline range"
              fullWidth={false}
            />
          )}
          <SegmentedControl
            options={[
              { value: 'items', label: 'All Items' },
              { value: 'sprints', label: 'Sprint Timeline' },
            ]}
            value={view}
            onChange={setView}
            aria-label="Roadmap view"
            fullWidth={false}
          />
        </div>
      </div>

      {isLoading ? (
        <div className="flex h-48 items-center justify-center text-[var(--color-text-muted)]">Loading…</div>
      ) : isError ? (
        <div
          data-testid="roadmap-error"
          className="flex h-48 items-center justify-center rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[color-mix(in_srgb,var(--color-danger)_10%,transparent)] px-4 text-center"
        >
          <p className="flex items-center gap-2 text-[var(--text-sm)] text-[var(--color-danger)]">
            <AlertTriangle className="h-4 w-4 shrink-0" />
            {friendlyErrorMessage(error, 'The roadmap is unavailable right now. Try again.')}
          </p>
        </div>
      ) : view === 'items' ? (
        months.length === 0 ? (
          <div className="flex h-48 items-center justify-center rounded-[var(--radius-lg)] border-2 border-dashed border-[var(--color-border)]">
            <p className="text-[var(--color-text-muted)]">No items with due dates. Add a due date to an item to see it here.</p>
          </div>
        ) : (
          <div className="space-y-6">
            {months.map(month => (
              <div key={month}>
                <h2 className="mb-2 text-[var(--text-sm)] font-semibold text-[var(--color-text-muted)] uppercase tracking-wide">
                  {formatMonth(month)}
                </h2>
                <div className="space-y-1.5">
                  {grouped[month].map(ri => <ItemRow key={ri.item.id} ri={ri} spaceKey={space?.key} />)}
                </div>
              </div>
            ))}
          </div>
        )
      ) : (
        sprintRoadmap.length === 0 ? (
          <div className="flex h-48 items-center justify-center rounded-[var(--radius-lg)] border-2 border-dashed border-[var(--color-border)]">
            <p className="text-[var(--color-text-muted)]">No sprints with items found.</p>
          </div>
        ) : (
          <div className="space-y-6">
            <SprintTimeline sprints={sprintRoadmap} zoom={zoom} spaceKey={space?.key} />
            {/* The per-sprint detail below the chart: the timeline answers
                "when", these answer "what". */}
            <div className="space-y-3">
              {sprintRoadmap.map(rs => <SprintCard key={rs.sprint.id} rs={rs} spaceKey={space?.key} />)}
            </div>
          </div>
        )
      )}
    </div>
  );
}
