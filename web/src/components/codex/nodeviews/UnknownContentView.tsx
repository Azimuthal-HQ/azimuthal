/**
 * The block preservation placeholder, as a reader and an author see it.
 *
 * ADR-0012 section 2: "It renders as a visible, labelled placeholder — not an
 * error, not a blank space. A reader can see that something exists here, what
 * it was, and that it has been preserved."
 *
 * Three properties are load-bearing:
 *
 * - **Labelled.** The original type and its source are shown, always. A
 *   generic "unsupported content" box would satisfy the letter of the ADR and
 *   none of its purpose.
 * - **Inert.** `contentEditable={false}` throughout. The body shown is
 *   `az_text`, a plain-text rendering — editing it would produce a block whose
 *   displayed content no longer matches the bytes the server will write back,
 *   which is worse than not showing it at all.
 * - **Deletable.** It is a selectable atom, so an author can remove it. That
 *   is a legitimate edit and publish makes them confirm it by name.
 */
import { NodeViewWrapper } from '@tiptap/react';
import type { NodeViewProps } from '@tiptap/react';
import { Shield } from 'lucide-react';

import { PRESERVED_ATTRS } from '../../../lib/codex/schema';
import { preservedSourceLabel, preservedTypeLabel } from '../preserved';

export function UnknownContentView({ node, selected }: NodeViewProps) {
  const attrs = node.attrs as Record<string, string | null>;
  const name = preservedTypeLabel(attrs[PRESERVED_ATTRS.name]);
  const source = preservedSourceLabel(attrs[PRESERVED_ATTRS.source]);
  const text = (attrs[PRESERVED_ATTRS.text] ?? '').trim();

  return (
    <NodeViewWrapper
      className="my-3"
      data-testid="codex-preserved-block"
      data-preserved-id={attrs[PRESERVED_ATTRS.id] ?? undefined}
      data-preserved-name={attrs[PRESERVED_ATTRS.name] ?? undefined}
    >
      <div
        contentEditable={false}
        className={[
          'rounded-[var(--radius-lg)] border border-dashed bg-[var(--color-surface-hover)]',
          selected
            ? 'border-[var(--module-codex)] ring-1 ring-[var(--module-codex)]'
            : 'border-[var(--color-border)]',
        ].join(' ')}
      >
        <div className="flex items-center gap-2 border-b border-dashed border-[var(--color-border)] px-3 py-1.5">
          <Shield className="h-3.5 w-3.5 shrink-0 text-[var(--module-codex)]" aria-hidden="true" />
          <span className="text-[var(--text-xs)] font-semibold uppercase tracking-wide text-[var(--module-codex)]">
            Preserved
          </span>
          <span className="truncate text-[var(--text-xs)] text-[var(--color-text)]">{name}</span>
          <span className="ml-auto shrink-0 text-[var(--text-xs)] text-[var(--color-text-muted)]">
            from {source}
          </span>
        </div>
        <div className="px-3 py-2">
          {text ? (
            <p className="whitespace-pre-wrap break-words font-[var(--font-mono)] text-[var(--text-xs)] leading-relaxed text-[var(--color-text-muted)]">
              {text}
            </p>
          ) : (
            <p className="text-[var(--text-xs)] italic text-[var(--color-text-muted)]">
              This block has no text to show. Its content is kept exactly as it was.
            </p>
          )}
          <p className="mt-2 text-[var(--text-xs)] text-[var(--color-text-muted)]">
            This editor cannot display this content, so it is kept exactly as it was and cannot be
            edited here. Deleting this block removes it permanently.
          </p>
        </div>
      </div>
    </NodeViewWrapper>
  );
}
