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
import type { CodexPageImage, WikiPage } from '../../lib/api';
import { CodexDocumentProvider } from './CodexDocumentContext';
import { CodexToolbar } from './CodexToolbar';
import { codexExtensions } from './extensions';
import { editorSurfaceClasses } from './editorStyles';

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

  const editor = useEditor({
    extensions: codexExtensions(),
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
    },
  });

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
      </div>
    </CodexDocumentProvider>
  );
}
