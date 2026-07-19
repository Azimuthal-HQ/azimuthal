import { useEffect } from 'react';
import { Outlet, useParams } from 'react-router-dom';
import { cn } from '../lib/utils';
import { useSpace } from '../lib/api';
import { NotFoundPage } from './NotFoundPage';
import { isModuleKey, type ModuleKey } from './modules';
import { BeaconSidebar } from './sidebars/BeaconSidebar';
import { CodexSidebar } from './sidebars/CodexSidebar';
import { VectorSidebar } from './sidebars/VectorSidebar';
import { useRecentSpaces } from './hooks/useRecentSpaces';
import { useSidebarCollapsed } from './hooks/useSidebarCollapsed';

/**
 * SpaceLayout owns the space-scoped left panel (ADR-0005 points 2–3). It is
 * mounted once at /:module/:spaceId and every sub-route renders through its
 * outlet, so the sidebar never remounts on sub-route change. The module is
 * derived only from the :module URL param — never from the sub-route name.
 */
export function SpaceLayout() {
  const { module, spaceId } = useParams<{ module: string; spaceId: string }>();
  const valid = isModuleKey(module) && !!spaceId;

  if (!valid) {
    return (
      <main className="min-h-screen bg-[var(--color-bg)] pt-[var(--topnav-height)]">
        <div className="mx-auto max-w-[1280px] px-[var(--space-4)] py-[var(--space-6)]">
          <NotFoundPage />
        </div>
      </main>
    );
  }

  return <ValidSpaceLayout module={module} spaceId={spaceId} />;
}

function ValidSpaceLayout({ module, spaceId }: { module: ModuleKey; spaceId: string }) {
  const [collapsed] = useSidebarCollapsed();
  const spaceQuery = useSpace(spaceId);
  const { recordVisit } = useRecentSpaces(module);

  useEffect(() => {
    recordVisit(spaceId);
  }, [spaceId, recordVisit]);

  const sidebar =
    module === 'beacon' ? (
      <BeaconSidebar space={spaceQuery.data} spaceId={spaceId} />
    ) : module === 'codex' ? (
      <CodexSidebar space={spaceQuery.data} spaceId={spaceId} />
    ) : (
      <VectorSidebar space={spaceQuery.data} spaceId={spaceId} />
    );

  return (
    <>
      {sidebar}
      <main
        className={cn(
          'min-h-screen bg-[var(--color-bg)] pt-[var(--topnav-height)]',
          collapsed ? 'md:pl-[var(--sidebar-width-collapsed)]' : 'md:pl-[var(--sidebar-width)]',
        )}
      >
        <div className="mx-auto max-w-[1280px] px-[var(--space-4)] py-[var(--space-6)]">
          <Outlet />
        </div>
      </main>
    </>
  );
}
