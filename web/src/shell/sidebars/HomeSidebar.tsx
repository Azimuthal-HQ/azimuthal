import { Bookmark, Home, LayoutDashboard, LayoutGrid, Library } from 'lucide-react';
import { cn } from '../../lib/utils';
import { SidebarChrome, SidebarNavItem, SidebarSection, useSidebarIsCollapsed } from './SidebarChrome';
import { useAuth } from '../../lib/auth';
import { useDashboards } from '../../lib/api';

/**
 * HomeSidebar is the left panel outside any space. Its header is a static
 * "Your work" label — the space picker is inert here because Home is scoped
 * to the user, not a space (ADR-0005 point 9).
 */
export function HomeSidebar() {
  return (
    <SidebarChrome testId="home-sidebar" header={<YourWorkHeader />} settingsTo="/settings">
      <nav className="flex flex-col gap-[2px]">
        <SidebarNavItem to="/" icon={Home} label="Overview" end />
        {/* Spaces. Declared here because P5's Home is a dashboard rather than
            a space directory — the grid of space cards the interim Home
            carried is gone, and this is the one-click replacement for it. */}
        <SidebarNavItem to="/spaces" icon={Library} label="Spaces" />
        {/* Saved views (P4). Declared here because this panel is where the
            top-level, org-scoped destinations live — a view belongs to no
            space and no module, so it has nowhere else to be listed. */}
        <SidebarNavItem to="/views" icon={Bookmark} label="Views" />
      </nav>
      <DashboardsSection />
    </SidebarChrome>
  );
}

/**
 * The caller's Home dashboards, server-ordered.
 *
 * Data-driven rather than a static link, on the BeaconSidebar queues
 * precedent: the list is what the server returned, in the order it returned
 * it, and it is never client-sorted. It shows the `home` module only —
 * Beacon and Vector dashboards are reachable from /dashboards, and hanging
 * them in a space sidebar would put a cross-container destination inside a
 * container's panel.
 */
function DashboardsSection() {
  const { user } = useAuth();
  const orgId = user?.orgId ?? '';
  const { data: dashboards } = useDashboards(orgId, 'home');

  return (
    <SidebarSection label="Dashboards">
      {(dashboards ?? []).map((d) => (
        <SidebarNavItem
          key={d.id}
          to={`/dashboards/${d.id}`}
          icon={LayoutGrid}
          label={d.name}
          testId="sidebar-dashboard-item"
        />
      ))}
      <SidebarNavItem to="/dashboards" icon={LayoutGrid} label="All dashboards" />
    </SidebarSection>
  );
}

function YourWorkHeader() {
  const collapsed = useSidebarIsCollapsed();
  return (
    <div
      data-testid="home-sidebar-header"
      title="Your work"
      className={cn(
        'flex cursor-default items-center gap-[var(--space-2)] rounded-[var(--radius-lg)]',
        'border border-[var(--color-border)] px-[var(--space-2)] py-[var(--space-2)]',
        'text-[var(--text-sm)] font-medium text-[var(--color-text)]',
        collapsed && 'justify-center border-transparent',
      )}
    >
      <LayoutDashboard className="h-4 w-4 shrink-0 text-[var(--color-primary)]" />
      {!collapsed && <span className="truncate">Your work</span>}
    </div>
  );
}
