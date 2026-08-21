import type { Ticket, WorkflowState } from '../../lib/api';

/** A board column. `id` is the status text a ticket in the column carries. */
export interface KanbanColumn {
  id: string;
  label: string;
  /** The state's configured colour, when it comes from a workflow state. */
  color?: string;
}

/**
 * The well-known service-desk statuses. Used ONLY as a fallback when a space
 * has neither a workflow (so no states) nor a single ticket — so the board is a
 * familiar empty shell rather than blank. A configured space never reaches it.
 */
export const DEFAULT_KANBAN_COLUMNS: KanbanColumn[] = [
  { id: 'open', label: 'Open' },
  { id: 'in_progress', label: 'In Progress' },
  { id: 'resolved', label: 'Resolved' },
  { id: 'closed', label: 'Closed' },
];

/**
 * Derives the board's columns from the space workflow's states rather than a
 * hardcoded category set. Each state becomes a column, in workflow position
 * order, coloured as configured (migration 016: workflow_states carries
 * name/category/color/position).
 *
 * This is the fix for tickets in CUSTOM states rendering nowhere: the old board
 * hardcoded {open, in_progress, resolved, closed} and silently dropped every
 * ticket whose status was anything else. Now every state the workflow actually
 * declares has a column.
 *
 * Two safety nets keep the board from ever hiding work — because a ticket in no
 * column is the exact defect this fixes:
 *   - a status matching no state (a renamed state, or a workflow-less space)
 *     still earns a column of its own; and
 *   - a space with neither a workflow nor a ticket falls back to the well-known
 *     set, so an unconfigured board is empty-but-familiar, not blank.
 */
export function buildKanbanColumns(
  states: WorkflowState[] | undefined,
  tickets: Ticket[] | undefined,
): KanbanColumn[] {
  const cols: KanbanColumn[] = (states ?? [])
    .slice()
    .sort((a, b) => a.position - b.position)
    .map((s) => ({ id: s.name, label: s.name, color: s.color }));

  const known = new Set(cols.map((c) => c.id));
  for (const t of tickets ?? []) {
    if (!known.has(t.status)) {
      known.add(t.status);
      cols.push({ id: t.status, label: t.status });
    }
  }

  return cols.length > 0 ? cols : DEFAULT_KANBAN_COLUMNS;
}
