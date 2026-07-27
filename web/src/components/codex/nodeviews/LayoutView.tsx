/**
 * The layout macro: a row of columns.
 *
 * The columns are real document content — `layoutColumn` nodes holding blocks
 * — so `NodeViewContent` renders them and the grid is styling over the top.
 * Column count is a property of the content, not an attribute, which is why
 * adding and removing one is a document edit rather than an attribute change.
 */
import { NodeViewContent, NodeViewWrapper } from '@tiptap/react';
import type { NodeViewProps } from '@tiptap/react';
import { Columns3, Minus, Plus } from 'lucide-react';

const MAX_COLUMNS = 4;
const MIN_COLUMNS = 1;

export function LayoutView({ node, editor, getPos }: NodeViewProps) {
  const columns = node.childCount;

  function addColumn() {
    if (columns >= MAX_COLUMNS) return;
    const pos = getPos();
    if (typeof pos !== 'number') return;
    editor
      .chain()
      .focus()
      .insertContentAt(pos + node.nodeSize - 1, {
        type: 'layoutColumn',
        content: [{ type: 'paragraph' }],
      })
      .run();
  }

  function removeColumn() {
    if (columns <= MIN_COLUMNS) return;
    const pos = getPos();
    if (typeof pos !== 'number') return;
    const last = node.child(columns - 1);
    const from = pos + node.nodeSize - 1 - last.nodeSize;
    editor
      .chain()
      .focus()
      .deleteRange({ from, to: pos + node.nodeSize - 1 })
      .run();
  }

  return (
    <NodeViewWrapper className="my-3" data-testid="codex-layout" data-columns={columns}>
      <div className="rounded-[var(--radius-lg)] border border-dashed border-[var(--color-border)] p-2">
        {editor.isEditable && (
          <div
            className="mb-1.5 flex items-center gap-2 text-[var(--color-text-muted)]"
            contentEditable={false}
          >
            <Columns3 className="h-3.5 w-3.5" aria-hidden="true" />
            <span className="text-[var(--text-xs)] font-semibold uppercase tracking-wide">
              {columns} column{columns === 1 ? '' : 's'}
            </span>
            <span className="ml-auto flex gap-1">
              <button
                type="button"
                onClick={removeColumn}
                disabled={columns <= MIN_COLUMNS}
                aria-label="Remove column"
                data-testid="codex-layout-remove-column"
                className="rounded-[var(--radius-sm)] p-0.5 transition-colors hover:bg-[var(--color-surface-hover)] hover:text-[var(--color-text)] disabled:opacity-30 focus:outline-none focus-visible:ring-1 focus-visible:ring-[var(--module-codex)]"
              >
                <Minus className="h-3.5 w-3.5" aria-hidden="true" />
              </button>
              <button
                type="button"
                onClick={addColumn}
                disabled={columns >= MAX_COLUMNS}
                aria-label="Add column"
                data-testid="codex-layout-add-column"
                className="rounded-[var(--radius-sm)] p-0.5 transition-colors hover:bg-[var(--color-surface-hover)] hover:text-[var(--color-text)] disabled:opacity-30 focus:outline-none focus-visible:ring-1 focus-visible:ring-[var(--module-codex)]"
              >
                <Plus className="h-3.5 w-3.5" aria-hidden="true" />
              </button>
            </span>
          </div>
        )}
        <NodeViewContent
          className="grid gap-3 [&>.codex-layout-column]:min-w-0 [&>.codex-layout-column>*:last-child]:mb-0"
          style={{ gridTemplateColumns: `repeat(${Math.max(columns, 1)}, minmax(0, 1fr))` }}
        />
      </div>
    </NodeViewWrapper>
  );
}
