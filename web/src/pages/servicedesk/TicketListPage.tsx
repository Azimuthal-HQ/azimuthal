import { useState, useMemo } from 'react';
import { Link, useParams } from 'react-router-dom';
import { Plus, Search, AlertTriangle, ArrowUp, Minus, ArrowDown, AlertCircle } from 'lucide-react';
import { Button } from '../../components/ui/button';
import { Badge, type BadgeProps } from '../../components/ui/badge';
import { Input } from '../../components/ui/input';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
  DialogClose,
} from '../../components/ui/dialog';
import { cn } from '../../lib/utils';
import { useTickets, useCreateTicket, type TicketStatus } from '../../lib/api';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type TicketPriority = 'critical' | 'high' | 'medium' | 'low';

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

const PRIORITY_ICON: Record<TicketPriority, typeof AlertTriangle> = {
  critical: AlertTriangle,
  high: ArrowUp,
  medium: Minus,
  low: ArrowDown,
};

const PRIORITY_NAME_TO_API: Record<TicketPriority, string> = {
  critical: 'urgent',
  high: 'high',
  medium: 'medium',
  low: 'low',
};

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

/** Filterable list/table view of service desk tickets. */
export function TicketListPage() {
  const { spaceId = '' } = useParams<{ spaceId: string }>();
  const { data: tickets, isLoading, error } = useTickets(spaceId);
  const createTicketMutation = useCreateTicket(spaceId);

  const [statusFilter, setStatusFilter] = useState<TicketStatus | 'all'>('all');
  const [priorityFilter, setPriorityFilter] = useState<number | 'all'>('all');
  const [search, setSearch] = useState('');

  // Modal state
  const [dialogOpen, setDialogOpen] = useState(false);
  const [formTitle, setFormTitle] = useState('');
  const [formDescription, setFormDescription] = useState('');
  const [formPriority, setFormPriority] = useState<TicketPriority>('medium');

  function resetForm() {
    setFormTitle('');
    setFormDescription('');
    setFormPriority('medium');
  }

  async function handleCreate() {
    const title = formTitle.trim();
    if (!title) return;

    const body = {
      title,
      description: formDescription.trim() || undefined,
      priority: PRIORITY_NAME_TO_API[formPriority],
    };
    console.log('[TicketListPage] Creating ticket:', JSON.stringify(body));

    try {
      await createTicketMutation.mutateAsync(body);
      setDialogOpen(false);
      resetForm();
    } catch (err) {
      console.error('[TicketListPage] Create ticket error:', err);
    }
  }

  const filtered = useMemo(() => {
    if (!tickets) return [];
    return tickets.filter((t) => {
      if (statusFilter !== 'all' && t.status !== statusFilter) return false;
      if (priorityFilter !== 'all' && t.priority !== priorityFilter) return false;
      if (search && !t.title.toLowerCase().includes(search.toLowerCase()) && !t.id.toLowerCase().includes(search.toLowerCase())) return false;
      return true;
    });
  }, [tickets, statusFilter, priorityFilter, search]);

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <h1 className="text-[var(--text-2xl)] font-bold text-[var(--color-text)]">
          Tickets
        </h1>
        <Button onClick={() => setDialogOpen(true)}>
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
          onChange={(e) => {
            const val = e.target.value;
            setPriorityFilter(val === 'all' ? 'all' : Number(val));
          }}
          className={cn(
            'h-9 rounded-[var(--radius-md)] border border-[var(--color-border)]',
            'bg-[var(--color-surface)] px-3 text-[var(--text-sm)] text-[var(--color-text)]',
            'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)]',
          )}
        >
          <option value="all">All Priorities</option>
          <option value="0">Critical</option>
          <option value="1">High</option>
          <option value="2">Medium</option>
          <option value="3">Low</option>
        </select>
      </div>

      {/* Loading */}
      {isLoading && (
        <div className="flex h-32 items-center justify-center text-[var(--color-text-muted)]">
          Loading tickets...
        </div>
      )}

      {/* Error */}
      {error && (
        <div className="flex items-center gap-3 rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[var(--color-danger)]/10 p-4">
          <AlertCircle className="h-5 w-5 text-[var(--color-danger)]" />
          <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
            Failed to load tickets: {error.message}
          </p>
        </div>
      )}

      {/* Table */}
      {tickets && (
        <div className="overflow-x-auto rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-surface)]">
          <table className="w-full text-left text-[var(--text-sm)]">
            <thead>
              <tr className="border-b border-[var(--color-border)]">
                <th className="whitespace-nowrap px-4 py-3 font-medium text-[var(--color-text-muted)]">ID</th>
                <th className="px-4 py-3 font-medium text-[var(--color-text-muted)]">Title</th>
                <th className="px-4 py-3 font-medium text-[var(--color-text-muted)]">Status</th>
                <th className="px-4 py-3 font-medium text-[var(--color-text-muted)]">Priority</th>
                <th className="whitespace-nowrap px-4 py-3 font-medium text-[var(--color-text-muted)]">Created</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((ticket) => {
                const ticketPath = `/spaces/${spaceId}/tickets/${ticket.id}`;
                return (
                  <tr
                    key={ticket.id}
                    className="border-b border-[var(--color-border)] last:border-b-0 hover:bg-[var(--color-surface-hover)] transition-colors"
                  >
                    <td className="whitespace-nowrap px-4 py-3">
                      <Link
                        to={ticketPath}
                        className="font-[var(--font-mono)] text-[var(--color-primary)] hover:underline"
                        style={{ fontFamily: 'var(--font-mono)' }}
                      >
                        {(ticket.id ?? '').slice(0, 8)}
                      </Link>
                    </td>
                    <td className="px-4 py-3 text-[var(--color-text)]">
                      <Link to={ticketPath} className="hover:underline">
                        {ticket.title}
                      </Link>
                    </td>
                    <td className="px-4 py-3">
                      <Badge variant={STATUS_VARIANT[ticket.status]}>
                        {STATUS_LABEL[ticket.status]}
                      </Badge>
                    </td>
                    <td className="px-4 py-3">
                      <Badge variant={PRIORITY_VARIANT[ticket.priority] ?? 'secondary'}>
                        {PRIORITY_LABEL[ticket.priority] ?? 'Unknown'}
                      </Badge>
                    </td>
                    <td className="whitespace-nowrap px-4 py-3 text-[var(--color-text-muted)]">
                      {(ticket.created_at ?? '').slice(0, 10)}
                    </td>
                  </tr>
                );
              })}

              {filtered.length === 0 && !isLoading && (
                <tr>
                  <td colSpan={5} className="px-4 py-8 text-center text-[var(--color-text-muted)]">
                    No tickets match the current filters.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      )}

      {/* New Ticket dialog */}
      <Dialog open={dialogOpen} onOpenChange={(open) => { setDialogOpen(open); if (!open) resetForm(); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>New Ticket</DialogTitle>
            <DialogDescription>
              Create a service desk ticket to track an issue or request.
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-4 py-2">
            {/* Title */}
            <div className="space-y-2">
              <label htmlFor="ticket-title" className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                Title
              </label>
              <Input
                id="ticket-title"
                placeholder="e.g. Login page returns 500 error"
                value={formTitle}
                onChange={(e) => setFormTitle(e.target.value)}
                autoFocus
              />
            </div>

            {/* Description */}
            <div className="space-y-2">
              <label htmlFor="ticket-desc" className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                Description <span className="text-[var(--color-text-muted)] font-normal">(optional)</span>
              </label>
              <textarea
                id="ticket-desc"
                placeholder="Describe the issue, steps to reproduce, expected vs actual behaviour"
                value={formDescription}
                onChange={(e) => setFormDescription(e.target.value)}
                rows={3}
                className={cn(
                  'flex w-full rounded-[var(--radius-md)] border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-[var(--text-sm)] text-[var(--color-text)] shadow-[var(--shadow-sm)] transition-colors placeholder:text-[var(--color-text-muted)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)] focus-visible:ring-offset-1 resize-y',
                )}
              />
            </div>

            {/* Priority */}
            <div className="space-y-2">
              <label className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">Priority</label>
              <div className="grid grid-cols-4 gap-2">
                {(['critical', 'high', 'medium', 'low'] as const).map((p) => {
                  const Icon = PRIORITY_ICON[p];
                  return (
                    <button
                      key={p}
                      type="button"
                      onClick={() => setFormPriority(p)}
                      className={cn(
                        'flex flex-col items-center gap-1.5 rounded-[var(--radius-lg)] border p-3 transition-colors',
                        formPriority === p
                          ? 'border-[var(--color-primary)] bg-[var(--color-primary-muted)] text-[var(--color-primary)]'
                          : 'border-[var(--color-border)] text-[var(--color-text-muted)] hover:border-[var(--color-text-muted)] hover:text-[var(--color-text)]',
                      )}
                    >
                      <Icon className="h-4 w-4" />
                      <span className="text-[var(--text-xs)] font-medium">{p.charAt(0).toUpperCase() + p.slice(1)}</span>
                    </button>
                  );
                })}
              </div>
            </div>

            {createTicketMutation.error && (
              <p className="text-[var(--text-sm)] text-[var(--color-danger)]">{createTicketMutation.error.message}</p>
            )}
          </div>

          <DialogFooter>
            <DialogClose asChild>
              <Button variant="outline" type="button">Cancel</Button>
            </DialogClose>
            <Button onClick={handleCreate} disabled={createTicketMutation.isPending || !formTitle.trim()}>
              {createTicketMutation.isPending ? 'Creating...' : 'Create Ticket'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
