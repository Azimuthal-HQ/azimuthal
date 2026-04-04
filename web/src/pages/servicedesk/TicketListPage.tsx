import { useState, useMemo } from 'react';
import { Link } from 'react-router-dom';
import { Plus, Search, AlertTriangle, ArrowUp, Minus, ArrowDown } from 'lucide-react';
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
import { useToast } from '../../components/ui/toast';
import { cn } from '../../lib/utils';
import { createTicket } from '../../lib/api';

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

const INITIAL_TICKETS: TicketRow[] = [
  { id: 'TICKET-101', title: 'Login page returns 500 for SSO users', status: 'open', priority: 'critical', assignee: 'Alice Chen', assigneeInitials: 'AC', created: '2026-03-28' },
  { id: 'TICKET-102', title: 'CSV export truncates long descriptions', status: 'in_progress', priority: 'high', assignee: 'Bob Martinez', assigneeInitials: 'BM', created: '2026-03-27' },
  { id: 'TICKET-103', title: 'Add dark mode support for email templates', status: 'open', priority: 'medium', assignee: 'Charlie Osei', assigneeInitials: 'CO', created: '2026-03-26' },
  { id: 'TICKET-104', title: 'Dashboard widgets fail to load on Safari', status: 'resolved', priority: 'high', assignee: 'Alice Chen', assigneeInitials: 'AC', created: '2026-03-25' },
  { id: 'TICKET-105', title: 'Improve ticket search performance', status: 'in_progress', priority: 'medium', assignee: 'Dana Kim', assigneeInitials: 'DK', created: '2026-03-24' },
  { id: 'TICKET-106', title: 'Broken link in onboarding wizard step 3', status: 'closed', priority: 'low', assignee: 'Eve Johnson', assigneeInitials: 'EJ', created: '2026-03-22' },
  { id: 'TICKET-107', title: 'Rate-limit API responses to prevent abuse', status: 'open', priority: 'high', assignee: 'Bob Martinez', assigneeInitials: 'BM', created: '2026-03-21' },
  { id: 'TICKET-108', title: 'Attachment upload fails for files over 20 MB', status: 'closed', priority: 'medium', assignee: 'Charlie Osei', assigneeInitials: 'CO', created: '2026-03-20' },
];

const MOCK_MEMBERS = [
  { id: 'm1', name: 'Alice Chen', initials: 'AC' },
  { id: 'm2', name: 'Bob Martinez', initials: 'BM' },
  { id: 'm3', name: 'Charlie Osei', initials: 'CO' },
  { id: 'm4', name: 'Dana Kim', initials: 'DK' },
  { id: 'm5', name: 'Eve Johnson', initials: 'EJ' },
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

const PRIORITY_ICON: Record<TicketPriority, typeof AlertTriangle> = {
  critical: AlertTriangle,
  high: ArrowUp,
  medium: Minus,
  low: ArrowDown,
};

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

let ticketCounter = 109;

/** Filterable list/table view of service desk tickets. */
export function TicketListPage() {
  const { toast } = useToast();
  const [tickets, setTickets] = useState(INITIAL_TICKETS);
  const [statusFilter, setStatusFilter] = useState<TicketStatus | 'all'>('all');
  const [priorityFilter, setPriorityFilter] = useState<TicketPriority | 'all'>('all');
  const [search, setSearch] = useState('');

  // Modal state
  const [dialogOpen, setDialogOpen] = useState(false);
  const [formTitle, setFormTitle] = useState('');
  const [formDescription, setFormDescription] = useState('');
  const [formPriority, setFormPriority] = useState<TicketPriority>('medium');
  const [formAssignee, setFormAssignee] = useState('');
  const [assigneeSearch, setAssigneeSearch] = useState('');
  const [assigneeDropdownOpen, setAssigneeDropdownOpen] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  function resetForm() {
    setFormTitle('');
    setFormDescription('');
    setFormPriority('medium');
    setFormAssignee('');
    setAssigneeSearch('');
    setAssigneeDropdownOpen(false);
    setSubmitting(false);
  }

  const filteredMembers = useMemo(() => {
    if (!assigneeSearch) return MOCK_MEMBERS;
    return MOCK_MEMBERS.filter((m) =>
      m.name.toLowerCase().includes(assigneeSearch.toLowerCase()),
    );
  }, [assigneeSearch]);

  const selectedMember = useMemo(
    () => MOCK_MEMBERS.find((m) => m.id === formAssignee),
    [formAssignee],
  );

  async function handleCreate() {
    const title = formTitle.trim();
    if (!title) return;

    setSubmitting(true);

    try {
      const apiCall = createTicket('default', {
        title,
        description: formDescription.trim() || undefined,
        priority: ['critical', 'high', 'medium', 'low'].indexOf(formPriority),
        assignee_id: formAssignee || undefined,
      });
      const timeout = new Promise<never>((_, reject) =>
        setTimeout(() => reject(new Error('timeout')), 3000),
      );
      await Promise.race([apiCall, timeout]);

      toast({ title: 'Ticket created', variant: 'success' });
    } catch {
      // Mock mode fallback — add locally
      const id = `TICKET-${++ticketCounter}`;
      const member = selectedMember;
      const newTicket: TicketRow = {
        id,
        title,
        status: 'open',
        priority: formPriority,
        assignee: member?.name ?? 'Unassigned',
        assigneeInitials: member?.initials ?? '—',
        created: new Date().toISOString().slice(0, 10),
      };
      setTickets((prev) => [newTicket, ...prev]);
      toast({ title: 'Mock mode — backend not connected', variant: 'warning' });
    } finally {
      setSubmitting(false);
      setDialogOpen(false);
      resetForm();
    }
  }

  const filtered = useMemo(() => {
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
                      <span className="text-[var(--text-xs)] font-medium">{PRIORITY_LABEL[p]}</span>
                    </button>
                  );
                })}
              </div>
            </div>

            {/* Assignee */}
            <div className="space-y-2">
              <label className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                Assignee <span className="text-[var(--color-text-muted)] font-normal">(optional)</span>
              </label>
              <div className="relative">
                <Input
                  placeholder="Search team members..."
                  value={selectedMember ? selectedMember.name : assigneeSearch}
                  onChange={(e) => {
                    setAssigneeSearch(e.target.value);
                    setFormAssignee('');
                    setAssigneeDropdownOpen(true);
                  }}
                  onFocus={() => setAssigneeDropdownOpen(true)}
                  onBlur={() => setTimeout(() => setAssigneeDropdownOpen(false), 200)}
                />
                {assigneeDropdownOpen && filteredMembers.length > 0 && (
                  <div className="absolute top-full left-0 right-0 z-10 mt-1 max-h-40 overflow-y-auto rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-surface)] py-1 shadow-[var(--shadow-lg)]">
                    {filteredMembers.map((m) => (
                      <button
                        key={m.id}
                        type="button"
                        onMouseDown={(e) => e.preventDefault()}
                        onClick={() => {
                          setFormAssignee(m.id);
                          setAssigneeSearch('');
                          setAssigneeDropdownOpen(false);
                        }}
                        className={cn(
                          'flex w-full items-center gap-2 px-3 py-2 text-left text-[var(--text-sm)] transition-colors',
                          formAssignee === m.id
                            ? 'bg-[var(--color-primary-muted)] text-[var(--color-primary)]'
                            : 'text-[var(--color-text)] hover:bg-[var(--color-surface-hover)]',
                        )}
                      >
                        <span className={cn(
                          'flex h-6 w-6 items-center justify-center rounded-full',
                          'bg-[var(--color-primary-muted)] text-[var(--text-xs)] font-medium text-[var(--color-primary)]',
                        )}>
                          {m.initials}
                        </span>
                        {m.name}
                      </button>
                    ))}
                  </div>
                )}
              </div>
            </div>
          </div>

          <DialogFooter>
            <DialogClose asChild>
              <Button variant="outline" type="button">Cancel</Button>
            </DialogClose>
            <Button onClick={handleCreate} disabled={submitting || !formTitle.trim()}>
              {submitting ? 'Creating...' : 'Create Ticket'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
