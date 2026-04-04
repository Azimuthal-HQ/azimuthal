import { useState } from 'react';
import { useParams, Link } from 'react-router-dom';
import { ChevronRight, MessageSquare, Clock, User } from 'lucide-react';
import { Button } from '../../components/ui/button';
import { Badge, type BadgeProps } from '../../components/ui/badge';
import { Input } from '../../components/ui/input';
import { Card, CardContent } from '../../components/ui/card';
import { cn } from '../../lib/utils';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type TicketStatus = 'open' | 'in_progress' | 'resolved' | 'closed';
type TicketPriority = 'critical' | 'high' | 'medium' | 'low';

interface Comment {
  id: string;
  author: string;
  authorInitials: string;
  body: string;
  timestamp: string;
}

interface TicketDetail {
  id: string;
  title: string;
  description: string;
  status: TicketStatus;
  priority: TicketPriority;
  assignee: string;
  assigneeInitials: string;
  reporter: string;
  reporterInitials: string;
  labels: string[];
  created: string;
  updated: string;
  comments: Comment[];
}

// ---------------------------------------------------------------------------
// Mock data
// ---------------------------------------------------------------------------

const MOCK_TICKETS: Record<string, TicketDetail> = {
  'TICKET-101': {
    id: 'TICKET-101',
    title: 'Login page returns 500 for SSO users',
    description:
      'When SSO users attempt to log in via the SAML provider, the server responds with a 500 Internal Server Error. This appears to be related to a recent change in the session middleware that does not handle the SAML callback correctly.\n\nSteps to reproduce:\n1. Navigate to /login\n2. Click "Sign in with SSO"\n3. Complete authentication with the identity provider\n4. Observe the 500 error on redirect back\n\nExpected: The user should be redirected to the dashboard.\n\nThis is blocking all SSO-enabled customers.',
    status: 'open',
    priority: 'critical',
    assignee: 'Alice Chen',
    assigneeInitials: 'AC',
    reporter: 'Dana Kim',
    reporterInitials: 'DK',
    labels: ['sso', 'auth', 'regression'],
    created: '2026-03-28',
    updated: '2026-03-29',
    comments: [
      { id: 'c1', author: 'Bob Martinez', authorInitials: 'BM', body: 'I can confirm this is happening on our staging environment too. The error log shows a nil pointer dereference in the session handler.', timestamp: '2026-03-28 14:23' },
      { id: 'c2', author: 'Alice Chen', authorInitials: 'AC', body: 'Looking into this now. The issue seems to be in the SAML callback handler where we try to read the session before it has been initialized.', timestamp: '2026-03-29 09:10' },
    ],
  },
  'TICKET-102': {
    id: 'TICKET-102',
    title: 'CSV export truncates long descriptions',
    description:
      'When exporting tickets to CSV, any description field longer than 255 characters is silently truncated. This causes data loss for users who rely on exports for auditing.\n\nThe issue is in the export service where the CSV writer uses a fixed buffer size.',
    status: 'in_progress',
    priority: 'high',
    assignee: 'Bob Martinez',
    assigneeInitials: 'BM',
    reporter: 'Eve Johnson',
    reporterInitials: 'EJ',
    labels: ['export', 'data-loss'],
    created: '2026-03-27',
    updated: '2026-03-28',
    comments: [
      { id: 'c3', author: 'Bob Martinez', authorInitials: 'BM', body: 'Found the root cause. Switching to an unbounded writer. Fix incoming shortly.', timestamp: '2026-03-28 11:45' },
    ],
  },
};

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

const ALL_STATUSES: TicketStatus[] = ['open', 'in_progress', 'resolved', 'closed'];

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

/** Detail page for a single service desk ticket. */
export function TicketDetailPage() {
  const { ticketId } = useParams<{ ticketId: string }>();
  const ticket = ticketId ? MOCK_TICKETS[ticketId] : undefined;

  const [status, setStatus] = useState<TicketStatus>(ticket?.status ?? 'open');
  const [commentText, setCommentText] = useState('');
  const [comments, setComments] = useState<Comment[]>(ticket?.comments ?? []);

  if (!ticket) {
    return (
      <div className="flex h-64 items-center justify-center text-[var(--color-text-muted)]">
        Ticket not found.
      </div>
    );
  }

  function handleAddComment() {
    if (!commentText.trim()) return;
    const newComment: Comment = {
      id: `c${Date.now()}`,
      author: 'You',
      authorInitials: 'YO',
      body: commentText.trim(),
      timestamp: new Date().toISOString().slice(0, 16).replace('T', ' '),
    };
    setComments((prev) => [...prev, newComment]);
    setCommentText('');
  }

  return (
    <div className="space-y-6">
      {/* Breadcrumb */}
      <nav className="flex items-center gap-1 text-[var(--text-sm)] text-[var(--color-text-muted)]">
        <Link to="/tickets" className="hover:text-[var(--color-text)]">
          Tickets
        </Link>
        <ChevronRight className="h-4 w-4" />
        <span className="text-[var(--color-text)]">{ticket.id}</span>
      </nav>

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
        {/* Main content */}
        <div className="space-y-6 lg:col-span-2">
          <h1 className="text-[var(--text-2xl)] font-bold text-[var(--color-text)]">
            {ticket.title}
          </h1>

          {/* Description */}
          <Card>
            <CardContent className="p-5">
              <div className="whitespace-pre-wrap text-[var(--text-sm)] leading-relaxed text-[var(--color-text)]">
                {ticket.description}
              </div>
            </CardContent>
          </Card>

          {/* Activity timeline */}
          <div className="space-y-4">
            <h2 className="text-[var(--text-lg)] font-semibold text-[var(--color-text)]">
              Activity
            </h2>

            <div className="space-y-4">
              {comments.map((comment) => (
                <div key={comment.id} className="flex gap-3">
                  <span
                    className={cn(
                      'flex h-8 w-8 shrink-0 items-center justify-center rounded-full',
                      'bg-[var(--color-primary-muted)] text-[var(--text-xs)] font-medium text-[var(--color-primary)]',
                    )}
                  >
                    {comment.authorInitials}
                  </span>
                  <div className="flex-1 rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-surface)] p-4">
                    <div className="mb-2 flex items-center justify-between">
                      <span className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                        {comment.author}
                      </span>
                      <span className="text-[var(--text-xs)] text-[var(--color-text-muted)]">
                        {comment.timestamp}
                      </span>
                    </div>
                    <p className="text-[var(--text-sm)] leading-relaxed text-[var(--color-text)]">
                      {comment.body}
                    </p>
                  </div>
                </div>
              ))}
            </div>

            {/* New comment */}
            <div className="flex gap-3">
              <span
                className={cn(
                  'flex h-8 w-8 shrink-0 items-center justify-center rounded-full',
                  'bg-[var(--color-primary-muted)] text-[var(--text-xs)] font-medium text-[var(--color-primary)]',
                )}
              >
                YO
              </span>
              <div className="flex flex-1 gap-2">
                <Input
                  placeholder="Add a comment..."
                  value={commentText}
                  onChange={(e) => setCommentText(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' && !e.shiftKey) {
                      e.preventDefault();
                      handleAddComment();
                    }
                  }}
                  className="flex-1"
                />
                <Button onClick={handleAddComment} disabled={!commentText.trim()}>
                  <MessageSquare className="mr-2 h-4 w-4" />
                  Comment
                </Button>
              </div>
            </div>
          </div>
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
                  <Badge variant={STATUS_VARIANT[status]}>
                    {STATUS_LABEL[status]}
                  </Badge>
                  <select
                    value={status}
                    onChange={(e) => setStatus(e.target.value as TicketStatus)}
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
                <Badge variant={PRIORITY_VARIANT[ticket.priority]}>
                  {PRIORITY_LABEL[ticket.priority]}
                </Badge>
              </div>

              {/* Assignee */}
              <div>
                <label className="mb-1 block text-[var(--text-xs)] font-medium uppercase tracking-wider text-[var(--color-text-muted)]">
                  Assignee
                </label>
                <div className="flex items-center gap-2">
                  <span
                    className={cn(
                      'flex h-6 w-6 items-center justify-center rounded-full',
                      'bg-[var(--color-primary-muted)] text-[var(--text-xs)] font-medium text-[var(--color-primary)]',
                    )}
                  >
                    {ticket.assigneeInitials}
                  </span>
                  <span className="text-[var(--text-sm)] text-[var(--color-text)]">
                    {ticket.assignee}
                  </span>
                </div>
              </div>

              {/* Reporter */}
              <div>
                <label className="mb-1 block text-[var(--text-xs)] font-medium uppercase tracking-wider text-[var(--color-text-muted)]">
                  Reporter
                </label>
                <div className="flex items-center gap-2">
                  <User className="h-4 w-4 text-[var(--color-text-muted)]" />
                  <span className="text-[var(--text-sm)] text-[var(--color-text)]">
                    {ticket.reporter}
                  </span>
                </div>
              </div>

              {/* Labels */}
              <div>
                <label className="mb-1 block text-[var(--text-xs)] font-medium uppercase tracking-wider text-[var(--color-text-muted)]">
                  Labels
                </label>
                <div className="flex flex-wrap gap-1">
                  {ticket.labels.map((label) => (
                    <Badge key={label} variant="outline">{label}</Badge>
                  ))}
                </div>
              </div>

              {/* Dates */}
              <div className="space-y-2 border-t border-[var(--color-border)] pt-4">
                <div className="flex items-center gap-2 text-[var(--text-xs)] text-[var(--color-text-muted)]">
                  <Clock className="h-3 w-3" />
                  Created {ticket.created}
                </div>
                <div className="flex items-center gap-2 text-[var(--text-xs)] text-[var(--color-text-muted)]">
                  <Clock className="h-3 w-3" />
                  Updated {ticket.updated}
                </div>
              </div>
            </CardContent>
          </Card>
        </div>
      </div>
    </div>
  );
}
