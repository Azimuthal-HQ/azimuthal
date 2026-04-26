import { useCallback, useEffect, useRef, useState } from 'react';
import { ChevronDown, LogOut, Settings, User } from 'lucide-react';
import { cn } from '../../lib/utils';
import { Logo } from './Logo';
import { DarkModeToggle } from '../theme/DarkModeToggle';
import { NotificationsBell } from './NotificationsBell';

export interface Space {
  id: string;
  name: string;
  type: 'service_desk' | 'wiki' | 'project';
}

interface TopNavProps {
  spaces?: Space[];
  currentSpaceId?: string | null;
  onSpaceChange?: (spaceId: string) => void;
  onLogout?: () => void;
  userName?: string;
  className?: string;
}

/** Fixed top navigation bar with logo, space switcher, and user actions. */
export function TopNav({
  spaces: rawSpaces = [],
  currentSpaceId,
  onSpaceChange,
  onLogout,
  userName = 'User',
  className,
}: TopNavProps) {
  const spaces = Array.isArray(rawSpaces) ? rawSpaces : [rawSpaces];
  const [spaceSwitcherOpen, setSpaceSwitcherOpen] = useState(false);
  const [userMenuOpen, setUserMenuOpen] = useState(false);
  const spaceSwitcherRef = useRef<HTMLDivElement>(null);
  const userMenuRef = useRef<HTMLDivElement>(null);

  const currentSpace = spaces.find((s) => s.id === currentSpaceId);

  const closeMenus = useCallback(() => {
    setSpaceSwitcherOpen(false);
    setUserMenuOpen(false);
  }, []);

  useEffect(() => {
    function handleClickOutside(e: MouseEvent) {
      const target = e.target as Node;
      if (
        spaceSwitcherRef.current &&
        !spaceSwitcherRef.current.contains(target)
      ) {
        setSpaceSwitcherOpen(false);
      }
      if (userMenuRef.current && !userMenuRef.current.contains(target)) {
        setUserMenuOpen(false);
      }
    }
    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, []);

  return (
    <header
      className={cn(
        'fixed top-0 left-0 right-0 z-40 flex items-center',
        'h-[var(--topnav-height)] px-[var(--space-4)]',
        'bg-[var(--color-surface)] border-b border-[var(--color-border)]',
        className,
      )}
    >
      {/* Left: Logo */}
      <div className="flex items-center shrink-0">
        <Logo size={28} showText className="mr-[var(--space-4)]" />
      </div>

      {/* Center: Space switcher */}
      <div className="flex-1 flex justify-center" ref={spaceSwitcherRef}>
        {spaces.length > 0 && (
          <div className="relative">
            <button
              type="button"
              onClick={() => {
                setSpaceSwitcherOpen((prev) => !prev);
                setUserMenuOpen(false);
              }}
              className={cn(
                'flex items-center gap-[var(--space-2)] px-[var(--space-3)] py-[var(--space-1)]',
                'rounded-[var(--radius-md)] text-[var(--text-sm)]',
                'text-[var(--color-text)] hover:bg-[var(--color-surface-hover)]',
                'transition-colors duration-150',
              )}
            >
              <span className="hidden md:inline truncate max-w-[200px]">
                {currentSpace?.name ?? 'Select space'}
              </span>
              <span className="md:hidden">
                <ChevronDown className="h-4 w-4" />
              </span>
              <ChevronDown className="hidden md:block h-4 w-4 text-[var(--color-text-muted)]" />
            </button>

            {spaceSwitcherOpen && (
              <div
                className={cn(
                  'absolute top-full left-1/2 -translate-x-1/2 mt-[var(--space-1)]',
                  'w-56 rounded-[var(--radius-lg)] py-[var(--space-1)]',
                  'bg-[var(--color-surface)] border border-[var(--color-border)]',
                  'shadow-[var(--shadow-lg)]',
                )}
              >
                {spaces.map((space) => (
                  <button
                    key={space.id}
                    type="button"
                    onClick={() => {
                      onSpaceChange?.(space.id);
                      closeMenus();
                    }}
                    className={cn(
                      'w-full text-left px-[var(--space-3)] py-[var(--space-2)]',
                      'text-[var(--text-sm)] transition-colors duration-150',
                      space.id === currentSpaceId
                        ? 'bg-[var(--color-primary-muted)] text-[var(--color-primary)]'
                        : 'text-[var(--color-text)] hover:bg-[var(--color-surface-hover)]',
                    )}
                  >
                    {space.name}
                  </button>
                ))}
              </div>
            )}
          </div>
        )}
      </div>

      {/* Right: Actions */}
      <div className="flex items-center gap-[var(--space-1)] shrink-0">
        <DarkModeToggle />

        {/* Notification bell */}
        <NotificationsBell />

        {/* User menu */}
        <div className="relative" ref={userMenuRef}>
          <button
            type="button"
            onClick={() => {
              setUserMenuOpen((prev) => !prev);
              setSpaceSwitcherOpen(false);
            }}
            className={cn(
              'inline-flex h-9 w-9 items-center justify-center rounded-[var(--radius-full)]',
              'bg-[var(--color-primary)] text-white text-[var(--text-sm)] font-medium',
              'hover:bg-[var(--color-primary-hover)] transition-colors duration-200',
              'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)]',
            )}
            aria-label="User menu"
          >
            {userName.charAt(0).toUpperCase()}
          </button>

          {userMenuOpen && (
            <div
              className={cn(
                'absolute top-full right-0 mt-[var(--space-1)]',
                'w-48 rounded-[var(--radius-lg)] py-[var(--space-1)]',
                'bg-[var(--color-surface)] border border-[var(--color-border)]',
                'shadow-[var(--shadow-lg)]',
              )}
            >
              <MenuButton icon={User} label="Profile" onClick={closeMenus} />
              <MenuButton icon={Settings} label="Settings" onClick={closeMenus} />
              <div className="my-[var(--space-1)] border-t border-[var(--color-border)]" />
              <MenuButton
                icon={LogOut}
                label="Logout"
                onClick={() => {
                  closeMenus();
                  onLogout?.();
                }}
              />
            </div>
          )}
        </div>
      </div>
    </header>
  );
}

function MenuButton({
  icon: Icon,
  label,
  onClick,
}: {
  icon: React.ComponentType<{ className?: string }>;
  label: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        'w-full flex items-center gap-[var(--space-2)] px-[var(--space-3)] py-[var(--space-2)]',
        'text-[var(--text-sm)] text-[var(--color-text)]',
        'hover:bg-[var(--color-surface-hover)] transition-colors duration-150',
      )}
    >
      <Icon className="h-4 w-4 text-[var(--color-text-muted)]" />
      {label}
    </button>
  );
}
