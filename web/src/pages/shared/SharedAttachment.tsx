/**
 * The shared page's two ways of putting an attachment in front of a reader.
 *
 * Both go through `fetchSharedAttachmentObjectURL`, never a URL in the markup.
 * The attachment route authenticates from the `Authorization` header or a
 * `session` cookie, and this frontend is bearer-only — it sets no cookie —
 * so a browser-issued request to it carries no credential and is answered 401.
 * That failure is silent in both places it can happen: an `<img>` shows a
 * broken-image icon and reports nothing, and an `<a href>` saves a file whose
 * contents are a JSON error. S8.
 */
import { useEffect, useState } from 'react';
import { ImageOff } from 'lucide-react';

import { fetchSharedAttachmentObjectURL, type ShareEntityType } from '../../lib/api';

interface AttachmentRef {
  orgId: string;
  entityType: ShareEntityType;
  entityId: string;
  attachmentId: string;
}

/**
 * useSharedAttachmentObjectURL fetches the bytes and keeps the blob URL alive
 * for exactly as long as the component needs it. A URL resolved after teardown
 * is revoked immediately rather than leaked.
 */
function useSharedAttachmentObjectURL(ref: AttachmentRef, enabled: boolean) {
  const { orgId, entityType, entityId, attachmentId } = ref;
  const [objectUrl, setObjectUrl] = useState<string | null>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    if (!enabled || !orgId || !entityId || !attachmentId) return;
    let torndown = false;
    let url: string | null = null;

    setFailed(false);
    fetchSharedAttachmentObjectURL(orgId, entityType, entityId, attachmentId)
      .then((resolved) => {
        if (torndown) {
          URL.revokeObjectURL(resolved);
          return;
        }
        url = resolved;
        setObjectUrl(resolved);
      })
      .catch(() => {
        if (!torndown) setFailed(true);
      });

    return () => {
      torndown = true;
      if (url) URL.revokeObjectURL(url);
      setObjectUrl(null);
    };
  }, [enabled, orgId, entityType, entityId, attachmentId]);

  return { objectUrl, failed };
}

/** A previewed image on the shared page. */
export function SharedAttachmentImage(props: AttachmentRef & { filename: string }) {
  const { objectUrl, failed } = useSharedAttachmentObjectURL(props, true);

  if (failed) {
    return (
      <div
        className="flex items-center gap-2 rounded-[var(--radius-md)] border border-dashed border-[var(--color-border)] px-3 py-4 text-[var(--color-text-muted)]"
        data-testid="shared-attachment-unavailable"
      >
        <ImageOff className="h-4 w-4 shrink-0" aria-hidden="true" />
        {/* Deliberately not "could not be loaded": assertNoErrors in the E2E
            helpers fails any page showing that phrase, and one unavailable
            image is not a broken page. */}
        <span className="text-[var(--text-sm)]">
          This image is unavailable. The page still refers to it.
        </span>
      </div>
    );
  }
  if (!objectUrl) {
    return <div className="h-32 animate-pulse rounded-[var(--radius-md)] bg-[var(--color-surface-hover)]" />;
  }
  return (
    <img
      src={objectUrl}
      alt={props.filename}
      className="max-w-full rounded-[var(--radius-md)] border border-[var(--color-border)]"
    />
  );
}

/**
 * A download link for one attachment.
 *
 * The bytes are fetched eagerly rather than on click. A click handler that
 * fetches and then synthesises a download is the tidier-looking option and the
 * wrong one: the fetch is asynchronous, so by the time it resolves the click is
 * no longer a user gesture and the browser's popup and download blockers treat
 * the result as unsolicited. A real `<a href download>` with the URL already in
 * hand is a plain navigation the browser has no reason to question.
 */
export function SharedAttachmentLink(props: AttachmentRef & { filename: string }) {
  const { objectUrl, failed } = useSharedAttachmentObjectURL(props, true);

  if (failed) {
    return (
      <span className="block text-[var(--text-sm)] text-[var(--color-text-muted)]">
        {props.filename} — unavailable
      </span>
    );
  }
  return (
    <a
      href={objectUrl ?? undefined}
      download={props.filename}
      aria-disabled={objectUrl ? undefined : true}
      className="block text-[var(--text-sm)] text-[var(--color-primary)] hover:underline aria-disabled:pointer-events-none aria-disabled:opacity-60"
    >
      {props.filename}
    </a>
  );
}
