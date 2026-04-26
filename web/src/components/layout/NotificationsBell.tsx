import { useCallback, useEffect, useRef, useState } from 'react';
import { Bell } from 'lucide-react';
import { useNavigate } from 'react-router-dom';
import { cn } from '../../lib/utils';
import {
  useNotifications,
  useMarkNotificationRead,
  useMarkAllNotificationsRead,
  type Notification,
} from '../../lib/api';

/** Notification dropdown anchored to a bell icon in the top nav. */
export function NotificationsBell() {
  const [open, setOpen] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);
  const navigate = useNavigate();

  const { data, isLoading } = useNotifications();
  const markRead = useMarkNotificationRead();
  const markAll = useMarkAllNotificationsRead();

  const unread = data?.unread_count ?? 0;
  const items = data?.notifications ?? [];

  const close = useCallback(() => setOpen(false), []);

  useEffect(() => {
    function onClick(e: MouseEvent) {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    }
    document.addEventListener('mousedown', onClick);
    return () => document.removeEventListener('mousedown', onClick);
  }, []);

  function handleSelect(n: Notification) {
    if (!n.is_read) markRead.mutate(n.id);
    const path = pathForEntity(n);
    close();
    if (path) navigate(path);
  }

  return (
    <div className="relative" ref={containerRef}>
      <button
        type="button"
        onClick={() => setOpen((p) => !p)}
        className={cn(
          'relative inline-flex h-9 w-9 items-center justify-center rounded-[var(--radius-md)]',
          'text-[var(--color-text-muted)] hover:text-[var(--color-text)]',
          'hover:bg-[var(--color-surface-hover)] transition-colors duration-200',
          'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)]',
        )}
        aria-label="Notifications"
        data-testid="notifications-bell"
      >
        <Bell className="h-[18px] w-[18px]" />
        {unread > 0 && (
          <span
            data-testid="notifications-badge"
            className={cn(
              'absolute -top-0.5 -right-0.5 inline-flex min-w-[18px] h-[18px]',
              'items-center justify-center rounded-full px-1',
              'bg-[var(--color-primary)] text-white text-[10px] font-semibold',
            )}
          >
            {unread > 99 ? '99+' : unread}
          </span>
        )}
      </button>

      {open && (
        <div
          data-testid="notifications-panel"
          className={cn(
            'absolute top-full right-0 mt-[var(--space-1)] w-80',
            'rounded-[var(--radius-lg)] py-[var(--space-1)]',
            'bg-[var(--color-surface)] border border-[var(--color-border)]',
            'shadow-[var(--shadow-lg)] z-50',
            'max-h-[420px] overflow-y-auto',
          )}
        >
          <div className="flex items-center justify-between px-[var(--space-3)] py-[var(--space-2)] border-b border-[var(--color-border)]">
            <span className="text-[var(--text-sm)] font-semibold text-[var(--color-text)]">
              Notifications
            </span>
            {unread > 0 && (
              <button
                type="button"
                onClick={() => markAll.mutate()}
                className="text-[var(--text-xs)] text-[var(--color-primary)] hover:underline"
              >
                Mark all read
              </button>
            )}
          </div>

          {isLoading && (
            <div className="px-[var(--space-3)] py-[var(--space-4)] text-[var(--text-sm)] text-[var(--color-text-muted)]">
              Loading…
            </div>
          )}
          {!isLoading && items.length === 0 && (
            <div className="px-[var(--space-3)] py-[var(--space-4)] text-[var(--text-sm)] text-[var(--color-text-muted)]">
              No notifications yet
            </div>
          )}
          {!isLoading &&
            items.map((n) => (
              <button
                key={n.id}
                type="button"
                onClick={() => handleSelect(n)}
                data-testid="notification-item"
                className={cn(
                  'w-full text-left px-[var(--space-3)] py-[var(--space-2)]',
                  'border-b border-[var(--color-border)] last:border-b-0',
                  'hover:bg-[var(--color-surface-hover)] transition-colors duration-150',
                  !n.is_read && 'bg-[var(--color-primary-muted)]',
                )}
              >
                <div className="text-[var(--text-sm)] text-[var(--color-text)] truncate">
                  {n.title}
                </div>
                {n.body && (
                  <div className="text-[var(--text-xs)] text-[var(--color-text-muted)] truncate">
                    {n.body}
                  </div>
                )}
                <div className="text-[10px] text-[var(--color-text-muted)] mt-0.5">
                  {new Date(n.created_at).toLocaleString()}
                </div>
              </button>
            ))}
        </div>
      )}
    </div>
  );
}

// pathForEntity computes the in-app URL the bell click-through should
// navigate to. The mapping mirrors App.tsx routes; entities without a known
// route just close the panel.
function pathForEntity(n: Notification): string | null {
  if (!n.entity_id) return null;
  switch (n.entity_kind) {
    case 'ticket':
      return `/?ticket=${n.entity_id}`;
    case 'item':
      return `/?item=${n.entity_id}`;
    case 'page':
      return `/?page=${n.entity_id}`;
    default:
      return null;
  }
}
