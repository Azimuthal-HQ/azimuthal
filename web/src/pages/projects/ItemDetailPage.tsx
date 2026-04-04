import { useState } from 'react';
import { useParams, Link } from 'react-router-dom';
import { ArrowLeft, Clock, User as UserIcon } from 'lucide-react';
import { Badge, type BadgeProps } from '../../components/ui/badge';
import { Button } from '../../components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '../../components/ui/card';
import { Input } from '../../components/ui/input';
import { cn } from '../../lib/utils';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type ItemType = 'story' | 'bug' | 'task';
type ItemPriority = 'critical' | 'high' | 'medium' | 'low';
type ItemStatus = 'todo' | 'in_progress' | 'in_review' | 'done';

interface ProjectItem {
  id: string;
  key: string;
  title: string;
  description: string;
  type: ItemType;
  priority: ItemPriority;
  status: ItemStatus;
  sprint: string | null;
  points: number | null;
  assignee: string;
  assigneeInitials: string;
  reporter: string;
  created: string;
  updated: string;
  labels: string[];
}

// ---------------------------------------------------------------------------
// Mock data
// ---------------------------------------------------------------------------

const MOCK_ITEMS: Record<string, ProjectItem> = {
  'PROD-101': { id: '1', key: 'PROD-101', title: 'User registration flow', description: 'Implement the full user registration flow including email verification, password strength validation, and welcome onboarding sequence.\n\n## Acceptance Criteria\n- User can sign up with email and password\n- Email verification is sent on registration\n- Password must meet strength requirements\n- Welcome wizard shown on first login', type: 'story', priority: 'high', status: 'done', sprint: 'Sprint 12', points: 8, assignee: 'Alice Chen', assigneeInitials: 'AC', reporter: 'Dana Kim', created: '2026-03-10', updated: '2026-03-28', labels: ['auth', 'onboarding'] },
  'PROD-102': { id: '2', key: 'PROD-102', title: 'Fix password reset email not sending', description: 'The password reset flow silently fails when the SMTP server is unreachable. No error is shown to the user.\n\n## Steps to Reproduce\n1. Click "Forgot Password"\n2. Enter valid email\n3. Click Submit\n4. No email arrives, no error shown\n\n## Expected\nEither send the email or show an error message.', type: 'bug', priority: 'critical', status: 'in_progress', sprint: 'Sprint 12', points: 3, assignee: 'Bob Martinez', assigneeInitials: 'BM', reporter: 'Eve Johnson', created: '2026-03-12', updated: '2026-03-30', labels: ['auth', 'email'] },
  'PROD-103': { id: '3', key: 'PROD-103', title: 'Add RBAC middleware', description: 'Implement role-based access control middleware that checks user permissions before allowing access to protected endpoints.', type: 'story', priority: 'high', status: 'in_review', sprint: 'Sprint 12', points: 13, assignee: 'Charlie Osei', assigneeInitials: 'CO', reporter: 'Alice Chen', created: '2026-03-14', updated: '2026-04-01', labels: ['security', 'api'] },
  'PROD-104': { id: '4', key: 'PROD-104', title: 'Set up CI pipeline for frontend', description: 'Configure GitHub Actions to run lint, type-check, and build on every push to the web/ directory.', type: 'task', priority: 'medium', status: 'todo', sprint: 'Sprint 12', points: 5, assignee: 'Dana Kim', assigneeInitials: 'DK', reporter: 'Bob Martinez', created: '2026-03-16', updated: '2026-03-16', labels: ['devops'] },
  'PROD-105': { id: '5', key: 'PROD-105', title: 'Dashboard analytics widgets', description: 'Add analytics widgets to the main dashboard showing ticket resolution time, sprint velocity, and wiki page views.', type: 'story', priority: 'medium', status: 'todo', sprint: 'Sprint 13', points: 8, assignee: 'Alice Chen', assigneeInitials: 'AC', reporter: 'Dana Kim', created: '2026-03-20', updated: '2026-03-20', labels: ['analytics', 'dashboard'] },
  'PROD-106': { id: '6', key: 'PROD-106', title: 'Broken avatar upload on mobile', description: 'Avatar upload fails on mobile browsers. The file picker opens but the selected image is not uploaded.', type: 'bug', priority: 'high', status: 'todo', sprint: 'Sprint 13', points: 3, assignee: 'Bob Martinez', assigneeInitials: 'BM', reporter: 'Charlie Osei', created: '2026-03-22', updated: '2026-03-22', labels: ['mobile', 'media'] },
  'PROD-107': { id: '7', key: 'PROD-107', title: 'API rate limiting implementation', description: 'Implement token bucket rate limiting on all public API endpoints to prevent abuse.', type: 'story', priority: 'high', status: 'todo', sprint: 'Sprint 13', points: 8, assignee: 'Charlie Osei', assigneeInitials: 'CO', reporter: 'Alice Chen', created: '2026-03-24', updated: '2026-03-24', labels: ['security', 'api'] },
  'PROD-108': { id: '8', key: 'PROD-108', title: 'Write integration tests for wiki module', description: 'Add integration tests covering page creation, editing, version history, and conflict detection.', type: 'task', priority: 'medium', status: 'todo', sprint: null, points: 5, assignee: 'Eve Johnson', assigneeInitials: 'EJ', reporter: 'Dana Kim', created: '2026-03-25', updated: '2026-03-25', labels: ['testing'] },
  'PROD-109': { id: '9', key: 'PROD-109', title: 'Evaluate caching strategy for search', description: 'Research and prototype caching approaches for full-text search results.', type: 'task', priority: 'low', status: 'todo', sprint: null, points: 3, assignee: 'Bob Martinez', assigneeInitials: 'BM', reporter: 'Alice Chen', created: '2026-03-26', updated: '2026-03-26', labels: ['performance'] },
  'PROD-110': { id: '10', key: 'PROD-110', title: 'Notification preferences page', description: 'Allow users to configure which notifications they receive via email and in-app.', type: 'story', priority: 'medium', status: 'todo', sprint: null, points: 5, assignee: 'Dana Kim', assigneeInitials: 'DK', reporter: 'Eve Johnson', created: '2026-03-28', updated: '2026-03-28', labels: ['notifications', 'settings'] },
  'PROD-111': { id: '11', key: 'PROD-111', title: 'Implement notification preferences', description: 'Build the preferences UI and backend for notification settings.', type: 'story', priority: 'medium', status: 'todo', sprint: 'Sprint 12', points: 5, assignee: 'Eve Johnson', assigneeInitials: 'EJ', reporter: 'Dana Kim', created: '2026-03-15', updated: '2026-03-15', labels: ['notifications'] },
  'PROD-112': { id: '12', key: 'PROD-112', title: 'Add search indexing for wiki pages', description: 'Implement full-text search indexing using PostgreSQL tsvector for wiki page content.', type: 'story', priority: 'high', status: 'in_progress', sprint: 'Sprint 12', points: 8, assignee: 'Alice Chen', assigneeInitials: 'AC', reporter: 'Charlie Osei', created: '2026-03-18', updated: '2026-03-31', labels: ['search', 'wiki'] },
  'PROD-113': { id: '13', key: 'PROD-113', title: 'Fix broken link in onboarding step 3', description: 'The link to the documentation in the third onboarding step leads to a 404 page.', type: 'bug', priority: 'low', status: 'done', sprint: 'Sprint 12', points: 2, assignee: 'Bob Martinez', assigneeInitials: 'BM', reporter: 'Eve Johnson', created: '2026-03-19', updated: '2026-03-27', labels: ['onboarding'] },
};

// ---------------------------------------------------------------------------
// Badge helpers
// ---------------------------------------------------------------------------

const TYPE_VARIANT: Record<ItemType, BadgeProps['variant']> = { story: 'default', bug: 'danger', task: 'secondary' };
const TYPE_LABEL: Record<ItemType, string> = { story: 'Story', bug: 'Bug', task: 'Task' };
const PRIORITY_VARIANT: Record<ItemPriority, BadgeProps['variant']> = { critical: 'danger', high: 'warning', medium: 'secondary', low: 'outline' };
const PRIORITY_LABEL: Record<ItemPriority, string> = { critical: 'Critical', high: 'High', medium: 'Medium', low: 'Low' };
const STATUS_VARIANT: Record<ItemStatus, BadgeProps['variant']> = { todo: 'secondary', in_progress: 'warning', in_review: 'default', done: 'success' };
const STATUS_LABEL: Record<ItemStatus, string> = { todo: 'To Do', in_progress: 'In Progress', in_review: 'In Review', done: 'Done' };

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

/** Detail view for a project item, matching the ticket detail pattern. */
export function ItemDetailPage() {
  const { itemKey } = useParams<{ itemKey: string }>();
  const item = MOCK_ITEMS[itemKey ?? ''];
  const [comment, setComment] = useState('');

  if (!item) {
    return (
      <div className="flex flex-col items-center justify-center py-20 text-[var(--color-text-muted)]">
        <p className="text-lg font-medium">Item not found</p>
        <Link to="/backlog" className="mt-2 text-[var(--color-primary)] hover:underline">
          Back to backlog
        </Link>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Breadcrumb */}
      <div className="flex items-center gap-2 text-[var(--text-sm)] text-[var(--color-text-muted)]">
        <Link to="/backlog" className="flex items-center gap-1 hover:text-[var(--color-text)]">
          <ArrowLeft className="h-4 w-4" />
          Backlog
        </Link>
        <span>/</span>
        <span className="text-[var(--color-text)]" style={{ fontFamily: 'var(--font-mono)' }}>{item.key}</span>
      </div>

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
        {/* Main content — left 2/3 */}
        <div className="space-y-6 lg:col-span-2">
          {/* Title & badges */}
          <div>
            <div className="mb-2 flex flex-wrap items-center gap-2">
              <Badge variant={TYPE_VARIANT[item.type]}>{TYPE_LABEL[item.type]}</Badge>
              <span className="text-[var(--text-xs)] text-[var(--color-text-muted)]" style={{ fontFamily: 'var(--font-mono)' }}>{item.key}</span>
            </div>
            <h1 className="text-[var(--text-2xl)] font-bold text-[var(--color-text)]">{item.title}</h1>
          </div>

          {/* Description */}
          <Card>
            <CardHeader><CardTitle>Description</CardTitle></CardHeader>
            <CardContent>
              <div className="whitespace-pre-wrap text-[var(--text-sm)] text-[var(--color-text)] leading-relaxed">
                {item.description}
              </div>
            </CardContent>
          </Card>

          {/* Activity */}
          <Card>
            <CardHeader><CardTitle>Activity</CardTitle></CardHeader>
            <CardContent className="space-y-4">
              <div className="flex items-start gap-3">
                <span className={cn('flex h-8 w-8 shrink-0 items-center justify-center rounded-full', 'bg-[var(--color-primary-muted)] text-[var(--text-xs)] font-medium text-[var(--color-primary)]')}>
                  {item.assigneeInitials}
                </span>
                <div>
                  <p className="text-[var(--text-sm)] text-[var(--color-text)]">
                    <span className="font-medium">{item.assignee}</span>{' '}
                    moved this item to <Badge variant={STATUS_VARIANT[item.status]}>{STATUS_LABEL[item.status]}</Badge>
                  </p>
                  <p className="text-[var(--text-xs)] text-[var(--color-text-muted)]">{item.updated}</p>
                </div>
              </div>

              <div className="border-t border-[var(--color-border)] pt-4">
                <div className="flex gap-2">
                  <Input placeholder="Add a comment..." value={comment} onChange={(e) => setComment(e.target.value)} className="flex-1" />
                  <Button size="sm" disabled={!comment.trim()}>Comment</Button>
                </div>
              </div>
            </CardContent>
          </Card>
        </div>

        {/* Sidebar — right 1/3 */}
        <div className="space-y-4">
          <Card>
            <CardContent className="space-y-4 p-4">
              <DetailRow label="Status">
                <Badge variant={STATUS_VARIANT[item.status]}>{STATUS_LABEL[item.status]}</Badge>
              </DetailRow>
              <DetailRow label="Priority">
                <Badge variant={PRIORITY_VARIANT[item.priority]}>{PRIORITY_LABEL[item.priority]}</Badge>
              </DetailRow>
              <DetailRow label="Assignee">
                <div className="flex items-center gap-2">
                  <span className={cn('flex h-6 w-6 items-center justify-center rounded-full', 'bg-[var(--color-primary-muted)] text-[var(--text-xs)] font-medium text-[var(--color-primary)]')}>
                    {item.assigneeInitials}
                  </span>
                  <span className="text-[var(--text-sm)] text-[var(--color-text)]">{item.assignee}</span>
                </div>
              </DetailRow>
              <DetailRow label="Reporter">
                <div className="flex items-center gap-2">
                  <UserIcon className="h-4 w-4 text-[var(--color-text-muted)]" />
                  <span className="text-[var(--text-sm)] text-[var(--color-text)]">{item.reporter}</span>
                </div>
              </DetailRow>
              <DetailRow label="Sprint">
                <span className="text-[var(--text-sm)] text-[var(--color-text)]">{item.sprint ?? 'Backlog'}</span>
              </DetailRow>
              <DetailRow label="Points">
                <span className="text-[var(--text-sm)] text-[var(--color-text)]">{item.points ?? '\u2014'}</span>
              </DetailRow>
              <DetailRow label="Labels">
                <div className="flex flex-wrap gap-1">
                  {item.labels.map((label) => (
                    <Badge key={label} variant="outline">{label}</Badge>
                  ))}
                </div>
              </DetailRow>
              <div className="border-t border-[var(--color-border)] pt-3 space-y-1">
                <div className="flex items-center gap-1 text-[var(--text-xs)] text-[var(--color-text-muted)]">
                  <Clock className="h-3 w-3" /> Created {item.created}
                </div>
                <div className="flex items-center gap-1 text-[var(--text-xs)] text-[var(--color-text-muted)]">
                  <Clock className="h-3 w-3" /> Updated {item.updated}
                </div>
              </div>
            </CardContent>
          </Card>
        </div>
      </div>
    </div>
  );
}

function DetailRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <label className="mb-1 block text-[var(--text-xs)] font-medium uppercase tracking-wider text-[var(--color-text-muted)]">{label}</label>
      {children}
    </div>
  );
}
