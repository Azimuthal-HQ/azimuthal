import { BarChart3, Columns3, Ticket } from 'lucide-react';
import type { Space } from '../../lib/api';
import { spacePath } from '../modules';
import { SidebarChrome, SidebarNavItem } from './SidebarChrome';
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
    </SidebarChrome>
  );
}
