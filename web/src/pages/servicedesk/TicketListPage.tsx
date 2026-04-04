import { useState, useMemo } from 'react';
import { Link } from 'react-router-dom';
import { Plus, Search } from 'lucide-react';
import { Button } from '../../components/ui/button';
import { Badge, type BadgeProps } from '../../components/ui/badge';
import { Input } from '../../components/ui/input';
import { cn } from '../../lib/utils';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type TicketStatus = 'open' | 'in_progress' | 'resolved' | 'closed';
type TicketPriority = 'critical' | 'high' | 'medium' | 'low';

interface TicketRow {
  id: string;
  title: string;
  status: TicketStatus;
  priority: TicketPriority;
  assignee: string;
  assigneeInitials: string;
  created: string;
}

// ---------------------------------------------------------------------------
// Mock data
// ---------------------------------------------------------------------------

const MOCK_TICKETS: TicketRow[] = [
  { id: 'TICKET-101', title: 'Login page returns 500 for SSO users', status: 'open', priority: 'critical', assignee: 'Alice Chen', assigneeInitials: 'AC', created: '2026-03-28' },
  { id: 'TICKET-102', title: 'CSV export truncates long descriptions', status: 'in_progress', priority: 'high', assignee: 'Bob Martinez', assigneeInitials: 'BM', created: '2026-03-27' },
  { id: 'TICKET-103', title: 'Add dark mode support for email templates', status: 'open', priority: 'medium', assignee: 'Charlie Osei', assigneeInitials: 'CO', created: '2026-03-26' },
  { id: 'TICKET-104', title: 'Dashboard widgets fail to load on Safari', status: 'resolved', priority: 'high', assignee: 'Alice Chen', assigneeInitials: 'AC', created: '2026-03-25' },
  { id: 'TICKET-105', title: 'Improve ticket search performance', status: 'in_progress', priority: 'medium', assignee: 'Dana Kim', assigneeInitials: 'DK', created: '2026-03-24' },
  { id: 'TICKET-106', title: 'Broken link in onboarding wizard step 3', status: 'closed', priority: 'low', assignee: 'Eve Johnson', assigneeInitials: 'EJ', created: '2026-03-22' },
  { id: 'TICKET-107', title: 'Rate-limit API responses to prevent abuse', status: 'open', priority: 'high', assignee: 'Bob Martinez', assigneeInitials: 'BM', created: '2026-03-21' },
  { id: 'TICKET-108', title: 'Attachment upload fails for files over 20 MB', status: 'closed', priority: 'medium', assignee: 'Charlie Osei', assigneeInitials: 'CO', created: '2026-03-20' },
];

// ---------------------------------------------------------------------------
// Badge helpers
// ---------------------------------------------------------------------------

const STATUS_VARIANT: Record<TicketStatus, BadgeProps['variant']> = {
  open: 'default',
  in_progress: 'warning',
  resolved: 'success',
  closed: 'secondary',
};

const STATUS_LABEL: Record<TicketStatus, string> = {
  open: 'Open',
  in_progress: 'In Progress',
  resolved: 'Resolved',
  closed: 'Closed',
};

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
// Component
// ---------------------------------------------------------------------------

/** Filterable list/table view of service desk tickets. */
export function TicketListPage() {
  const [statusFilter, setStatusFilter] = useState<TicketStatus | 'all'>('all');
  const [priorityFilter, setPriorityFilter] = useState<TicketPriority | 'all'>('all');
  const [search, setSearch] = useState('');

  const filtered = useMemo(() => {
    return MOCK_TICKETS.filter((t) => {
      if (statusFilter !== 'all' && t.status !== statusFilter) return false;
      if (priorityFilter !== 'all' && t.priority !== priorityFilter) return false;
      if (search && !t.title.toLowerCase().includes(search.toLowerCase()) && !t.id.toLowerCase().includes(search.toLowerCase())) return false;
      return true;
    });
  }, [statusFilter, priorityFilter, search]);

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <h1 className="text-[var(--text-2xl)] font-bold text-[var(--color-text)]">
          Tickets
        </h1>
        <Button>
          <Plus className="mr-2 h-4 w-4" />
          New Ticket
        </Button>
      </div>

      {/* Filter bar */}
      <div className="flex flex-wrap items-center gap-3">
        <div className="relative flex-1 min-w-[200px] max-w-xs">
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-[var(--color-text-muted)]" />
          <Input
            placeholder="Search tickets..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="pl-9"
          />
        </div>

        <select
          value={statusFilter}
          onChange={(e) => setStatusFilter(e.target.value as TicketStatus | 'all')}
          className={cn(
            'h-9 rounded-[var(--radius-md)] border border-[var(--color-border)]',
            'bg-[var(--color-surface)] px-3 text-[var(--text-sm)] text-[var(--color-text)]',
            'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)]',
          )}
        >
          <option value="all">All Statuses</option>
          <option value="open">Open</option>
          <option value="in_progress">In Progress</option>
          <option value="resolved">Resolved</option>
          <option value="closed">Closed</option>
        </select>

        <select
          value={priorityFilter}
          onChange={(e) => setPriorityFilter(e.target.value as TicketPriority | 'all')}
          className={cn(
            'h-9 rounded-[var(--radius-md)] border border-[var(--color-border)]',
            'bg-[var(--color-surface)] px-3 text-[var(--text-sm)] text-[var(--color-text)]',
            'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)]',
          )}
        >
          <option value="all">All Priorities</option>
          <option value="critical">Critical</option>
          <option value="high">High</option>
          <option value="medium">Medium</option>
          <option value="low">Low</option>
        </select>
      </div>

      {/* Table */}
      <div className="overflow-x-auto rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-surface)]">
        <table className="w-full text-left text-[var(--text-sm)]">
          <thead>
            <tr className="border-b border-[var(--color-border)]">
              <th className="whitespace-nowrap px-4 py-3 font-medium text-[var(--color-text-muted)]">ID</th>
              <th className="px-4 py-3 font-medium text-[var(--color-text-muted)]">Title</th>
              <th className="px-4 py-3 font-medium text-[var(--color-text-muted)]">Status</th>
              <th className="px-4 py-3 font-medium text-[var(--color-text-muted)]">Priority</th>
              <th className="px-4 py-3 font-medium text-[var(--color-text-muted)]">Assignee</th>
              <th className="px-4 py-3 font-medium text-[var(--color-text-muted)]">Created</th>
            </tr>
          </thead>
          <tbody>
            {filtered.map((ticket) => (
              <tr
                key={ticket.id}
                className="border-b border-[var(--color-border)] last:border-b-0 hover:bg-[var(--color-surface-hover)] transition-colors"
              >
                <td className="whitespace-nowrap px-4 py-3">
                  <Link
                    to={`/tickets/${ticket.id}`}
                    className="font-[var(--font-mono)] text-[var(--color-primary)] hover:underline"
                    style={{ fontFamily: 'var(--font-mono)' }}
                  >
                    {ticket.id}
                  </Link>
                </td>
                <td className="px-4 py-3 text-[var(--color-text)]">
                  <Link to={`/tickets/${ticket.id}`} className="hover:underline">
                    {ticket.title}
                  </Link>
                </td>
                <td className="px-4 py-3">
                  <Badge variant={STATUS_VARIANT[ticket.status]}>
                    {STATUS_LABEL[ticket.status]}
                  </Badge>
                </td>
                <td className="px-4 py-3">
                  <Badge variant={PRIORITY_VARIANT[ticket.priority]}>
                    {PRIORITY_LABEL[ticket.priority]}
                  </Badge>
                </td>
                <td className="px-4 py-3">
                  <div className="flex items-center gap-2">
                    <span
                      className={cn(
                        'flex h-6 w-6 items-center justify-center rounded-full',
                        'bg-[var(--color-primary-muted)] text-[var(--text-xs)] font-medium text-[var(--color-primary)]',
                      )}
                    >
                      {ticket.assigneeInitials}
                    </span>
                    <span className="text-[var(--color-text)]">{ticket.assignee}</span>
                  </div>
                </td>
                <td className="whitespace-nowrap px-4 py-3 text-[var(--color-text-muted)]">
                  {ticket.created}
                </td>
              </tr>
            ))}

            {filtered.length === 0 && (
              <tr>
                <td colSpan={6} className="px-4 py-8 text-center text-[var(--color-text-muted)]">
                  No tickets match the current filters.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
