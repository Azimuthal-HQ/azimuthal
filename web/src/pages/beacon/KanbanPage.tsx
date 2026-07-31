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
  useDroppable,
} from '@dnd-kit/core';
import {
  SortableContext,
  useSortable,
  verticalListSortingStrategy,
} from '@dnd-kit/sortable';
import { AlertCircle } from 'lucide-react';
import { PriorityPill, normalizePriority } from '../../components/priority';
import { cn } from '../../lib/utils';
import {
  useTickets,
  useSpace,
  useTicketStatusTransition,
  useMe,
  useMembers,
  friendlyErrorMessage,
  pendingApprovalOf,
  type Ticket,
  type TicketStatus,
} from '../../lib/api';

type MemberName = (id: string | null | undefined) => string | undefined;

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

// ---------------------------------------------------------------------------
// Sortable ticket card
// ---------------------------------------------------------------------------

interface SortableTicketCardProps {
  ticket: Ticket;
  overlay?: boolean;
  spaceId?: string;
  spaceKey?: string;
  memberName?: MemberName;
}

function SortableTicketCard({ ticket, spaceId, spaceKey, memberName }: SortableTicketCardProps) {
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
      <TicketCard ticket={ticket} spaceId={spaceId} spaceKey={spaceKey} memberName={memberName} />
    </div>
  );
}

/** Board card per the dashboards prototype: mono key, title, pill + avatar. */
function TicketCard({ ticket, overlay, spaceId, spaceKey, memberName }: SortableTicketCardProps) {
  const ticketPath = `/beacon/${spaceId}/tickets/${ticket.id}`;
  const assignee = memberName?.(ticket.assignee_id);
  return (
    <div
      className={cn(
        'cursor-grab rounded-[10px] border border-[var(--color-border)] bg-[var(--color-surface)] p-2.5 transition-colors',
        'hover:border-[var(--color-text-muted)]',
        overlay && 'rotate-2 shadow-[var(--shadow-lg)]',
      )}
    >
      <Link
        to={ticketPath}
        className="text-[11px] text-[var(--color-text-muted)] hover:underline"
        style={{ fontFamily: 'var(--font-mono)' }}
      >
        {ticket.number ? `${spaceKey ?? 'SD'}-${ticket.number}` : (ticket.id ?? '').slice(0, 8)}
      </Link>
      <p className="mb-2.5 mt-1.5 text-[13px] leading-[1.4] text-[var(--color-text)]">
        {ticket.title}
      </p>
      <div className="flex items-center">
        <PriorityPill priority={normalizePriority(ticket.priority)} />
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

interface DroppableColumnProps {
  column: ColumnDef;
  tickets: Ticket[];
  spaceId?: string;
  spaceKey?: string;
  memberName?: MemberName;
}

/**
 * Contained column per the dashboards prototype: a bordered container with a
 * quiet header (label + faint count), cards inside, and a contained empty
 * state — never floating labels in open space.
 */
function DroppableColumn({ column, tickets, spaceId, spaceKey, memberName }: DroppableColumnProps) {
  // The column itself must be a droppable: sortable cards only cover the space
  // they occupy, so without this an empty column would reject every drop.
  const { setNodeRef, isOver } = useDroppable({ id: column.id });

  return (
    <div
      ref={setNodeRef}
      data-column-id={column.id}
      className={cn(
        'flex w-72 shrink-0 flex-col rounded-[11px] border border-[var(--color-border)] bg-[var(--color-bg)] p-2',
        isOver && 'ring-2 ring-[var(--color-primary)]',
      )}
    >
      <div className="flex items-center gap-2 px-1.5 pb-2 pt-1">
        <h3 className="text-[var(--text-sm)] font-medium text-[var(--color-text-muted)]">
          {column.label}
        </h3>
        <span className="text-[var(--text-xs)] text-[var(--color-text-muted)]" style={{ fontFamily: 'var(--font-mono)' }}>
          {tickets.length}
        </span>
      </div>
      <SortableContext
        items={tickets.map((t) => t.id)}
        strategy={verticalListSortingStrategy}
      >
        <div className="flex min-h-16 flex-1 flex-col gap-2">
          {tickets.length === 0 && (
            <p className="rounded-[10px] border border-dashed border-[var(--color-border)] px-3 py-4 text-center text-[var(--text-xs)] text-[var(--color-text-muted)]">
              No tickets
            </p>
          )}
          {tickets.map((ticket) => (
            <SortableTicketCard key={ticket.id} ticket={ticket} spaceId={spaceId} spaceKey={spaceKey} memberName={memberName} />
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
  const { data: space } = useSpace(spaceId);
  const { data: tickets, isLoading, error } = useTickets(spaceId);

  // Assignee avatars on the cards (dashboards prototype): resolve ids to
  // display names through the existing members list — rendering only.
  const { data: me } = useMe();
  const { data: members } = useMembers(me?.org_id ?? '', spaceId);
  const memberName = useCallback<MemberName>(
    (id) => (id ? (members ?? []).find((m) => m.user_id === id)?.display_name : undefined),
    [members],
  );
  const [activeTicket, setActiveTicket] = useState<Ticket | null>(null);
  const [transitionError, setTransitionError] = useState<string | null>(null);
  const transitionMutation = useTicketStatusTransition(spaceId);

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
      setTransitionError(null);
      const id = event.active.id as string;
      const ticket = tickets?.find((t) => t.id === id);
      if (ticket) setActiveTicket(ticket);
    },
    [tickets],
  );

  // Resolves a drop target to its column: either the column droppable itself
  // or the column of the card the pointer was over.
  const columnForDropTarget = useCallback(
    (overId: string): ColumnId | null => {
      if (COLUMNS.some((c) => c.id === overId)) return overId as ColumnId;
      const overTicket = tickets?.find((t) => t.id === overId);
      return overTicket && COLUMNS.some((c) => c.id === overTicket.status)
        ? (overTicket.status as ColumnId)
        : null;
    },
    [tickets],
  );

  const handleDragEnd = useCallback(
    (event: DragEndEvent) => {
      setActiveTicket(null);
      const { active, over } = event;
      if (!over) return;

      const ticket = tickets?.find((t) => t.id === active.id);
      const targetColumn = columnForDropTarget(over.id as string);
      if (!ticket || !targetColumn || ticket.status === targetColumn) return;

      transitionMutation.mutate(
        { ticketId: ticket.id, status: targetColumn as TicketStatus },
        {
          // A gated transition answers 202 with a pending-approval body, which
          // is NOT an error, so onError never sees it. Without this branch the
          // mutation "succeeded", the refetch put the card back where it
          // started, and the user watched it snap back with no explanation —
          // the silent no-op PR #86's contract forbids, arriving through the
          // success path rather than the failure one.
          onSuccess: (result) => {
            const pending = pendingApprovalOf(result);
            setTransitionError(pending ? pending.message : null);
          },
          onError: (err) =>
            setTransitionError(
              friendlyErrorMessage(err, `"${ticket.title}" could not be moved.`),
            ),
        },
      );
    },
    [tickets, columnForDropTarget, transitionMutation],
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
      <div className="flex items-center gap-3 rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[color-mix(in_srgb,var(--color-danger)_10%,transparent)] p-4">
        <AlertCircle className="h-5 w-5 text-[var(--color-danger)]" />
        <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
          {friendlyErrorMessage(error, 'The board could not be loaded.')}
        </p>
      </div>
    );
  }

  return (
    <div className="space-y-5">
      <h1 className="text-[var(--text-lg)] font-semibold tracking-[-.01em] text-[var(--color-text)]">
        Board
      </h1>

      {transitionError && (
        <div className="flex items-center gap-3 rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[var(--color-danger)]/10 p-3">
          <AlertCircle className="h-4 w-4 shrink-0 text-[var(--color-danger)]" />
          <p className="text-[var(--text-sm)] text-[var(--color-danger)]">{transitionError}</p>
        </div>
      )}

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
              spaceKey={space?.key}
              memberName={memberName}
            />
          ))}
        </div>

        <DragOverlay>
          {activeTicket ? (
            <div className="w-72">
              <TicketCard ticket={activeTicket} overlay spaceId={spaceId} spaceKey={space?.key} memberName={memberName} />
            </div>
          ) : null}
        </DragOverlay>
      </DndContext>
    </div>
  );
}
