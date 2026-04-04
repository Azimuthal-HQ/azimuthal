import { useState, useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
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
  arrayMove,
} from '@dnd-kit/sortable';
import { Bug, BookOpen, CheckSquare } from 'lucide-react';
import { Badge, type BadgeProps } from '../../components/ui/badge';
import { Card, CardContent } from '../../components/ui/card';
import { cn } from '../../lib/utils';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type ColumnId = 'todo' | 'in_progress' | 'in_review' | 'done';
type ItemType = 'story' | 'bug' | 'task';

interface SprintItem {
  id: string;
  key: string;
  title: string;
  type: ItemType;
  assignee: string;
  assigneeInitials: string;
  points: number | null;
}

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

const TYPE_ICON: Record<ItemType, typeof Bug> = {
  story: BookOpen,
  bug: Bug,
  task: CheckSquare,
};

const TYPE_VARIANT: Record<ItemType, BadgeProps['variant']> = {
  story: 'default',
  bug: 'danger',
  task: 'secondary',
};

// ---------------------------------------------------------------------------
// Mock data
// ---------------------------------------------------------------------------

const SPRINT_INFO = {
  name: 'Sprint 12',
  startDate: '2026-03-18',
  endDate: '2026-04-01',
  totalPoints: 44,
  completedPoints: 11,
};

const INITIAL_COLUMNS: Record<ColumnId, SprintItem[]> = {
  todo: [
    { id: '4', key: 'PROD-104', title: 'Set up CI pipeline for frontend', type: 'task', assignee: 'Dana Kim', assigneeInitials: 'DK', points: 5 },
    { id: '11', key: 'PROD-111', title: 'Implement notification preferences', type: 'story', assignee: 'Eve Johnson', assigneeInitials: 'EJ', points: 5 },
  ],
  in_progress: [
    { id: '2', key: 'PROD-102', title: 'Fix password reset email not sending', type: 'bug', assignee: 'Bob Martinez', assigneeInitials: 'BM', points: 3 },
    { id: '12', key: 'PROD-112', title: 'Add search indexing for wiki pages', type: 'story', assignee: 'Alice Chen', assigneeInitials: 'AC', points: 8 },
  ],
  in_review: [
    { id: '3', key: 'PROD-103', title: 'Add RBAC middleware', type: 'story', assignee: 'Charlie Osei', assigneeInitials: 'CO', points: 13 },
  ],
  done: [
    { id: '1', key: 'PROD-101', title: 'User registration flow', type: 'story', assignee: 'Alice Chen', assigneeInitials: 'AC', points: 8 },
    { id: '13', key: 'PROD-113', title: 'Fix broken link in onboarding step 3', type: 'bug', assignee: 'Bob Martinez', assigneeInitials: 'BM', points: 2 },
  ],
};

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function findColumn(
  columns: Record<ColumnId, SprintItem[]>,
  itemId: string,
): ColumnId | undefined {
  for (const col of COLUMNS) {
    if (columns[col.id].some((item) => item.id === itemId)) {
      return col.id;
    }
  }
  return undefined;
}

// ---------------------------------------------------------------------------
// Sortable item card
// ---------------------------------------------------------------------------

interface SortableItemCardProps {
  item: SprintItem;
  overlay?: boolean;
  onItemClick?: (key: string) => void;
}

function SortableItemCard({ item, onItemClick }: { item: SprintItem; onItemClick?: (key: string) => void }) {
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

function ItemCard({ item, overlay, onItemClick }: SortableItemCardProps) {
  const TypeIcon = TYPE_ICON[item.type];

  return (
    <Card
      className={cn(
        'cursor-grab transition-shadow hover:shadow-[var(--shadow-md)]',
        overlay && 'shadow-[var(--shadow-lg)] rotate-2',
      )}
      onClick={() => onItemClick?.(item.key)}
    >
      <CardContent className="space-y-2 p-3">
        <div className="flex items-center gap-2">
          <TypeIcon className="h-4 w-4 text-[var(--color-text-muted)]" />
          <span
            className="text-[var(--text-xs)] font-medium text-[var(--color-primary)] hover:underline"
            style={{ fontFamily: 'var(--font-mono)' }}
          >
            {item.key}
          </span>
        </div>
        <p className="text-[var(--text-sm)] leading-snug text-[var(--color-text)]">
          {item.title}
        </p>
        <div className="flex items-center justify-between">
          <span
            className={cn(
              'flex h-6 w-6 items-center justify-center rounded-full',
              'bg-[var(--color-primary-muted)] text-[var(--text-xs)] font-medium text-[var(--color-primary)]',
            )}
            title={item.assignee}
          >
            {item.assigneeInitials}
          </span>
          {item.points !== null && (
            <Badge variant={TYPE_VARIANT[item.type]}>
              {item.points} pts
            </Badge>
          )}
        </div>
      </CardContent>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Droppable column
// ---------------------------------------------------------------------------

interface DroppableColumnProps {
  column: ColumnDef;
  items: SprintItem[];
  onItemClick?: (key: string) => void;
}

function DroppableColumn({ column, items, onItemClick }: DroppableColumnProps) {
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

/** Sprint board page with drag-and-drop columns and sprint progress. */
export function SprintBoardPage() {
  const navigate = useNavigate();
  const [columns, setColumns] = useState<Record<ColumnId, SprintItem[]>>(INITIAL_COLUMNS);
  const [activeItem, setActiveItem] = useState<SprintItem | null>(null);

  const handleItemClick = useCallback((key: string) => {
    navigate(`/backlog/${key}`);
  }, [navigate]);

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
  );

  const progressPercent = Math.round(
    (SPRINT_INFO.completedPoints / SPRINT_INFO.totalPoints) * 100,
  );

  const handleDragStart = useCallback(
    (event: DragStartEvent) => {
      const id = event.active.id as string;
      const col = findColumn(columns, id);
      if (col) {
        const item = columns[col].find((i) => i.id === id);
        if (item) setActiveItem(item);
      }
    },
    [columns],
  );

  const handleDragEnd = useCallback(
    (event: DragEndEvent) => {
      setActiveItem(null);
      const { active, over } = event;
      if (!over) return;

      const activeId = active.id as string;
      const overId = over.id as string;

      const fromCol = findColumn(columns, activeId);
      if (!fromCol) return;

      const toCol = findColumn(columns, overId) ?? (overId as ColumnId);

      if (fromCol === toCol) {
        const oldIndex = columns[fromCol].findIndex((i) => i.id === activeId);
        const newIndex = columns[fromCol].findIndex((i) => i.id === overId);
        if (oldIndex !== -1 && newIndex !== -1 && oldIndex !== newIndex) {
          setColumns((prev) => ({
            ...prev,
            [fromCol]: arrayMove(prev[fromCol], oldIndex, newIndex),
          }));
        }
      } else {
        const item = columns[fromCol].find((i) => i.id === activeId);
        if (!item) return;

        const targetCol = COLUMNS.find((c) => c.id === toCol) ? toCol : findColumn(columns, overId);
        if (!targetCol) return;

        setColumns((prev) => ({
          ...prev,
          [fromCol]: prev[fromCol].filter((i) => i.id !== activeId),
          [targetCol]: [...prev[targetCol], item],
        }));
      }
    },
    [columns],
  );

  return (
    <div className="space-y-6">
      {/* Sprint header */}
      <div className="space-y-3">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-[var(--text-2xl)] font-bold text-[var(--color-text)]">
              {SPRINT_INFO.name}
            </h1>
            <p className="mt-1 text-[var(--text-sm)] text-[var(--color-text-muted)]">
              {SPRINT_INFO.startDate} &mdash; {SPRINT_INFO.endDate}
            </p>
          </div>
          <div className="text-right">
            <p className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">
              {SPRINT_INFO.completedPoints} / {SPRINT_INFO.totalPoints} pts
            </p>
            <p className="text-[var(--text-xs)] text-[var(--color-text-muted)]">
              {progressPercent}% complete
            </p>
          </div>
        </div>

        {/* Progress bar */}
        <div className="h-2 w-full overflow-hidden rounded-full bg-[var(--color-surface-hover)]">
          <div
            className="h-full rounded-full bg-[var(--color-primary)] transition-all"
            style={{ width: `${progressPercent}%` }}
          />
        </div>
      </div>

      {/* Board */}
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
