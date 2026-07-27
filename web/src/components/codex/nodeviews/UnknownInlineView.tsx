/**
 * The inline preservation placeholder.
 *
 * Same contract as the block one — labelled, inert, deletable — in an inline
 * shape. It renders the preserved text so the sentence around it still reads,
 * with the type on hover and a marker that says the content is kept rather
 * than rendered.
 */
import { NodeViewWrapper } from '@tiptap/react';
import type { NodeViewProps } from '@tiptap/react';

import { PRESERVED_ATTRS } from '../../../lib/codex/schema';
import { preservedSourceLabel, preservedTypeLabel } from '../preserved';

export function UnknownInlineView({ node, selected }: NodeViewProps) {
  const attrs = node.attrs as Record<string, string | null>;
  const name = preservedTypeLabel(attrs[PRESERVED_ATTRS.name]);
  const source = preservedSourceLabel(attrs[PRESERVED_ATTRS.source]);
  const text = (attrs[PRESERVED_ATTRS.text] ?? '').trim();

  return (
    <NodeViewWrapper
      as="span"
      data-testid="codex-preserved-inline"
      data-preserved-id={attrs[PRESERVED_ATTRS.id] ?? undefined}
      data-preserved-name={attrs[PRESERVED_ATTRS.name] ?? undefined}
    >
      <span
        contentEditable={false}
        title={`Preserved ${name} (from ${source}). This editor cannot display it, so it is kept exactly as it was.`}
        className={[
          'mx-0.5 inline-flex items-baseline gap-1 rounded-[var(--radius-sm)] border border-dashed px-1 py-px align-baseline',
          'text-[var(--color-text-muted)]',
          selected
            ? 'border-[var(--module-codex)] bg-[color-mix(in_srgb,var(--module-codex)_18%,transparent)]'
            : 'border-[var(--color-border)] bg-[var(--color-surface-hover)]',
        ].join(' ')}
      >
        <span aria-hidden="true" className="text-[var(--module-codex)]">
          ◈
        </span>
        <span className="text-[var(--text-xs)]">{text || name}</span>
        <span className="sr-only">
          {' '}
          (preserved {name} from {source}; this editor cannot display it)
        </span>
      </span>
    </NodeViewWrapper>
  );
}
