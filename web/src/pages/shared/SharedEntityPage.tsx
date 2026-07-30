import { useParams } from 'react-router-dom';
import { FileText, Ticket as TicketIcon, ListChecks } from 'lucide-react';
import {
  useMe,
  useSharedEntity,
  useSharedAttachments,
  friendlyErrorMessage,
  type ShareEntityType,
} from '../../lib/api';
import { ShareBadge } from '../../components/ShareBadge';
import { Markdown } from '../../components/Markdown';
import { SharedAttachmentImage, SharedAttachmentLink } from './SharedAttachment';
import { Badge } from '../../components/ui';
import { PriorityPill, normalizePriority } from '../../components/priority';

const VALID_TYPES: ShareEntityType[] = ['page', 'ticket', 'project_item'];

/**
 * The types the server will actually stream inline, sniffed from the object's
 * own bytes (internal/core/attachments/serve.go, ServeTypeFor). Anything else
 * — SVG included, because it is scriptable — comes back as
 * application/octet-stream with Content-Disposition: attachment.
 *
 * `content_type` on the wire is the type the UPLOADER declared, so it only
 * predicts the server's answer, it does not determine it. That is why the
 * download list below is unconditional: a preview is allowed to be wrong, but
 * every attachment must stay reachable either way.
 */
const PREVIEWABLE_TYPES = new Set(['image/png', 'image/jpeg', 'image/gif', 'image/webp']);

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
                .filter((a) => PREVIEWABLE_TYPES.has(a.content_type))
                .map((a) => (
                  <SharedAttachmentImage
                    key={a.id}
                    orgId={orgId}
                    entityType={et}
                    entityId={entityId ?? ''}
                    attachmentId={a.id}
                    filename={a.filename}
                  />
                ))}
            </div>
          )}

          {/* The shared renderer (P5). data-testid preserved: web/e2e asserts
              on it. */}
          <Markdown testId="shared-body">{entity.data.body || ''}</Markdown>

          {/* Every attachment as a download link — including the ones
              previewed above. The list is deliberately NOT the complement of
              the preview filter: `content_type` is the uploader's claim, so any
              filter here can disagree with what the server decides to stream,
              and an attachment that matches neither list would be unreachable
              from this page entirely. A duplicated image link is a much
              smaller cost than a file with no way to open it. */}
          {attachments.data && attachments.data.length > 0 && (
            <div className="space-y-1 border-t border-[var(--color-border)] pt-3" data-testid="shared-attachment-links">
              <p className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">Attachments</p>
              {attachments.data
                .map((a) => (
                  <SharedAttachmentLink
                    key={a.id}
                    orgId={orgId}
                    entityType={et}
                    entityId={entityId ?? ''}
                    attachmentId={a.id}
                    filename={a.filename}
                  />
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
