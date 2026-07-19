import { useEffect, useRef, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import {
  Bell,
  Check,
  ChevronRight,
  Globe,
  LogOut,
  Menu,
  Plus,
  Search,
  Settings,
  User,
} from 'lucide-react';
import { cn } from '../lib/utils';
import { useAuth } from '../lib/auth';
import {
  useMarkAllNotificationsRead,
  useMarkNotificationRead,
  useNotifications,
  useOrganization,
  type Notification,
} from '../lib/api';
import { Logo } from '../components/layout/Logo';
import { DarkModeToggle } from '../components/theme/DarkModeToggle';
import { ProductTabs } from './ProductTabs';
import { FocusChip } from './FocusChip';
import { useShellUI } from './ShellUIContext';

/**
 * TopBar is the persistent application header (ADR-0005): logomark, product
 * tabs, team focus chip, then search, create, notifications, and the avatar
 * menu. The org lives in the avatar menu — it is a tenant boundary, not a
 * navigation control (ADR-0005 point 4).
 */
export function TopBar() {
  const navigate = useNavigate();
  const { user, logout } = useAuth();
  const { mobileNavOpen, setMobileNavOpen } = useShellUI();

  const org = useOrganization(user?.orgId ?? '');
  const orgName = org.data?.name ?? 'Your workspace';

  const [notifOpen, setNotifOpen] = useState(false);
  const [avatarOpen, setAvatarOpen] = useState(false);
  const notifRef = useRef<HTMLDivElement>(null);
  const avatarRef = useRef<HTMLDivElement>(null);

  const { data: notifData } = useNotifications();
  const markRead = useMarkNotificationRead();
  const markAllRead = useMarkAllNotificationsRead();
  const unreadCount = notifData?.unread_count ?? 0;
  const notifications = notifData?.notifications ?? [];

  useEffect(() => {
    function handleClickOutside(e: MouseEvent) {
      const target = e.target as Node;
      if (notifRef.current && !notifRef.current.contains(target)) setNotifOpen(false);
      if (avatarRef.current && !avatarRef.current.contains(target)) setAvatarOpen(false);
    }
    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, []);

  useEffect(() => {
    function handleEscape(e: KeyboardEvent) {
      if (e.key === 'Escape') {
        setNotifOpen(false);
        setAvatarOpen(false);
      }
    }
    document.addEventListener('keydown', handleEscape);
    return () => document.removeEventListener('keydown', handleEscape);
  }, []);

  const initial = (user?.email ?? 'U').charAt(0).toUpperCase();

  return (
    <header
      data-testid="topbar"
      className={cn(
        'fixed top-0 left-0 right-0 z-40 flex items-center gap-[var(--space-2)]',
        'h-[var(--topnav-height)] px-[var(--space-3)]',
        'bg-[var(--color-surface)] border-b border-[var(--color-border)]',
      )}
    >
      <button
        type="button"
        onClick={() => setMobileNavOpen(!mobileNavOpen)}
        aria-label="Toggle navigation"
        className={cn(
          'inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-[var(--radius-md)] md:hidden',
          'text-[var(--color-text-muted)] hover:bg-[var(--color-surface-hover)] hover:text-[var(--color-text)]',
        )}
      >
        <Menu className="h-5 w-5" />
      </button>

      <Link to="/" aria-label="Azimuthal home" className="mr-[var(--space-1)] hidden shrink-0 md:block">
        <Logo size={26} />
      </Link>

      <ProductTabs />
      <FocusChip className="ml-[var(--space-2)]" />

      <div className="ml-auto flex shrink-0 items-center gap-[var(--space-2)]">
        <button
          type="button"
          onClick={() => navigate('/search')}
          className={cn(
            'hidden h-8 items-center gap-[var(--space-2)] rounded-[var(--radius-md)] px-[var(--space-3)] lg:flex lg:w-[210px]',
            'border border-[var(--color-border)] bg-[var(--color-bg)]',
            'text-[var(--text-sm)] text-[var(--color-text-muted)]',
            'hover:border-[var(--color-primary)] transition-colors duration-150',
          )}
        >
          <Search className="h-4 w-4" />
          Search everything
        </button>
        <button
          type="button"
          onClick={() => navigate('/search')}
          aria-label="Search everything"
          className={cn(
            'inline-flex h-9 w-9 items-center justify-center rounded-[var(--radius-md)] lg:hidden',
            'text-[var(--color-text-muted)] hover:bg-[var(--color-surface-hover)] hover:text-[var(--color-text)]',
          )}
        >
          <Search className="h-[18px] w-[18px]" />
        </button>

        <button
          type="button"
          onClick={() => navigate('/?create=space')}
          className={cn(
            'flex h-8 items-center gap-[var(--space-1)] rounded-[var(--radius-md)] px-[var(--space-3)]',
            'bg-[var(--color-primary)] text-[var(--text-sm)] font-medium text-white',
            'hover:bg-[var(--color-primary-hover)] transition-colors duration-150',
          )}
        >
          <Plus className="h-4 w-4" />
          <span className="hidden sm:inline">Create</span>
        </button>

        {/* Notification bell */}
        <div className="relative" ref={notifRef}>
          <button
            type="button"
            onClick={() => {
              setNotifOpen((prev) => !prev);
              setAvatarOpen(false);
            }}
            className={cn(
              'relative inline-flex h-9 w-9 items-center justify-center rounded-[var(--radius-md)]',
              'text-[var(--color-text-muted)] hover:text-[var(--color-text)]',
              'hover:bg-[var(--color-surface-hover)] transition-colors duration-200',
              'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)]',
            )}
            aria-label="Notifications"
          >
            <Bell className="h-[18px] w-[18px]" />
            {unreadCount > 0 && (
              <span
                className={cn(
                  'absolute top-1 right-1 flex h-4 w-4 items-center justify-center',
                  'rounded-full bg-[var(--color-primary)] text-white',
                  'text-[10px] font-semibold leading-none',
                )}
              >
                {unreadCount > 9 ? '9+' : unreadCount}
              </span>
            )}
          </button>

          {notifOpen && (
            <div
              className={cn(
                'absolute top-full right-0 mt-[var(--space-1)]',
                'w-80 rounded-[var(--radius-lg)]',
                'bg-[var(--color-surface)] border border-[var(--color-border)]',
                'shadow-[var(--shadow-lg)] overflow-hidden',
              )}
            >
              <div className="flex items-center justify-between border-b border-[var(--color-border)] px-[var(--space-3)] py-[var(--space-2)]">
                <span className="text-[var(--text-sm)] font-semibold text-[var(--color-text)]">
                  Notifications
                </span>
                {unreadCount > 0 && (
                  <button
                    type="button"
                    onClick={() => markAllRead.mutate()}
                    className="flex items-center gap-1 text-[var(--text-xs)] text-[var(--color-primary)] hover:underline"
                  >
                    <Check className="h-3 w-3" />
                    Mark all read
                  </button>
                )}
              </div>
              <div className="max-h-80 overflow-y-auto">
                {notifications.length === 0 ? (
                  <p className="py-[var(--space-4)] text-center text-[var(--text-sm)] text-[var(--color-text-muted)]">
                    No notifications
                  </p>
                ) : (
                  notifications.map((n) => (
                    <NotificationRow
                      key={n.id}
                      notification={n}
                      onRead={() => {
                        if (!n.is_read) markRead.mutate(n.id);
                      }}
                    />
                  ))
                )}
              </div>
            </div>
          )}
        </div>

        {/* Avatar menu — org home (ADR-0005 point 4) */}
        <div className="relative" ref={avatarRef}>
          <button
            type="button"
            data-testid="avatar-menu"
            onClick={() => {
              setAvatarOpen((prev) => !prev);
              setNotifOpen(false);
            }}
            className={cn(
              'inline-flex h-8 w-8 items-center justify-center rounded-[var(--radius-full)]',
              'bg-[var(--color-primary-muted)] text-[var(--text-sm)] font-medium text-[var(--color-primary)]',
              'hover:bg-[var(--color-primary)] hover:text-white transition-colors duration-200',
              'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)]',
            )}
            aria-label="Account menu"
          >
            {initial}
          </button>

          {avatarOpen && (
            <div
              className={cn(
                'absolute top-full right-0 mt-[var(--space-1)]',
                'w-64 rounded-[var(--radius-lg)] py-[var(--space-1)]',
                'bg-[var(--color-surface)] border border-[var(--color-border)]',
                'shadow-[var(--shadow-lg)]',
              )}
            >
              <div className="border-b border-[var(--color-border)] px-[var(--space-3)] py-[var(--space-2)]">
                <div className="flex items-center gap-[var(--space-2)]">
                  <span
                    className={cn(
                      'flex h-8 w-8 shrink-0 items-center justify-center rounded-[var(--radius-md)]',
                      'bg-[var(--color-primary-muted)] text-[var(--text-sm)] font-medium text-[var(--color-primary)]',
                    )}
                  >
                    {orgName.charAt(0).toUpperCase()}
                  </span>
                  <span className="min-w-0">
                    <span className="block truncate text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                      {orgName}
                    </span>
                    <span className="block truncate text-[var(--text-xs)] text-[var(--color-text-muted)]">
                      {user?.email}
                    </span>
                  </span>
                </div>
              </div>

              <div
                aria-disabled="true"
                title="Switching organisations arrives with Teams"
                className={cn(
                  'flex cursor-not-allowed items-center gap-[var(--space-2)] px-[var(--space-3)] py-[var(--space-2)]',
                  'text-[var(--text-sm)] text-[var(--color-text-muted)] opacity-60',
                )}
              >
                <Globe className="h-4 w-4" />
                Switch organisation
                <ChevronRight className="ml-auto h-4 w-4" />
              </div>

              <MenuLink icon={User} label="Profile" onClick={() => { setAvatarOpen(false); navigate('/settings'); }} />
              <MenuLink
                icon={Settings}
                label="Workspace settings"
                onClick={() => { setAvatarOpen(false); navigate('/settings/organization'); }}
              />

              <div className="flex items-center justify-between px-[var(--space-3)] py-[var(--space-1)]">
                <span className="text-[var(--text-sm)] text-[var(--color-text)]">Theme</span>
                <DarkModeToggle />
              </div>

              <div className="my-[var(--space-1)] border-t border-[var(--color-border)]" />
              <MenuLink
                icon={LogOut}
                label="Sign out"
                onClick={() => {
                  setAvatarOpen(false);
                  logout();
                  navigate('/login', { replace: true });
                }}
              />
            </div>
          )}
        </div>
      </div>
    </header>
  );
}

function MenuLink({
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
        'flex w-full items-center gap-[var(--space-2)] px-[var(--space-3)] py-[var(--space-2)]',
        'text-[var(--text-sm)] text-[var(--color-text)]',
        'hover:bg-[var(--color-surface-hover)] transition-colors duration-150',
      )}
    >
      <Icon className="h-4 w-4 text-[var(--color-text-muted)]" />
      {label}
    </button>
  );
}

function NotificationRow({
  notification: n,
  onRead,
}: {
  notification: Notification;
  onRead: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onRead}
      className={cn(
        'w-full text-left px-[var(--space-3)] py-[var(--space-2)]',
        'border-b border-[var(--color-border)] last:border-b-0',
        'hover:bg-[var(--color-surface-hover)] transition-colors duration-150',
        !n.is_read && 'bg-[var(--color-primary-muted)]',
      )}
    >
      <p className="text-[var(--text-sm)] text-[var(--color-text)] line-clamp-2">{n.title}</p>
      <p className="mt-0.5 text-[var(--text-xs)] text-[var(--color-text-muted)]">
        {new Date(n.created_at).toLocaleString()}
      </p>
    </button>
  );
}
