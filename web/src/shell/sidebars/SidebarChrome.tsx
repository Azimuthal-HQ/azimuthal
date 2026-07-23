import { createContext, useContext } from 'react';
import { NavLink } from 'react-router-dom';
import { ChevronLeft, Settings } from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import { cn } from '../../lib/utils';
import { useSidebarCollapsed } from '../hooks/useSidebarCollapsed';
import { useShellUI } from '../ShellUIContext';

const CollapsedContext = createContext(false);

/** useSidebarIsCollapsed reads the chrome's collapse state from any nested item. */
export function useSidebarIsCollapsed(): boolean {
  return useContext(CollapsedContext);
}

interface SidebarChromeProps {
  /** Rendered at the very top: the space picker, or Home's static label. */
  header: React.ReactNode;
  /** Nav content. Scrolls as one region unless a child manages its own scroll (Codex). */
  children: React.ReactNode;
  /** Destination of the footer Settings item. */
  settingsTo: string;
  /** When false, the nav region does not scroll itself (Codex owns its tree scroll). */
  scrollableNav?: boolean;
  testId: string;
  module?: string;
}

/**
 * SidebarChrome is the shared frame of every left panel (ADR-0005 point 2):
 * header slot on top, nav in the middle, Settings + collapse control pinned
 * at the bottom. Collapses to an icon rail; on mobile it becomes a drawer
 * toggled from the top bar.
 */
export function SidebarChrome({
  header,
  children,
  settingsTo,
  scrollableNav = true,
  testId,
  module,
}: SidebarChromeProps) {
  const [collapsed, setCollapsed] = useSidebarCollapsed();
  const { mobileNavOpen, setMobileNavOpen } = useShellUI();

  return (
    <CollapsedContext.Provider value={collapsed}>
      {mobileNavOpen && (
        <div
          className="fixed inset-0 z-30 bg-black/50 md:hidden"
          onClick={() => setMobileNavOpen(false)}
          aria-hidden="true"
        />
      )}
      <aside
        data-testid={testId}
        data-module={module}
        data-collapsed={collapsed || undefined}
        className={cn(
          'fixed top-[var(--topnav-height)] left-0 bottom-0 z-30',
          'flex flex-col bg-[var(--color-surface)] border-r border-[var(--color-border)]',
          'px-[var(--space-2)] py-[var(--space-3)]',
          'transition-[width,transform] duration-150 ease-in-out',
          collapsed ? 'w-[var(--sidebar-width-collapsed)]' : 'w-[var(--sidebar-width)]',
          mobileNavOpen ? 'translate-x-0' : '-translate-x-full',
          'md:translate-x-0',
        )}
      >
        <div className="shrink-0">{header}</div>

        <div
          className={cn(
            'mt-[var(--space-3)] flex min-h-0 flex-1 flex-col',
            scrollableNav && 'overflow-y-auto',
          )}
        >
          {children}
        </div>

        <div className="mt-[var(--space-2)] shrink-0 border-t border-[var(--color-border)] pt-[var(--space-2)]">
          <SidebarNavItem to={settingsTo} icon={Settings} label="Settings" />
          <button
            type="button"
            data-testid="sidebar-collapse"
            onClick={() => setCollapsed((prev) => !prev)}
            title={collapsed ? 'Expand' : 'Collapse'}
            className={cn(
              'flex w-full items-center gap-[var(--space-3)] rounded-[var(--radius-md)] px-[var(--space-3)] py-[var(--space-2)]',
              'text-[var(--text-sm)] text-[var(--color-text-muted)]',
              'hover:bg-[var(--color-surface-hover)] hover:text-[var(--color-text)] transition-colors duration-150',
              collapsed && 'justify-center px-0',
            )}
          >
            <ChevronLeft className={cn('h-[18px] w-[18px] shrink-0 transition-transform', collapsed && 'rotate-180')} />
            {!collapsed && 'Collapse'}
          </button>
        </div>
      </aside>
    </CollapsedContext.Provider>
  );
}

interface SidebarNavItemProps {
  to: string;
  icon: LucideIcon;
  label: string;
  count?: number;
  end?: boolean;
}

/** SidebarNavItem is a single nav row; icon-only with a tooltip when collapsed. */
export function SidebarNavItem({ to, icon: Icon, label, count, end }: SidebarNavItemProps) {
  const collapsed = useSidebarIsCollapsed();
  const { setMobileNavOpen } = useShellUI();

  return (
    <NavLink
      to={to}
      end={end}
      title={label}
      onClick={() => setMobileNavOpen(false)}
      className={({ isActive }) =>
        cn(
          'flex items-center gap-[var(--space-3)] rounded-[var(--radius-md)] px-[var(--space-3)] py-[var(--space-2)]',
          // 13px per the dashboards concept's sidebar (final-round density pass).
          'text-[13px] font-medium transition-colors duration-150',
          isActive
            ? 'bg-[var(--color-primary-muted)] text-[var(--color-primary)]'
            : 'text-[var(--color-text-muted)] hover:text-[var(--color-text)] hover:bg-[var(--color-surface-hover)]',
          collapsed && 'justify-center px-0',
        )
      }
    >
      <Icon className="h-[18px] w-[18px] shrink-0" />
      {!collapsed && <span className="min-w-0 flex-1 truncate">{label}</span>}
      {!collapsed && count !== undefined && (
        <span className="shrink-0 text-[var(--text-xs)] text-[var(--color-text-muted)]" style={{ fontFamily: 'var(--font-mono)' }}>
          {count}
        </span>
      )}
    </NavLink>
  );
}

/** SidebarSection renders an uppercase section label; hidden when collapsed. */
export function SidebarSection({ label, children }: { label: string; children: React.ReactNode }) {
  const collapsed = useSidebarIsCollapsed();
  return (
    <div className="mt-[var(--space-3)]">
      {!collapsed && (
        <p className="px-[var(--space-3)] pb-[var(--space-1)] text-[10px] font-medium uppercase tracking-wider text-[var(--color-text-muted)]">
          {label}
        </p>
      )}
      <nav className="flex flex-col gap-[2px]">{children}</nav>
    </div>
  );
}
