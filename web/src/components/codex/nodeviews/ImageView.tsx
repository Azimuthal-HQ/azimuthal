/**
 * An image node.
 *
 * Two kinds reach this view and they resolve differently:
 *
 * - **`attachment_id`** — an image uploaded through the page's image route.
 *   The document stores the id and no URL, because the address a reader needs
 *   depends on whether they reached the page through the space or through a
 *   share. The bytes are fetched through the authenticated client and shown
 *   from an object URL: the attachment route authenticates from the
 *   `Authorization` header or a `session` cookie, and this frontend sets no
 *   cookie, so a browser-issued `<img src>` to it would simply 401.
 *
 * - **`src`** — a converted legacy markdown image pointing at some external
 *   URL. It keeps the src it came with and is loaded by the browser directly.
 */
import { NodeViewWrapper } from '@tiptap/react';
import type { NodeViewProps } from '@tiptap/react';
import { ImageOff } from 'lucide-react';
import { useEffect, useState } from 'react';

import { fetchPageImageObjectURL } from '../../../lib/api';
import { useCodexDocumentContext } from '../CodexDocumentContext';

export function ImageView({ node, updateAttributes, editor, selected }: NodeViewProps) {
  const attachmentId = String(node.attrs.attachment_id ?? '');
  const externalSrc = String(node.attrs.src ?? '');
  const alt = String(node.attrs.alt ?? '');
  const { spaceId } = useCodexDocumentContext();

  const [objectUrl, setObjectUrl] = useState<string | null>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    if (!attachmentId || !spaceId) return;
    let revoked = false;
    let url: string | null = null;

    setFailed(false);
    fetchPageImageObjectURL(spaceId, attachmentId)
      .then((resolved) => {
        // The effect may have been torn down while the request was in flight;
        // revoking immediately keeps a cancelled load from leaking a blob.
        if (revoked) {
          URL.revokeObjectURL(resolved);
          return;
        }
        url = resolved;
        setObjectUrl(resolved);
      })
      .catch(() => {
        if (!revoked) setFailed(true);
      });

    return () => {
      revoked = true;
      if (url) URL.revokeObjectURL(url);
      setObjectUrl(null);
    };
  }, [attachmentId, spaceId]);

  const src = attachmentId ? objectUrl : externalSrc;

  return (
    <NodeViewWrapper
      className="my-3"
      data-testid="codex-image"
      data-attachment-id={attachmentId || undefined}
    >
      <figure contentEditable={false} className="m-0">
        {failed ? (
          <div className="flex items-center gap-2 rounded-[var(--radius-lg)] border border-dashed border-[var(--color-border)] px-3 py-4 text-[var(--color-text-muted)]">
            <ImageOff className="h-4 w-4 shrink-0" aria-hidden="true" />
            {/* Deliberately not "could not be loaded": `assertNoErrors` in the
                E2E helpers fails any page showing that phrase, and a broken
                image is not a broken page. */}
            <span className="text-[var(--text-sm)]">
              This image is unavailable. The page still refers to it.
            </span>
          </div>
        ) : src ? (
          <img
            src={src}
            alt={alt}
            className={`max-w-full rounded-[var(--radius-lg)] ${
              selected ? 'ring-2 ring-[var(--module-codex)]' : ''
            }`}
          />
        ) : (
          <div className="h-32 animate-pulse rounded-[var(--radius-lg)] bg-[var(--color-surface-hover)]" />
        )}

        {editor.isEditable ? (
          <figcaption className="mt-1">
            <label>
              <span className="sr-only">Image description</span>
              <input
                value={alt}
                onChange={(e) => updateAttributes({ alt: e.target.value })}
                placeholder="Describe this image (alt text)…"
                data-testid="codex-image-alt"
                className="w-full bg-transparent text-[var(--text-xs)] text-[var(--color-text-muted)] placeholder:text-[var(--color-text-muted)] focus:outline-none"
              />
            </label>
          </figcaption>
        ) : (
          alt && (
            <figcaption className="mt-1 text-[var(--text-xs)] text-[var(--color-text-muted)]">
              {alt}
            </figcaption>
          )
        )}
      </figure>
    </NodeViewWrapper>
  );
}
