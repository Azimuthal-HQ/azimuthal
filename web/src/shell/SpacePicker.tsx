import { useEffect, useMemo, useRef, useState } from 'react';
import { useLocation, useNavigate, useParams } from 'react-router-dom';
import { ChevronDown, Grid2x2, Search, Star } from 'lucide-react';
import { cn } from '../lib/utils';
import { useAuth } from '../lib/auth';
import { useSpaces, type Space } from '../lib/api';
import { MODULES, spacePath, type ModuleKey } from './modules';
import { useRecentSpaces } from './hooks/useRecentSpaces';
import { useStarredSpaces } from './hooks/useStarredSpaces';

/** Sub-routes that survive a space switch; anything else falls back to the module default. */
const PORTABLE_SUBPATHS = new Set(['board', 'backlog', 'sprints', 'roadmap', 'labels', 'settings']);

interface SpacePickerProps {
  module: ModuleKey;
  currentSpace: Space | undefined;
  collapsed: boolean;
}

interface SpaceGroup {
  label: string;
  spaces: Space[];
}

/**
 * groupSpaces groups picker rows by owning team (ADR-0006 point 6). Teams
 * and spaces.owner_team_id arrive in P2 (migrations 007–008); until then
 * every space belongs to the single "All spaces" group. P2 replaces the
 * constant key with the space's owner_team_id and the label with the team
 * name — the rendering below already handles any number of groups.
 */
function groupSpaces(spaces: Space[]): SpaceGroup[] {
  if (spaces.length === 0) return [];
  return [{ label: 'All spaces', spaces }];
}

/**
 * SpacePicker is the space switcher at the top of the sidebar: searchable,
 * grouped by owning team, with recents and starred pinned above the groups.
 */
export function SpacePicker({ module, currentSpace, collapsed }: SpacePickerProps) {
  const def = MODULES[module];
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const { spaceId } = useParams<{ spaceId: string }>();
  const { user } = useAuth();

  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const rootRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  const spacesQuery = useSpaces(user?.orgId ?? '');
  const { recents } = useRecentSpaces(module);
  const { isStarred, toggleStar } = useStarredSpaces();

  const moduleSpaces = useMemo(
    () => (spacesQuery.data ?? []).filter((s) => s.type === module),
    [spacesQuery.data, module],
  );

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return moduleSpaces;
    return moduleSpaces.filter((s) => s.name.toLowerCase().includes(q));
  }, [moduleSpaces, query]);

  const pinned = useMemo(() => {
    if (query.trim()) return [];
    const byId = new Map(moduleSpaces.map((s) => [s.id, s]));
    const seen = new Set<string>();
    const out: Space[] = [];
    for (const id of [...recents, ...moduleSpaces.filter((s) => isStarred(s.id)).map((s) => s.id)]) {
      const space = byId.get(id);
      if (space && !seen.has(id)) {
        seen.add(id);
        out.push(space);
      }
    }
    return out;
  }, [moduleSpaces, recents, isStarred, query]);

  useEffect(() => {
    function handleClickOutside(e: MouseEvent) {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false);
    }
    function handleEscape(e: KeyboardEvent) {
      if (e.key === 'Escape') setOpen(false);
    }
    document.addEventListener('mousedown', handleClickOutside);
    document.addEventListener('keydown', handleEscape);
    return () => {
      document.removeEventListener('mousedown', handleClickOutside);
      document.removeEventListener('keydown', handleEscape);
    };
  }, []);

  useEffect(() => {
    if (open) inputRef.current?.focus();
  }, [open]);

  const selectSpace = (space: Space) => {
    const lastSegment = pathname.split('/').filter(Boolean).at(-1) ?? '';
    const subpath =
      spaceId && lastSegment !== spaceId && PORTABLE_SUBPATHS.has(lastSegment)
        ? lastSegment
        : def.defaultSubpath;
    setOpen(false);
    setQuery('');
    navigate(spacePath(module, space.id, subpath));
  };

  const row = (space: Space) => (
    <div
      key={space.id}
      className={cn(
        'group flex items-center gap-[var(--space-2)] rounded-[var(--radius-md)]',
        space.id === currentSpace?.id
          ? 'bg-[var(--color-primary-muted)] text-[var(--color-primary)]'
          : 'text-[var(--color-text)] hover:bg-[var(--color-surface-hover)]',
      )}
    >
      <button
        type="button"
        onClick={() => selectSpace(space)}
        className="flex min-w-0 flex-1 items-center gap-[var(--space-2)] px-[var(--space-2)] py-[var(--space-2)] text-left text-[var(--text-sm)]"
      >
        <def.icon className="h-4 w-4 shrink-0 text-[var(--color-text-muted)]" />
        <span className="truncate">{space.name}</span>
      </button>
      <button
        type="button"
        onClick={() => toggleStar(space.id)}
        aria-label={isStarred(space.id) ? `Unstar ${space.name}` : `Star ${space.name}`}
        className={cn(
          'mr-[var(--space-2)] shrink-0 rounded p-1 transition-opacity',
          isStarred(space.id)
            ? 'text-[var(--color-warning)]'
            : 'text-[var(--color-text-muted)] opacity-0 hover:text-[var(--color-text)] group-hover:opacity-100',
        )}
      >
        <Star className={cn('h-3.5 w-3.5', isStarred(space.id) && 'fill-current')} />
      </button>
    </div>
  );

  const groupHeading = (label: string) => (
    <p className="px-[var(--space-2)] pb-[var(--space-1)] pt-[var(--space-2)] text-[10px] font-medium uppercase tracking-wider text-[var(--color-text-muted)]">
      {label}
    </p>
  );

  return (
    <div className="relative" ref={rootRef}>
      <button
        type="button"
        data-testid="space-picker-button"
        onClick={() => setOpen((prev) => !prev)}
        title={currentSpace?.name ?? `Select a ${def.name} space`}
        className={cn(
          'flex w-full items-center gap-[var(--space-2)] rounded-[var(--radius-lg)]',
          'border border-[var(--color-border)] px-[var(--space-2)] py-[var(--space-2)]',
          'text-[var(--text-sm)] font-medium text-[var(--color-text)]',
          'hover:bg-[var(--color-surface-hover)] transition-colors duration-150',
          collapsed && 'justify-center border-transparent',
        )}
      >
        <def.icon className="h-4 w-4 shrink-0 text-[var(--color-primary)]" />
        {!collapsed && (
          <>
            <span className="min-w-0 flex-1 truncate text-left">
              {currentSpace?.name ?? `Select a ${def.name} space`}
            </span>
            <ChevronDown className="h-4 w-4 shrink-0 text-[var(--color-text-muted)]" />
          </>
        )}
      </button>

      {open && (
        <div
          data-testid="space-picker"
          className={cn(
            'absolute left-0 top-full z-50 mt-[var(--space-1)] w-72 rounded-[var(--radius-lg)] p-[var(--space-2)]',
            'bg-[var(--color-surface)] border border-[var(--color-border)] shadow-[var(--shadow-lg)]',
          )}
        >
          <div
            className={cn(
              'mb-[var(--space-2)] flex items-center gap-[var(--space-2)] rounded-[var(--radius-md)] px-[var(--space-2)]',
              'border border-[var(--color-border)] bg-[var(--color-bg)]',
            )}
          >
            <Search className="h-4 w-4 shrink-0 text-[var(--color-text-muted)]" />
            <input
              ref={inputRef}
              data-testid="space-picker-search"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Find a space"
              className="h-8 w-full bg-transparent text-[var(--text-sm)] text-[var(--color-text)] outline-none placeholder:text-[var(--color-text-muted)]"
            />
          </div>

          <div className="max-h-80 overflow-y-auto">
            {pinned.length > 0 && (
              <>
                {groupHeading('Recent and starred')}
                {pinned.map(row)}
              </>
            )}
            {groupSpaces(filtered).map((group) => (
              <div key={group.label}>
                {groupHeading(group.label)}
                {group.spaces.map(row)}
              </div>
            ))}
            {filtered.length === 0 && (
              <p className="px-[var(--space-2)] py-[var(--space-4)] text-[var(--text-sm)] text-[var(--color-text-muted)]">
                No spaces match that.
              </p>
            )}
          </div>

          <div className="mt-[var(--space-2)] border-t border-[var(--color-border)] pt-[var(--space-2)]">
            <button
              type="button"
              onClick={() => {
                setOpen(false);
                navigate('/');
              }}
              className={cn(
                'flex w-full items-center gap-[var(--space-2)] rounded-[var(--radius-md)] px-[var(--space-2)] py-[var(--space-2)]',
                'text-[var(--text-sm)] text-[var(--color-text-muted)] hover:bg-[var(--color-surface-hover)] hover:text-[var(--color-text)]',
              )}
            >
              <Grid2x2 className="h-4 w-4" />
              Browse all spaces
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
