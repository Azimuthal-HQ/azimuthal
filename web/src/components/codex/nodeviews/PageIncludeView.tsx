/**
 * The page-include macro: a reference to another page in this space.
 *
 * It names and links the target rather than inlining its body. Inlining would
 * mean rendering a second document inside this one, and a second document
 * carries its own preserved content — which would then appear on a page that
 * cannot publish it, and would be counted as lost the moment somebody edited
 * around it. Naming the target keeps the reference honest and the
 * preservation accounting confined to one document.
 *
 * The target is stored as `page_id` only, and its title is resolved live: a
 * cached copy would go stale the moment the page was renamed.
 */
import { NodeViewWrapper } from '@tiptap/react';
import type { NodeViewProps } from '@tiptap/react';
import { FileSymlink } from 'lucide-react';
import { useState } from 'react';
import { Link } from 'react-router-dom';

import { PagePicker } from '../PagePicker';
import { useCodexDocumentContext, usePageTitle } from '../CodexDocumentContext';

export function PageIncludeView({ node, editor, updateAttributes }: NodeViewProps) {
  const pageId = String(node.attrs.page_id ?? '');
  const { spaceId } = useCodexDocumentContext();
  const title = usePageTitle(pageId);
  const [picking, setPicking] = useState(false);

  return (
    <NodeViewWrapper className="my-3" data-testid="codex-page-include" data-page-id={pageId}>
      <div
        contentEditable={false}
        className="flex items-center gap-2 rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-surface-hover)] px-3 py-2"
      >
        <FileSymlink className="h-3.5 w-3.5 shrink-0 text-[var(--module-codex)]" aria-hidden="true" />
        <span className="text-[var(--text-xs)] font-semibold uppercase tracking-wide text-[var(--color-text-muted)]">
          Included page
        </span>
        {pageId && title ? (
          <Link
            to={`/codex/${spaceId}/pages/${pageId}`}
            className="truncate text-[var(--text-sm)] text-[var(--color-primary)] hover:underline"
          >
            {title}
          </Link>
        ) : (
          <span className="truncate text-[var(--text-sm)] italic text-[var(--color-text-muted)]">
            {pageId
              ? 'This page is no longer in this space.'
              : 'No page chosen yet.'}
          </span>
        )}
        {editor.isEditable && (
          <button
            type="button"
            onClick={() => setPicking(true)}
            data-testid="codex-page-include-choose"
            className="ml-auto shrink-0 rounded-[var(--radius-md)] border border-[var(--color-border)] px-2 py-0.5 text-[var(--text-xs)] text-[var(--color-text-muted)] transition-colors hover:text-[var(--color-text)] focus:outline-none focus-visible:ring-1 focus-visible:ring-[var(--module-codex)]"
          >
            {pageId ? 'Change' : 'Choose page'}
          </button>
        )}
      </div>

      {picking && (
        <PagePicker
          title="Include a page"
          selectedId={pageId}
          onSelect={(id) => {
            updateAttributes({ page_id: id });
            setPicking(false);
          }}
          onClose={() => setPicking(false)}
        />
      )}
    </NodeViewWrapper>
  );
}
