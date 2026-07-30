import { AlertCircle, Lock, Search, SearchX } from 'lucide-react';
import { useEffect, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import {
  ContainerChip,
  ModuleChip,
  SharedChip,
  Snippet,
} from '../../components/search/searchPresentation';
import { hitHref, hitReference } from '../../components/search/searchLinks';
import { useDebouncedValue } from '../../components/search/useDebouncedValue';
import { useSearch, type SearchHit, type SearchResults } from '../../lib/api';
import { useAuth } from '../../lib/auth';
import { cn } from '../../lib/utils';
import { EmptyState } from '../../shell/EmptyState';

/**
 * The full cross-module search surface (P6, spec §5/§7).
 *
 * Results are MIXED and rank-ordered, not grouped by module. One ranking is the
 * product claim — "search everything" — and grouping would need a rule for
 * ordering the groups against each other that does not exist. Provenance is a
 * per-row chip instead, so a reader still sees which module a hit came from.
 *
 * The query lives in the URL. A search worth reading is worth linking to, and it
 * lets the top bar hand this page a query by navigating rather than by sharing
 * state with it.
 */
export function SearchPage() {
  const { user } = useAuth();
  const orgId = user?.orgId ?? '';
  const [params, setParams] = useSearchParams();
  const urlQuery = params.get('q') ?? '';

  const [input, setInput] = useState(urlQuery);
  // Typing updates the box immediately and the URL only once typing settles, so
  // the address bar does not collect a history entry per keystroke.
  const debounced = useDebouncedValue(input, 250);

  useEffect(() => {
    if (debounced === urlQuery) return;
    setParams(debounced ? { q: debounced } : {}, { replace: true });
  }, [debounced, urlQuery, setParams]);

  // Snippets on: this is the reading surface, and the caller the per-page
  // ts_headline cost was budgeted for.
  const query = useSearch(orgId, debounced, { limit: 25, snippet: true });

  return (
    <div className="mx-auto w-full max-w-[860px] px-[var(--space-4)] py-[var(--space-6)]">
      <label className="relative block">
        <span className="sr-only">Search everything</span>
        <Search
          className="pointer-events-none absolute left-[var(--space-3)] top-1/2 h-4 w-4 -translate-y-1/2 text-[var(--color-text-muted)]"
          aria-hidden
        />
        <input
          autoFocus
          value={input}
          onChange={(e) => setInput(e.target.value)}
          placeholder="Search everything — try type:ticket or tag:runbooks"
          data-testid="search-input"
          className={cn(
            'h-11 w-full rounded-[var(--radius-md)] pl-[var(--space-9)] pr-[var(--space-3)]',
            'border border-[var(--color-border)] bg-[var(--color-bg)]',
            'text-[var(--text-base)] text-[var(--color-text)]',
            'focus:border-[var(--color-primary)] focus:outline-none',
          )}
        />
      </label>

      <div className="mt-[var(--space-5)]">
        <SearchBody
          hasQuery={debounced.trim().length > 0}
          isLoading={query.isLoading}
          isError={query.isError}
          errorMessage={query.error?.message}
          onRetry={() => void query.refetch()}
          data={query.data}
        />
      </div>
    </div>
  );
}

interface BodyProps {
  hasQuery: boolean;
  isLoading: boolean;
  isError: boolean;
  errorMessage?: string;
  onRetry: () => void;
  data?: SearchResults;
}

/**
 * The states, kept apart on purpose.
 *
 * "Nothing matched", "there was nothing to search for", "you can read nothing"
 * and "the request failed" are four different answers, and collapsing any of
 * them into a shared empty state is the S2-of-#64 lesson. An error rendered as
 * "no results" is the worst of the four: it tells the reader their query was
 * fine and the answer was zero.
 */
export function SearchBody({
  hasQuery,
  isLoading,
  isError,
  errorMessage,
  onRetry,
  data,
}: BodyProps) {
  if (!hasQuery) {
    return (
      <EmptyState
        icon={Search}
        title="Search everything"
        description="Find pages, tickets and items across every space you can read. Narrow with type:page, type:ticket, type:item, or tag:name."
      />
    );
  }

  if (isError) {
    return (
      <EmptyState
        icon={AlertCircle}
        title="Search failed"
        description={errorMessage || 'Something went wrong running that search.'}
        action={
          <button
            type="button"
            onClick={onRetry}
            className="rounded-[var(--radius-md)] border border-[var(--color-border)] px-[var(--space-3)] py-[var(--space-1)] text-[var(--text-sm)] hover:border-[var(--color-primary)]"
          >
            Try again
          </button>
        }
      />
    );
  }

  if (isLoading || !data) {
    return <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">Searching…</p>;
  }

  if (data.state === 'no_readable_scope') {
    return (
      <EmptyState
        icon={Lock}
        title="Nothing to search yet"
        description="You do not have access to any space, and nothing has been shared with you. Ask an administrator for access to a space."
      />
    );
  }

  if (data.state === 'no_searchable_terms') {
    return (
      <EmptyState
        icon={SearchX}
        title="No searchable terms"
        description="That query is made only of very common words or punctuation, so there is nothing to match on. Try a more specific word."
      />
    );
  }

  if (data.results.length === 0) {
    return (
      <EmptyState
        icon={SearchX}
        title="No results"
        description="Nothing matched that search. Check the spelling, or widen it by removing a type: or tag: filter."
      />
    );
  }

  return (
    <>
      <p className="mb-[var(--space-3)] text-[var(--text-sm)] text-[var(--color-text-muted)]">
        {data.results.length} result{data.results.length === 1 ? '' : 's'}
        {data.tag ? ` tagged #${data.tag}` : ''}
        {data.modules.length < 3 ? ` in ${data.modules.join(', ')}` : ''}
      </p>
      <ul className="flex flex-col gap-[var(--space-1)]">
        {data.results.map((hit) => (
          <li key={`${hit.module}:${hit.id}`}>
            <ResultRow hit={hit} />
          </li>
        ))}
      </ul>
    </>
  );
}

export function ResultRow({ hit }: { hit: SearchHit }) {
  const reference = hitReference(hit);
  return (
    <Link
      to={hitHref(hit)}
      data-testid="search-result"
      data-module={hit.module}
      className={cn(
        'block rounded-[var(--radius-md)] border border-transparent px-[var(--space-3)] py-[var(--space-2)]',
        'hover:border-[var(--color-border)] hover:bg-[var(--color-surface-hover)]',
      )}
    >
      <div className="flex items-center gap-[var(--space-2)]">
        <ModuleChip module={hit.module} />
        {reference && (
          <span className="shrink-0 font-mono text-[var(--text-xs)] text-[var(--color-text-muted)]">
            {reference}
          </span>
        )}
        <span className="truncate text-[var(--text-base)] text-[var(--color-text)]">{hit.title}</span>
        <span className="ml-auto flex shrink-0 items-center gap-[var(--space-2)]">
          {hit.origin === 'share' ? <SharedChip /> : <ContainerChip hit={hit} />}
        </span>
      </div>
      {hit.snippet && <Snippet text={hit.snippet} />}
    </Link>
  );
}
