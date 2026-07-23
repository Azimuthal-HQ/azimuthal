import { useParams } from 'react-router-dom';
import ReactMarkdown from 'react-markdown';
import { FileText, Ticket as TicketIcon, ListChecks } from 'lucide-react';
import {
  useMe,
  useSharedEntity,
  useSharedAttachments,
  sharedAttachmentURL,
  friendlyErrorMessage,
  type ShareEntityType,
} from '../../lib/api';
import { ShareBadge } from '../../components/ShareBadge';
import { Badge } from '../../components/ui';
import { PriorityPill, normalizePriority } from '../../components/priority';

const VALID_TYPES: ShareEntityType[] = ['page', 'ticket', 'project_item'];

/**
 * SharedEntityPage (P3, ADR-0008): the standalone view for the shared read
 * route. It renders OUTSIDE the app shell — no space sidebar, no product
 * tabs, no breadcrumb chain into a space the viewer cannot enter. The only
 * navigation is the entity's own title. A persistent ShareBadge marks it as
 * shared. Read-only by construction: there is no edit, comment, or move
 * affordance anywhere on this page.
 */
export function SharedEntityPage() {
  const { entityType, entityId } = useParams<{ entityType: string; entityId: string }>();
  const { data: me } = useMe();
  const orgId = me?.org_id ?? '';

  const typeOk = VALID_TYPES.includes((entityType ?? '') as ShareEntityType);
  const et = (entityType ?? '') as ShareEntityType;

  const entity = useSharedEntity(orgId, et, entityId ?? '', { enabled: typeOk && !!orgId && !!entityId });
  const attachments = useSharedAttachments(orgId, et, entityId ?? '', {
    enabled: typeOk && !!orgId && !!entityId && !!entity.data,
  });

  if (!typeOk) {
    return <SharedShell><NotAvailable message="This link is not valid." /></SharedShell>;
  }

  return (
    <SharedShell>
      {entity.isLoading && (
        <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">Loading…</p>
      )}
      {entity.isError && (
        <NotAvailable
          message={friendlyErrorMessage(
            entity.error,
            'This item is not available to you. The share may have been revoked or expired.',
          )}
        />
      )}
      {entity.data && (
        <article data-testid="shared-entity" className="space-y-4">
          {/* Breadcrumbs degrade to the entity itself — never a clickable
              ancestor chain into a space the viewer cannot enter. */}
          <nav className="flex items-center gap-2 text-[var(--text-sm)] text-[var(--color-text-muted)]" aria-label="Breadcrumb">
            <EntityIcon type={entity.data.entity_type} />
            <span data-testid="shared-breadcrumb">{entity.data.title}</span>
            <ShareBadge shared detail="shared with you" />
          </nav>

          <div className="flex items-center gap-3">
            <h1 className="text-[19px] font-semibold leading-[1.3] tracking-[-.01em] text-[var(--color-text)]">{entity.data.title}</h1>
          </div>

          {(entity.data.status || entity.data.priority) && (
            <div className="flex gap-2">
              {entity.data.status && <Badge variant="secondary">{entity.data.status}</Badge>}
              {entity.data.priority && <PriorityPill priority={normalizePriority(entity.data.priority)} />}
            </div>
          )}

          {/* Attachments follow the entity (ADR-0008 rule 3): a shared page's
              images load here with no space access. */}
          {attachments.data && attachments.data.length > 0 && (
            <div className="space-y-2" data-testid="shared-attachments">
              {attachments.data
                .filter((a) => a.content_type.startsWith('image/'))
                .map((a) => (
                  <img
                    key={a.id}
                    src={sharedAttachmentURL(orgId, et, entityId ?? '', a.id)}
                    alt={a.filename}
                    className="max-w-full rounded-[var(--radius-md)] border border-[var(--color-border)]"
                  />
                ))}
            </div>
          )}

          <div
            className="prose prose-sm dark:prose-invert max-w-none leading-[1.7] prose-headings:text-[var(--color-text)] prose-headings:font-semibold prose-p:text-[var(--color-text)] prose-li:text-[var(--color-text)] prose-strong:text-[var(--color-text)] prose-a:text-[var(--color-primary)] prose-code:font-[var(--font-mono)] prose-code:text-[var(--color-text)] prose-code:bg-[var(--color-input)] prose-code:rounded prose-code:px-1.5 prose-code:py-0.5 prose-pre:bg-[var(--color-input)] prose-pre:border prose-pre:border-[var(--color-border)]"
            data-testid="shared-body"
          >
            <ReactMarkdown>{entity.data.body || ''}</ReactMarkdown>
          </div>

          {/* Non-image attachments as download links. */}
          {attachments.data && attachments.data.some((a) => !a.content_type.startsWith('image/')) && (
            <div className="space-y-1 border-t border-[var(--color-border)] pt-3">
              <p className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">Attachments</p>
              {attachments.data
                .filter((a) => !a.content_type.startsWith('image/'))
                .map((a) => (
                  <a
                    key={a.id}
                    href={sharedAttachmentURL(orgId, et, entityId ?? '', a.id)}
                    className="block text-[var(--text-sm)] text-[var(--color-primary)] hover:underline"
                  >
                    {a.filename}
                  </a>
                ))}
            </div>
          )}
        </article>
      )}
    </SharedShell>
  );
}

function SharedShell({ children }: { children: React.ReactNode }) {
  return (
    <main className="min-h-screen bg-[var(--color-bg)]" data-testid="shared-view">
      <div className="mx-auto max-w-[820px] px-[var(--space-4)] py-[var(--space-8)]">{children}</div>
    </main>
  );
}

function NotAvailable({ message }: { message: string }) {
  return (
    <div className="rounded-[var(--radius-lg)] border border-[var(--color-border)] p-8 text-center" data-testid="shared-not-available">
      <p className="text-[var(--text-base)] text-[var(--color-text-muted)]">{message}</p>
    </div>
  );
}

function EntityIcon({ type }: { type: ShareEntityType }) {
  const cls = 'h-4 w-4';
  if (type === 'page') return <FileText className={cls} aria-hidden />;
  if (type === 'ticket') return <TicketIcon className={cls} aria-hidden />;
  return <ListChecks className={cls} aria-hidden />;
}
