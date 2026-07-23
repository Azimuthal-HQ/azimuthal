import { NavLink, Outlet } from 'react-router-dom';
import { Shield } from 'lucide-react';
import { cn } from '../../lib/utils';
import { useAuth } from '../../lib/auth';
import { useOrganization } from '../../lib/api';
import { NotFoundPage } from '../../shell/NotFoundPage';

/**
 * AdminLayout hosts the P2.5 administration area: People, Teams, Access,
 * Spaces, Audit log. Reachable from the avatar menu, org admins only.
 *
 * Non-admins render the branded not-found state — the same 404 the API
 * answers with. The client check is presentation; the server-side
 * RequireOrgAdmin404 guard is the gate.
 */
export function AdminLayout() {
  const { user } = useAuth();
  const org = useOrganization(user?.orgId ?? '');

  if (org.isLoading) {
    return (
      <div className="py-[var(--space-8)] text-center text-[var(--text-sm)] text-[var(--color-text-muted)]">
        Loading…
      </div>
    );
  }
  if (!org.data?.caller_is_admin) {
    return <NotFoundPage />;
  }

  return (
    <div data-testid="admin-layout">
      <div className="mb-[var(--space-6)] flex items-center gap-[var(--space-3)]">
        <span
          className={cn(
            'flex h-10 w-10 items-center justify-center rounded-[var(--radius-lg)]',
            'bg-[var(--color-primary-muted)] text-[var(--color-primary)]',
          )}
        >
          <Shield className="h-5 w-5" />
        </span>
        <div>
          <h1 className="text-[var(--text-lg)] font-semibold tracking-[-.01em] text-[var(--color-text)]">Administration</h1>
          <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
            People, access, and governance for {org.data.name}
          </p>
        </div>
      </div>

      <nav
        className="mb-[var(--space-6)] flex gap-[var(--space-1)] border-b border-[var(--color-border)]"
        aria-label="Administration sections"
      >
        <AdminTab to="/admin/people" label="People" testid="admin-tab-people" />
        <AdminTab to="/admin/teams" label="Teams" testid="admin-tab-teams" />
        <AdminTab to="/admin/access" label="Access" testid="admin-tab-access" />
        <AdminTab to="/admin/spaces" label="Spaces" testid="admin-tab-spaces" />
        <AdminTab to="/admin/audit-log" label="Audit log" testid="admin-tab-audit" />
      </nav>

      <Outlet />
    </div>
  );
}

function AdminTab({ to, label, testid }: { to: string; label: string; testid: string }) {
  return (
    <NavLink
      to={to}
      data-testid={testid}
      className={({ isActive }) =>
        cn(
          'border-b-2 px-[var(--space-3)] py-[var(--space-2)] text-[var(--text-sm)] font-medium transition-colors',
          isActive
            ? 'border-[var(--color-primary)] text-[var(--color-text)]'
            : 'border-transparent text-[var(--color-text-muted)] hover:text-[var(--color-text)]',
        )
      }
    >
      {label}
    </NavLink>
  );
}
