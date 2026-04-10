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
import { useProjectItems, type ProjectItem } from '../../lib/api';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type ColumnId = 'todo' | 'in_progress' | 'in_review' | 'done';

interface ColumnDef {
  id: ColumnId;
  label: string;
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const COLUMNS: ColumnDef[] = [
  { id: 'todo', label: 'To Do' },
  { id: 'in_progress', label: 'In Progress' },
  { id: 'in_review', label: 'In Review' },
  { id: 'done', label: 'Done' },
];

const PRIORITY_VARIANT: Record<number, BadgeProps['variant']> = {
  0: 'danger', 1: 'warning', 2: 'secondary', 3: 'outline',
};

// ---------------------------------------------------------------------------
// Sortable item card
// ---------------------------------------------------------------------------

function SortableItemCard({ item, onItemClick }: { item: ProjectItem; onItemClick?: (id: string) => void }) {
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
      <ItemCard item={item} onItemClick={onItemClick} />
    </div>
  );
}

function ItemCard({ item, overlay, onItemClick }: { item: ProjectItem; overlay?: boolean; onItemClick?: (id: string) => void }) {
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
          className="text-[var(--text-xs)] font-medium text-[var(--color-primary)]"
          style={{ fontFamily: 'var(--font-mono)' }}
        >
          {item.id.slice(0, 8)}
        </span>
        <p className="text-[var(--text-sm)] leading-snug text-[var(--color-text)]">
          {item.title}
        </p>
        <div className="flex items-center justify-between">
          <Badge variant={PRIORITY_VARIANT[item.priority] ?? 'secondary'}>
            {item.priority === 0 ? 'Critical' : item.priority === 1 ? 'High' : item.priority === 2 ? 'Medium' : 'Low'}
          </Badge>
        </div>
      </CardContent>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Droppable column
// ---------------------------------------------------------------------------

function DroppableColumn({ column, items, onItemClick }: { column: ColumnDef; items: ProjectItem[]; onItemClick?: (id: string) => void }) {
  return (
    <div className="flex w-72 shrink-0 flex-col rounded-[var(--radius-lg)] bg-[var(--color-bg)] p-3">
      <div className="mb-3 flex items-center justify-between">
        <h3 className="text-[var(--text-sm)] font-semibold text-[var(--color-text)]">
          {column.label}
        </h3>
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
            <SortableItemCard key={item.id} item={item} onItemClick={onItemClick} />
          ))}
        </div>
      </SortableContext>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Main component
// ---------------------------------------------------------------------------

/** Sprint board page with drag-and-drop columns. */
export function SprintBoardPage() {
  const navigate = useNavigate();
  const { spaceId = '' } = useParams<{ spaceId: string }>();
  const { data: items, isLoading, error } = useProjectItems(spaceId);
  const [activeItem, setActiveItem] = useState<ProjectItem | null>(null);

  const columns = useMemo(() => {
    const map: Record<ColumnId, ProjectItem[]> = {
      todo: [],
      in_progress: [],
      in_review: [],
      done: [],
    };
    if (items) {
      for (const item of items) {
        if (map[item.status as ColumnId]) {
          map[item.status as ColumnId].push(item);
        }
      }
    }
    return map;
  }, [items]);

  const handleItemClick = useCallback((id: string) => {
    const backlogPath = `/spaces/${spaceId}/backlog/${id}`;
    navigate(backlogPath);
  }, [navigate, spaceId]);

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
  );

  const handleDragStart = useCallback(
    (event: DragStartEvent) => {
      const id = event.active.id as string;
      const item = items?.find((i) => i.id === id);
      if (item) setActiveItem(item);
    },
    [items],
  );

  const handleDragEnd = useCallback(
    (_event: DragEndEvent) => {
      setActiveItem(null);
    },
    [],
  );

  if (isLoading) {
    return (
      <div className="flex h-64 items-center justify-center text-[var(--color-text-muted)]">
        Loading board...
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
      <h1 className="text-[var(--text-2xl)] font-bold text-[var(--color-text)]">
        Sprint Board
      </h1>

      <DndContext
        sensors={sensors}
        collisionDetection={closestCorners}
        onDragStart={handleDragStart}
        onDragEnd={handleDragEnd}
      >
        <div className="flex gap-4 overflow-x-auto pb-4">
          {COLUMNS.map((col) => (
            <DroppableColumn
              key={col.id}
              column={col}
              items={columns[col.id]}
              onItemClick={handleItemClick}
            />
          ))}
        </div>

        <DragOverlay>
          {activeItem ? (
            <div className="w-72">
              <ItemCard item={activeItem} overlay />
            </div>
          ) : null}
        </DragOverlay>
      </DndContext>
    </div>
  );
}
