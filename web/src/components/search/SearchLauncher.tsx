import { Search } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useSearch, type SearchHit } from '../../lib/api';
import { useAuth } from '../../lib/auth';
import { cn } from '../../lib/utils';
import {
  ContainerChip,
  ModuleChip,
  SharedChip,
} from './searchPresentation';
import { hitHref, hitReference } from './searchLinks';
import { useDebouncedValue } from './useDebouncedValue';

/**
 * The top bar's "Search everything" control (P6, spec §7).
 *
 * A launcher rather than an inline box: the shell is narrow, the results are
 * mixed across three modules, and an overlay can show enough of each hit to be
 * chooseable. Enter goes to the full results page, so the typeahead is a
 * shortcut and never the only way to reach an answer.
 *
 * NO SNIPPETS here, deliberately. This fires on every settled keystroke, and
 * ts_headline costs one query per module in the page — the reading surface
 * pays that, the shortcut does not.
 */
export function SearchLauncher() {
  const navigate = useNavigate();
  const { user } = useAuth();
  const orgId = user?.orgId ?? '';

  const [open, setOpen] = useState(false);
  const [input, setInput] = useState('');
  const debounced = useDebouncedValue(input, 200);
  const inputRef = useRef<HTMLInputElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);

  const query = useSearch(orgId, debounced, { limit: 8 });

  // Closing clears the box, so the next launch starts clean rather than
  // flashing the previous search's results. Done in the handler rather than in
  // an effect on `open`: state set from an effect is an extra render and a
  // lint error, and every path that closes this control comes through here.
  const close = () => {
    setOpen(false);
    setInput('');
  };

  useEffect(() => {
    if (open) inputRef.current?.focus();
  }, [open]);

  // Close on outside click. Registered only while open, so the app does not
  // carry a document listener for a control nobody has touched.
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (!containerRef.current?.contains(e.target as Node)) {
        setOpen(false);
        setInput('');
      }
    };
    document.addEventListener('mousedown', onDown);
    return () => document.removeEventListener('mousedown', onDown);
  }, [open]);

  const goToResults = () => {
    const term = input.trim();
    if (!term) return;
    close();
    navigate(`/search?q=${encodeURIComponent(term)}`);
  };

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape') close();
    if (e.key === 'Enter') goToResults();
  };

  const hits: SearchHit[] = query.data?.results ?? [];
  const state = query.data?.state;

  return (
    <div ref={containerRef} className="relative">
      <button
        type="button"
        onClick={() => setOpen(true)}
        aria-label="Search everything"
        aria-expanded={open}
        data-testid="search-launcher"
        className={cn(
          'hidden h-8 items-center gap-[var(--space-2)] rounded-[var(--radius-md)] px-[var(--space-3)] lg:flex lg:w-[210px]',
          'border border-[var(--color-border)] bg-[var(--color-bg)]',
          'text-[var(--text-sm)] text-[var(--color-text-muted)]',
          'hover:border-[var(--color-primary)] transition-colors duration-150',
        )}
      >
        <Search className="h-4 w-4" />
        Search everything
      </button>
      <button
        type="button"
        onClick={() => setOpen(true)}
        aria-label="Search everything"
        className={cn(
          'inline-flex h-9 w-9 items-center justify-center rounded-[var(--radius-md)] lg:hidden',
          'text-[var(--color-text-muted)] hover:bg-[var(--color-surface-hover)] hover:text-[var(--color-text)]',
        )}
      >
        <Search className="h-[18px] w-[18px]" />
      </button>

      {open && (
        <div
          className={cn(
            'absolute right-0 top-[calc(100%+var(--space-2))] z-50 w-[min(560px,90vw)]',
            'rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-surface)]',
            'shadow-lg',
          )}
        >
          <input
            ref={inputRef}
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={onKeyDown}
            placeholder="Search everything — Enter for all results"
            data-testid="search-launcher-input"
            className={cn(
              'h-11 w-full rounded-t-[var(--radius-lg)] border-b border-[var(--color-border)]',
              'bg-transparent px-[var(--space-4)] text-[var(--text-base)] text-[var(--color-text)]',
              'focus:outline-none',
            )}
          />
          <LauncherBody
            hasQuery={debounced.trim().length > 0}
            isLoading={query.isLoading}
            isError={query.isError}
            state={state}
            hits={hits}
            onPick={close}
            onSeeAll={goToResults}
          />
        </div>
      )}
    </div>
  );
}

interface BodyProps {
  hasQuery: boolean;
  isLoading: boolean;
  isError: boolean;
  state?: string;
  hits: SearchHit[];
  onPick: () => void;
  onSeeAll: () => void;
}

function LauncherBody({ hasQuery, isLoading, isError, state, hits, onPick, onSeeAll }: BodyProps) {
  const message = (text: string) => (
    <p className="px-[var(--space-4)] py-[var(--space-4)] text-[var(--text-sm)] text-[var(--color-text-muted)]">
      {text}
    </p>
  );

  if (!hasQuery) return message('Start typing to search pages, tickets and items.');
  // Distinguished here too, not only on the results page: a reader who typed
  // stopwords into the launcher deserves the same answer they would get on the
  // full surface, rather than a bare "no results".
  if (isError) return message('Search failed. Press Enter to open the full results page and retry.');
  if (isLoading) return message('Searching…');
  if (state === 'no_readable_scope') return message('You do not have access to any space yet.');
  if (state === 'no_searchable_terms') return message('That query has no searchable terms.');
  if (hits.length === 0) return message('No results.');

  return (
    <>
      <ul className="max-h-[320px] overflow-y-auto py-[var(--space-1)]">
        {hits.map((hit) => (
          <li key={`${hit.module}:${hit.id}`}>
            <a
              href={hitHref(hit)}
              onClick={onPick}
              data-testid="search-launcher-result"
              className="flex items-center gap-[var(--space-2)] px-[var(--space-4)] py-[var(--space-2)] hover:bg-[var(--color-surface-hover)]"
            >
              <ModuleChip module={hit.module} />
              {hitReference(hit) && (
                <span className="shrink-0 font-mono text-[var(--text-xs)] text-[var(--color-text-muted)]">
                  {hitReference(hit)}
                </span>
              )}
              <span className="truncate text-[var(--text-sm)] text-[var(--color-text)]">{hit.title}</span>
              <span className="ml-auto flex shrink-0 items-center gap-[var(--space-2)]">
                {hit.origin === 'share' ? <SharedChip /> : <ContainerChip hit={hit} />}
              </span>
            </a>
          </li>
        ))}
      </ul>
      <button
        type="button"
        onClick={onSeeAll}
        className="w-full border-t border-[var(--color-border)] px-[var(--space-4)] py-[var(--space-2)] text-left text-[var(--text-sm)] text-[var(--color-primary)] hover:bg-[var(--color-surface-hover)]"
      >
        See all results
      </button>
    </>
  );
}
