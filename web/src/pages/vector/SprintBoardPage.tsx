import { useState, useCallback, useMemo } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import {
  DndContext,
  closestCorners,
  DragOverlay,
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
import { AlertCircle, Bookmark, Bug, Flag, SquareCheck } from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import { PriorityPill, normalizePriority } from '../../components/priority';
import { ItemKeyChip } from '../../components/ItemKeyChip';
import { cn } from '../../lib/utils';
import { useQueryClient } from '@tanstack/react-query';
import {
  useActiveSprint,
  useSprintItems,
  useWorkflowStates,
  useSpace,
  useMe,
  useMembers,
  queryKeys,
  transitionProjectItemStatus,
  friendlyErrorMessage,
  type ProjectItem,
  type WorkflowState,
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
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

// Fallback column hues come from the token set; workflow-derived columns
// keep their API-provided colors.
const FALLBACK_COLUMNS: ColumnDef[] = [
  { id: 'open', label: 'Open', color: 'var(--color-info)' },
  { id: 'todo', label: 'To Do', color: 'var(--color-text-muted)' },
  { id: 'in_progress', label: 'In Progress', color: 'var(--color-warning)' },
  { id: 'in_review', label: 'In Review', color: 'var(--color-primary)' },
  { id: 'done', label: 'Done', color: 'var(--color-success)' },
];

function workflowStatesToColumns(states: WorkflowState[]): ColumnDef[] {
  return [...states]
    .sort((a, b) => a.position - b.position)
    .map((s) => ({
      id: s.name,
      label: s.name.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase()),
      color: s.color,
    }));
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
function DroppableColumn({ column, items, onItemClick, spaceKey, memberName }: { column: ColumnDef; items: ProjectItem[]; onItemClick?: (id: string) => void; spaceKey?: string; memberName?: (id: string | null | undefined) => string | undefined }) {
  return (
    <div className="flex w-72 shrink-0 flex-col rounded-[11px] border border-[var(--color-border)] bg-[var(--color-bg)] p-2">
      <div className="flex items-center gap-2 px-1.5 pb-2 pt-1">
        <span className="h-2 w-2 rounded-full" style={{ backgroundColor: column.color }} />
        <h3 className="text-[var(--text-sm)] font-medium text-[var(--color-text-muted)]">
          {column.label}
        </h3>
        <span className="text-[var(--text-xs)] text-[var(--color-text-muted)]" style={{ fontFamily: 'var(--font-mono)' }}>
          {items.length}
        </span>
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

  // P6: derive columns from the space's workflow states
  const { data: workflowStates } = useWorkflowStates(spaceId);
  const columnDefs: ColumnDef[] = useMemo(
    () => workflowStates && workflowStates.length > 0
      ? workflowStatesToColumns(workflowStates)
      : FALLBACK_COLUMNS,
    [workflowStates],
  );

  const [activeItem, setActiveItem] = useState<ProjectItem | null>(null);
  // Optimistic local status overrides while a drag transition is in-flight
  const [pendingStatus, setPendingStatus] = useState<Record<string, string>>({});

  const queryClient = useQueryClient();

  const effectiveItems = useMemo(() => {
    if (!items) return [];
    return items.map((i) => ({
      ...i,
      status: pendingStatus[i.id] ?? i.status,
    }));
  }, [items, pendingStatus]);

  const columns = useMemo(() => {
    const map: Record<string, ProjectItem[]> = {};
    for (const col of columnDefs) {
      map[col.id] = [];
    }
    // initial state name used as fallback bucket
    const fallbackKey = columnDefs[0]?.id ?? 'open';
    for (const item of effectiveItems) {
      if (map[item.status] !== undefined) {
        map[item.status].push(item);
      } else {
        map[fallbackKey].push(item);
      }
    }
    return map;
  }, [effectiveItems, columnDefs]);

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

  // P2.6: persist drag-and-drop via status transition endpoint
  const handleDragEnd = useCallback(
    async (event: DragEndEvent) => {
      setActiveItem(null);
      const { active, over } = event;
      if (!over || !sprintId) return;

      const draggedId = active.id as string;
      const draggedItem = effectiveItems.find((i) => i.id === draggedId);
      if (!draggedItem) return;

      // Determine target column: over.id is either a column id or an item id
      let targetStatus = over.id as string;
      if (!columnDefs.find((c) => c.id === targetStatus)) {
        // over.id is an item — use that item's current column
        const overItem = effectiveItems.find((i) => i.id === targetStatus);
        targetStatus = overItem?.status ?? draggedItem.status;
      }

      if (targetStatus === draggedItem.status) return;

      // Optimistic update
      setPendingStatus((prev) => ({ ...prev, [draggedId]: targetStatus }));

      try {
        // Always through the canonical API client — a raw fetch here once
        // bypassed auth/refresh handling and failed silently on error.
        await transitionProjectItemStatus(spaceId, draggedId, targetStatus);
        // Invalidate to sync server state
        queryClient.invalidateQueries({ queryKey: queryKeys.sprintItems(spaceId, sprintId) });
      } catch {
        // Revert optimistic update on error
        setPendingStatus((prev) => {
          const next = { ...prev };
          delete next[draggedId];
          return next;
        });
      }
    },
    [effectiveItems, columnDefs, spaceId, sprintId, queryClient],
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
      <div className="flex items-baseline gap-3">
        <h1 className="text-[var(--text-lg)] font-semibold tracking-[-.01em] text-[var(--color-text)]">
          Board
        </h1>
        <span className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
          {activeSprint.name}
        </span>
      </div>

      <DndContext
        sensors={sensors}
        collisionDetection={closestCorners}
        onDragStart={handleDragStart}
        onDragEnd={handleDragEnd}
      >
        <div className="flex gap-4 overflow-x-auto pb-4">
          {columnDefs.map((col) => (
            <DroppableColumn
              key={col.id}
              column={col}
              items={columns[col.id] ?? []}
              onItemClick={handleItemClick}
              spaceKey={space?.key}
              memberName={memberName}
            />
          ))}
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
