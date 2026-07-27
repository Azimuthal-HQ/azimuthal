/**
 * The children-display macro: this page's child pages, resolved live.
 *
 * ADR-0012 section 4 puts cross-reference macros in the "renders against
 * Azimuthal's own data" group, so this renders the real list rather than a
 * placeholder for one. The list comes from the space's pages via context, not
 * from a fetch of its own — a page holding several of these would otherwise
 * issue the same request several times.
 */
import { NodeViewWrapper } from '@tiptap/react';
import type { NodeViewProps } from '@tiptap/react';
import { FileText, Network } from 'lucide-react';
import { useMemo } from 'react';
import { Link } from 'react-router-dom';

import { useCodexDocumentContext } from '../CodexDocumentContext';
import type { WikiPage } from '../../../lib/api';

/** Pages descending from `rootId`, to `depth` levels, in tree order. */
function descendants(pages: WikiPage[], rootId: string, depth: number): { page: WikiPage; level: number }[] {
  const out: { page: WikiPage; level: number }[] = [];
  const walk = (parentId: string, level: number) => {
    if (level > depth) return;
    for (const page of pages.filter((p) => p.parent_id === parentId)) {
      out.push({ page, level });
      walk(page.id, level + 1);
    }
  };
  walk(rootId, 1);
  return out;
}

export function ChildrenDisplayView({ node, editor, updateAttributes }: NodeViewProps) {
  const depth = Number(node.attrs.depth ?? 1);
  const { spaceId, pageId, pages } = useCodexDocumentContext();

  const children = useMemo(
    () => (pageId ? descendants(pages, pageId, depth) : []),
    [pages, pageId, depth],
  );

  return (
    <NodeViewWrapper className="my-3" data-testid="codex-children-display">
      <div
        contentEditable={false}
        className="rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-surface-hover)] px-3 py-2"
      >
        <div className="mb-1.5 flex items-center gap-2">
          <Network className="h-3.5 w-3.5 text-[var(--module-codex)]" aria-hidden="true" />
          <span className="text-[var(--text-xs)] font-semibold uppercase tracking-wide text-[var(--color-text-muted)]">
            Child pages
          </span>
          {editor.isEditable && (
            <label className="ml-auto flex items-center gap-1 text-[var(--text-xs)] text-[var(--color-text-muted)]">
              Depth
              <select
                value={depth}
                onChange={(e) => updateAttributes({ depth: Number(e.target.value) })}
                aria-label="Child page depth"
                data-testid="codex-children-depth"
                className="rounded-[var(--radius-sm)] border border-[var(--color-border)] bg-[var(--color-input)] px-1 py-0.5 text-[var(--color-text)] focus:border-[var(--module-codex)] focus:outline-none"
              >
                {[1, 2, 3].map((n) => (
                  <option key={n} value={n}>
                    {n}
                  </option>
                ))}
              </select>
            </label>
          )}
        </div>
        {children.length === 0 ? (
          <p className="text-[var(--text-xs)] italic text-[var(--color-text-muted)]">
            This page has no child pages.
          </p>
        ) : (
          <ul className="space-y-0.5">
            {children.map(({ page, level }) => (
              <li key={page.id} style={{ paddingLeft: `${(level - 1) * 0.75}rem` }}>
                <Link
                  to={`/codex/${spaceId}/pages/${page.id}`}
                  className="flex items-center gap-1.5 truncate text-[var(--text-sm)] text-[var(--color-primary)] hover:underline"
                >
                  <FileText className="h-3 w-3 shrink-0" aria-hidden="true" />
                  {page.title}
                </Link>
              </li>
            ))}
          </ul>
        )}
      </div>
    </NodeViewWrapper>
  );
}
