/**
 * The table-of-contents macro, rendered from the document's own headings.
 *
 * It reads the live document rather than a stored copy, so it is never stale
 * — and it renders the real thing in the editor too, which is the point of a
 * first-class macro as opposed to a labelled box saying "a table of contents
 * goes here".
 */
import { NodeViewWrapper } from '@tiptap/react';
import type { NodeViewProps } from '@tiptap/react';
import { ListTree } from 'lucide-react';
import { useMemo } from 'react';

interface HeadingEntry {
  level: number;
  text: string;
}

export function TableOfContentsView({ node, editor, updateAttributes }: NodeViewProps) {
  const maxLevel = Number(node.attrs.maxLevel ?? 3);

  const headings = useMemo<HeadingEntry[]>(() => {
    const out: HeadingEntry[] = [];
    editor.state.doc.descendants((child) => {
      if (child.type.name === 'heading') {
        const level = Number(child.attrs.level ?? 1);
        if (level <= maxLevel) out.push({ level, text: child.textContent });
      }
      return true;
    });
    return out;
    // The editor's document is mutable in place, so `editor.state` is the
    // dependency that actually changes when the headings do.
  }, [editor.state, maxLevel]);

  return (
    <NodeViewWrapper className="my-3" data-testid="codex-toc">
      <div
        contentEditable={false}
        className="rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-surface-hover)] px-3 py-2"
      >
        <div className="mb-1.5 flex items-center gap-2">
          <ListTree className="h-3.5 w-3.5 text-[var(--module-codex)]" aria-hidden="true" />
          <span className="text-[var(--text-xs)] font-semibold uppercase tracking-wide text-[var(--color-text-muted)]">
            On this page
          </span>
          {editor.isEditable && (
            <label className="ml-auto flex items-center gap-1 text-[var(--text-xs)] text-[var(--color-text-muted)]">
              Depth
              <select
                value={maxLevel}
                onChange={(e) => updateAttributes({ maxLevel: Number(e.target.value) })}
                aria-label="Heading depth"
                data-testid="codex-toc-depth"
                className="rounded-[var(--radius-sm)] border border-[var(--color-border)] bg-[var(--color-input)] px-1 py-0.5 text-[var(--color-text)] focus:border-[var(--module-codex)] focus:outline-none"
              >
                {[1, 2, 3, 4, 5, 6].map((n) => (
                  <option key={n} value={n}>
                    {n}
                  </option>
                ))}
              </select>
            </label>
          )}
        </div>
        {headings.length === 0 ? (
          <p className="text-[var(--text-xs)] italic text-[var(--color-text-muted)]">
            No headings on this page yet.
          </p>
        ) : (
          <ul className="space-y-0.5">
            {headings.map((h, i) => (
              <li
                key={`${h.level}-${i}-${h.text}`}
                style={{ paddingLeft: `${(h.level - 1) * 0.75}rem` }}
                className="truncate text-[var(--text-sm)] text-[var(--color-text)]"
              >
                {h.text || <span className="italic text-[var(--color-text-muted)]">Untitled</span>}
              </li>
            ))}
          </ul>
        )}
      </div>
    </NodeViewWrapper>
  );
}
