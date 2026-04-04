import { NavLink } from 'react-router-dom';
import {
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
  label: string;
  to: string;
  icon: React.ComponentType<{ className?: string }>;
}

const NAV_ITEMS: Record<string, NavItem[]> = {
  service_desk: [
    { label: 'Dashboard', to: 'dashboard', icon: LayoutDashboard },
    { label: 'Tickets', to: 'tickets', icon: Ticket },
    { label: 'Kanban Board', to: 'board', icon: Columns3 },
    { label: 'Reports', to: 'reports', icon: BarChart3 },
  ],
  wiki: [
    { label: 'All Pages', to: 'pages', icon: FileText },
    { label: 'Recent', to: 'recent', icon: Clock },
    { label: 'Favorites', to: 'favorites', icon: Star },
    { label: 'Trash', to: 'trash', icon: Trash2 },
  ],
  project: [
    { label: 'Backlog', to: 'backlog', icon: ListTodo },
    { label: 'Sprint Board', to: 'board', icon: Columns3 },
    { label: 'Roadmap', to: 'roadmap', icon: Map },
    { label: 'Labels', to: 'labels', icon: Tags },
  ],
  dashboard: [
    { label: 'Home', to: '/', icon: Home },
    { label: 'All Spaces', to: '/spaces', icon: Compass },
    { label: 'Settings', to: '/settings', icon: Settings },
  ],
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
        <nav className="flex flex-col gap-[var(--space-1)]" aria-label="Sidebar navigation">
          {items.map((item) => (
            <NavLink
              key={item.to}
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
