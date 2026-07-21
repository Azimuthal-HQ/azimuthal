import { useEffect, useRef, useState } from 'react';
import { Search, Users, X } from 'lucide-react';
import { cn } from '../lib/utils';
import { useMemberSearch, useTeams, type PersonRef, type Team } from '../lib/api';

/** What a picker selection resolves to. */
export type PickedSubject =
  | { kind: 'user'; id: string; label: string; email?: string }
  | { kind: 'team'; id: string; label: string };

interface PersonTeamPickerProps {
  orgId: string;
  /** 'user' | 'team' | 'both' — which subject kinds are offered. */
  subjects?: 'user' | 'team' | 'both';
  /** Current selection, controlled by the caller. */
  value: PickedSubject | null;
  onChange: (subject: PickedSubject | null) => void;
  placeholder?: string;
  disabled?: boolean;
  /** data-testid prefix; inputs and options derive stable ids from it. */
  testId?: string;
}

/**
 * PersonTeamPicker (P2.5 W5): the reusable replacement for every free-text
 * UUID field. Searches people by name or email through the member-search
 * endpoint and teams by name over the org team list, and returns a typed
 * subject. Nobody knows a UUID; nobody has to.
 */
export function PersonTeamPicker({
  orgId,
  subjects = 'both',
  value,
  onChange,
  placeholder,
  disabled,
  testId = 'person-team-picker',
}: PersonTeamPickerProps) {
  const [query, setQuery] = useState('');
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);

  const wantUsers = subjects !== 'team';
  const wantTeams = subjects !== 'user';

  const people = useMemberSearch(wantUsers ? orgId : '', query);
  const teams = useTeams(wantTeams ? orgId : '');

  useEffect(() => {
    function onClickOutside(e: MouseEvent) {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false);
    }
    document.addEventListener('mousedown', onClickOutside);
    return () => document.removeEventListener('mousedown', onClickOutside);
  }, []);

  const q = query.trim().toLowerCase();
  const teamMatches: Team[] = wantTeams && q
    ? (teams.data ?? []).filter((t) => t.name.toLowerCase().includes(q) || t.slug.toLowerCase().includes(q)).slice(0, 8)
    : [];
  const personMatches: PersonRef[] = wantUsers && q ? (people.data ?? []) : [];
  const searching = q.length > 0 && wantUsers && people.isLoading;
  const empty = q.length > 0 && !searching && teamMatches.length === 0 && personMatches.length === 0;

  if (value) {
    return (
      <span
        data-testid={`${testId}-selected`}
        className={cn(
          'inline-flex h-8 max-w-full items-center gap-[var(--space-2)] rounded-[var(--radius-md)]',
          'border border-[var(--color-border)] bg-[var(--color-surface-hover)] px-[var(--space-2)]',
          'text-[var(--text-sm)] text-[var(--color-text)]',
        )}
      >
        {value.kind === 'team' && <Users className="h-3.5 w-3.5 shrink-0 text-[var(--color-text-muted)]" />}
        <span className="truncate">{value.label}</span>
        {value.kind === 'user' && value.email && (
          <span className="truncate text-[var(--text-xs)] text-[var(--color-text-muted)]">{value.email}</span>
        )}
        <button
          type="button"
          aria-label="Clear selection"
          data-testid={`${testId}-clear`}
          onClick={() => onChange(null)}
          disabled={disabled}
          className="shrink-0 rounded p-0.5 text-[var(--color-text-muted)] hover:bg-[var(--color-border)] hover:text-[var(--color-text)]"
        >
          <X className="h-3.5 w-3.5" />
        </button>
      </span>
    );
  }

  return (
    <div ref={rootRef} className="relative w-64">
      <div className="pointer-events-none absolute inset-y-0 left-2 flex items-center">
        <Search className="h-3.5 w-3.5 text-[var(--color-text-muted)]" />
      </div>
      <input
        type="text"
        role="combobox"
        aria-expanded={open}
        aria-controls={`${testId}-results`}
        data-testid={`${testId}-input`}
        value={query}
        disabled={disabled}
        placeholder={placeholder ?? defaultPlaceholder(subjects)}
        onChange={(e) => {
          setQuery(e.target.value);
          setOpen(true);
        }}
        onFocus={() => setOpen(true)}
        className={cn(
          'h-8 w-full rounded-[var(--radius-md)] border border-[var(--color-border)]',
          'bg-[var(--color-surface)] pl-7 pr-2 text-[var(--text-sm)] text-[var(--color-text)]',
          'placeholder:text-[var(--color-text-muted)] focus:outline-none focus:ring-1 focus:ring-[var(--color-primary)]',
        )}
      />
      {open && q.length > 0 && (
        <ul
          id={`${testId}-results`}
          data-testid={`${testId}-results`}
          className={cn(
            'absolute z-30 mt-1 max-h-64 w-80 overflow-auto rounded-[var(--radius-md)]',
            'border border-[var(--color-border)] bg-[var(--color-surface)] py-1 shadow-[var(--shadow-lg)]',
          )}
        >
          {teamMatches.map((t) => (
            <li key={t.id}>
              <button
                type="button"
                data-testid={`${testId}-option-team-${t.slug}`}
                onClick={() => {
                  onChange({ kind: 'team', id: t.id, label: t.name });
                  setQuery('');
                  setOpen(false);
                }}
                className="flex w-full items-center gap-[var(--space-2)] px-[var(--space-3)] py-[var(--space-2)] text-left text-[var(--text-sm)] text-[var(--color-text)] hover:bg-[var(--color-surface-hover)]"
              >
                <Users className="h-3.5 w-3.5 shrink-0 text-[var(--color-text-muted)]" />
                <span className="truncate">{t.name}</span>
                <span className="ml-auto shrink-0 text-[var(--text-xs)] text-[var(--color-text-muted)]">team</span>
              </button>
            </li>
          ))}
          {personMatches.map((p) => (
            <li key={p.id}>
              <button
                type="button"
                data-testid={`${testId}-option-user-${p.email}`}
                onClick={() => {
                  onChange({ kind: 'user', id: p.id, label: p.display_name, email: p.email });
                  setQuery('');
                  setOpen(false);
                }}
                className="flex w-full items-center gap-[var(--space-2)] px-[var(--space-3)] py-[var(--space-2)] text-left text-[var(--text-sm)] text-[var(--color-text)] hover:bg-[var(--color-surface-hover)]"
              >
                <span className="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-[var(--color-primary-muted)] text-[10px] font-medium text-[var(--color-primary)]">
                  {p.display_name.charAt(0).toUpperCase()}
                </span>
                <span className="truncate">{p.display_name}</span>
                <span className="ml-auto truncate pl-2 text-[var(--text-xs)] text-[var(--color-text-muted)]">{p.email}</span>
              </button>
            </li>
          ))}
          {searching && (
            <li className="px-[var(--space-3)] py-[var(--space-2)] text-[var(--text-sm)] text-[var(--color-text-muted)]">
              Searching…
            </li>
          )}
          {empty && (
            <li
              data-testid={`${testId}-empty`}
              className="px-[var(--space-3)] py-[var(--space-2)] text-[var(--text-sm)] text-[var(--color-text-muted)]"
            >
              No matches for “{query.trim()}”
            </li>
          )}
        </ul>
      )}
    </div>
  );
}

function defaultPlaceholder(subjects: 'user' | 'team' | 'both'): string {
  if (subjects === 'user') return 'Search people by name or email…';
  if (subjects === 'team') return 'Search teams…';
  return 'Search people or teams…';
}
