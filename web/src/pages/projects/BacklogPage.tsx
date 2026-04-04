import { useState, useMemo } from 'react';
import { Plus, Search } from 'lucide-react';
import { Button } from '../../components/ui/button';
import { Badge, type BadgeProps } from '../../components/ui/badge';
import { Input } from '../../components/ui/input';
import { cn } from '../../lib/utils';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type ItemType = 'story' | 'bug' | 'task';
type ItemPriority = 'critical' | 'high' | 'medium' | 'low';
type ItemStatus = 'todo' | 'in_progress' | 'in_review' | 'done';

interface BacklogItem {
  id: string;
  key: string;
  title: string;
  type: ItemType;
  priority: ItemPriority;
  status: ItemStatus;
  sprint: string | null;
  points: number | null;
}

// ---------------------------------------------------------------------------
// Mock data
// ---------------------------------------------------------------------------

const MOCK_ITEMS: BacklogItem[] = [
  { id: '1', key: 'PROD-101', title: 'User registration flow', type: 'story', priority: 'high', status: 'done', sprint: 'Sprint 12', points: 8 },
  { id: '2', key: 'PROD-102', title: 'Fix password reset email not sending', type: 'bug', priority: 'critical', status: 'in_progress', sprint: 'Sprint 12', points: 3 },
  { id: '3', key: 'PROD-103', title: 'Add RBAC middleware', type: 'story', priority: 'high', status: 'in_review', sprint: 'Sprint 12', points: 13 },
  { id: '4', key: 'PROD-104', title: 'Set up CI pipeline for frontend', type: 'task', priority: 'medium', status: 'todo', sprint: 'Sprint 12', points: 5 },
  { id: '5', key: 'PROD-105', title: 'Dashboard analytics widgets', type: 'story', priority: 'medium', status: 'todo', sprint: 'Sprint 13', points: 8 },
  { id: '6', key: 'PROD-106', title: 'Broken avatar upload on mobile', type: 'bug', priority: 'high', status: 'todo', sprint: 'Sprint 13', points: 3 },
  { id: '7', key: 'PROD-107', title: 'API rate limiting implementation', type: 'story', priority: 'high', status: 'todo', sprint: 'Sprint 13', points: 8 },
  { id: '8', key: 'PROD-108', title: 'Write integration tests for wiki module', type: 'task', priority: 'medium', status: 'todo', sprint: null, points: 5 },
  { id: '9', key: 'PROD-109', title: 'Evaluate caching strategy for search', type: 'task', priority: 'low', status: 'todo', sprint: null, points: 3 },
  { id: '10', key: 'PROD-110', title: 'Notification preferences page', type: 'story', priority: 'medium', status: 'todo', sprint: null, points: 5 },
];

// ---------------------------------------------------------------------------
// Badge helpers
// ---------------------------------------------------------------------------

const TYPE_VARIANT: Record<ItemType, BadgeProps['variant']> = {
  story: 'default',
  bug: 'danger',
  task: 'secondary',
};

const TYPE_LABEL: Record<ItemType, string> = {
  story: 'Story',
  bug: 'Bug',
  task: 'Task',
};

const PRIORITY_VARIANT: Record<ItemPriority, BadgeProps['variant']> = {
  critical: 'danger',
  high: 'warning',
  medium: 'secondary',
  low: 'outline',
};

const PRIORITY_LABEL: Record<ItemPriority, string> = {
  critical: 'Critical',
  high: 'High',
  medium: 'Medium',
  low: 'Low',
};

const STATUS_LABEL: Record<ItemStatus, string> = {
  todo: 'To Do',
  in_progress: 'In Progress',
  in_review: 'In Review',
  done: 'Done',
};

const STATUS_VARIANT: Record<ItemStatus, BadgeProps['variant']> = {
  todo: 'secondary',
  in_progress: 'warning',
  in_review: 'default',
  done: 'success',
};

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

/** Backlog list page for project items grouped by sprint. */
export function BacklogPage() {
  const [typeFilter, setTypeFilter] = useState<ItemType | 'all'>('all');
  const [search, setSearch] = useState('');

  const filtered = useMemo(() => {
    return MOCK_ITEMS.filter((item) => {
      if (typeFilter !== 'all' && item.type !== typeFilter) return false;
      if (search && !item.title.toLowerCase().includes(search.toLowerCase()) && !item.key.toLowerCase().includes(search.toLowerCase())) return false;
      return true;
    });
  }, [typeFilter, search]);

  // Group by sprint
  const groups = useMemo(() => {
    const map = new Map<string, BacklogItem[]>();
    for (const item of filtered) {
      const group = item.sprint ?? 'Backlog';
      const arr = map.get(group) ?? [];
      arr.push(item);
      map.set(group, arr);
    }
    // Sort: backlog last, sprints in order
    const entries = Array.from(map.entries());
    entries.sort((a, b) => {
      if (a[0] === 'Backlog') return 1;
      if (b[0] === 'Backlog') return -1;
      return a[0].localeCompare(b[0]);
    });
    return entries;
  }, [filtered]);

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <h1 className="text-[var(--text-2xl)] font-bold text-[var(--color-text)]">
          Backlog
        </h1>
        <Button>
          <Plus className="mr-2 h-4 w-4" />
          Create Item
        </Button>
      </div>

      {/* Filter bar */}
      <div className="flex flex-wrap items-center gap-3">
        <div className="relative flex-1 min-w-[200px] max-w-xs">
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-[var(--color-text-muted)]" />
          <Input
            placeholder="Search items..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="pl-9"
          />
        </div>

        <select
          value={typeFilter}
          onChange={(e) => setTypeFilter(e.target.value as ItemType | 'all')}
          className={cn(
            'h-9 rounded-[var(--radius-md)] border border-[var(--color-border)]',
            'bg-[var(--color-surface)] px-3 text-[var(--text-sm)] text-[var(--color-text)]',
            'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)]',
          )}
        >
          <option value="all">All Types</option>
          <option value="story">Story</option>
          <option value="bug">Bug</option>
          <option value="task">Task</option>
        </select>
      </div>

      {/* Grouped table */}
      {groups.map(([groupName, items]) => (
        <div key={groupName} className="space-y-2">
          <h2 className="text-[var(--text-sm)] font-semibold text-[var(--color-text-muted)]">
            {groupName}
            <span className="ml-2 text-[var(--text-xs)] font-normal">
              ({items.length} items
              {items.some((i) => i.points !== null) &&
                ` \u00b7 ${items.reduce((sum, i) => sum + (i.points ?? 0), 0)} pts`}
              )
            </span>
          </h2>

          <div className="overflow-x-auto rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-surface)]">
            <table className="w-full text-left text-[var(--text-sm)]">
              <thead>
                <tr className="border-b border-[var(--color-border)]">
                  <th className="whitespace-nowrap px-4 py-3 font-medium text-[var(--color-text-muted)]">Key</th>
                  <th className="px-4 py-3 font-medium text-[var(--color-text-muted)]">Title</th>
                  <th className="px-4 py-3 font-medium text-[var(--color-text-muted)]">Type</th>
                  <th className="px-4 py-3 font-medium text-[var(--color-text-muted)]">Priority</th>
                  <th className="px-4 py-3 font-medium text-[var(--color-text-muted)]">Status</th>
                  <th className="px-4 py-3 text-right font-medium text-[var(--color-text-muted)]">Points</th>
                </tr>
              </thead>
              <tbody>
                {items.map((item) => (
                  <tr
                    key={item.id}
                    className="border-b border-[var(--color-border)] last:border-b-0 hover:bg-[var(--color-surface-hover)] transition-colors"
                  >
                    <td className="whitespace-nowrap px-4 py-3">
                      <span
                        className="font-medium text-[var(--color-primary)]"
                        style={{ fontFamily: 'var(--font-mono)' }}
                      >
                        {item.key}
                      </span>
                    </td>
                    <td className="px-4 py-3 text-[var(--color-text)]">{item.title}</td>
                    <td className="px-4 py-3">
                      <Badge variant={TYPE_VARIANT[item.type]}>
                        {TYPE_LABEL[item.type]}
                      </Badge>
                    </td>
                    <td className="px-4 py-3">
                      <Badge variant={PRIORITY_VARIANT[item.priority]}>
                        {PRIORITY_LABEL[item.priority]}
                      </Badge>
                    </td>
                    <td className="px-4 py-3">
                      <Badge variant={STATUS_VARIANT[item.status]}>
                        {STATUS_LABEL[item.status]}
                      </Badge>
                    </td>
                    <td className="whitespace-nowrap px-4 py-3 text-right text-[var(--color-text-muted)]">
                      {item.points ?? '\u2014'}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      ))}

      {groups.length === 0 && (
        <div className="flex h-32 items-center justify-center text-[var(--color-text-muted)]">
          No items match the current filters.
        </div>
      )}
    </div>
  );
}
