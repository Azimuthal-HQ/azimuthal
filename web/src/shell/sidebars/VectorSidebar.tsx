import { Columns3, ListTodo, Map, Tags, Zap } from 'lucide-react';
import type { Space } from '../../lib/api';
import { spacePath } from '../modules';
import { SidebarChrome, SidebarNavItem, SidebarSection } from './SidebarChrome';
import { SpacePicker } from '../SpacePicker';
import { useSidebarCollapsed } from '../hooks/useSidebarCollapsed';

/** VectorSidebar is the space-scoped left panel for the Vector (project) module. */
export function VectorSidebar({ space, spaceId }: { space: Space | undefined; spaceId: string }) {
  const [collapsed] = useSidebarCollapsed();
  return (
    <SidebarChrome
      testId="space-sidebar"
      module="vector"
      header={<SpacePicker module="vector" currentSpace={space} collapsed={collapsed} />}
      settingsTo={spacePath('vector', spaceId, 'settings')}
    >
      <nav className="flex flex-col gap-[2px]">
        <SidebarNavItem to={spacePath('vector', spaceId, 'backlog')} icon={ListTodo} label="Backlog" />
        <SidebarNavItem to={spacePath('vector', spaceId, 'board')} icon={Columns3} label="Board" />
        <SidebarNavItem to={spacePath('vector', spaceId, 'sprints')} icon={Zap} label="Sprints" />
        <SidebarNavItem to={spacePath('vector', spaceId, 'roadmap')} icon={Map} label="Roadmap" />
      </nav>
      <SidebarSection label="Configure">
        <SidebarNavItem to={spacePath('vector', spaceId, 'labels')} icon={Tags} label="Tags" />
      </SidebarSection>
    </SidebarChrome>
  );
}
