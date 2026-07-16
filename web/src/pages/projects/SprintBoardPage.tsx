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
import { AlertCircle } from 'lucide-react';
import { Badge, type BadgeProps } from '../../components/ui/badge';
import { Card, CardContent } from '../../components/ui/card';
import { cn } from '../../lib/utils';
import { useQueryClient } from '@tanstack/react-query';
import {
  useActiveSprint,
  useSprintItems,
  useWorkflowStates,
  useSpace,
  queryKeys,
  transitionProjectItemStatus,
  type ProjectItem,
  type WorkflowState,
} from '../../lib/api';

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

const FALLBACK_COLUMNS: ColumnDef[] = [
  { id: 'open', label: 'Open', color: '#3b82f6' },
  { id: 'todo', label: 'To Do', color: '#3b82f6' },
  { id: 'in_progress', label: 'In Progress', color: '#f59e0b' },
  { id: 'in_review', label: 'In Review', color: '#8b5cf6' },
  { id: 'done', label: 'Done', color: '#10b981' },
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

const PRIORITY_VARIANT: Record<string, BadgeProps['variant']> = {
  critical: 'danger', urgent: 'danger', high: 'warning', medium: 'secondary', low: 'outline',
};
const PRIORITY_LABEL: Record<string, string> = {
  critical: 'Critical', urgent: 'Critical', high: 'High', medium: 'Medium', low: 'Low',
};

// ---------------------------------------------------------------------------
// Sortable item card
// ---------------------------------------------------------------------------

function SortableItemCard({ item, onItemClick, spaceKey }: { item: ProjectItem; onItemClick?: (id: string) => void; spaceKey?: string }) {
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
      <ItemCard item={item} onItemClick={onItemClick} spaceKey={spaceKey} />
    </div>
  );
}

function ItemCard({ item, overlay, onItemClick, spaceKey }: { item: ProjectItem; overlay?: boolean; onItemClick?: (id: string) => void; spaceKey?: string }) {
  const priorityKey = String(item.priority ?? '').toLowerCase();
  return (
    <Card
      className={cn(
        'cursor-grab transition-shadow hover:shadow-[var(--shadow-md)]',
        overlay && 'shadow-[var(--shadow-lg)] rotate-2',
      )}
      onClick={() => onItemClick?.(item.id)}
    >
      <CardContent className="space-y-2 p-3">
        <span
          className="text-[var(--text-xs)] font-medium text-[var(--color-text-muted)]"
          style={{ fontFamily: 'var(--font-mono)' }}
        >
          {item.number ? `${spaceKey ?? 'PROJ'}-${item.number}` : (item.id ?? '').slice(0, 8)}
        </span>
        <p className="text-[var(--text-sm)] leading-snug text-[var(--color-text)]">
          {item.title}
        </p>
        <div className="flex items-center justify-between">
          <Badge variant={PRIORITY_VARIANT[priorityKey] ?? 'secondary'}>
            {PRIORITY_LABEL[priorityKey] ?? 'Medium'}
          </Badge>
        </div>
      </CardContent>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Droppable column
// ---------------------------------------------------------------------------

function DroppableColumn({ column, items, onItemClick, spaceKey }: { column: ColumnDef; items: ProjectItem[]; onItemClick?: (id: string) => void; spaceKey?: string }) {
  return (
    <div className="flex w-72 shrink-0 flex-col rounded-[var(--radius-lg)] bg-[var(--color-bg)] p-3">
      <div className="mb-3 flex items-center justify-between">
        <div className="flex items-center gap-2">
          <span className="h-2 w-2 rounded-full" style={{ backgroundColor: column.color }} />
          <h3 className="text-[var(--text-sm)] font-semibold text-[var(--color-text)]">
            {column.label}
          </h3>
        </div>
        <span className="flex h-5 min-w-[20px] items-center justify-center rounded-full bg-[var(--color-surface-hover)] px-1.5 text-[var(--text-xs)] font-medium text-[var(--color-text-muted)]">
          {items.length}
        </span>
      </div>
      <SortableContext
        items={items.map((i) => i.id)}
        strategy={verticalListSortingStrategy}
      >
        <div className="flex flex-1 flex-col gap-2">
          {items.map((item) => (
            <SortableItemCard key={item.id} item={item} onItemClick={onItemClick} spaceKey={spaceKey} />
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
    navigate(`/spaces/${spaceId}/backlog/${id}`);
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
      <div className="space-y-6">
        <h1 className="text-[var(--text-2xl)] font-bold text-[var(--color-text)]">
          Sprint Board
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
      <div className="flex items-center gap-3 rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[var(--color-danger)]/10 p-4">
        <AlertCircle className="h-5 w-5 text-[var(--color-danger)]" />
        <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
          Failed to load items: {error.message}
        </p>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <div className="flex items-baseline gap-3">
        <h1 className="text-[var(--text-2xl)] font-bold text-[var(--color-text)]">
          Sprint Board
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
            />
          ))}
        </div>

        <DragOverlay>
          {activeItem ? (
            <div className="w-72">
              <ItemCard item={activeItem} overlay spaceKey={space?.key} />
            </div>
          ) : null}
        </DragOverlay>
      </DndContext>
    </div>
  );
}
