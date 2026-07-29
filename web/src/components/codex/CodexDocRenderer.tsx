/**
 * The published-document reading surface (issue #15 B5).
 *
 * It is the editor, not editable — the same extensions, the same node views,
 * the same stylesheet. That is the whole design: a separate renderer is a
 * second implementation of the document model, and it drifts. Panels, code
 * blocks, tables, macros and — the part that matters — labelled preserved
 * blocks all render exactly as the author saw them, because they *are* the
 * same components.
 *
 * ## Where the document comes from
 *
 * From `GET …/wiki/{pageID}/document`, never from `WikiPage.doc`. The stored
 * document still contains node types outside the editor's schema; handing it
 * to ProseMirror would drop them silently, and a reader would see a blank
 * where content exists — the failure ADR-0012 section 2 exists to prevent.
 * The `/document` route shields first, so every preserved item arrives as a
 * placeholder carrying its name, source and text. Reading it needs only
 * space-read, so a reader who cannot edit can still open it.
 *
 * Legacy markdown pages (`doc IS NULL`) do not come here at all: they keep
 * their existing markdown rendering, unchanged, per migration 036's
 * dual-format contract.
 */
import { EditorContent, useEditor } from '@tiptap/react';
import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';

import type { CodexDoc } from '../../lib/codex/schema';
import { LINK_ATTRS } from '../../lib/codex/schema';
import type { WikiPage } from '../../lib/api';
import { CodexDocumentProvider } from './CodexDocumentContext';
import { codexExtensions } from './extensions';
import { editorSurfaceClasses } from './editorStyles';
import { UnresolvedLinkDialog } from './UnresolvedLinkDialog';

interface CodexDocRendererProps {
  doc: CodexDoc;
  spaceId: string;
  pageId: string;
  pages: WikiPage[];
}

export function CodexDocRenderer({ doc, spaceId, pageId, pages }: CodexDocRendererProps) {
  const navigate = useNavigate();

  const editor = useEditor(
    {
      extensions: codexExtensions(),
      content: doc,
      editable: false,
      editorProps: {
        attributes: { 'data-testid': 'codex-document' },
      },
    },
    // Rebuild when the document changes: unlike the editor, nothing here holds
    // unsaved state that a rebuild could discard.
    [doc],
  );

  useEffect(() => {
    editor?.setEditable(false);
  }, [editor]);

  /** The unresolved link a reader clicked, if any. */
  const [unresolved, setUnresolved] = useState<string | null>(null);

  /**
   * The two things a link in a document can be, and neither is an href.
   *
   * A RESOLVED internal link carries `page_id` and no href, because a page's
   * URL depends on the space it is read in — turning one into a route is this
   * surface's job, not the document's. Navigation is attempted regardless of
   * whether the target still exists or is still readable: access is decided at
   * the destination, exactly as it is for a notification (the S1 precedent), so
   * a link renders identically for every reader and reveals nothing about what
   * anyone else can see.
   *
   * An UNRESOLVED link carries `target_title` and names a page nobody has
   * written. Clicking it opens the create-or-open dialogue rather than
   * navigating, because there is nowhere to navigate to yet.
   */
  function handleClick(event: React.MouseEvent<HTMLElement>) {
    const anchor = (event.target as HTMLElement).closest?.('a[data-page-id], a[data-target-title]');
    if (!anchor) return;

    const targetTitle = anchor.getAttribute('data-target-title');
    if (targetTitle) {
      event.preventDefault();
      setUnresolved(targetTitle);
      return;
    }

    const target = anchor.getAttribute(`data-${LINK_ATTRS.pageId.replace('_', '-')}`);
    if (!target) return;
    event.preventDefault();
    navigate(`/codex/${spaceId}/pages/${target}`);
  }

  if (!editor) return null;

  return (
    <CodexDocumentProvider value={{ spaceId, pageId, pages }}>
      {/* <article> is load-bearing: the wiki E2E scopes its persistence
          assertions to it, so that a contentEditable region full of the text
          just typed cannot satisfy a check meant to prove the page was saved
          and re-rendered. */}
      <article className={editorSurfaceClasses} onClick={handleClick}>
        <EditorContent editor={editor} />
      </article>

      {unresolved && (
        <UnresolvedLinkDialog
          targetTitle={unresolved}
          spaceId={spaceId}
          pages={pages}
          onClose={() => setUnresolved(null)}
        />
      )}
    </CodexDocumentProvider>
  );
}
