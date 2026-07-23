import { useState, useMemo } from 'react';
import { Link, useParams } from 'react-router-dom';
import { Plus, Search, AlertCircle } from 'lucide-react';
import { Button } from '../../components/ui/button';
import { Badge, type BadgeProps } from '../../components/ui/badge';
import { Input } from '../../components/ui/input';
import { Field, FieldLabel } from '../../components/ui/field';
import { SegmentedControl } from '../../components/ui/segmented';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
  DialogClose,
} from '../../components/ui/dialog';
import {
  PRIORITY_SEGMENT_OPTIONS,
  PRIORITY_TO_API,
  PriorityPill,
  normalizePriority,
  type PriorityKey,
} from '../../components/priority';
import { cn } from '../../lib/utils';
import {
  useTickets,
  useCreateTicket,
  useSpace,
  friendlyErrorMessage,
  type TicketStatus,
} from '../../lib/api';

// ---------------------------------------------------------------------------
// Status vocabulary
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

const filterSelectClass = cn(
  'h-9 rounded-[var(--radius-lg)] border border-[var(--color-border)]',
  'bg-[var(--color-input)] px-3 text-[var(--text-sm)] text-[var(--color-text)]',
  'focus-visible:outline-none focus-visible:border-[var(--color-primary)] focus-visible:ring-1 focus-visible:ring-[var(--color-primary)]',
);

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

/** Filterable list/table view of service desk tickets. */
export function TicketListPage() {
  const { spaceId = '' } = useParams<{ spaceId: string }>();
  const { data: space } = useSpace(spaceId);
  const { data: tickets, isLoading, error } = useTickets(spaceId);
  const createTicketMutation = useCreateTicket(spaceId);

  const [statusFilter, setStatusFilter] = useState<TicketStatus | 'all'>('all');
  const [priorityFilter, setPriorityFilter] = useState<string>('all');
  const [search, setSearch] = useState('');

  // Modal state
  const [dialogOpen, setDialogOpen] = useState(false);
  const [formTitle, setFormTitle] = useState('');
  const [formDescription, setFormDescription] = useState('');
  const [formPriority, setFormPriority] = useState<PriorityKey>('medium');

  function resetForm() {
    setFormTitle('');
    setFormDescription('');
    setFormPriority('medium');
  }

  async function handleCreate() {
    const title = formTitle.trim();
    if (!title) return;

    try {
      await createTicketMutation.mutateAsync({
        title,
        description: formDescription.trim() || '',
        priority: PRIORITY_TO_API[formPriority],
      });
      setDialogOpen(false);
      resetForm();
    } catch {
      // Surfaced below through friendlyErrorMessage.
    }
  }

  const filtered = useMemo(() => {
    if (!tickets) return [];
    return tickets.filter((t) => {
      if (statusFilter !== 'all' && t.status !== statusFilter) return false;
      if (priorityFilter !== 'all' && String(t.priority).toLowerCase() !== priorityFilter) return false;
      if (search && !t.title.toLowerCase().includes(search.toLowerCase()) && !t.id.toLowerCase().includes(search.toLowerCase())) return false;
      return true;
    });
  }, [tickets, statusFilter, priorityFilter, search]);

  return (
    <div className="space-y-5">
      {/* Header */}
      <div className="flex items-center justify-between">
        <h1 className="text-[var(--text-lg)] font-semibold tracking-[-.01em] text-[var(--color-text)]">
          Tickets
        </h1>
        <Button onClick={() => setDialogOpen(true)}>
          <Plus className="mr-2 h-4 w-4" />
          New Ticket
        </Button>
      </div>

      {/* Filter bar */}
      <div className="flex flex-wrap items-center gap-3">
        <div className="relative min-w-[200px] max-w-xs flex-1">
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
          className={filterSelectClass}
        >
          <option value="all">All Statuses</option>
          <option value="open">Open</option>
          <option value="in_progress">In Progress</option>
          <option value="resolved">Resolved</option>
          <option value="closed">Closed</option>
        </select>

        <select
          value={priorityFilter}
          onChange={(e) => setPriorityFilter(e.target.value)}
          className={filterSelectClass}
        >
          <option value="all">All Priorities</option>
          <option value="critical">Critical</option>
          <option value="high">High</option>
          <option value="medium">Medium</option>
          <option value="low">Low</option>
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
        <div className="flex items-center gap-3 rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[color-mix(in_srgb,var(--color-danger)_10%,transparent)] p-4">
          <AlertCircle className="h-5 w-5 text-[var(--color-danger)]" />
          <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
            {friendlyErrorMessage(error, 'Tickets could not be loaded.')}
          </p>
        </div>
      )}

      {/* Table */}
      {tickets && (
        <div className="overflow-x-auto rounded-[var(--radius-xl)] border border-[var(--color-border)] bg-[var(--color-surface)]">
          <table className="w-full text-left text-[13px]">
            <thead>
              <tr className="border-b border-[var(--color-border)]">
                <th className="whitespace-nowrap px-3 py-2.5 text-[11px] font-normal uppercase tracking-[.04em] text-[var(--color-text-muted)]">ID</th>
                <th className="px-3 py-2.5 text-[11px] font-normal uppercase tracking-[.04em] text-[var(--color-text-muted)]">Title</th>
                <th className="px-3 py-2.5 text-[11px] font-normal uppercase tracking-[.04em] text-[var(--color-text-muted)]">Status</th>
                <th className="px-3 py-2.5 text-[11px] font-normal uppercase tracking-[.04em] text-[var(--color-text-muted)]">Priority</th>
                <th className="whitespace-nowrap px-3 py-2.5 text-[11px] font-normal uppercase tracking-[.04em] text-[var(--color-text-muted)]">Created</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((ticket) => {
                const ticketPath = `/beacon/${spaceId}/tickets/${ticket.id}`;
                return (
                  <tr
                    key={ticket.id}
                    className="border-b border-[var(--color-border)] transition-colors last:border-b-0 hover:bg-[var(--color-surface-hover)]"
                  >
                    <td className="whitespace-nowrap px-3 py-3">
                      <Link
                        to={ticketPath}
                        className="text-[var(--text-xs)] text-[var(--color-primary)] hover:underline"
                        style={{ fontFamily: 'var(--font-mono)' }}
                      >
                        {ticket.number ? `${space?.key ?? 'SD'}-${ticket.number}` : (ticket.id ?? '').slice(0, 8)}
                      </Link>
                    </td>
                    <td className="px-3 py-3 text-[var(--color-text)]">
                      <Link to={ticketPath} className="hover:underline">
                        {ticket.title}
                      </Link>
                    </td>
                    <td className="px-3 py-3">
                      <Badge variant={STATUS_VARIANT[ticket.status]}>
                        {STATUS_LABEL[ticket.status]}
                      </Badge>
                    </td>
                    <td className="px-3 py-3">
                      <PriorityPill priority={normalizePriority(ticket.priority)} />
                    </td>
                    <td className="whitespace-nowrap px-3 py-3 text-[var(--color-text-muted)]">
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

          <div className="py-2">
            <Field>
              <FieldLabel htmlFor="ticket-title">Title</FieldLabel>
              <Input
                id="ticket-title"
                placeholder="e.g. Login page returns 500 error"
                value={formTitle}
                onChange={(e) => setFormTitle(e.target.value)}
                autoFocus
              />
            </Field>

            <Field>
              <FieldLabel htmlFor="ticket-desc" optional>
                Description
              </FieldLabel>
              <textarea
                id="ticket-desc"
                placeholder="Describe the issue, steps to reproduce, expected vs actual behaviour"
                value={formDescription}
                onChange={(e) => setFormDescription(e.target.value)}
                rows={3}
                className={cn(
                  'flex w-full resize-y rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-input)] px-3 py-2 text-[var(--text-sm)] text-[var(--color-text)] transition-colors placeholder:text-[var(--color-text-muted)] focus-visible:outline-none focus-visible:border-[var(--color-primary)] focus-visible:ring-1 focus-visible:ring-[var(--color-primary)]',
                )}
              />
            </Field>

            <Field>
              <FieldLabel id="ticket-priority-label">Priority</FieldLabel>
              <SegmentedControl
                options={PRIORITY_SEGMENT_OPTIONS}
                value={formPriority}
                onChange={setFormPriority}
                aria-label="Priority"
              />
            </Field>

            {createTicketMutation.error && (
              <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
                {friendlyErrorMessage(createTicketMutation.error, 'The ticket could not be created.')}
              </p>
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
