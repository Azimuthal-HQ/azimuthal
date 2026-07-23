import { useMemo } from 'react';
import { Link, Navigate, useParams } from 'react-router-dom';
import { cn } from '../lib/utils';
import { useAuth } from '../lib/auth';
import { useSpaces } from '../lib/api';
import { EmptyState } from './EmptyState';
import { NotFoundPage } from './NotFoundPage';
import { isModuleKey, MODULES, spacePath } from './modules';
import { useRecentSpaces } from './hooks/useRecentSpaces';
import { useActiveTeamContext } from './hooks/useActiveTeamContext';

/**
 * ModuleLandingRedirect handles a bare /:module URL (a product tab click):
 * it forwards to the target module's space for the team the user was last
 * working in (module-switch context preservation), then the most recently
 * visited space of that module, then the first available one — or a branded
 * empty state when the org has no spaces of that module yet.
 */
export function ModuleLandingRedirect() {
  const { module } = useParams<{ module: string }>();
  const { user } = useAuth();
  const spacesQuery = useSpaces(user?.orgId ?? '');
  const { recents } = useRecentSpaces(isModuleKey(module) ? module : 'beacon');
  const activeTeamId = useActiveTeamContext();

  const target = useMemo(() => {
    if (!isModuleKey(module) || !spacesQuery.data) return null;
    // Locked directory rows (readable: false) are visible but not enterable —
    // never redirect into one.
    const moduleSpaces = spacesQuery.data.filter(
      (s) => s.type === module && s.readable !== false,
    );
    // Prefer the target module's space owned by the team the user was last in.
    const contextSpace = activeTeamId
      ? moduleSpaces.find((s) => s.owner_team_id === activeTeamId)
      : undefined;
    const recent = recents.map((id) => moduleSpaces.find((s) => s.id === id)).find(Boolean);
    const space = contextSpace ?? recent ?? moduleSpaces[0];
    return space ? spacePath(module, space.id, MODULES[module].defaultSubpath) : null;
  }, [module, spacesQuery.data, recents, activeTeamId]);

  if (!isModuleKey(module)) {
    return (
      <main className="min-h-screen bg-[var(--color-bg)] pt-[var(--topnav-height)]">
        <div className="mx-auto max-w-[1280px] px-[var(--space-4)] py-[var(--space-6)]">
          <NotFoundPage />
        </div>
      </main>
    );
  }

  const def = MODULES[module];

  return (
    <main className="min-h-screen bg-[var(--color-bg)] pt-[var(--topnav-height)]">
      <div className="mx-auto max-w-[1280px] px-[var(--space-4)] py-[var(--space-6)]">
        {spacesQuery.isLoading ? (
          <p className="py-[var(--space-12)] text-center text-[var(--text-sm)] text-[var(--color-text-muted)]">
            Loading {def.name}…
          </p>
        ) : target ? (
          <Navigate to={target} replace />
        ) : (
          <EmptyState
            icon={def.icon}
            title={`No ${def.name} spaces yet`}
            description={`Spaces are where ${def.name} work lives. Create the first one to get started.`}
            action={
              <Link
                to="/?create=space"
                className={cn(
                  'inline-flex h-9 items-center gap-[var(--space-1)] rounded-[var(--radius-md)] px-[var(--space-4)]',
                  'bg-[var(--color-primary)] text-[var(--text-sm)] font-medium text-white',
                  'hover:bg-[var(--color-primary-hover)] transition-colors duration-150',
                )}
              >
                Create a space
              </Link>
            }
          />
        )}
      </div>
    </main>
  );
}
