/**
 * The `[[` autocomplete popup.
 *
 * It renders in the editor's own React tree rather than in a portal driven by
 * the ProseMirror plugin, which is why `wikilinks.ts` pushes state out through
 * a callback instead of mounting anything itself. Two reasons: a second React
 * root inside an editor is a lifecycle problem nobody needs, and the popup has
 * to be able to say "create a page called that" — which is application
 * behaviour, not editor behaviour.
 *
 * Keyboard navigation lives in the plugin, not here. Arrow keys have to be
 * intercepted before ProseMirror moves the cursor, and that can only happen
 * inside the plugin's own key handler — so `activeIndex` arrives as a prop and
 * this component never owns it. Two owners of that index would let the
 * highlighted row and the row Enter selects disagree.
 */
import { FileText, Plus } from 'lucide-react';

import type { WikilinkSuggestionState } from './extensions/wikilinks';

interface WikilinkSuggestionsProps {
  state: WikilinkSuggestionState | null;
  /** The space the candidates come from, shown per row. */
  spaceLabel: string;
}

export function WikilinkSuggestions({ state, spaceLabel }: WikilinkSuggestionsProps) {
  if (!state) return null;

  const { items, query, activeIndex, rect, onChoose } = state;
  const trimmed = query.trim();

  return (
    <div
      data-testid="codex-wikilink-suggestions"
      role="listbox"
      aria-label="Link to a page"
      className="fixed z-50 w-72 overflow-hidden rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-surface)] shadow-[var(--shadow-lg)]"
      style={{
        // Positioned from the caret's own client rect. `fixed` rather than
        // absolute so the popup is not clipped by the editor's overflow-hidden
        // chrome, which is what would happen on the last line of a page.
        left: rect ? Math.round(rect.left) : 0,
        top: rect ? Math.round(rect.bottom + 4) : 0,
      }}
    >
      {items.length > 0 && (
        <ul className="max-h-64 overflow-y-auto">
          {items.map((page, index) => (
            <li key={page.id}>
              <button
                type="button"
                role="option"
                aria-selected={index === activeIndex}
                data-testid="codex-wikilink-option"
                onMouseDown={(e) => {
                  // mousedown, not click: a click would first blur the editor,
                  // and the suggestion's range is resolved against a selection
                  // that no longer exists by then.
                  e.preventDefault();
                  onChoose(page);
                }}
                className={`flex w-full items-center gap-2 px-3 py-2 text-left text-[var(--text-sm)] transition-colors ${
                  index === activeIndex
                    ? 'bg-[color-mix(in_srgb,var(--module-codex)_15%,transparent)]'
                    : 'hover:bg-[var(--color-surface-hover)]'
                }`}
              >
                <FileText
                  className="h-3.5 w-3.5 shrink-0 text-[var(--color-text-muted)]"
                  aria-hidden="true"
                />
                <span className="min-w-0 flex-1 truncate text-[var(--color-text)]">
                  {page.title}
                </span>
                {/* The space each candidate lives in. Every candidate is in the
                    space being edited today, so this reads as constant — it is
                    here because a page reference is only unambiguous WITH its
                    space (two spaces may hold pages of the same title, which is
                    legal), and a list that omitted it would have to be redesigned
                    rather than extended the day the scope widens. */}
                <span className="shrink-0 text-[var(--text-xs)] text-[var(--color-text-muted)]">
                  {spaceLabel}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}

      {trimmed !== '' && (
        <button
          type="button"
          role="option"
          aria-selected={items.length === 0}
          data-testid="codex-wikilink-create"
          onMouseDown={(e) => {
            e.preventDefault();
            onChoose(null);
          }}
          className={`flex w-full items-center gap-2 border-t border-[var(--color-border)] px-3 py-2 text-left text-[var(--text-sm)] text-[var(--color-text-muted)] transition-colors hover:bg-[var(--color-surface-hover)] ${
            items.length === 0 ? 'border-t-0' : ''
          }`}
        >
          <Plus className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
          {/* Not "create the page" — nothing is created here. The link is left
              unresolved and the page is made when somebody clicks it, which is
              what lets an author write a whole document of forward references
              without stopping to create twelve empty pages. */}
          <span>
            Link to “{trimmed}” — a page to write later
          </span>
        </button>
      )}

      {items.length === 0 && trimmed === '' && (
        <p className="px-3 py-2 text-[var(--text-sm)] text-[var(--color-text-muted)]">
          No pages in this space yet.
        </p>
      )}
    </div>
  );
}
