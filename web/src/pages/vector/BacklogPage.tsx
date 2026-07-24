import { useEffect, useState, useMemo, useRef } from 'react';
import { Link, useParams, useSearchParams } from 'react-router-dom';
import { Plus, Search, AlertCircle, GripVertical } from 'lucide-react';
import { Button } from '../../components/ui/button';
import { Badge, type BadgeProps } from '../../components/ui/badge';
import { Input } from '../../components/ui/input';
import { Field, FieldLabel } from '../../components/ui/field';
import { SegmentedControl } from '../../components/ui/segmented';
import { ItemKeyChip, itemKeyLabel } from '../../components/ItemKeyChip';
import { TypeFilter } from '../../components/TypeFilter';
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
import { getCurrentOrgId } from '../../lib/auth';
import {
  useProjectItems,
  useCreateProjectItem,
  useRankItem,
  useSpace,
  useItemTypes,
  useSprints,
  useAssignItemSprint,
  friendlyErrorMessage,
  type ProjectItem,
  type ItemType,
  type Sprint,
} from '../../lib/api';

// The default type set, shown in the create picker until the org's item types
// load (mirrors the server-seeded task/story/bug/epic).
const DEFAULT_TYPE_OPTIONS: Pick<ItemType, 'slug' | 'name'>[] = [
  { slug: 'task', name: 'Task' },
  { slug: 'story', name: 'Story' },
  { slug: 'bug', name: 'Bug' },
  { slug: 'epic', name: 'Epic' },
];

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
// Sprint grouping
// ---------------------------------------------------------------------------

// Sentinel group key for items on no sprint. A literal that cannot collide with
// a UUID, so it can never be confused with a real sprint id.
const BACKLOG_KEY = '__backlog__';

// Group ordering for sprint sections, matching the Sprints page.
const SPRINT_GROUP_ORDER: Record<string, number> = { active: 0, planned: 1, completed: 2 };

const sprintSelectClass = cn(
  'h-8 rounded-[var(--radius-lg)] border border-[var(--color-border)]',
  'bg-[var(--color-input)] px-2 text-[var(--text-xs)] text-[var(--color-text)]',
  'focus-visible:outline-none focus-visible:border-[var(--color-primary)] focus-visible:ring-1 focus-visible:ring-[var(--color-primary)]',
);

interface SprintGroup {
  key: string;
  label: string;
  /** Undefined for the backlog group and for ids with no matching sprint. */
  sprint?: Sprint;
  items: ProjectItem[];
}

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
  const orgId = getCurrentOrgId() ?? '';
  const { data: itemTypes } = useItemTypes(orgId);
  // Active (non-archived) types for the creation picker, falling back to the
  // default set before the query resolves.
  const typeOptions = (itemTypes ?? []).filter((t) => !t.archived_at);
  const pickerTypes = typeOptions.length > 0 ? typeOptions : DEFAULT_TYPE_OPTIONS;

  // Sprints power the group headings and the assign controls. Completed
  // sprints stay in the lookup (so their groups keep a real name) but are not
  // offered as assignment targets.
  const { data: sprints = [] } = useSprints(spaceId);
  const sprintById = useMemo(() => new Map(sprints.map((s) => [s.id, s])), [sprints]);
  const assignableSprints = useMemo(
    () => sprints.filter((s) => s.status !== 'completed'),
    [sprints],
  );
  const assignMutation = useAssignItemSprint(spaceId);
  const [assignError, setAssignError] = useState<string | null>(null);

  const [search, setSearch] = useState('');
  // Type filter: selected type slugs; empty means all types (W5).
  const [typeFilter, setTypeFilter] = useState<Set<string>>(new Set());

  // Multi-select for bulk sprint assignment.
  const [selected, setSelected] = useState<Set<string>>(new Set());

  function toggleSelected(id: string) {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  // Assign one item. Errors surface in the filter bar rather than throwing:
  // a failed re-sprint must not blank the list the user is working in.
  async function assignOne(itemId: string, sprintId: string | null) {
    setAssignError(null);
    try {
      await assignMutation.mutateAsync({ itemId, sprintId });
    } catch (e) {
      setAssignError(friendlyErrorMessage(e, 'The item could not be moved.'));
    }
  }

  // Bulk assign every selected item. Sequential rather than concurrent: the
  // rank/sprint writes touch the same rows and the item count here is small.
  async function assignSelected(sprintId: string | null) {
    setAssignError(null);
    const ids = Array.from(selected);
    try {
      for (const id of ids) {
        await assignMutation.mutateAsync({ itemId: id, sprintId });
      }
      setSelected(new Set());
    } catch (e) {
      setAssignError(friendlyErrorMessage(e, 'The items could not be moved.'));
    }
  }

  function toggleType(slug: string) {
    setTypeFilter((prev) => {
      const next = new Set(prev);
      if (next.has(slug)) next.delete(slug);
      else next.add(slug);
      return next;
    });
  }

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
      // Type filter composes with search: an empty selection admits every
      // type, otherwise the item's kind must be one of the selected types.
      // An item with no kind is excluded while a specific-type filter is active.
      if (typeFilter.size > 0 && !(item.kind && typeFilter.has(item.kind))) return false;
      return true;
    });
  }, [items, search, typeFilter]);

  // Group by sprint. The group key is the sprint id (or BACKLOG_KEY), but the
  // heading shows the sprint's *name* — this page previously rendered the raw
  // sprint UUID as the group title, which is unreadable and leaks an internal
  // identifier into the UI.
  const groups = useMemo(() => {
    const map = new Map<string, ProjectItem[]>();
    for (const item of filtered) {
      const group = item.sprint_id ?? BACKLOG_KEY;
      const arr = map.get(group) ?? [];
      arr.push(item);
      map.set(group, arr);
    }

    const entries: SprintGroup[] = Array.from(map.entries()).map(([key, groupItems]) => ({
      key,
      // A sprint the list hasn't loaded (or one from outside this space) falls
      // back to a neutral label rather than exposing the id.
      label: key === BACKLOG_KEY ? 'Backlog' : (sprintById.get(key)?.name ?? 'Unknown sprint'),
      sprint: key === BACKLOG_KEY ? undefined : sprintById.get(key),
      items: groupItems,
    }));

    // Backlog stays last; sprints lead with the active one, then planned, then
    // completed, mirroring the Sprints page ordering.
    entries.sort((a, b) => {
      if (a.key === BACKLOG_KEY) return 1;
      if (b.key === BACKLOG_KEY) return -1;
      const rank = (s?: Sprint) => SPRINT_GROUP_ORDER[s?.status ?? ''] ?? 3;
      const byStatus = rank(a.sprint) - rank(b.sprint);
      return byStatus !== 0 ? byStatus : a.label.localeCompare(b.label);
    });

    // Sort items within each group by rank (lexicographic string ordering)
    for (const group of entries) {
      group.items.sort((a, b) => {
        if (!a.rank && !b.rank) return 0;
        if (!a.rank) return 1;
        if (!b.rank) return -1;
        return a.rank.localeCompare(b.rank);
      });
    }
    return entries;
  }, [filtered, sprintById]);

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
        <TypeFilter
          options={pickerTypes.map((t) => ({ slug: t.slug, name: t.name }))}
          selected={typeFilter}
          onToggle={toggleType}
        />
      </div>

      {/* Bulk sprint assignment — appears only with a selection. */}
      {selected.size > 0 && (
        <div
          data-testid="backlog-bulk-bar"
          className="flex flex-wrap items-center gap-3 rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2"
        >
          <span className="text-[var(--text-sm)] text-[var(--color-text)]">
            {selected.size} selected
          </span>
          <select
            aria-label="Move selected to sprint"
            value=""
            disabled={assignMutation.isPending}
            onChange={(e) => {
              const v = e.target.value;
              if (!v) return;
              void assignSelected(v === BACKLOG_KEY ? null : v);
              e.target.value = '';
            }}
            className={sprintSelectClass}
          >
            <option value="">Move to…</option>
            <option value={BACKLOG_KEY}>Backlog</option>
            {assignableSprints.map((s) => (
              <option key={s.id} value={s.id}>{s.name}</option>
            ))}
          </select>
          <Button size="sm" variant="outline" onClick={() => setSelected(new Set())}>
            Clear
          </Button>
        </div>
      )}

      {assignError && (
        <div className="flex items-center gap-3 rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[color-mix(in_srgb,var(--color-danger)_10%,transparent)] p-3">
          <AlertCircle className="h-4 w-4 text-[var(--color-danger)]" />
          <p className="text-[var(--text-sm)] text-[var(--color-danger)]">{assignError}</p>
        </div>
      )}

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
      {items && groups.map(({ key: groupKey, label: groupLabel, items: groupItems }) => (
        <div key={groupKey} className="space-y-2">
          <h2 className="text-[var(--text-sm)] font-semibold text-[var(--color-text-muted)]">
            {groupLabel}
            <span className="ml-2 text-[var(--text-xs)] font-normal">
              ({groupItems.length} items)
            </span>
          </h2>

          <div className="overflow-x-auto rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-surface)]">
            <table className="w-full text-left text-[13px]">
              <thead>
                <tr className="border-b border-[var(--color-border)]">
                  <th className="w-8 px-2 py-2.5" />
                  <th className="w-8 px-2 py-2.5" />
                  <th className="whitespace-nowrap px-3 py-2.5 text-[11px] font-normal uppercase tracking-[.04em] text-[var(--color-text-muted)]">ID</th>
                  <th className="px-3 py-2.5 text-[11px] font-normal uppercase tracking-[.04em] text-[var(--color-text-muted)]">Title</th>
                  <th className="px-3 py-2.5 text-[11px] font-normal uppercase tracking-[.04em] text-[var(--color-text-muted)]">Priority</th>
                  <th className="px-3 py-2.5 text-[11px] font-normal uppercase tracking-[.04em] text-[var(--color-text-muted)]">Status</th>
                  <th className="px-3 py-2.5 text-[11px] font-normal uppercase tracking-[.04em] text-[var(--color-text-muted)]">Sprint</th>
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
                      <td className="px-2 py-3">
                        <input
                          type="checkbox"
                          aria-label={`Select ${itemKeyLabel(item, space?.key)}`}
                          checked={selected.has(item.id)}
                          onChange={() => toggleSelected(item.id)}
                          className="h-3.5 w-3.5 cursor-pointer accent-[var(--color-primary)]"
                        />
                      </td>
                      <td className="whitespace-nowrap px-3 py-3">
                        <Link to={itemPath} className="hover:opacity-80" aria-label={`Open ${itemKeyLabel(item, space?.key)}`}>
                          <ItemKeyChip item={item} spaceKey={space?.key} />
                        </Link>
                      </td>
                      <td className="px-3 py-3 text-[var(--color-text)]">
                        <Link to={itemPath} className="hover:underline">{item.title}</Link>
                      </td>
                      <td className="px-3 py-3"><PriorityPill priority={normalizePriority(item.priority)} /></td>
                      <td className="px-3 py-3"><Badge variant={STATUS_VARIANT[item.status] ?? 'secondary'}>{STATUS_LABEL[item.status] ?? item.status}</Badge></td>
                      <td className="px-3 py-3">
                        {/* Row action: re-sprint one item. Draggable rows swallow
                            pointer events, so stop propagation on the control. */}
                        <select
                          aria-label={`Sprint for ${itemKeyLabel(item, space?.key)}`}
                          value={item.sprint_id ?? BACKLOG_KEY}
                          disabled={assignMutation.isPending}
                          draggable={false}
                          onPointerDown={(e) => e.stopPropagation()}
                          onDragStart={(e) => { e.preventDefault(); e.stopPropagation(); }}
                          onChange={(e) => {
                            const v = e.target.value;
                            void assignOne(item.id, v === BACKLOG_KEY ? null : v);
                          }}
                          className={sprintSelectClass}
                        >
                          <option value={BACKLOG_KEY}>Backlog</option>
                          {/* A completed sprint stays listed while it owns this
                              item, so the control shows the truth rather than
                              silently reading as "Backlog". */}
                          {assignableSprints.map((s) => (
                            <option key={s.id} value={s.id}>{s.name}</option>
                          ))}
                          {item.sprint_id && !assignableSprints.some((s) => s.id === item.sprint_id) && (
                            <option value={item.sprint_id}>
                              {sprintById.get(item.sprint_id)?.name ?? 'Unknown sprint'}
                            </option>
                          )}
                        </select>
                      </td>
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
                {pickerTypes.map((t) => (
                  <option key={t.slug} value={t.slug}>{t.name}</option>
                ))}
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
