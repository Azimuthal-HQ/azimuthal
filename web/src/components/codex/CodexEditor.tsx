/**
 * The Codex editor surface (issue #15 B1).
 *
 * A TipTap editor bound to exactly the vocabulary in
 * `internal/core/wiki/doc/schema.json`, which `extensions/index.ts` assembles
 * and `extensions.test.ts` holds to that list in both directions.
 *
 * The document it emits is ProseMirror JSON, handed straight to the draft and
 * publish routes. It is never converted to markdown on the way out: the
 * markdown projection is derived server-side for search, and round-tripping
 * through it in the client is exactly the lossy path ADR-0012 exists to
 * prevent.
 */
import { EditorContent, useEditor } from '@tiptap/react';
import type { Editor } from '@tiptap/react';
import { useEffect, useRef, useState } from 'react';

import type { CodexDoc } from '../../lib/codex/schema';
import { markdownPasteContent } from '../../lib/codex/markdownPaste';
import type { CodexPageImage, WikiPage } from '../../lib/api';
import { CodexDocumentProvider } from './CodexDocumentContext';
import { CodexToolbar } from './CodexToolbar';
import { codexExtensions } from './extensions';
import type { WikilinkSuggestionState } from './extensions/wikilinks';
import { editorSurfaceClasses } from './editorStyles';
import { WikilinkSuggestions } from './WikilinkSuggestions';

export interface CodexEditorProps {
  /**
   * The document to edit. Read once, at mount — ProseMirror owns the document
   * after that, and pushing a new one in mid-session would discard the
   * author's unsaved edits. The page remounts this component (via `key`) when
   * it genuinely means to start from a different document.
   */
  initialDoc: CodexDoc;
  spaceId: string;
  pageId: string;
  /** The space's pages, for resolving cross-references to titles. */
  pages: WikiPage[];
  onChange: (doc: CodexDoc) => void;
  /** Uploads a file and returns the stored image, or throws. */
  onUploadImage: (file: File) => Promise<CodexPageImage>;
  /** Surfaces an upload refusal — the server sniffs, so this is a real path. */
  onImageError: (message: string) => void;
  disabled?: boolean;
}

export function CodexEditor({
  initialDoc,
  spaceId,
  pageId,
  pages,
  onChange,
  onUploadImage,
  onImageError,
  disabled,
}: CodexEditorProps) {
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [uploading, setUploading] = useState(false);

  // onChange is called from a ProseMirror transaction handler that is
  // registered once, so it must not close over a stale callback.
  const onChangeRef = useRef(onChange);
  useEffect(() => {
    onChangeRef.current = onChange;
  }, [onChange]);

  /**
   * The `[[` popup's state, and any transient message the extension raised.
   *
   * Both live here rather than inside the extension because both are
   * application surfaces: a popup that can offer "a page to write later", and a
   * sentence explaining that `![[…]]` degraded to a link. The extension pushes;
   * this renders.
   */
  const [suggestion, setSuggestion] = useState<WikilinkSuggestionState | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  /**
   * The editor, behind a ref.
   *
   * `useEditor`'s configuration is built once at mount, so the paste handler
   * inside `editorProps` cannot close over the editor it belongs to — it does
   * not exist yet. The pages list has the same problem in reverse: it changes
   * as the space's tree loads, and a handler that captured the first value
   * would offer an empty autocomplete forever. Both are read through refs, so
   * every keystroke sees the current value without the editor being rebuilt.
   */
  const editorRef = useRef<Editor | null>(null);
  const pagesRef = useRef(pages);
  useEffect(() => {
    pagesRef.current = pages;
  }, [pages]);

  const editor = useEditor({
    extensions: codexExtensions({
      wikilinks: {
        getPages: () => pagesRef.current,
        getCurrentPageId: () => pageId,
        onSuggestionChange: setSuggestion,
        onNotice: setNotice,
      },
    }),
    content: initialDoc,
    editable: !disabled,
    onUpdate({ editor: e }) {
      onChangeRef.current(e.getJSON() as CodexDoc);
    },
    editorProps: {
      attributes: {
        class: 'outline-none min-h-[24rem] px-4 py-3',
        'data-testid': 'codex-editor-content',
      },
      /**
       * Markdown pasted as plain text becomes real structure.
       *
       * Two guards decide whether to take over the paste at all, and both
       * matter. If the clipboard carries HTML, ProseMirror's own parser is
       * strictly better than anything here — that is a paste from a browser or
       * a word processor, with real structure already in it. And if the text
       * is not markdown-shaped, `markdownPasteContent` returns null and the
       * paste proceeds exactly as it always did: "See issue #42" is prose
       * containing a hash, not a heading.
       */
      handlePaste(_view, event) {
        const clipboard = event.clipboardData;
        if (!clipboard) return false;
        if (clipboard.getData('text/html').trim() !== '') return false;

        const content = markdownPasteContent(clipboard.getData('text/plain'));
        const target = editorRef.current;
        if (!content || !target) return false;

        target.chain().focus().insertContent(content).run();
        return true;
      },
    },
  });

  useEffect(() => {
    editorRef.current = editor;
  }, [editor]);

  useEffect(() => {
    editor?.setEditable(!disabled);
  }, [editor, disabled]);

  async function handleFile(file: File | undefined, target: Editor | null) {
    if (!file || !target) return;
    setUploading(true);
    try {
      const image = await onUploadImage(file);
      // attachment_id, never a URL: the address a reader needs depends on how
      // they reached the page, so the document stores the reference.
      target
        .chain()
        .focus()
        .insertContent({
          type: 'image',
          attrs: { attachment_id: image.attachment_id, alt: '', src: null },
        })
        .run();
    } catch (err) {
      onImageError(err instanceof Error ? err.message : 'That image could not be added.');
    } finally {
      setUploading(false);
    }
  }

  if (!editor) return null;

  return (
    <CodexDocumentProvider value={{ spaceId, pageId, pages }}>
      <div
        data-testid="codex-editor"
        className="overflow-hidden rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-surface)] focus-within:border-[var(--module-codex)]"
      >
        <CodexToolbar
          editor={editor}
          uploadingImage={uploading}
          onInsertImage={() => fileInputRef.current?.click()}
        />

        <input
          ref={fileInputRef}
          type="file"
          accept="image/png,image/jpeg,image/webp,image/gif"
          className="hidden"
          data-testid="codex-image-input"
          onChange={(e) => {
            const file = e.target.files?.[0];
            // Clear first: choosing the same file twice must fire again.
            e.target.value = '';
            void handleFile(file, editor);
          }}
        />

        <div className={editorSurfaceClasses}>
          <EditorContent editor={editor} />
        </div>

        {notice && (
          <p
            data-testid="codex-editor-notice"
            role="status"
            className="flex items-start gap-2 border-t border-[var(--color-border)] bg-[var(--color-surface-hover)] px-4 py-2 text-[var(--text-sm)] text-[var(--color-text-muted)]"
          >
            <span className="flex-1">{notice}</span>
            <button
              type="button"
              onClick={() => setNotice(null)}
              className="shrink-0 text-[var(--text-xs)] underline underline-offset-2"
            >
              Dismiss
            </button>
          </p>
        )}
      </div>

      {/* Every candidate is in the space being edited, so the label is a
          constant today rather than a lookup. It is passed in, not hard-coded
          in the popup, because the SHAPE is the point: a page reference is only
          unambiguous with its space (two spaces may legally hold pages of the
          same title), so the day candidates span spaces this becomes a real
          name per row rather than a redesign. */}
      <WikilinkSuggestions state={suggestion} spaceLabel="this space" />
    </CodexDocumentProvider>
  );
}
