import { useState, useMemo } from 'react';
import { Link, useParams } from 'react-router-dom';
import { Plus, Search, AlertCircle } from 'lucide-react';
import { Button } from '../../components/ui/button';
import { Badge, type BadgeProps } from '../../components/ui/badge';
import { Input } from '../../components/ui/input';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
  DialogClose,
} from '../../components/ui/dialog';
import { cn } from '../../lib/utils';
import { useProjectItems, useCreateProjectItem, type ProjectItem } from '../../lib/api';

// ---------------------------------------------------------------------------
// Badge helpers
// ---------------------------------------------------------------------------

const PRIORITY_VARIANT: Record<string, BadgeProps['variant']> = {
  critical: 'danger', urgent: 'danger', high: 'warning', medium: 'secondary', low: 'outline',
};
const PRIORITY_LABEL: Record<string, string> = {
  critical: 'Critical', urgent: 'Critical', high: 'High', medium: 'Medium', low: 'Low',
};
// Audit ref: testing-audit.md §3.3 — keys aligned with the values the
// backend actually returns from internal/core/projects/item.go
// (default status is "open", not "todo").
const STATUS_LABEL: Record<string, string> = {
  open: 'Open', todo: 'To Do', in_progress: 'In Progress', in_review: 'In Review', done: 'Done', closed: 'Closed',
};
const STATUS_VARIANT: Record<string, BadgeProps['variant']> = {
  open: 'default', todo: 'secondary', in_progress: 'warning', in_review: 'default', done: 'success', closed: 'secondary',
};

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

/** Backlog list page for project items. */
export function BacklogPage() {
  const { spaceId = '' } = useParams<{ spaceId: string }>();
  const { data: items, isLoading, error } = useProjectItems(spaceId);
  const createMutation = useCreateProjectItem(spaceId);

  const [search, setSearch] = useState('');

  // Modal state
  const [dialogOpen, setDialogOpen] = useState(false);
  const [formTitle, setFormTitle] = useState('');
  const [formPriority, setFormPriority] = useState('medium');
  const [formKind, setFormKind] = useState('task');
  const [formDescription, setFormDescription] = useState('');

  function resetForm() {
    setFormTitle('');
    setFormPriority('medium');
    setFormKind('task');
    setFormDescription('');
  }

  async function handleCreate() {
    const title = formTitle.trim();
    if (!title) return;

    const body = {
      title,
      description: formDescription.trim() || '',
      kind: formKind,
      priority: formPriority || 'medium',
    };
    console.log('[BacklogPage] Creating item:', JSON.stringify(body));

    try {
      await createMutation.mutateAsync(body);
      setDialogOpen(false);
      resetForm();
    } catch (err) {
      console.error('[BacklogPage] Create item error:', err);
    }
  }

  const filtered = useMemo(() => {
    if (!items) return [];
    return items.filter((item) => {
      if (search && !item.title.toLowerCase().includes(search.toLowerCase()) && !item.id.toLowerCase().includes(search.toLowerCase())) return false;
      return true;
    });
  }, [items, search]);

  // Group by sprint
  const groups = useMemo(() => {
    const map = new Map<string, ProjectItem[]>();
    for (const item of filtered) {
      const group = item.sprint_id ?? 'Backlog';
      const arr = map.get(group) ?? [];
      arr.push(item);
      map.set(group, arr);
    }
    const entries = Array.from(map.entries());
    entries.sort((a, b) => {
      if (a[0] === 'Backlog') return 1;
      if (b[0] === 'Backlog') return -1;
      return a[0].localeCompare(b[0]);
    });
    // Sort items within each group by sort_order, treating falsy values as lowest priority
    for (const [, groupItems] of entries) {
      groupItems.sort((a, b) => {
        if (!a.sort_order && !b.sort_order) return 0;
        if (!a.sort_order) return 1;
        if (!b.sort_order) return -1;
        return a.sort_order - b.sort_order;
      });
    }
    return entries;
  }, [filtered]);

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <h1 className="text-[var(--text-2xl)] font-bold text-[var(--color-text)]">
          Backlog
        </h1>
        <Button onClick={() => setDialogOpen(true)}>
          <Plus className="mr-2 h-4 w-4" />
          Create Item
        </Button>
      </div>

      {/* Filter bar */}
      <div className="flex flex-wrap items-center gap-3">
        <div className="relative flex-1 min-w-[200px] max-w-xs">
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-[var(--color-text-muted)]" />
          <Input placeholder="Search items..." value={search} onChange={(e) => setSearch(e.target.value)} className="pl-9" />
        </div>
      </div>

      {/* Loading */}
      {isLoading && (
        <div className="flex h-32 items-center justify-center text-[var(--color-text-muted)]">
          Loading items...
        </div>
      )}

      {/* Error */}
      {error && (
        <div className="flex items-center gap-3 rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[var(--color-danger)]/10 p-4">
          <AlertCircle className="h-5 w-5 text-[var(--color-danger)]" />
          <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
            Failed to load items: {error.message}
          </p>
        </div>
      )}

      {/* Grouped table */}
      {items && groups.map(([groupName, groupItems]) => (
        <div key={groupName} className="space-y-2">
          <h2 className="text-[var(--text-sm)] font-semibold text-[var(--color-text-muted)]">
            {groupName}
            <span className="ml-2 text-[var(--text-xs)] font-normal">
              ({groupItems.length} items)
            </span>
          </h2>

          <div className="overflow-x-auto rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-surface)]">
            <table className="w-full text-left text-[var(--text-sm)]">
              <thead>
                <tr className="border-b border-[var(--color-border)]">
                  <th className="whitespace-nowrap px-4 py-3 font-medium text-[var(--color-text-muted)]">ID</th>
                  <th className="px-4 py-3 font-medium text-[var(--color-text-muted)]">Title</th>
                  <th className="px-4 py-3 font-medium text-[var(--color-text-muted)]">Priority</th>
                  <th className="px-4 py-3 font-medium text-[var(--color-text-muted)]">Status</th>
                </tr>
              </thead>
              <tbody>
                {groupItems.map((item) => {
                  const itemPath = `/spaces/${spaceId}/backlog/${item.id}`;
                  return (
                    <tr key={item.id} className="border-b border-[var(--color-border)] last:border-b-0 hover:bg-[var(--color-surface-hover)] transition-colors">
                      <td className="whitespace-nowrap px-4 py-3">
                        <Link to={itemPath} className="font-medium text-[var(--color-primary)] hover:underline" style={{ fontFamily: 'var(--font-mono)' }}>
                          {item.number ? `PROJ-${item.number}` : (item.id ?? '').slice(0, 8)}
                        </Link>
                      </td>
                      <td className="px-4 py-3 text-[var(--color-text)]">
                        <Link to={itemPath} className="hover:underline">{item.title}</Link>
                      </td>
                      <td className="px-4 py-3"><Badge variant={PRIORITY_VARIANT[String(item.priority).toLowerCase()] ?? 'secondary'}>{PRIORITY_LABEL[String(item.priority).toLowerCase()] ?? 'Medium'}</Badge></td>
                      <td className="px-4 py-3"><Badge variant={STATUS_VARIANT[item.status] ?? 'secondary'}>{STATUS_LABEL[item.status] ?? item.status}</Badge></td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </div>
      ))}

      {items && groups.length === 0 && (
        <div className="flex h-32 items-center justify-center text-[var(--color-text-muted)]">
          No items yet. Create one to get started.
        </div>
      )}

      {/* Create Item dialog */}
      <Dialog open={dialogOpen} onOpenChange={(open) => { setDialogOpen(open); if (!open) resetForm(); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Create Item</DialogTitle>
            <DialogDescription>
              Add a new item to the project backlog.
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-4 py-2">
            <div className="space-y-2">
              <label htmlFor="item-title" className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">Title</label>
              <Input id="item-title" placeholder="e.g. Implement user registration flow" value={formTitle} onChange={(e) => setFormTitle(e.target.value)} autoFocus />
            </div>

            <div className="flex gap-4">
              <div className="space-y-2 flex-1">
                <label htmlFor="item-kind" className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">Type</label>
                <select
                  id="item-kind"
                  value={formKind}
                  onChange={(e) => setFormKind(e.target.value)}
                  className={cn(
                    'flex h-9 w-full rounded-[var(--radius-md)] border border-[var(--color-border)]',
                    'bg-[var(--color-surface)] px-3 text-[var(--text-sm)] text-[var(--color-text)]',
                    'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)]',
                  )}
                >
                  <option value="task">Task</option>
                  <option value="story">Story</option>
                  <option value="bug">Bug</option>
                  <option value="epic">Epic</option>
                </select>
              </div>

              <div className="space-y-2 flex-1">
                <label className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">Priority</label>
                <div className="grid grid-cols-4 gap-2">
                  {(['critical', 'high', 'medium', 'low'] as const).map((p) => (
                    <button
                      key={p}
                      type="button"
                      onClick={() => setFormPriority(p)}
                      className={cn(
                        'rounded-[var(--radius-md)] border px-2 py-1.5 text-[var(--text-sm)] capitalize transition-colors',
                        formPriority === p
                          ? 'border-[var(--color-primary)] bg-[var(--color-primary-muted)] text-[var(--color-primary)] font-medium'
                          : 'border-[var(--color-border)] hover:border-[var(--color-text-muted)]',
                      )}
                    >
                      {p.charAt(0).toUpperCase() + p.slice(1)}
                    </button>
                  ))}
                </div>
              </div>
            </div>

            <div className="space-y-2">
              <label htmlFor="item-desc" className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                Description <span className="text-[var(--color-text-muted)] font-normal">(optional)</span>
              </label>
              <textarea
                id="item-desc"
                placeholder="What needs to be built and why"
                value={formDescription}
                onChange={(e) => setFormDescription(e.target.value)}
                rows={3}
                className={cn(
                  'flex w-full rounded-[var(--radius-md)] border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-[var(--text-sm)] text-[var(--color-text)] shadow-[var(--shadow-sm)] transition-colors placeholder:text-[var(--color-text-muted)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)] focus-visible:ring-offset-1 resize-y',
                )}
              />
            </div>

            {createMutation.error && (
              <p className="text-[var(--text-sm)] text-[var(--color-danger)]">{createMutation.error.message}</p>
            )}
          </div>

          <DialogFooter>
            <DialogClose asChild>
              <Button variant="outline" type="button">Cancel</Button>
            </DialogClose>
            <Button onClick={handleCreate} disabled={createMutation.isPending || !formTitle.trim()}>
              {createMutation.isPending ? 'Creating...' : 'Create Item'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
