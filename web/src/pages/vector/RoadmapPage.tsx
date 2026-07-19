import { useState } from 'react';
import { useParams } from 'react-router-dom';
import { AlertTriangle } from 'lucide-react';
import { cn, formatUTCDate } from '../../lib/utils';
import { useRoadmap, useRoadmapSprints, type RoadmapItem, type RoadmapSprint } from '../../lib/api';

type ViewMode = 'items' | 'sprints';

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

const PRIORITY_DOT: Record<string, string> = {
  urgent:   'bg-[var(--color-danger)]',
  high:     'bg-[var(--color-warning)]',
  medium:   'bg-[var(--color-info)]',
  low:      'bg-[var(--color-text-muted)]',
};

function ItemRow({ ri }: { ri: RoadmapItem }) {
  return (
    <div className="flex items-center gap-3 rounded-[var(--radius-md)] border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2">
      <span className={cn('h-2 w-2 rounded-full shrink-0', PRIORITY_DOT[ri.item.priority] ?? 'bg-[var(--color-text-muted)]')} />
      <span className="flex-1 text-[var(--text-sm)] text-[var(--color-text)] truncate">{ri.item.title}</span>
      <span className="text-[var(--text-xs)] text-[var(--color-text-muted)] shrink-0">{ri.item.status}</span>
      {ri.overdue && (
        <span className="flex items-center gap-1 rounded-full bg-[var(--color-danger)]/15 px-2 py-0.5 text-[var(--text-xs)] text-[var(--color-danger)] font-medium">
          <AlertTriangle className="h-3 w-3" />Overdue
        </span>
      )}
      <span className="text-[var(--text-xs)] text-[var(--color-text-muted)] shrink-0">{ri.due_at.slice(0, 10)}</span>
    </div>
  );
}

function SprintCard({ rs }: { rs: RoadmapSprint }) {
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
              <span className={cn('h-1.5 w-1.5 rounded-full shrink-0', PRIORITY_DOT[item.priority] ?? 'bg-[var(--color-text-muted)]')} />
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

  const { data: roadmapItems = [], isLoading: loadingItems } = useRoadmap(spaceId);
  const { data: sprintRoadmap = [], isLoading: loadingSprints } = useRoadmapSprints(spaceId, { enabled: view === 'sprints' });

  const isLoading = view === 'items' ? loadingItems : loadingSprints;

  const grouped = groupByMonth(roadmapItems);
  const months = Object.keys(grouped).sort();

  const overdueItems = roadmapItems.filter(ri => ri.overdue);

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-[var(--text-2xl)] font-bold text-[var(--color-text)]">Roadmap</h1>
          {overdueItems.length > 0 && (
            <p className="mt-0.5 flex items-center gap-1 text-[var(--text-sm)] text-[var(--color-danger)]">
              <AlertTriangle className="h-4 w-4" />
              {overdueItems.length} overdue item{overdueItems.length !== 1 ? 's' : ''}
            </p>
          )}
        </div>
        <div className="flex rounded-[var(--radius-md)] border border-[var(--color-border)] overflow-hidden">
          {(['items', 'sprints'] as const).map(v => (
            <button
              key={v}
              type="button"
              onClick={() => setView(v)}
              className={cn(
                'px-3 py-1.5 text-[var(--text-sm)] capitalize transition-colors',
                view === v
                  ? 'bg-[var(--color-primary)] text-white font-medium'
                  : 'bg-[var(--color-surface)] text-[var(--color-text-muted)] hover:bg-[var(--color-surface-hover)]',
              )}
            >
              {v === 'items' ? 'All Items' : 'Sprint Timeline'}
            </button>
          ))}
        </div>
      </div>

      {isLoading ? (
        <div className="flex h-48 items-center justify-center text-[var(--color-text-muted)]">Loading…</div>
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
                  {grouped[month].map(ri => <ItemRow key={ri.item.id} ri={ri} />)}
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
          <div className="space-y-3">
            {sprintRoadmap.map(rs => <SprintCard key={rs.sprint.id} rs={rs} />)}
          </div>
        )
      )}
    </div>
  );
}
