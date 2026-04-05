import { NavLink, Link } from 'react-router-dom';
import {
  ArrowLeft,
  BarChart3,
  Clock,
  Columns3,
  Compass,
  FileText,
  Home,
  LayoutDashboard,
  ListTodo,
  Map,
  Settings,
  Star,
  Tags,
  Ticket,
  Trash2,
} from 'lucide-react';
import { cn } from '../../lib/utils';

export type SpaceType = 'service_desk' | 'wiki' | 'project' | null;

interface NavItem {
  id: string;
  label: string;
  to: string;
  icon: React.ComponentType<{ className?: string }>;
}

const SPACE_TYPE_LABEL: Record<string, string> = {
  service_desk: 'Service Desk',
  wiki: 'Wiki',
  project: 'Project',
};

const SPACE_TYPE_ICON: Record<string, React.ComponentType<{ className?: string }>> = {
  service_desk: Ticket,
  wiki: FileText,
  project: ListTodo,
};

const NAV_ITEMS: Record<string, NavItem[]> = {
  service_desk: [
    { id: 'sd-tickets', label: 'Tickets', to: '/tickets', icon: LayoutDashboard },
    { id: 'sd-kanban', label: 'Kanban Board', to: '/kanban', icon: Columns3 },
    { id: 'sd-reports', label: 'Reports', to: '/tickets', icon: BarChart3 },
  ],
  wiki: [
    { id: 'wiki-pages', label: 'All Pages', to: '/wiki', icon: FileText },
    { id: 'wiki-recent', label: 'Recent', to: '/wiki', icon: Clock },
    { id: 'wiki-favorites', label: 'Favorites', to: '/wiki', icon: Star },
    { id: 'wiki-trash', label: 'Trash', to: '/wiki', icon: Trash2 },
  ],
  project: [
    { id: 'proj-backlog', label: 'Backlog', to: '/backlog', icon: ListTodo },
    { id: 'proj-board', label: 'Sprint Board', to: '/board', icon: Columns3 },
    { id: 'proj-roadmap', label: 'Roadmap', to: '/backlog', icon: Map },
    { id: 'proj-labels', label: 'Labels', to: '/backlog', icon: Tags },
  ],
  dashboard: [
    { id: 'dash-home', label: 'Home', to: '/', icon: Home },
    { id: 'dash-spaces', label: 'All Spaces', to: '/', icon: Compass },
    { id: 'dash-settings', label: 'Settings', to: '/settings', icon: Settings },
  ],
};

// Space type display names (used as fallback when no space name is provided)
const SPACE_NAME: Record<string, string> = {
  service_desk: 'Service Desk',
  wiki: 'Wiki',
  project: 'Project',
};

interface SidebarProps {
  spaceType: SpaceType;
  isOpen: boolean;
  onToggle: () => void;
  className?: string;
}

/** Left navigation sidebar with space-type-aware nav items. */
export function Sidebar({ spaceType, isOpen, onToggle, className }: SidebarProps) {
  const items = NAV_ITEMS[spaceType ?? 'dashboard'];
  const isInSpace = spaceType !== null;

  return (
    <>
      {/* Mobile overlay backdrop */}
      {isOpen && (
        <div
          className="fixed inset-0 z-30 bg-black/50 md:hidden"
          onClick={onToggle}
          aria-hidden="true"
        />
      )}

      <aside
        className={cn(
          'fixed top-[var(--topnav-height)] left-0 bottom-0 z-30',
          'w-[var(--sidebar-width)] bg-[var(--color-surface)]',
          'border-r border-[var(--color-border)]',
          'flex flex-col py-[var(--space-4)] px-[var(--space-3)]',
          'transition-transform duration-200 ease-in-out',
          // Mobile: hidden by default, shown when open
          isOpen ? 'translate-x-0' : '-translate-x-full',
          // Desktop: always visible
          'md:translate-x-0',
          className,
        )}
      >
        {/* Bug 3 fix: Space/module indicator at top of sidebar */}
        {isInSpace && spaceType && (
          <div className="mb-4 pb-3 border-b border-[var(--color-border)]">
            <Link
              to="/"
              className="flex items-center gap-2 mb-2 text-[var(--text-xs)] text-[var(--color-text-muted)] hover:text-[var(--color-primary)] transition-colors"
            >
              <ArrowLeft className="h-3 w-3" />
              Back to Dashboard
            </Link>
            <div className="flex items-center gap-2">
              {(() => {
                const SpaceIcon = SPACE_TYPE_ICON[spaceType];
                return SpaceIcon ? (
                  <div className="flex h-8 w-8 items-center justify-center rounded-[var(--radius-md)] bg-[var(--color-primary-muted)]">
                    <SpaceIcon className="h-4 w-4 text-[var(--color-primary)]" />
                  </div>
                ) : null;
              })()}
              <div className="min-w-0">
                <p className="text-[var(--text-sm)] font-semibold text-[var(--color-text)] truncate">
                  {SPACE_NAME[spaceType]}
                </p>
                <p className="text-[var(--text-xs)] text-[var(--color-text-muted)]">
                  {SPACE_TYPE_LABEL[spaceType]}
                </p>
              </div>
            </div>
          </div>
        )}

        <nav className="flex flex-col gap-[var(--space-1)]" aria-label="Sidebar navigation">
          {items.map((item) => (
            <NavLink
              key={item.id}
              to={item.to}
              end={item.to === '/'}
              onClick={() => {
                // Close sidebar on mobile after navigation
                if (window.innerWidth < 768) {
                  onToggle();
                }
              }}
              className={({ isActive }) =>
                cn(
                  'flex items-center gap-[var(--space-3)] px-[var(--space-3)] py-[var(--space-2)]',
                  'rounded-[var(--radius-md)] text-[var(--text-sm)] font-medium',
                  'transition-colors duration-150',
                  isActive
                    ? 'bg-[var(--color-primary-muted)] text-[var(--color-primary)]'
                    : 'text-[var(--color-text-muted)] hover:text-[var(--color-text)] hover:bg-[var(--color-surface-hover)]',
                )
              }
            >
              <item.icon className="h-[18px] w-[18px] shrink-0" />
              {item.label}
            </NavLink>
          ))}
        </nav>
      </aside>
    </>
  );
}
