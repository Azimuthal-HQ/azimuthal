import {
  useEffect,
  useId,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
} from 'react';
import { cn } from '../lib/utils';
import { usePageSuggestions, type PageSuggestion } from '../lib/api';
import { Input } from './ui/input';

export interface PageRefFieldProps {
  /** Org whose pages the typeahead searches; '' disables the query. */
  orgId: string;
  /** Called with the chosen page. The input clears itself after a pick. */
  onSelect: (page: PageSuggestion) => void;
  placeholder?: string;
  disabled?: boolean;
  /** data-testid of the input; every inner id derives from it. */
  testId?: string;
  className?: string;
}

/**
 * PageRefField (A4): the page picker behind the relations panel, a typeahead
 * over every Codex page the caller can read (`GET /orgs/{orgID}/pages/suggest`).
 *
 * It follows TicketRefField for the mechanics — the open-gated React-Query
 * lookup, the combobox a11y wiring, the flip-up positioning, click-outside
 * closing, the `${testId}-…` id scheme — with one deliberate difference in the
 * opposite direction from that component's free-text rule: this field resolves
 * to a SELECTION, like PersonTeamPicker, because a relation target is a real
 * entity's (type, id) and free text has nowhere to go. Typing only filters;
 * only choosing a suggestion calls onSelect, and Enter accepts only an
 * explicitly highlighted row. If you find yourself submitting the raw text,
 * you are inventing a page reference the server has no way to resolve.
 *
 * Escape closes the suggestion list; inside a Radix dialog it also closes the
 * dialog, exactly as TicketRefField documents — shipped behaviour, left alone.
 */
export function PageRefField({
  orgId,
  onSelect,
  placeholder = 'Search pages…',
  disabled = false,
  testId = 'page-ref',
  className,
}: PageRefFieldProps) {
  const [text, setText] = useState('');
  const [open, setOpen] = useState(false);
  // -1 = nothing highlighted; Enter only accepts a suggestion above that.
  const [active, setActive] = useState(-1);
  // Flip above the input when there is no room below — the panel is
  // absolutely positioned, and opening downward covers whatever follows the
  // field (see TicketRefField for the dialog-button incident this prevents).
  const [dropUp, setDropUp] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);
  const reactId = useId();
  const inputId = `${testId}-${reactId}`;
  const listId = `${testId}-suggestions`;

  const q = text.trim();
  // The query is disabled until the field is in use, so a panel that merely
  // renders the picker never asks the server for suggestions.
  const suggestions = usePageSuggestions(open ? orgId : '', q);

  useEffect(() => {
    function onClickOutside(e: MouseEvent) {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false);
    }
    document.addEventListener('mousedown', onClickOutside);
    return () => document.removeEventListener('mousedown', onClickOutside);
  }, []);

  // Measured in the event that opens (or re-filters) the list, not in an
  // effect — same occasions TicketRefField's effect fired on, without the
  // render-then-set cascade the lint rule exists to catch in NEW components.
  // jsdom reports zeroes for every rect, which resolves to "room below" — the
  // stable downward default unit tests rely on.
  const measureDropUp = () => {
    const el = rootRef.current;
    if (!el) return;
    const PANEL_MAX_PX = 256; // matches max-h-64
    const below = window.innerHeight - el.getBoundingClientRect().bottom;
    setDropUp(below < PANEL_MAX_PX && el.getBoundingClientRect().top > below);
  };

  const options: PageSuggestion[] = open && q ? (suggestions.data ?? []) : [];
  const searching = open && q.length > 0 && suggestions.isLoading;
  const listVisible = open && q.length > 0 && (searching || options.length > 0);

  const accept = (s: PageSuggestion) => {
    onSelect(s);
    setText('');
    setOpen(false);
    setActive(-1);
  };

  const onKeyDown = (e: ReactKeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Escape' || e.key === 'Tab') {
      setOpen(false);
      setActive(-1);
      return;
    }
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      if (!open) {
        measureDropUp();
        setOpen(true);
        return;
      }
      setActive((i) => (options.length === 0 ? -1 : Math.min(i + 1, options.length - 1)));
      return;
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault();
      setActive((i) => Math.max(i - 1, -1));
      return;
    }
    if (e.key === 'Enter') {
      // A relation cannot be created from unresolved text, so Enter without a
      // highlighted row does nothing rather than guessing the first match.
      if (open && active >= 0 && active < options.length) {
        e.preventDefault();
        accept(options[active]);
      }
    }
  };

  return (
    <div ref={rootRef} className={cn('relative', className)}>
      <Input
        id={inputId}
        type="text"
        role="combobox"
        autoComplete="off"
        aria-expanded={listVisible}
        aria-controls={listId}
        aria-autocomplete="list"
        aria-activedescendant={active >= 0 ? `${listId}-${active}` : undefined}
        aria-label="Search pages"
        data-testid={testId}
        value={text}
        disabled={disabled}
        placeholder={placeholder}
        onChange={(e) => {
          setText(e.target.value);
          measureDropUp();
          setOpen(true);
          setActive(-1);
        }}
        onFocus={() => {
          measureDropUp();
          setOpen(true);
        }}
        onKeyDown={onKeyDown}
      />
      {listVisible && (
        <ul
          id={listId}
          role="listbox"
          aria-label="Page suggestions"
          data-testid={`${testId}-suggestions`}
          className={cn(
            'absolute z-30 max-h-64 w-full min-w-[18rem] overflow-auto rounded-[var(--radius-md)]',
            'border border-[var(--color-border)] bg-[var(--color-surface)] py-1 shadow-[var(--shadow-lg)]',
            dropUp ? 'bottom-full mb-1' : 'mt-1',
          )}
        >
          {options.map((s, i) => (
            <li
              key={s.page_id}
              id={`${listId}-${i}`}
              role="option"
              aria-selected={i === active}
              data-testid={`${testId}-option-${s.page_id}`}
              // Keep focus in the input: a blur here would tear the list
              // down before the click landed.
              onMouseDown={(e) => e.preventDefault()}
              onMouseEnter={() => setActive(i)}
              onClick={() => accept(s)}
              className={cn(
                'cursor-pointer px-[var(--space-3)] py-[var(--space-2)] text-left',
                'hover:bg-[var(--color-surface-hover)]',
                i === active && 'bg-[var(--color-surface-hover)]',
              )}
            >
              <span className="block truncate text-[var(--text-sm)] text-[var(--color-text)]">
                {s.title}
              </span>
              <span className="mt-0.5 block text-[var(--text-xs)] text-[var(--color-text-muted)]">
                {s.space_key} · {s.space_name}
              </span>
            </li>
          ))}
          {searching && (
            <li className="px-[var(--space-3)] py-[var(--space-2)] text-[var(--text-sm)] text-[var(--color-text-muted)]">
              Searching…
            </li>
          )}
        </ul>
      )}
    </div>
  );
}
