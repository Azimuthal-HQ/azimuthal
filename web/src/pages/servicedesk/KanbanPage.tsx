import { useState, useCallback } from 'react';
import { Link } from 'react-router-dom';
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
import { Badge, type BadgeProps } from '../../components/ui/badge';
import { Card, CardContent } from '../../components/ui/card';
import { cn } from '../../lib/utils';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type ColumnId = 'open' | 'in_progress' | 'resolved' | 'closed';
type TicketPriority = 'critical' | 'high' | 'medium' | 'low';

interface KanbanTicket {
  id: string;
  title: string;
  priority: TicketPriority;
  assignee: string;
  assigneeInitials: string;
}

interface ColumnDef {
  id: ColumnId;
  label: string;
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const COLUMNS: ColumnDef[] = [
  { id: 'open', label: 'Open' },
  { id: 'in_progress', label: 'In Progress' },
  { id: 'resolved', label: 'Resolved' },
  { id: 'closed', label: 'Closed' },
];

const PRIORITY_VARIANT: Record<TicketPriority, BadgeProps['variant']> = {
  critical: 'danger',
  high: 'warning',
  medium: 'secondary',
  low: 'outline',
};

const PRIORITY_LABEL: Record<TicketPriority, string> = {
  critical: 'Critical',
  high: 'High',
  medium: 'Medium',
  low: 'Low',
};

// ---------------------------------------------------------------------------
// Mock data
// ---------------------------------------------------------------------------

const INITIAL_COLUMNS: Record<ColumnId, KanbanTicket[]> = {
  open: [
    { id: 'TICKET-101', title: 'Login page returns 500 for SSO users', priority: 'critical', assignee: 'Alice Chen', assigneeInitials: 'AC' },
    { id: 'TICKET-103', title: 'Add dark mode support for email templates', priority: 'medium', assignee: 'Charlie Osei', assigneeInitials: 'CO' },
    { id: 'TICKET-107', title: 'Rate-limit API responses to prevent abuse', priority: 'high', assignee: 'Bob Martinez', assigneeInitials: 'BM' },
  ],
  in_progress: [
    { id: 'TICKET-102', title: 'CSV export truncates long descriptions', priority: 'high', assignee: 'Bob Martinez', assigneeInitials: 'BM' },
    { id: 'TICKET-105', title: 'Improve ticket search performance', priority: 'medium', assignee: 'Dana Kim', assigneeInitials: 'DK' },
  ],
  resolved: [
    { id: 'TICKET-104', title: 'Dashboard widgets fail to load on Safari', priority: 'high', assignee: 'Alice Chen', assigneeInitials: 'AC' },
  ],
  closed: [
    { id: 'TICKET-106', title: 'Broken link in onboarding wizard step 3', priority: 'low', assignee: 'Eve Johnson', assigneeInitials: 'EJ' },
    { id: 'TICKET-108', title: 'Attachment upload fails for files over 20 MB', priority: 'medium', assignee: 'Charlie Osei', assigneeInitials: 'CO' },
  ],
};

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function findColumn(
  columns: Record<ColumnId, KanbanTicket[]>,
  ticketId: string,
): ColumnId | undefined {
  for (const col of COLUMNS) {
    if (columns[col.id].some((t) => t.id === ticketId)) {
      return col.id;
    }
  }
  return undefined;
}

// ---------------------------------------------------------------------------
// Sortable ticket card
// ---------------------------------------------------------------------------

interface SortableTicketCardProps {
  ticket: KanbanTicket;
  overlay?: boolean;
}

function SortableTicketCard({ ticket, overlay }: SortableTicketCardProps) {
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({ id: ticket.id });

  const style: React.CSSProperties = {
    transform: transform
      ? `translate3d(${transform.x}px, ${transform.y}px, 0)`
      : undefined,
    transition,
    opacity: isDragging ? 0.4 : 1,
  };

  return (
    <div ref={setNodeRef} style={style} {...attributes} {...listeners}>
      <TicketCard ticket={ticket} overlay={overlay} />
    </div>
  );
}

function TicketCard({ ticket, overlay }: SortableTicketCardProps) {
  return (
    <Card
      className={cn(
        'cursor-grab transition-shadow hover:shadow-[var(--shadow-md)]',
        overlay && 'shadow-[var(--shadow-lg)] rotate-2',
      )}
    >
      <CardContent className="space-y-2 p-3">
        <Link
          to={`/tickets/${ticket.id}`}
          className="text-[var(--text-xs)] font-medium text-[var(--color-primary)] hover:underline"
          style={{ fontFamily: 'var(--font-mono)' }}
        >
          {ticket.id}
        </Link>
        <p className="text-[var(--text-sm)] leading-snug text-[var(--color-text)]">
          {ticket.title}
        </p>
        <div className="flex items-center justify-between">
          <Badge variant={PRIORITY_VARIANT[ticket.priority]}>
            {PRIORITY_LABEL[ticket.priority]}
          </Badge>
          <span
            className={cn(
              'flex h-6 w-6 items-center justify-center rounded-full',
              'bg-[var(--color-primary-muted)] text-[var(--text-xs)] font-medium text-[var(--color-primary)]',
            )}
            title={ticket.assignee}
          >
            {ticket.assigneeInitials}
          </span>
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
  tickets: KanbanTicket[];
}

function DroppableColumn({ column, tickets }: DroppableColumnProps) {
  return (
    <div className="flex w-72 shrink-0 flex-col rounded-[var(--radius-lg)] bg-[var(--color-bg)] p-3">
      <div className="mb-3 flex items-center justify-between">
        <h3 className="text-[var(--text-sm)] font-semibold text-[var(--color-text)]">
          {column.label}
        </h3>
        <span className="flex h-5 min-w-[20px] items-center justify-center rounded-full bg-[var(--color-surface-hover)] px-1.5 text-[var(--text-xs)] font-medium text-[var(--color-text-muted)]">
          {tickets.length}
        </span>
      </div>
      <SortableContext
        items={tickets.map((t) => t.id)}
        strategy={verticalListSortingStrategy}
      >
        <div className="flex flex-1 flex-col gap-2">
          {tickets.map((ticket) => (
            <SortableTicketCard key={ticket.id} ticket={ticket} />
          ))}
        </div>
      </SortableContext>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Main component
// ---------------------------------------------------------------------------

/** Kanban board view for service desk tickets with drag-and-drop. */
export function KanbanPage() {
  const [columns, setColumns] = useState<Record<ColumnId, KanbanTicket[]>>(INITIAL_COLUMNS);
  const [activeTicket, setActiveTicket] = useState<KanbanTicket | null>(null);

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
  );

  const handleDragStart = useCallback(
    (event: DragStartEvent) => {
      const id = event.active.id as string;
      const col = findColumn(columns, id);
      if (col) {
        const ticket = columns[col].find((t) => t.id === id);
        if (ticket) setActiveTicket(ticket);
      }
    },
    [columns],
  );

  const handleDragEnd = useCallback(
    (event: DragEndEvent) => {
      setActiveTicket(null);
      const { active, over } = event;
      if (!over) return;

      const activeId = active.id as string;
      const overId = over.id as string;

      const fromCol = findColumn(columns, activeId);
      if (!fromCol) return;

      // Dropping onto another ticket
      const toCol = findColumn(columns, overId) ?? (overId as ColumnId);

      if (fromCol === toCol) {
        // Reorder within column
        const oldIndex = columns[fromCol].findIndex((t) => t.id === activeId);
        const newIndex = columns[fromCol].findIndex((t) => t.id === overId);
        if (oldIndex !== -1 && newIndex !== -1 && oldIndex !== newIndex) {
          setColumns((prev) => ({
            ...prev,
            [fromCol]: arrayMove(prev[fromCol], oldIndex, newIndex),
          }));
        }
      } else {
        // Move between columns
        const ticket = columns[fromCol].find((t) => t.id === activeId);
        if (!ticket) return;

        const targetCol = COLUMNS.find((c) => c.id === toCol) ? toCol : findColumn(columns, overId);
        if (!targetCol) return;

        setColumns((prev) => ({
          ...prev,
          [fromCol]: prev[fromCol].filter((t) => t.id !== activeId),
          [targetCol]: [...prev[targetCol], ticket],
        }));
      }
    },
    [columns],
  );

  return (
    <div className="space-y-6">
      <h1 className="text-[var(--text-2xl)] font-bold text-[var(--color-text)]">
        Kanban Board
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
              tickets={columns[col.id]}
            />
          ))}
        </div>

        <DragOverlay>
          {activeTicket ? (
            <div className="w-72">
              <TicketCard ticket={activeTicket} overlay />
            </div>
          ) : null}
        </DragOverlay>
      </DndContext>
    </div>
  );
}
