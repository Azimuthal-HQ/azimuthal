import { Bookmark, Home, LayoutDashboard, Plus } from 'lucide-react';
import { cn } from '../../lib/utils';
import { SidebarChrome, SidebarNavItem, SidebarSection, useSidebarIsCollapsed } from './SidebarChrome';

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
        {/* Saved views (P4). Declared here because this panel is where the
            top-level, org-scoped destinations live — a view belongs to no
            space and no module, so it has nowhere else to be listed. */}
        <SidebarNavItem to="/views" icon={Bookmark} label="Views" />
      </nav>
      <SidebarSection label="Dashboards">
        <SidebarNavItem to="/home/new" icon={Plus} label="New dashboard" />
      </SidebarSection>
    </SidebarChrome>
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
