/**
 * The expand macro: a collapsible section with an editable summary line.
 *
 * The body stays mounted when collapsed rather than being unmounted. A
 * ProseMirror node view that removes its content DOM loses the editor's
 * mapping into that subtree; hiding it with CSS keeps the document intact and
 * collapse a purely visual state.
 */
import { NodeViewContent, NodeViewWrapper } from '@tiptap/react';
import type { NodeViewProps } from '@tiptap/react';
import { ChevronRight } from 'lucide-react';
import { useState } from 'react';

export function ExpandView({ node, updateAttributes, editor }: NodeViewProps) {
  const title = String(node.attrs.title ?? '');
  const [open, setOpen] = useState(true);

  return (
    <NodeViewWrapper className="my-3" data-testid="codex-expand">
      <div className="overflow-hidden rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-surface)]">
        <div
          className="flex items-center gap-2 border-b border-[var(--color-border)] px-2 py-1.5"
          contentEditable={false}
        >
          <button
            type="button"
            onClick={() => setOpen((o) => !o)}
            aria-expanded={open}
            aria-label={open ? 'Collapse section' : 'Expand section'}
            data-testid="codex-expand-toggle"
            className="rounded-[var(--radius-sm)] p-0.5 text-[var(--color-text-muted)] transition-colors hover:text-[var(--color-text)] focus:outline-none focus-visible:ring-1 focus-visible:ring-[var(--module-codex)]"
          >
            <ChevronRight
              className={`h-3.5 w-3.5 transition-transform ${open ? 'rotate-90' : ''}`}
              aria-hidden="true"
            />
          </button>
          {editor.isEditable ? (
            <input
              value={title}
              onChange={(e) => updateAttributes({ title: e.target.value })}
              placeholder="Summary…"
              aria-label="Section summary"
              data-testid="codex-expand-title"
              className="flex-1 bg-transparent text-[var(--text-sm)] font-medium text-[var(--color-text)] placeholder:text-[var(--color-text-muted)] focus:outline-none"
            />
          ) : (
            <span className="flex-1 text-[var(--text-sm)] font-medium text-[var(--color-text)]">
              {title || 'Details'}
            </span>
          )}
        </div>
        <NodeViewContent className={`px-3 py-2 [&>*:last-child]:mb-0 ${open ? '' : 'hidden'}`} />
      </div>
    </NodeViewWrapper>
  );
}
