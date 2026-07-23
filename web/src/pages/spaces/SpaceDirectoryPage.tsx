import { useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { Filter, Grid2x2, Lock } from 'lucide-react';
import { Button } from '../../components/ui/button';
import { Card, CardContent } from '../../components/ui/card';
import { cn } from '../../lib/utils';
import { useAuth } from '../../lib/auth';
import { friendlyErrorMessage, useSpaces, useTeams, type Space, type Team } from '../../lib/api';
import { ModuleChip } from '../../shell/ModuleChip';
import { MODULES, MODULE_KEYS, spacePath, type ModuleKey } from '../../shell/modules';
import { useTeamFocus } from '../../shell/hooks/useTeamFocus';

// ---------------------------------------------------------------------------
// Grouping
// ---------------------------------------------------------------------------

interface DirectoryGroup {
  /** owner_team_id, or 'other' when the owning team cannot be resolved. */
  key: string;
  team: Team | null;
  label: string;
  spaces: Space[];
}

/** Groups directory rows (locked ones included) by owning team, "Other spaces" last. */
function groupByTeam(spaces: Space[], teams: Team[]): DirectoryGroup[] {
  const teamById = new Map(teams.map((t) => [t.id, t]));
  const byKey = new Map<string, DirectoryGroup>();
  for (const space of spaces) {
    const team = space.owner_team_id ? (teamById.get(space.owner_team_id) ?? null) : null;
    const key = team ? team.id : 'other';
    let group = byKey.get(key);
    if (!group) {
      group = { key, team, label: team?.name ?? 'Other spaces', spaces: [] };
      byKey.set(key, group);
    }
    group.spaces.push(space);
  }
  for (const group of byKey.values()) {
    group.spaces.sort((a, b) => a.name.localeCompare(b.name));
  }
  return [...byKey.values()].sort((a, b) => {
    if (a.team === null) return 1;
    if (b.team === null) return -1;
    return a.label.localeCompare(b.label);
  });
}

// ---------------------------------------------------------------------------
// Rows
// ---------------------------------------------------------------------------

function ReadableRow({ space }: { space: Space }) {
  const def = MODULES[space.type];
  return (
    <Link
      to={spacePath(space.type, space.id, def.defaultSubpath)}
      data-testid="directory-space-row"
      className={cn(
        'flex items-center gap-3 rounded-[var(--radius-md)] px-3 py-3',
        'text-[var(--color-text)] transition-colors hover:bg-[var(--color-surface-hover)]',
      )}
    >
      <def.icon className="h-4 w-4 shrink-0 text-[var(--color-primary)]" />
      <span className="min-w-0 flex-1 truncate text-[var(--text-sm)] font-medium">{space.name}</span>
      {space.description && (
        <span className="hidden min-w-0 max-w-md truncate text-[var(--text-xs)] text-[var(--color-text-muted)] md:block">
          {space.description}
        </span>
      )}
      {space.effective_role && (
        <span className="shrink-0 rounded-[var(--radius-full)] bg-[var(--color-surface-hover)] px-2 py-0.5 text-[var(--text-xs)] text-[var(--color-text-muted)]">
          {space.effective_role}
        </span>
      )}
      <ModuleChip module={space.type} />
    </Link>
  );
}

/**
 * A locked row is a discoverable-but-unreadable space: named so people know
 * it exists, deliberately inert — there is no request-access workflow in
 * v0.3, so the copy points at a space admin instead.
 */
function LockedRow({ space }: { space: Space }) {
  return (
    <div
      data-testid="locked-space-row"
      aria-disabled="true"
      className="flex items-center gap-3 rounded-[var(--radius-md)] px-3 py-3 opacity-70"
    >
      <Lock className="h-4 w-4 shrink-0 text-[var(--color-text-muted)]" />
      <span className="min-w-0 flex-1 truncate text-[var(--text-sm)] text-[var(--color-text-muted)]">
        {space.name}
      </span>
      <span className="shrink-0 text-[var(--text-xs)] text-[var(--color-text-muted)]">
        contact a space admin
      </span>
      <ModuleChip module={space.type} />
    </div>
  );
}

// ---------------------------------------------------------------------------
// SpaceDirectoryPage
// ---------------------------------------------------------------------------

/**
 * The org space directory (P2): every space the caller may know about,
 * grouped by owning team. Readable rows link into the space; locked rows
 * stay visible but inert. Group headers can focus the shell on one team.
 */
export function SpaceDirectoryPage() {
  const { user } = useAuth();
  const orgId = user?.orgId ?? '';
  const spacesQuery = useSpaces(orgId);
  const teamsQuery = useTeams(orgId);
  const { setFocus } = useTeamFocus();
  const [moduleFilter, setModuleFilter] = useState<'all' | ModuleKey>('all');

  const groups = useMemo(() => {
    const rows = (spacesQuery.data ?? []).filter(
      (s) => moduleFilter === 'all' || s.type === moduleFilter,
    );
    return groupByTeam(rows, teamsQuery.data ?? []);
  }, [spacesQuery.data, teamsQuery.data, moduleFilter]);

  return (
    <div className="space-y-6" data-testid="space-directory-page">
      <div className="flex items-center justify-between gap-4">
        <div className="flex items-center gap-3">
          <Grid2x2 className="h-6 w-6 text-[var(--color-primary)]" />
          <div>
            <h1 className="text-[var(--text-lg)] font-semibold tracking-[-.01em] text-[var(--color-text)]">Spaces</h1>
            <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
              Every space in the org, grouped by owning team.
            </p>
          </div>
        </div>
        <select
          data-testid="directory-module-filter"
          value={moduleFilter}
          onChange={(e) => setModuleFilter(e.target.value as 'all' | ModuleKey)}
          aria-label="Filter by module"
          className={cn(
            'h-9 rounded-[var(--radius-lg)] border border-[var(--color-border)]',
            'bg-[var(--color-input)] px-3 text-[var(--text-sm)] text-[var(--color-text)]',
            'focus-visible:outline-none focus-visible:border-[var(--color-primary)] focus-visible:ring-1 focus-visible:ring-[var(--color-primary)]',
          )}
        >
          <option value="all">All modules</option>
          {MODULE_KEYS.map((key) => (
            <option key={key} value={key}>
              {MODULES[key].name}
            </option>
          ))}
        </select>
      </div>

      {spacesQuery.isLoading && (
        <div className="flex h-32 items-center justify-center text-[var(--color-text-muted)]">
          Loading spaces…
        </div>
      )}

      {spacesQuery.error && (
        <div className="rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[color-mix(in_srgb,var(--color-danger)_10%,transparent)] p-4 text-[var(--text-sm)] text-[var(--color-danger)]">
          {friendlyErrorMessage(spacesQuery.error, 'The space directory could not be loaded.')}
        </div>
      )}

      {!spacesQuery.isLoading && !spacesQuery.error && groups.length === 0 && (
        <div className="flex min-h-[200px] items-center justify-center rounded-[var(--radius-lg)] border-2 border-dashed border-[var(--color-border)]">
          <p className="text-[var(--color-text-muted)]">
            {moduleFilter === 'all'
              ? 'No spaces in this organization yet.'
              : `No ${MODULES[moduleFilter].name} spaces to show.`}
          </p>
        </div>
      )}

      {groups.map((group) => {
        const team = group.team;
        return (
        <div key={group.key} data-testid="directory-team-group">
          <div className="mb-2 flex items-center justify-between">
            <h2 className="text-[var(--text-sm)] font-semibold uppercase tracking-wider text-[var(--color-text-muted)]">
              {group.label}
            </h2>
            {team && (
              <Button
                variant="ghost"
                size="sm"
                data-testid="directory-focus-button"
                aria-label={`Focus on ${team.name}`}
                onClick={() => setFocus(team.id, team.name)}
              >
                <Filter className="mr-1 h-3.5 w-3.5" />
                Focus
              </Button>
            )}
          </div>
          <Card>
            <CardContent className="p-2">
              {group.spaces.map((space) =>
                space.readable === false ? (
                  <LockedRow key={space.id} space={space} />
                ) : (
                  <ReadableRow key={space.id} space={space} />
                ),
              )}
            </CardContent>
          </Card>
        </div>
        );
      })}
    </div>
  );
}
