import { FileText, Layers, Share2, Ticket } from 'lucide-react';
import { splitSnippet, type SearchHit, type SearchModule } from '../../lib/api';
import { cn } from '../../lib/utils';

/**
 * Shared presentation for a cross-module search hit (P6, spec §7).
 *
 * Both surfaces — the top-bar typeahead and the full results page — render the
 * same row, so the disclosure rules below are stated once. A second copy would
 * be a second place for a share-only hit to accidentally grow a container.
 */

const MODULE_META: Record<SearchModule, { label: string; icon: typeof FileText }> = {
  codex: { label: 'Codex', icon: FileText },
  beacon: { label: 'Beacon', icon: Ticket },
  vector: { label: 'Vector', icon: Layers },
};

export function ModuleChip({ module }: { module: SearchModule }) {
  const meta = MODULE_META[module];
  const Icon = meta.icon;
  return (
    <span
      className={cn(
        'inline-flex shrink-0 items-center gap-1 rounded-[var(--radius-sm)] px-[var(--space-2)] py-[2px]',
        'bg-[var(--color-surface-hover)] text-[var(--text-xs)] text-[var(--color-text-muted)]',
      )}
    >
      <Icon className="h-3 w-3" aria-hidden />
      {meta.label}
    </span>
  );
}

/**
 * Provenance for a hit reached only through a share.
 *
 * This is what REPLACES the container chip, rather than sitting beside it. The
 * viewer is being told how they can see this at all — not where it lives, which
 * is precisely what they may not learn.
 */
export function SharedChip() {
  return (
    <span
      className={cn(
        'inline-flex shrink-0 items-center gap-1 rounded-[var(--radius-sm)] px-[var(--space-2)] py-[2px]',
        'bg-[var(--color-primary-muted)] text-[var(--text-xs)] text-[var(--color-primary)]',
      )}
      title="Shared with you. You do not have access to the space this lives in."
    >
      <Share2 className="h-3 w-3" aria-hidden />
      Shared
    </span>
  );
}

/**
 * A ts_headline excerpt, rendered as TEXT.
 *
 * splitSnippet turns the server's U+0002/U+0003 delimiters into runs, and each
 * run becomes a text node inside a real element. Nothing here interprets the
 * snippet as markup, which matters because ts_headline escapes nothing and the
 * excerpt is assembled from stored page bodies — an innerHTML render would put
 * whatever a page contains straight into the document.
 */
export function Snippet({ text }: { text: string }) {
  const parts = splitSnippet(text);
  if (parts.length === 0) return null;
  return (
    <p className="mt-[2px] line-clamp-2 text-[var(--text-sm)] text-[var(--color-text-muted)]">
      {parts.map((part, i) =>
        part.match ? (
          <mark
            key={i}
            className="rounded-[2px] bg-[var(--color-primary-muted)] px-[2px] text-[var(--color-text)]"
          >
            {part.text}
          </mark>
        ) : (
          <span key={i}>{part.text}</span>
        ),
      )}
    </p>
  );
}

/**
 * The container a hit lives in — rendered ONLY when the server sent one.
 *
 * There is no fallback string. A share-only hit has no container to name, and
 * "Unknown space" would be an invention; the SharedChip is what stands in its
 * place.
 */
export function ContainerChip({ hit }: { hit: SearchHit }) {
  if (!hit.space_name) return null;
  return (
    <span className="shrink-0 text-[var(--text-xs)] text-[var(--color-text-muted)]">
      {hit.space_name}
    </span>
  );
}
