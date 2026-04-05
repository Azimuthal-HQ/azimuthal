import { useParams, Link } from 'react-router-dom';
import { ArrowLeft, Clock, AlertCircle } from 'lucide-react';
import { Badge, type BadgeProps } from '../../components/ui/badge';
import { Card, CardContent, CardHeader, CardTitle } from '../../components/ui/card';
import { useProjectItem } from '../../lib/api';

// ---------------------------------------------------------------------------
// Badge helpers
// ---------------------------------------------------------------------------

const PRIORITY_VARIANT: Record<number, BadgeProps['variant']> = { 0: 'danger', 1: 'warning', 2: 'secondary', 3: 'outline' };
const PRIORITY_LABEL: Record<number, string> = { 0: 'Critical', 1: 'High', 2: 'Medium', 3: 'Low' };
const STATUS_VARIANT: Record<string, BadgeProps['variant']> = { todo: 'secondary', in_progress: 'warning', in_review: 'default', done: 'success' };
const STATUS_LABEL: Record<string, string> = { todo: 'To Do', in_progress: 'In Progress', in_review: 'In Review', done: 'Done' };

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

/** Detail view for a project item. */
export function ItemDetailPage() {
  const { spaceId, itemKey } = useParams<{ spaceId: string; itemKey: string }>();
  const effectiveSpaceId = spaceId ?? 'default';
  const itemId = itemKey ?? '';

  // We need a useProjectItem hook - let's use a direct query
  const { data: item, isLoading, error } = useProjectItem(effectiveSpaceId, itemId);

  const backlogPath = spaceId ? `/spaces/${spaceId}/backlog` : '/backlog';

  if (isLoading) {
    return (
      <div className="flex h-64 items-center justify-center text-[var(--color-text-muted)]">
        Loading item...
      </div>
    );
  }

  if (error) {
    return (
      <div className="space-y-4">
        <Link to={backlogPath} className="flex items-center gap-1 text-[var(--text-sm)] text-[var(--color-text-muted)] hover:text-[var(--color-text)]">
          <ArrowLeft className="h-4 w-4" />
          Backlog
        </Link>
        <div className="flex items-center gap-3 rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[var(--color-danger)]/10 p-4">
          <AlertCircle className="h-5 w-5 text-[var(--color-danger)]" />
          <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
            {error.status === 404 ? 'Item not found.' : `Failed to load item: ${error.message}`}
          </p>
        </div>
      </div>
    );
  }

  if (!item) {
    return (
      <div className="flex flex-col items-center justify-center py-20 text-[var(--color-text-muted)]">
        <p className="text-lg font-medium">Item not found</p>
        <Link to={backlogPath} className="mt-2 text-[var(--color-primary)] hover:underline">
          Back to backlog
        </Link>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Breadcrumb */}
      <div className="flex items-center gap-2 text-[var(--text-sm)] text-[var(--color-text-muted)]">
        <Link to={backlogPath} className="flex items-center gap-1 hover:text-[var(--color-text)]">
          <ArrowLeft className="h-4 w-4" />
          Backlog
        </Link>
        <span>/</span>
        <span className="text-[var(--color-text)]" style={{ fontFamily: 'var(--font-mono)' }}>{item.id.slice(0, 8)}</span>
      </div>

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
        {/* Main content */}
        <div className="space-y-6 lg:col-span-2">
          <h1 className="text-[var(--text-2xl)] font-bold text-[var(--color-text)]">{item.title}</h1>

          {item.description && (
            <Card>
              <CardHeader><CardTitle>Description</CardTitle></CardHeader>
              <CardContent>
                <div className="whitespace-pre-wrap text-[var(--text-sm)] text-[var(--color-text)] leading-relaxed">
                  {item.description}
                </div>
              </CardContent>
            </Card>
          )}
        </div>

        {/* Sidebar */}
        <div className="space-y-4">
          <Card>
            <CardContent className="space-y-4 p-4">
              <DetailRow label="Status">
                <Badge variant={STATUS_VARIANT[item.status] ?? 'secondary'}>{STATUS_LABEL[item.status] ?? item.status}</Badge>
              </DetailRow>
              <DetailRow label="Priority">
                <Badge variant={PRIORITY_VARIANT[item.priority] ?? 'secondary'}>{PRIORITY_LABEL[item.priority] ?? 'Unknown'}</Badge>
              </DetailRow>
              <div className="border-t border-[var(--color-border)] pt-3 space-y-1">
                <div className="flex items-center gap-1 text-[var(--text-xs)] text-[var(--color-text-muted)]">
                  <Clock className="h-3 w-3" /> Created {item.created_at.slice(0, 10)}
                </div>
                <div className="flex items-center gap-1 text-[var(--text-xs)] text-[var(--color-text-muted)]">
                  <Clock className="h-3 w-3" /> Updated {item.updated_at.slice(0, 10)}
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
