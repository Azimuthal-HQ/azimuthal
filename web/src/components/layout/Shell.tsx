import { useState } from 'react';
import { Outlet, useLocation } from 'react-router-dom';
import { Menu } from 'lucide-react';
import { cn } from '../../lib/utils';
import { TopNav, type Space } from './TopNav';
import { Sidebar, type SpaceType } from './Sidebar';

/** Derives the space type from the current URL pathname. */
function deriveSpaceType(pathname: string): SpaceType {
  if (pathname.startsWith('/service-desk')) return 'service_desk';
  if (pathname.startsWith('/wiki')) return 'wiki';
  if (pathname.startsWith('/project')) return 'project';
  return null;
}

interface ShellProps {
  spaces?: Space[];
  currentSpaceId?: string | null;
  onSpaceChange?: (spaceId: string) => void;
  onLogout?: () => void;
  userName?: string;
}

/** Main layout shell that renders TopNav, Sidebar, and the route outlet. */
export function Shell({
  spaces = [],
  currentSpaceId,
  onSpaceChange,
  onLogout,
  userName,
}: ShellProps) {
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const location = useLocation();
  const spaceType = deriveSpaceType(location.pathname);

  return (
    <div className="min-h-screen bg-[var(--color-bg)]">
      <TopNav
        spaces={spaces}
        currentSpaceId={currentSpaceId}
        onSpaceChange={onSpaceChange}
        onLogout={onLogout}
        userName={userName}
      />

      {/* Mobile hamburger button */}
      <button
        type="button"
        onClick={() => setSidebarOpen((prev) => !prev)}
        className={cn(
          'fixed top-[calc(var(--topnav-height)+var(--space-2))] left-[var(--space-2)] z-40',
          'inline-flex h-9 w-9 items-center justify-center rounded-[var(--radius-md)]',
          'bg-[var(--color-surface)] border border-[var(--color-border)]',
          'text-[var(--color-text-muted)] hover:text-[var(--color-text)]',
          'hover:bg-[var(--color-surface-hover)] transition-colors duration-200',
          'md:hidden',
        )}
        aria-label="Toggle sidebar"
      >
        <Menu className="h-[18px] w-[18px]" />
      </button>

      <Sidebar
        spaceType={spaceType}
        isOpen={sidebarOpen}
        onToggle={() => setSidebarOpen(false)}
      />

      {/* Main content area */}
      <main
        className={cn(
          'pt-[var(--topnav-height)]',
          'md:pl-[var(--sidebar-width)]',
          'min-h-screen',
        )}
      >
        <div
          className={cn(
            'flex-1 overflow-y-auto p-[var(--space-6)]',
            'max-w-[1280px] mx-auto w-full',
          )}
        >
          <Outlet />
        </div>
      </main>
    </div>
  );
}
