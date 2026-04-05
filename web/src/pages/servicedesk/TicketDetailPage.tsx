import { useState } from 'react';
import { useParams, Link } from 'react-router-dom';
import { ChevronRight, MessageSquare, Clock, AlertCircle } from 'lucide-react';
import { Button } from '../../components/ui/button';
import { Badge, type BadgeProps } from '../../components/ui/badge';
import { Input } from '../../components/ui/input';
import { Card, CardContent } from '../../components/ui/card';
import { cn } from '../../lib/utils';
import { useTicket, useUpdateTicket, type TicketStatus } from '../../lib/api';

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

const PRIORITY_LABEL: Record<number, string> = {
  0: 'Critical',
  1: 'High',
  2: 'Medium',
  3: 'Low',
};

const PRIORITY_VARIANT: Record<number, BadgeProps['variant']> = {
  0: 'danger',
  1: 'warning',
  2: 'secondary',
  3: 'outline',
};

const ALL_STATUSES: TicketStatus[] = ['open', 'in_progress', 'resolved', 'closed'];

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

/** Detail page for a single service desk ticket. */
export function TicketDetailPage() {
  const { spaceId, ticketId } = useParams<{ spaceId: string; ticketId: string }>();
  const effectiveSpaceId = spaceId ?? 'default';
  const { data: ticket, isLoading, error } = useTicket(effectiveSpaceId, ticketId ?? '');
  const updateMutation = useUpdateTicket(effectiveSpaceId, ticketId ?? '');
  const [commentText, setCommentText] = useState('');

  function handleStatusChange(newStatus: TicketStatus) {
    updateMutation.mutate({ status: newStatus });
  }

  if (isLoading) {
    return (
      <div className="flex h-64 items-center justify-center text-[var(--color-text-muted)]">
        Loading ticket...
      </div>
    );
  }

  if (error) {
    return (
      <div className="flex items-center gap-3 rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[var(--color-danger)]/10 p-4">
        <AlertCircle className="h-5 w-5 text-[var(--color-danger)]" />
        <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
          {error.status === 404 ? 'Ticket not found.' : `Failed to load ticket: ${error.message}`}
        </p>
      </div>
    );
  }

  if (!ticket) {
    return (
      <div className="flex h-64 items-center justify-center text-[var(--color-text-muted)]">
        Ticket not found.
      </div>
    );
  }

  const ticketsPath = spaceId ? `/spaces/${spaceId}/tickets` : '/tickets';

  return (
    <div className="space-y-6">
      {/* Breadcrumb */}
      <nav className="flex items-center gap-1 text-[var(--text-sm)] text-[var(--color-text-muted)]">
        <Link to={ticketsPath} className="hover:text-[var(--color-text)]">
          Tickets
        </Link>
        <ChevronRight className="h-4 w-4" />
        <span className="text-[var(--color-text)]">{ticket.id.slice(0, 8)}</span>
      </nav>

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
        {/* Main content */}
        <div className="space-y-6 lg:col-span-2">
          <h1 className="text-[var(--text-2xl)] font-bold text-[var(--color-text)]">
            {ticket.title}
          </h1>

          {/* Description */}
          {ticket.description && (
            <Card>
              <CardContent className="p-5">
                <div className="whitespace-pre-wrap text-[var(--text-sm)] leading-relaxed text-[var(--color-text)]">
                  {ticket.description}
                </div>
              </CardContent>
            </Card>
          )}
        </div>

        {/* Sidebar */}
        <div className="space-y-4">
          <Card>
            <CardContent className="space-y-5 p-5">
              {/* Status */}
              <div>
                <label className="mb-1 block text-[var(--text-xs)] font-medium uppercase tracking-wider text-[var(--color-text-muted)]">
                  Status
                </label>
                <div className="flex items-center gap-2">
                  <Badge variant={STATUS_VARIANT[ticket.status]}>
                    {STATUS_LABEL[ticket.status]}
                  </Badge>
                  <select
                    value={ticket.status}
                    onChange={(e) => handleStatusChange(e.target.value as TicketStatus)}
                    className={cn(
                      'h-9 flex-1 rounded-[var(--radius-md)] border border-[var(--color-border)]',
                      'bg-[var(--color-surface)] px-3 text-[var(--text-sm)] text-[var(--color-text)]',
                      'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)]',
                    )}
                  >
                    {ALL_STATUSES.map((s) => (
                      <option key={s} value={s}>{STATUS_LABEL[s]}</option>
                    ))}
                  </select>
                </div>
              </div>

              {/* Priority */}
              <div>
                <label className="mb-1 block text-[var(--text-xs)] font-medium uppercase tracking-wider text-[var(--color-text-muted)]">
                  Priority
                </label>
                <Badge variant={PRIORITY_VARIANT[ticket.priority] ?? 'secondary'}>
                  {PRIORITY_LABEL[ticket.priority] ?? 'Unknown'}
                </Badge>
              </div>

              {/* Dates */}
              <div className="space-y-2 border-t border-[var(--color-border)] pt-4">
                <div className="flex items-center gap-2 text-[var(--text-xs)] text-[var(--color-text-muted)]">
                  <Clock className="h-3 w-3" />
                  Created {ticket.created_at.slice(0, 10)}
                </div>
                <div className="flex items-center gap-2 text-[var(--text-xs)] text-[var(--color-text-muted)]">
                  <Clock className="h-3 w-3" />
                  Updated {ticket.updated_at.slice(0, 10)}
                </div>
              </div>
            </CardContent>
          </Card>
        </div>
      </div>
    </div>
  );
}
