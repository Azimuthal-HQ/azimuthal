import { useState, useCallback, useMemo } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import {
  DndContext,
  closestCorners,
  DragOverlay,
  useDroppable,
  type DragStartEvent,
  type DragEndEvent,
  PointerSensor,
  useSensor,
  useSensors,
} from '@dnd-kit/core';
import {
  SortableContext,
  useSortable,
  verticalListSortingStrategy,
} from '@dnd-kit/sortable';
import { AlertCircle, AlertTriangle, Bookmark, Bug, Flag, SquareCheck } from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import { PriorityPill, normalizePriority } from '../../components/priority';
import { ItemKeyChip } from '../../components/ItemKeyChip';
import { TypeFilter } from '../../components/TypeFilter';
import { SegmentedControl } from '../../components/ui/segmented';
import { getCurrentOrgId } from '../../lib/auth';
import { cn } from '../../lib/utils';
import { useQueryClient } from '@tanstack/react-query';
import {
  useActiveSprint,
  useSprintItems,
  useWorkflowStates,
  useSpace,
  useMe,
  useMembers,
  useItemTypes,
  useBoardConfig,
  useUpdateProjectItem,
  queryKeys,
  transitionProjectItemStatus,
  friendlyErrorMessage,
  type ProjectItem,
  type WorkflowState,
  type BoardColumn as BoardColumnConfig,
} from '../../lib/api';

// Kind icons per the dashboards prototype's board cards (bug / story / task /
// epic). Unknown kinds fall back to the task check.
const KIND_ICON: Record<string, LucideIcon> = {
  bug: Bug,
  story: Bookmark,
  task: SquareCheck,
  epic: Flag,
};

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface ColumnDef {
  id: string;
  label: string;
  color: string;
  /** Statuses this column collects. A default column collects exactly one. */
  statuses: string[];
  /** The status a drop into this column sets — the first mapped status. */
  dropStatus: string;
  /** null means no limit. Limits are soft: over-limit warns, never blocks. */
  wipLimit: number | null;
}

// Swimlane grouping. No epic lanes — hierarchy does not exist yet (fenced).
type LaneMode = 'none' | 'assignee' | 'type';

const LANE_OPTIONS: { value: LaneMode; label: string }[] = [
  { value: 'none', label: 'No lanes' },
  { value: 'assignee', label: 'By assignee' },
  { value: 'type', label: 'By type' },
];

interface Lane {
  id: string;
  label: string;
  items: ProjectItem[];
  /**
   * The attribute value a cross-lane drop into this lane sets. null is a real
   * value (unassign / clear type); undefined marks the catch-all lane in
   * 'none' mode, where there is no attribute to set.
   */
  laneValue?: string | null;
}

// Explicit catch-all lanes — unassigned and typeless work is visible, never
// hidden. The sentinel cannot collide with a UUID or a type slug.
const NO_LANE = '__none__';

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

// Fallback column hues come from the token set; workflow-derived columns
// keep their API-provided colors.
function statusColumn(id: string, label: string, color: string): ColumnDef {
  return { id, label, color, statuses: [id], dropStatus: id, wipLimit: null };
}

const FALLBACK_COLUMNS: ColumnDef[] = [
  statusColumn('open', 'Open', 'var(--color-info)'),
  statusColumn('todo', 'To Do', 'var(--color-text-muted)'),
  statusColumn('in_progress', 'In Progress', 'var(--color-warning)'),
  statusColumn('in_review', 'In Review', 'var(--color-primary)'),
  statusColumn('done', 'Done', 'var(--color-success)'),
];

function titleCase(s: string) {
  return s.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase());
}

function workflowStatesToColumns(states: WorkflowState[]): ColumnDef[] {
  return [...states]
    .sort((a, b) => a.position - b.position)
    .map((s) => statusColumn(s.name, titleCase(s.name), s.color));
}

/**
 * Turns a saved board configuration into columns. Colours come from the
 * workflow state of the column's first status where one exists, so a
 * customised board keeps the hues the space already uses.
 */
function configToColumns(config: BoardColumnConfig[], states: WorkflowState[]): ColumnDef[] {
  const colorFor = new Map(states.map((s) => [s.name, s.color]));
  return [...config]
    .sort((a, b) => a.position - b.position)
    .map((c) => ({
      id: c.id,
      label: c.name,
      color: colorFor.get(c.statuses[0]) ?? 'var(--color-text-muted)',
      statuses: c.statuses,
      // A drop sets the column's first status. With one status mapped this is
      // exactly today's behaviour; with several, the first is the column's
      // canonical state.
      dropStatus: c.statuses[0] ?? '',
      wipLimit: c.wip_limit,
    }))
    .filter((c) => c.dropStatus !== '');
}

// ---------------------------------------------------------------------------
// Sortable item card
// ---------------------------------------------------------------------------

function SortableItemCard({ item, onItemClick, spaceKey, memberName }: { item: ProjectItem; onItemClick?: (id: string) => void; spaceKey?: string; memberName?: (id: string | null | undefined) => string | undefined }) {
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({ id: item.id });

  const style: React.CSSProperties = {
    transform: transform
      ? `translate3d(${transform.x}px, ${transform.y}px, 0)`
      : undefined,
    transition,
    opacity: isDragging ? 0.4 : 1,
  };

  return (
    <div ref={setNodeRef} style={style} {...attributes} {...listeners}>
      <ItemCard item={item} onItemClick={onItemClick} spaceKey={spaceKey} memberName={memberName} />
    </div>
  );
}

/**
 * Board card per the dashboards prototype: kind icon + mono key on top, the
 * title, then priority pill left and assignee avatar right. Rendering only —
 * the card shows what the API already returns.
 */
function ItemCard({ item, overlay, onItemClick, spaceKey, memberName }: { item: ProjectItem; overlay?: boolean; onItemClick?: (id: string) => void; spaceKey?: string; memberName?: (id: string | null | undefined) => string | undefined }) {
  const KindIcon = KIND_ICON[String(item.kind ?? '').toLowerCase()] ?? SquareCheck;
  const assignee = memberName?.(item.assignee_id);
  return (
    <div
      className={cn(
        'cursor-grab rounded-[10px] border border-[var(--color-border)] bg-[var(--color-surface)] p-2.5 transition-colors',
        'hover:border-[var(--color-text-muted)]',
        overlay && 'rotate-2 shadow-[var(--shadow-lg)]',
      )}
      onClick={() => onItemClick?.(item.id)}
    >
      <span className="flex items-center gap-1.5 text-[var(--color-text-muted)]">
        <KindIcon className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
        <ItemKeyChip item={item} spaceKey={spaceKey} />
      </span>
      <p className="mb-2.5 mt-1.5 text-[13px] leading-[1.4] text-[var(--color-text)]">
        {item.title}
      </p>
      <div className="flex items-center">
        <PriorityPill priority={normalizePriority(item.priority)} />
        <span
          className={cn(
            'ml-auto flex h-[22px] w-[22px] shrink-0 items-center justify-center rounded-full text-[10px] font-medium',
            assignee
              ? 'bg-[var(--color-primary-muted)] text-[var(--color-primary)]'
              : 'bg-[var(--color-surface-hover)] text-[var(--color-text-muted)]',
          )}
          title={assignee ?? 'Unassigned'}
        >
          {assignee?.[0]?.toUpperCase() ?? '–'}
        </span>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Droppable column
// ---------------------------------------------------------------------------

/**
 * Contained column per the dashboards prototype: a bordered container on the
 * page ground with a quiet header (label + faint count, no bubble), cards
 * inside, and a contained empty state — never floating labels in open space.
 */
function DroppableColumn({
  column, items, onItemClick, spaceKey, memberName, laneId, wipCount,
}: {
  column: ColumnDef;
  items: ProjectItem[];
  onItemClick?: (id: string) => void;
  spaceKey?: string;
  memberName?: (id: string | null | undefined) => string | undefined;
  /** Present when the board is laned; the droppable id encodes both axes. */
  laneId?: string;
  /**
   * The count a WIP limit is judged against — the whole column across every
   * lane, not this lane's slice. A limit of 3 means three items in progress,
   * however they are grouped.
   */
  wipCount: number;
}) {
  const droppableId = laneId ? `${column.id}::${laneId}` : column.id;
  const { setNodeRef } = useDroppable({ id: droppableId });

  const overLimit = column.wipLimit !== null && wipCount > column.wipLimit;
  const atLimit = column.wipLimit !== null && wipCount === column.wipLimit;

  return (
    <div
      ref={setNodeRef}
      data-testid="board-column"
      data-column-id={column.id}
      data-over-limit={overLimit || undefined}
      className={cn(
        'flex w-72 shrink-0 flex-col rounded-[11px] border bg-[var(--color-bg)] p-2',
        overLimit ? 'border-[var(--color-warning)]' : 'border-[var(--color-border)]',
      )}
    >
      <div className="flex items-center gap-2 px-1.5 pb-2 pt-1">
        <span className="h-2 w-2 rounded-full" style={{ backgroundColor: column.color }} />
        <h3 className="text-[var(--text-sm)] font-medium text-[var(--color-text-muted)]">
          {column.label}
        </h3>
        {/* The count carries the WIP state: it turns warning-coloured when the
            column is over its limit. Soft — nothing here blocks a drop. */}
        <span
          data-testid="column-count"
          className={cn(
            'text-[var(--text-xs)]',
            overLimit ? 'font-semibold text-[var(--color-warning)]' : 'text-[var(--color-text-muted)]',
          )}
          style={{ fontFamily: 'var(--font-mono)' }}
        >
          {items.length}
          {column.wipLimit !== null && <span className="opacity-70">/{column.wipLimit}</span>}
        </span>
        {overLimit && (
          <span
            data-testid="wip-overflow"
            title={`Over the WIP limit of ${column.wipLimit}`}
            className="ml-auto flex items-center gap-1 text-[var(--text-xs)] font-medium text-[var(--color-warning)]"
          >
            <AlertTriangle className="h-3 w-3" aria-hidden="true" />
            Over limit
          </span>
        )}
        {atLimit && !overLimit && (
          <span data-testid="wip-at-limit" className="ml-auto text-[var(--text-xs)] text-[var(--color-text-muted)]">
            At limit
          </span>
        )}
      </div>
      <SortableContext
        items={items.map((i) => i.id)}
        strategy={verticalListSortingStrategy}
      >
        <div className="flex min-h-16 flex-1 flex-col gap-2">
          {items.length === 0 && (
            <p className="rounded-[10px] border border-dashed border-[var(--color-border)] px-3 py-4 text-center text-[var(--text-xs)] text-[var(--color-text-muted)]">
              No items
            </p>
          )}
          {items.map((item) => (
            <SortableItemCard key={item.id} item={item} onItemClick={onItemClick} spaceKey={spaceKey} memberName={memberName} />
          ))}
        </div>
      </SortableContext>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Drop resolution
// ---------------------------------------------------------------------------

/**
 * Resolves what a drop landed on. dnd-kit reports either a droppable id — a
 * column, or "column::lane" on a laned board — or another card's id, which
 * means "where that card lives".
 */
export function resolveDropTarget(
  overId: string,
  items: ProjectItem[],
  columns: ColumnDef[],
  columnIdFor: (status: string) => string,
  laneMode: LaneMode,
): { columnId: string; laneId?: string } | null {
  const [columnPart, lanePart] = overId.split('::');
  if (columns.some((c) => c.id === columnPart)) {
    return { columnId: columnPart, laneId: lanePart };
  }

  const overItem = items.find((i) => i.id === overId);
  if (!overItem) return null;
  // Dropped onto another card: inherit both that card's column and its lane,
  // so a drop onto a card in another lane re-lanes as a drop onto that lane's
  // empty space would.
  return {
    columnId: columnIdFor(overItem.status),
    laneId: laneMode === 'none' ? undefined : laneKeyOf(overItem, laneMode),
  };
}

/** The lane an item belongs to under a given grouping. */
export function laneKeyOf(item: ProjectItem, mode: LaneMode): string {
  if (mode === 'none') return NO_LANE;
  return (mode === 'assignee' ? item.assignee_id : item.kind) || NO_LANE;
}

// ---------------------------------------------------------------------------
// Swimlanes
// ---------------------------------------------------------------------------

/**
 * Groups items into lanes. Unassigned and typeless work gets an explicit
 * catch-all lane rather than being hidden — a lane layout that quietly drops
 * items reads as "there is no such work".
 */
export function buildLanes(
  items: ProjectItem[],
  mode: LaneMode,
  memberName: (id: string | null | undefined) => string | undefined,
  typeOptions: { slug: string; name: string }[],
): Lane[] {
  if (mode === 'none') {
    return [{ id: NO_LANE, label: '', items }];
  }

  const buckets = new Map<string, ProjectItem[]>();
  for (const item of items) {
    const key = laneKeyOf(item, mode);
    const arr = buckets.get(key) ?? [];
    arr.push(item);
    buckets.set(key, arr);
  }

  const typeName = new Map(typeOptions.map((t) => [t.slug, t.name]));
  const label = (key: string) => {
    if (key === NO_LANE) return mode === 'assignee' ? 'Unassigned' : 'No type';
    if (mode === 'assignee') return memberName(key) ?? 'Unknown member';
    return typeName.get(key) ?? key;
  };

  const lanes = Array.from(buckets.entries()).map(([key, laneItems]) => ({
    id: key,
    label: label(key),
    items: laneItems,
    laneValue: key === NO_LANE ? null : key,
  }));

  // Named lanes alphabetically, catch-all last — it is the residue, not a peer.
  lanes.sort((a, b) => {
    if (a.id === NO_LANE) return 1;
    if (b.id === NO_LANE) return -1;
    return a.label.localeCompare(b.label);
  });
  return lanes;
}

// ---------------------------------------------------------------------------
// Main component
// ---------------------------------------------------------------------------

/** Sprint board page — shows active sprint items in drag-and-drop columns. */
export function SprintBoardPage() {
  const navigate = useNavigate();
  const { spaceId = '' } = useParams<{ spaceId: string }>();

  const { data: space } = useSpace(spaceId);

  // Assignee avatars on the cards (dashboards prototype): resolve ids to
  // display names through the existing members list — rendering only.
  const { data: me } = useMe();
  const { data: members } = useMembers(me?.org_id ?? '', spaceId);
  const memberName = useCallback(
    (id: string | null | undefined) =>
      id ? (members ?? []).find((m) => m.user_id === id)?.display_name : undefined,
    [members],
  );

  // P2.5: load active sprint first, then its items
  const { data: activeSprint, isLoading: sprintLoading } = useActiveSprint(spaceId);
  const sprintId = activeSprint?.id ?? '';
  const { data: items, isLoading: itemsLoading, error } = useSprintItems(spaceId, sprintId);

  // P6: derive columns from the space's workflow states. W4: a saved board
  // configuration wins; with none saved the derivation is unchanged, so an
  // uncustomised space renders exactly as it always did.
  const { data: workflowStates } = useWorkflowStates(spaceId);
  const { data: boardConfig } = useBoardConfig(spaceId);
  const columnDefs: ColumnDef[] = useMemo(() => {
    if (boardConfig?.customized && boardConfig.columns.length > 0) {
      return configToColumns(boardConfig.columns, workflowStates ?? []);
    }
    return workflowStates && workflowStates.length > 0
      ? workflowStatesToColumns(workflowStates)
      : FALLBACK_COLUMNS;
  }, [boardConfig, workflowStates]);

  // W5: the shared TypeFilter, reused here rather than reimplemented.
  const orgId = getCurrentOrgId() ?? '';
  const { data: itemTypes } = useItemTypes(orgId);
  const typeOptions = useMemo(
    () => (itemTypes ?? []).filter((t) => !t.archived_at).map((t) => ({ slug: t.slug, name: t.name })),
    [itemTypes],
  );
  const [typeFilter, setTypeFilter] = useState<Set<string>>(new Set());
  const toggleType = useCallback((slug: string) => {
    setTypeFilter((prev) => {
      const next = new Set(prev);
      if (next.has(slug)) next.delete(slug);
      else next.add(slug);
      return next;
    });
  }, []);

  const [laneMode, setLaneMode] = useState<LaneMode>('none');

  const [activeItem, setActiveItem] = useState<ProjectItem | null>(null);
  // Optimistic local status overrides while a drag transition is in-flight
  const [pendingStatus, setPendingStatus] = useState<Record<string, string>>({});
  // Optimistic lane-attribute overrides for a cross-lane drag
  const [pendingLane, setPendingLane] = useState<Record<string, Partial<ProjectItem>>>({});

  const queryClient = useQueryClient();
  const updateItem = useUpdateProjectItem(spaceId, activeItem?.id ?? '');

  const effectiveItems = useMemo(() => {
    if (!items) return [];
    return items.map((i) => ({
      ...i,
      status: pendingStatus[i.id] ?? i.status,
      ...pendingLane[i.id],
    }));
  }, [items, pendingStatus, pendingLane]);

  // The type filter narrows before lanes and columns are built, so both see
  // the same set — filtering afterwards double-counts in WIP totals.
  const visibleItems = useMemo(
    () => (typeFilter.size === 0
      ? effectiveItems
      : effectiveItems.filter((i) => i.kind && typeFilter.has(i.kind))),
    [effectiveItems, typeFilter],
  );

  // statusToColumn maps every mapped status to its column. A status no column
  // claims falls back to the first column, which is what the board has always
  // done with an unrecognised status.
  const statusToColumn = useMemo(() => {
    const map = new Map<string, string>();
    for (const col of columnDefs) {
      for (const st of col.statuses) map.set(st, col.id);
    }
    return map;
  }, [columnDefs]);

  const columnIdFor = useCallback(
    (status: string) => statusToColumn.get(status) ?? columnDefs[0]?.id ?? '',
    [statusToColumn, columnDefs],
  );

  // Column totals across every lane — what a WIP limit is judged against.
  const columnTotals = useMemo(() => {
    const totals: Record<string, number> = {};
    for (const col of columnDefs) totals[col.id] = 0;
    for (const item of visibleItems) {
      const id = columnIdFor(item.status);
      if (id) totals[id] = (totals[id] ?? 0) + 1;
    }
    return totals;
  }, [visibleItems, columnDefs, columnIdFor]);

  const lanes: Lane[] = useMemo(
    () => buildLanes(visibleItems, laneMode, memberName, typeOptions),
    [visibleItems, laneMode, memberName, typeOptions],
  );

  const columnsForLane = useCallback(
    (laneItems: ProjectItem[]) => {
      const map: Record<string, ProjectItem[]> = {};
      for (const col of columnDefs) map[col.id] = [];
      for (const item of laneItems) {
        const id = columnIdFor(item.status);
        if (map[id]) map[id].push(item);
      }
      return map;
    },
    [columnDefs, columnIdFor],
  );

  const handleItemClick = useCallback((id: string) => {
    navigate(`/vector/${spaceId}/backlog/${id}`);
  }, [navigate, spaceId]);

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
  );

  const handleDragStart = useCallback(
    (event: DragStartEvent) => {
      const id = event.active.id as string;
      const item = effectiveItems.find((i) => i.id === id);
      if (item) setActiveItem(item);
    },
    [effectiveItems],
  );

  // P2.6: persist drag-and-drop via status transition endpoint.
  // W4: a drop now carries two axes — the column (status, as before) and, when
  // the board is laned, the lane (assignee or type). Either, both, or neither
  // may change in one drop.
  const handleDragEnd = useCallback(
    async (event: DragEndEvent) => {
      setActiveItem(null);
      const { active, over } = event;
      if (!over || !sprintId) return;

      const draggedId = active.id as string;
      const draggedItem = effectiveItems.find((i) => i.id === draggedId);
      if (!draggedItem) return;

      const target = resolveDropTarget(String(over.id), effectiveItems, columnDefs, columnIdFor, laneMode);
      if (!target) return;

      const column = columnDefs.find((c) => c.id === target.columnId);
      const currentColumn = columnIdFor(draggedItem.status);
      // A column whose statuses already include this item's status is not a
      // move: dropping a "resolved" item into a column that collects both
      // "done" and "resolved" must not silently rewrite it to "done".
      const statusChanged = target.columnId !== currentColumn;
      const targetStatus = column?.dropStatus ?? draggedItem.status;

      const currentLane = laneKeyOf(draggedItem, laneMode);
      const laneChanged = laneMode !== 'none'
        && target.laneId !== undefined
        && target.laneId !== currentLane;

      if (!statusChanged && !laneChanged) return;

      if (statusChanged) setPendingStatus((prev) => ({ ...prev, [draggedId]: targetStatus }));
      const laneValue = target.laneId === NO_LANE ? null : (target.laneId ?? null);
      if (laneChanged) {
        setPendingLane((prev) => ({
          ...prev,
          [draggedId]: laneMode === 'assignee' ? { assignee_id: laneValue } : { kind: laneValue ?? '' },
        }));
      }

      try {
        if (statusChanged) {
          // Always through the canonical API client — a raw fetch here once
          // bypassed auth/refresh handling and failed silently on error.
          await transitionProjectItemStatus(spaceId, draggedId, targetStatus);
        }
        if (laneChanged) {
          // Type is not editable through the item PATCH contract, so a
          // cross-lane drag under "by type" is a verified no-op: the card
          // returns to its lane rather than half-applying. Flagged in the PR.
          if (laneMode === 'assignee') {
            await updateItem.mutateAsync({ assignee_id: laneValue });
          }
        }
        queryClient.invalidateQueries({ queryKey: queryKeys.sprintItems(spaceId, sprintId) });
      } catch {
        setPendingStatus((prev) => {
          const next = { ...prev };
          delete next[draggedId];
          return next;
        });
      } finally {
        // The lane override always clears: on success the refetch carries the
        // truth, and on the type no-op the card must snap back rather than
        // showing a change that never reached the server.
        setPendingLane((prev) => {
          const next = { ...prev };
          delete next[draggedId];
          return next;
        });
      }
    },
    [effectiveItems, columnDefs, columnIdFor, laneMode, spaceId, sprintId, queryClient, updateItem],
  );

  const isLoading = sprintLoading || (!!sprintId && itemsLoading);

  if (isLoading) {
    return (
      <div className="flex h-64 items-center justify-center text-[var(--color-text-muted)]">
        Loading board...
      </div>
    );
  }

  // P2.5: empty state when no active sprint
  if (!activeSprint) {
    return (
      <div className="space-y-5">
        <h1 className="text-[var(--text-lg)] font-semibold tracking-[-.01em] text-[var(--color-text)]">
          Board
        </h1>
        <div className="flex min-h-[300px] items-center justify-center rounded-[var(--radius-lg)] border-2 border-dashed border-[var(--color-border)] bg-[var(--color-surface)]">
          <div className="text-center space-y-2">
            <p className="text-[var(--text-lg)] font-medium text-[var(--color-text-muted)]">
              No active sprint
            </p>
            <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
              Start a sprint from the backlog to see items here.
            </p>
          </div>
        </div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="flex items-center gap-3 rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[color-mix(in_srgb,var(--color-danger)_10%,transparent)] p-4">
        <AlertCircle className="h-5 w-5 text-[var(--color-danger)]" />
        <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
          {friendlyErrorMessage(error, 'Sprint items could not be loaded.')}
        </p>
      </div>
    );
  }

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center gap-3">
        <div className="flex items-baseline gap-3">
          <h1 className="text-[var(--text-lg)] font-semibold tracking-[-.01em] text-[var(--color-text)]">
            Board
          </h1>
          <span className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
            {activeSprint.name}
          </span>
        </div>
        <div className="ml-auto flex flex-wrap items-center gap-3">
          {/* W5's TypeFilter, reused — never a second implementation. */}
          {typeOptions.length > 0 && (
            <TypeFilter options={typeOptions} selected={typeFilter} onToggle={toggleType} />
          )}
          <SegmentedControl
            options={LANE_OPTIONS}
            value={laneMode}
            onChange={setLaneMode}
            aria-label="Group into swimlanes"
            fullWidth={false}
          />
        </div>
      </div>

      <DndContext
        sensors={sensors}
        collisionDetection={closestCorners}
        onDragStart={handleDragStart}
        onDragEnd={handleDragEnd}
      >
        <div className="space-y-4">
          {lanes.map((lane) => {
            const laneColumns = columnsForLane(lane.items);
            return (
              <div key={lane.id} data-testid="board-lane" data-lane-id={lane.id}>
                {laneMode !== 'none' && (
                  <div className="mb-2 flex items-center gap-2">
                    <h2 className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                      {lane.label}
                    </h2>
                    <span
                      className="text-[var(--text-xs)] text-[var(--color-text-muted)]"
                      style={{ fontFamily: 'var(--font-mono)' }}
                    >
                      {lane.items.length}
                    </span>
                  </div>
                )}
                <div className="flex gap-4 overflow-x-auto pb-4">
                  {columnDefs.map((col) => (
                    <DroppableColumn
                      key={col.id}
                      column={col}
                      items={laneColumns[col.id] ?? []}
                      onItemClick={handleItemClick}
                      spaceKey={space?.key}
                      memberName={memberName}
                      laneId={laneMode === 'none' ? undefined : lane.id}
                      wipCount={columnTotals[col.id] ?? 0}
                    />
                  ))}
                </div>
              </div>
            );
          })}
        </div>

        <DragOverlay>
          {activeItem ? (
            <div className="w-72">
              <ItemCard item={activeItem} overlay spaceKey={space?.key} memberName={memberName} />
            </div>
          ) : null}
        </DragOverlay>
      </DndContext>
    </div>
  );
}
