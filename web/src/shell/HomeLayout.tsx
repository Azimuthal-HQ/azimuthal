import { Outlet } from 'react-router-dom';
import { cn } from '../lib/utils';
import { HomeSidebar } from './sidebars/HomeSidebar';
import { useSidebarCollapsed } from './hooks/useSidebarCollapsed';

/**
 * HomeLayout hosts everything scoped to the user or the org rather than a
 * space: the Home overview, home dashboards, settings, and admin pages. Its
 * left panel carries the static "Your work" header (ADR-0005 point 9).
 */
export function HomeLayout() {
  const [collapsed] = useSidebarCollapsed();
  return (
    <>
      <HomeSidebar />
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
