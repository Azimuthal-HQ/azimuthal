import { Routes, Route, Navigate } from 'react-router-dom';
import { Shell } from './components/layout/Shell';
import { LoginPage } from './pages/auth/LoginPage';
import { DashboardPage } from './pages/dashboard/DashboardPage';
import { TicketListPage } from './pages/servicedesk/TicketListPage';
import { TicketDetailPage } from './pages/servicedesk/TicketDetailPage';
import { KanbanPage } from './pages/servicedesk/KanbanPage';
import { WikiPage } from './pages/wiki/WikiPage';
import { BacklogPage } from './pages/projects/BacklogPage';
import { ItemDetailPage } from './pages/projects/ItemDetailPage';
import { SprintBoardPage } from './pages/projects/SprintBoardPage';
import { SettingsPage } from './pages/settings/SettingsPage';

export function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route path="/" element={<Shell />}>
        <Route index element={<DashboardPage />} />
        <Route path="dashboard" element={<Navigate to="/" replace />} />

        {/* Service desk — top-level convenience routes */}
        <Route path="tickets" element={<TicketListPage />} />
        <Route path="tickets/:ticketId" element={<TicketDetailPage />} />
        <Route path="kanban" element={<KanbanPage />} />

        {/* Wiki — top-level convenience routes */}
        <Route path="wiki" element={<WikiPage />} />
        <Route path="wiki/:pageId" element={<WikiPage />} />

        {/* Projects — top-level convenience routes */}
        <Route path="backlog" element={<BacklogPage />} />
        <Route path="backlog/:itemKey" element={<ItemDetailPage />} />
        <Route path="board" element={<SprintBoardPage />} />

        {/* Space-scoped routes (API-backed, with space ID) */}
        <Route path="spaces/:spaceId/tickets" element={<TicketListPage />} />
        <Route path="spaces/:spaceId/tickets/:ticketId" element={<TicketDetailPage />} />
        <Route path="spaces/:spaceId/kanban" element={<KanbanPage />} />
        <Route path="spaces/:spaceId/wiki" element={<WikiPage />} />
        <Route path="spaces/:spaceId/wiki/:pageId" element={<WikiPage />} />
        <Route path="spaces/:spaceId/backlog" element={<BacklogPage />} />
        <Route path="spaces/:spaceId/board" element={<SprintBoardPage />} />

        <Route path="settings" element={<SettingsPage />} />
        <Route path="settings/:section" element={<SettingsPage />} />
      </Route>
    </Routes>
  );
}
