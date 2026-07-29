/**
 * The one page picker.
 *
 * Two surfaces need to name a page in this space — the internal-link mark and
 * the page-include macro — so there is one picker and they both use it, per
 * `docs/design/shared-surfaces.md`'s standing rule about second
 * implementations. It filters the space's page list from context rather than
 * searching server-side: the list is already loaded for the tree, and a
 * request per keystroke would buy nothing.
 */
import { useMemo, useState } from 'react';
import { FileText, Search } from 'lucide-react';

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '../ui/dialog';
import { useCodexDocumentContext } from './CodexDocumentContext';
import { filterPages } from './pageSearch';

interface PagePickerProps {
  title: string;
  /** The page currently referenced, so the picker can mark it. */
  selectedId?: string;
  onSelect: (pageId: string) => void;
  onClose: () => void;
}

export function PagePicker({ title, selectedId, onSelect, onClose }: PagePickerProps) {
  const { pages, pageId } = useCodexDocumentContext();
  const [query, setQuery] = useState('');

  // The filtering lives in pageSearch.ts, not here. The `[[` autocomplete has
  // to offer the same candidates by the same rules — including the
  // cannot-refer-to-itself one — and two implementations of that would drift.
  const matches = useMemo(() => filterPages(pages, query, pageId), [pages, pageId, query]);

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent data-testid="codex-page-picker" className="max-w-md">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>Choose a page in this space.</DialogDescription>
        </DialogHeader>

        <label className="relative block">
          <span className="sr-only">Filter pages</span>
          <Search
            className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-[var(--color-text-muted)]"
            aria-hidden="true"
          />
          <input
            // autoFocus is deliberate: a picker opened by an explicit action
            // should be typeable immediately.
            autoFocus
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Filter pages…"
            data-testid="codex-page-picker-filter"
            className="w-full rounded-[var(--radius-md)] border border-[var(--color-border)] bg-[var(--color-input)] py-1.5 pl-8 pr-2 text-[var(--text-sm)] text-[var(--color-text)] placeholder:text-[var(--color-text-muted)] focus:border-[var(--module-codex)] focus:outline-none"
          />
        </label>

        <div className="max-h-64 overflow-y-auto rounded-[var(--radius-md)] border border-[var(--color-border)]">
          {matches.length === 0 ? (
            <p className="px-3 py-4 text-center text-[var(--text-sm)] text-[var(--color-text-muted)]">
              No pages match.
            </p>
          ) : (
            <ul>
              {matches.map((page) => (
                <li key={page.id}>
                  <button
                    type="button"
                    onClick={() => onSelect(page.id)}
                    aria-current={page.id === selectedId ? 'true' : undefined}
                    className={`flex w-full items-center gap-2 border-b border-[var(--color-border)] px-3 py-2 text-left text-[var(--text-sm)] transition-colors last:border-0 hover:bg-[var(--color-surface-hover)] ${
                      page.id === selectedId
                        ? 'bg-[color-mix(in_srgb,var(--module-codex)_15%,transparent)] text-[var(--color-text)]'
                        : 'text-[var(--color-text)]'
                    }`}
                  >
                    <FileText
                      className="h-3.5 w-3.5 shrink-0 text-[var(--color-text-muted)]"
                      aria-hidden="true"
                    />
                    <span className="truncate">{page.title}</span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
