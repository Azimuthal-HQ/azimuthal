import { useEffect, useState, useMemo, useRef } from 'react';
import { Link, useParams, useSearchParams } from 'react-router-dom';
import { Plus, Search, AlertCircle, GripVertical } from 'lucide-react';
import { Button } from '../../components/ui/button';
import { Badge, type BadgeProps } from '../../components/ui/badge';
import { Input } from '../../components/ui/input';
import { Field, FieldLabel } from '../../components/ui/field';
import { SegmentedControl } from '../../components/ui/segmented';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
  DialogClose,
} from '../../components/ui/dialog';
import {
  PRIORITY_SEGMENT_OPTIONS,
  PRIORITY_TO_API,
  PriorityPill,
  normalizePriority,
  type PriorityKey,
} from '../../components/priority';
import { cn } from '../../lib/utils';
import {
  useProjectItems,
  useCreateProjectItem,
  useRankItem,
  useSpace,
  friendlyErrorMessage,
  type ProjectItem,
} from '../../lib/api';

// ---------------------------------------------------------------------------
// Status vocabulary
// ---------------------------------------------------------------------------

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
  const { data: space } = useSpace(spaceId);
  const { data: items, isLoading, error } = useProjectItems(spaceId);
  const createMutation = useCreateProjectItem(spaceId);
  const rankMutation = useRankItem(spaceId);

  const [search, setSearch] = useState('');

  // Drag-to-reorder state
  const dragId = useRef<string | null>(null);
  const [dragOverId, setDragOverId] = useState<string | null>(null);

  function handleDragStart(id: string) {
    dragId.current = id;
  }

  function handleDrop(groupItems: ProjectItem[], targetId: string) {
    const srcId = dragId.current;
    if (!srcId || srcId === targetId) { dragId.current = null; setDragOverId(null); return; }
    const targetIdx = groupItems.findIndex(i => i.id === targetId);
    const after = targetIdx > 0 ? groupItems[targetIdx - 1] : undefined;
    rankMutation.mutate({ itemId: srcId, before_id: targetId, after_id: after?.id });
    dragId.current = null;
    setDragOverId(null);
  }

  // Modal state
  const [dialogOpen, setDialogOpen] = useState(false);

  // The top bar's contextual Create lands here as ?create=item.
  const [searchParams, setSearchParams] = useSearchParams();
  useEffect(() => {
    if (searchParams.get('create') === 'item') {
      setDialogOpen(true);
      setSearchParams({}, { replace: true });
    }
  }, [searchParams, setSearchParams]);

  const [formTitle, setFormTitle] = useState('');
  const [formPriority, setFormPriority] = useState<PriorityKey>('medium');
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

    try {
      await createMutation.mutateAsync({
        title,
        description: formDescription.trim() || '',
        kind: formKind,
        priority: PRIORITY_TO_API[formPriority],
      });
      setDialogOpen(false);
      resetForm();
    } catch {
      // Surfaced below through friendlyErrorMessage.
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
    // Sort items within each group by rank (lexicographic string ordering)
    for (const [, groupItems] of entries) {
      groupItems.sort((a, b) => {
        if (!a.rank && !b.rank) return 0;
        if (!a.rank) return 1;
        if (!b.rank) return -1;
        return a.rank.localeCompare(b.rank);
      });
    }
    return entries;
  }, [filtered]);

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <h1 className="text-[var(--text-lg)] font-semibold tracking-[-.01em] text-[var(--color-text)]">
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
        <div className="flex items-center gap-3 rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[color-mix(in_srgb,var(--color-danger)_10%,transparent)] p-4">
          <AlertCircle className="h-5 w-5 text-[var(--color-danger)]" />
          <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
            {friendlyErrorMessage(error, 'Backlog items could not be loaded.')}
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
            <table className="w-full text-left text-[13px]">
              <thead>
                <tr className="border-b border-[var(--color-border)]">
                  <th className="w-8 px-2 py-2.5" />
                  <th className="whitespace-nowrap px-3 py-2.5 text-[11px] font-normal uppercase tracking-[.04em] text-[var(--color-text-muted)]">ID</th>
                  <th className="px-3 py-2.5 text-[11px] font-normal uppercase tracking-[.04em] text-[var(--color-text-muted)]">Title</th>
                  <th className="px-3 py-2.5 text-[11px] font-normal uppercase tracking-[.04em] text-[var(--color-text-muted)]">Priority</th>
                  <th className="px-3 py-2.5 text-[11px] font-normal uppercase tracking-[.04em] text-[var(--color-text-muted)]">Status</th>
                </tr>
              </thead>
              <tbody>
                {groupItems.map((item) => {
                  const itemPath = `/vector/${spaceId}/backlog/${item.id}`;
                  const isOver = dragOverId === item.id;
                  return (
                    <tr
                      key={item.id}
                      draggable
                      onDragStart={() => handleDragStart(item.id)}
                      onDragOver={(e) => { e.preventDefault(); setDragOverId(item.id); }}
                      onDragLeave={() => setDragOverId(null)}
                      onDrop={() => handleDrop(groupItems, item.id)}
                      className={cn(
                        'border-b border-[var(--color-border)] last:border-b-0 transition-colors cursor-grab active:cursor-grabbing',
                        isOver ? 'bg-[var(--color-primary-muted)]' : 'hover:bg-[var(--color-surface-hover)]',
                      )}
                    >
                      <td className="px-2 py-3 text-[var(--color-text-muted)]">
                        <GripVertical className="h-4 w-4" />
                      </td>
                      <td className="whitespace-nowrap px-3 py-3">
                        <Link to={itemPath} className="text-[var(--text-xs)] text-[var(--color-primary)] hover:underline" style={{ fontFamily: 'var(--font-mono)' }}>
                          {item.number ? `${space?.key ?? 'PROJ'}-${item.number}` : (item.id ?? '').slice(0, 8)}
                        </Link>
                      </td>
                      <td className="px-3 py-3 text-[var(--color-text)]">
                        <Link to={itemPath} className="hover:underline">{item.title}</Link>
                      </td>
                      <td className="px-3 py-3"><PriorityPill priority={normalizePriority(item.priority)} /></td>
                      <td className="px-3 py-3"><Badge variant={STATUS_VARIANT[item.status] ?? 'secondary'}>{STATUS_LABEL[item.status] ?? item.status}</Badge></td>
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

          <div className="py-2">
            <Field>
              <FieldLabel htmlFor="item-title">Title</FieldLabel>
              <Input id="item-title" placeholder="e.g. Implement user registration flow" value={formTitle} onChange={(e) => setFormTitle(e.target.value)} autoFocus />
            </Field>

            <Field>
              <FieldLabel htmlFor="item-kind">Type</FieldLabel>
              <select
                id="item-kind"
                value={formKind}
                onChange={(e) => setFormKind(e.target.value)}
                className={cn(
                  'flex h-9 w-full rounded-[var(--radius-lg)] border border-[var(--color-border)]',
                  'bg-[var(--color-input)] px-3 text-[var(--text-sm)] text-[var(--color-text)]',
                  'focus-visible:outline-none focus-visible:border-[var(--color-primary)] focus-visible:ring-1 focus-visible:ring-[var(--color-primary)]',
                )}
              >
                <option value="task">Task</option>
                <option value="story">Story</option>
                <option value="bug">Bug</option>
                <option value="epic">Epic</option>
              </select>
            </Field>

            <Field>
              <FieldLabel id="item-priority-label">Priority</FieldLabel>
              <SegmentedControl
                options={PRIORITY_SEGMENT_OPTIONS}
                value={formPriority}
                onChange={setFormPriority}
                aria-label="Priority"
              />
            </Field>

            <Field>
              <FieldLabel htmlFor="item-desc" optional>
                Description
              </FieldLabel>
              <textarea
                id="item-desc"
                placeholder="What needs to be built and why"
                value={formDescription}
                onChange={(e) => setFormDescription(e.target.value)}
                rows={3}
                className={cn(
                  'flex w-full resize-y rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-input)] px-3 py-2 text-[var(--text-sm)] text-[var(--color-text)] transition-colors placeholder:text-[var(--color-text-muted)] focus-visible:outline-none focus-visible:border-[var(--color-primary)] focus-visible:ring-1 focus-visible:ring-[var(--color-primary)]',
                )}
              />
            </Field>

            {createMutation.error && (
              <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
                {friendlyErrorMessage(createMutation.error, 'The item could not be created.')}
              </p>
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
