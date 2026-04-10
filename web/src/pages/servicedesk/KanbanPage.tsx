import { useState, useCallback, useMemo } from 'react';
import { Link, useParams } from 'react-router-dom';
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
import { useTickets, type Ticket } from '../../lib/api';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type ColumnId = 'open' | 'in_progress' | 'resolved' | 'closed';

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

const PRIORITY_VARIANT: Record<number, BadgeProps['variant']> = {
  0: 'danger',
  1: 'warning',
  2: 'secondary',
  3: 'outline',
};

const PRIORITY_LABEL: Record<number, string> = {
  0: 'Critical',
  1: 'High',
  2: 'Medium',
  3: 'Low',
};

// ---------------------------------------------------------------------------
// Sortable ticket card
// ---------------------------------------------------------------------------

interface SortableTicketCardProps {
  ticket: Ticket;
  overlay?: boolean;
  spaceId?: string;
}

function SortableTicketCard({ ticket, spaceId }: { ticket: Ticket; spaceId?: string }) {
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
      <TicketCard ticket={ticket} spaceId={spaceId} />
    </div>
  );
}

function TicketCard({ ticket, overlay, spaceId }: SortableTicketCardProps) {
  const ticketPath = `/spaces/${spaceId}/tickets/${ticket.id}`;
  return (
    <Card
      className={cn(
        'cursor-grab transition-shadow hover:shadow-[var(--shadow-md)]',
        overlay && 'shadow-[var(--shadow-lg)] rotate-2',
      )}
    >
      <CardContent className="space-y-2 p-3">
        <Link
          to={ticketPath}
          className="text-[var(--text-xs)] font-medium text-[var(--color-primary)] hover:underline"
          style={{ fontFamily: 'var(--font-mono)' }}
        >
          {(ticket.id ?? '').slice(0, 8)}
        </Link>
        <p className="text-[var(--text-sm)] leading-snug text-[var(--color-text)]">
          {ticket.title}
        </p>
        <div className="flex items-center justify-between">
          <Badge variant={PRIORITY_VARIANT[ticket.priority] ?? 'secondary'}>
            {PRIORITY_LABEL[ticket.priority] ?? 'Unknown'}
          </Badge>
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
  tickets: Ticket[];
  spaceId?: string;
}

function DroppableColumn({ column, tickets, spaceId }: DroppableColumnProps) {
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
            <SortableTicketCard key={ticket.id} ticket={ticket} spaceId={spaceId} />
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
  const { spaceId = '' } = useParams<{ spaceId: string }>();
  const { data: tickets, isLoading, error } = useTickets(spaceId);
  const [activeTicket, setActiveTicket] = useState<Ticket | null>(null);

  const columns = useMemo(() => {
    const map: Record<ColumnId, Ticket[]> = {
      open: [],
      in_progress: [],
      resolved: [],
      closed: [],
    };
    if (tickets) {
      for (const t of tickets) {
        if (map[t.status as ColumnId]) {
          map[t.status as ColumnId].push(t);
        }
      }
    }
    return map;
  }, [tickets]);

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
  );

  const handleDragStart = useCallback(
    (event: DragStartEvent) => {
      const id = event.active.id as string;
      const ticket = tickets?.find((t) => t.id === id);
      if (ticket) setActiveTicket(ticket);
    },
    [tickets],
  );

  const handleDragEnd = useCallback(
    (_event: DragEndEvent) => {
      setActiveTicket(null);
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
          Failed to load tickets: {error.message}
        </p>
      </div>
    );
  }

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
              spaceId={spaceId}
            />
          ))}
        </div>

        <DragOverlay>
          {activeTicket ? (
            <div className="w-72">
              <TicketCard ticket={activeTicket} overlay spaceId={spaceId} />
            </div>
          ) : null}
        </DragOverlay>
      </DndContext>
    </div>
  );
}
