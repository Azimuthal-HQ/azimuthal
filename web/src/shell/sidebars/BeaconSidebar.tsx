import { NavLink } from 'react-router-dom';
import { BarChart3, Columns3, ListFilter, Plus, Ticket } from 'lucide-react';
import type { Space } from '../../lib/api';
import { useQueues } from '../../lib/api';
import { useAuth } from '../../lib/auth';
import { cn } from '../../lib/utils';
import { spacePath } from '../modules';
import { SidebarChrome, SidebarNavItem, useSidebarIsCollapsed } from './SidebarChrome';
import { SpacePicker } from '../SpacePicker';
import { useSidebarCollapsed } from '../hooks/useSidebarCollapsed';

/** BeaconSidebar is the space-scoped left panel for the Beacon (service desk) module. */
export function BeaconSidebar({ space, spaceId }: { space: Space | undefined; spaceId: string }) {
  const [collapsed] = useSidebarCollapsed();
  return (
    <SidebarChrome
      testId="space-sidebar"
      module="beacon"
      header={<SpacePicker module="beacon" currentSpace={space} collapsed={collapsed} />}
      settingsTo={spacePath('beacon', spaceId, 'settings')}
    >
      <nav className="flex flex-col gap-[2px]">
        <SidebarNavItem to={spacePath('beacon', spaceId, 'tickets')} icon={Ticket} label="Tickets" />
        <SidebarNavItem to={spacePath('beacon', spaceId, 'board')} icon={Columns3} label="Board" />
        <SidebarNavItem to={spacePath('beacon', spaceId, 'reports')} icon={BarChart3} label="Reports" />
      </nav>

      <QueuesSection spaceId={spaceId} />
    </SidebarChrome>
  );
}

/**
 * The space's queues, in the order the space put them in (P4).
 *
 * The order is the server's, straight from `position` — this never re-sorts by
 * name. The whole point of the reorder endpoint is that a service desk decides
 * which queue an agent's eye lands on first, and a client-side sort would throw
 * that away everywhere except the one screen where it is edited.
 *
 * `can_manage` from the list response is what puts the + here. A contributor
 * reads every queue in this list and simply gets no + — the capability rule
 * itself lives on the server and is not reproduced client-side.
 */
function QueuesSection({ spaceId }: { spaceId: string }) {
  const collapsed = useSidebarIsCollapsed();
  const { user } = useAuth();
  const orgId = user?.orgId ?? '';
  const queuesQuery = useQueues(orgId, spaceId);

  const queues = queuesQuery.data?.queues ?? [];
  const canManage = queuesQuery.data?.can_manage ?? false;

  // Collapsed to the icon rail: one row into the queues index, which is the
  // whole section in the only form that fits.
  if (collapsed) {
    return (
      <nav className="mt-[var(--space-3)] flex flex-col gap-[2px]">
        <SidebarNavItem to={spacePath('beacon', spaceId, 'queues')} icon={ListFilter} label="Queues" />
      </nav>
    );
  }

  return (
    <div className="mt-[var(--space-3)]">
      <div className="flex items-center px-[var(--space-3)] pb-[var(--space-1)]">
        <NavLink
          to={spacePath('beacon', spaceId, 'queues')}
          end
          className="text-[10px] font-medium uppercase tracking-wider text-[var(--color-text-muted)] transition-colors hover:text-[var(--color-text)]"
        >
          Queues
        </NavLink>
        {canManage && (
          <NavLink
            to={spacePath('beacon', spaceId, 'queues/new')}
            aria-label="New queue"
            title="New queue"
            data-testid="sidebar-new-queue"
            className="ml-auto rounded p-0.5 text-[var(--color-text-muted)] transition-colors hover:text-[var(--color-primary)]"
          >
            <Plus className="h-3.5 w-3.5" />
          </NavLink>
        )}
      </div>

      {queues.length === 0 ? (
        <p className="px-[var(--space-3)] py-[var(--space-1)] text-[var(--text-xs)] text-[var(--color-text-muted)]">
          {queuesQuery.isLoading ? 'Loading…' : 'No queues yet.'}
        </p>
      ) : (
        <nav data-testid="sidebar-queues" className="flex flex-col gap-[2px]">
          {queues.map((queue) => (
            // NavLink, not Link: this renders from the LAYOUT route, whose
            // params never include the child :queueId, so a useParams
            // comparison here would always be false.
            <NavLink
              key={queue.id}
              to={spacePath('beacon', spaceId, `queues/${queue.id}`)}
              end
              title={queue.name}
              data-testid="sidebar-queue-item"
              className={({ isActive }) =>
                cn(
                  'flex items-center gap-[var(--space-2)] rounded-[var(--radius-md)] px-[var(--space-3)] py-[6px]',
                  'text-[13px] transition-colors duration-150',
                  isActive
                    ? 'bg-[var(--color-primary-muted)] text-[var(--color-primary)]'
                    : 'text-[var(--color-text-muted)] hover:bg-[var(--color-surface-hover)] hover:text-[var(--color-text)]',
                )
              }
            >
              <ListFilter className="h-3.5 w-3.5 shrink-0" />
              <span className="truncate">{queue.name}</span>
            </NavLink>
          ))}
        </nav>
      )}
    </div>
  );
}
