import { Routes, Route, Navigate } from 'react-router-dom';
import { Shell } from './components/layout/Shell';
import { RequireAuth } from './components/auth/RequireAuth';
import { ErrorBoundary } from './components/ErrorBoundary';
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
      <Route path="/" element={<RequireAuth><Shell /></RequireAuth>}>
        <Route index element={<DashboardPage />} />
        <Route path="dashboard" element={<Navigate to="/" replace />} />

        {/* Space-scoped routes (API-backed, with space ID) */}
        <Route path="spaces/:spaceId/tickets" element={<ErrorBoundary><TicketListPage /></ErrorBoundary>} />
        <Route path="spaces/:spaceId/tickets/:ticketId" element={<ErrorBoundary><TicketDetailPage /></ErrorBoundary>} />
        <Route path="spaces/:spaceId/kanban" element={<ErrorBoundary><KanbanPage /></ErrorBoundary>} />
        <Route path="spaces/:spaceId/wiki" element={<ErrorBoundary><WikiPage /></ErrorBoundary>} />
        <Route path="spaces/:spaceId/wiki/:pageId" element={<ErrorBoundary><WikiPage /></ErrorBoundary>} />
        <Route path="spaces/:spaceId/backlog" element={<ErrorBoundary><BacklogPage /></ErrorBoundary>} />
        <Route path="spaces/:spaceId/backlog/:itemKey" element={<ErrorBoundary><ItemDetailPage /></ErrorBoundary>} />
        <Route path="spaces/:spaceId/board" element={<ErrorBoundary><SprintBoardPage /></ErrorBoundary>} />

        <Route path="settings" element={<SettingsPage />} />
        <Route path="settings/:section" element={<SettingsPage />} />
      </Route>
    </Routes>
  );
}
