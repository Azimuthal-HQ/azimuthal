import { Routes, Route, Navigate } from 'react-router-dom';
import { RequireAuth } from './components/auth/RequireAuth';
import { ErrorBoundary } from './components/ErrorBoundary';
import { AppShell } from './shell/AppShell';
import { HomeLayout } from './shell/HomeLayout';
import { SpaceLayout } from './shell/SpaceLayout';
import { ModuleLandingRedirect } from './shell/ModuleLandingRedirect';
import { NotFoundPage } from './shell/NotFoundPage';
import { LoginPage } from './pages/auth/LoginPage';
import { HomeOverviewPage } from './pages/home/HomeOverviewPage';
import { HomeDashboardPage } from './pages/home/HomeDashboardPage';
import { SearchPage } from './pages/home/SearchPage';
import { TicketListPage } from './pages/beacon/TicketListPage';
import { TicketDetailPage } from './pages/beacon/TicketDetailPage';
import { ReportsPage } from './pages/beacon/ReportsPage';
import { WikiPage } from './pages/codex/WikiPage';
import { ItemDetailPage } from './pages/vector/ItemDetailPage';
import {
  ModuleBacklogRoute,
  ModuleBoardRoute,
  ModuleIndexRoute,
  ModuleLabelsRoute,
  ModuleRoadmapRoute,
  ModuleSprintsRoute,
} from './pages/space/ModuleRoutes';
import { SpacePlaceholderPage } from './pages/space/SpacePlaceholderPage';
import { SpaceSettingsPage } from './pages/space/SpaceSettingsPage';
import { SpaceDirectoryPage } from './pages/spaces/SpaceDirectoryPage';
import { SettingsPage } from './pages/settings/SettingsPage';
import { WorkflowAdminPage } from './pages/settings/WorkflowAdminPage';
import { InviteAcceptPage } from './pages/auth/InviteAcceptPage';
import { SharedEntityPage } from './pages/shared/SharedEntityPage';
import { AdminLayout } from './pages/admin/AdminLayout';
import { PeoplePage } from './pages/admin/PeoplePage';
import { TeamsAdminPage } from './pages/admin/TeamsAdminPage';
import { AccessMatrixPage } from './pages/admin/AccessMatrixPage';
import { SpacesAdminPage } from './pages/admin/SpacesAdminPage';
import { AuditLogPage } from './pages/admin/AuditLogPage';

export function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      {/* Invite acceptance is public: the token in the URL is the credential
          (P2.5 W2). It must stay outside the auth wall. */}
      <Route path="/invite/:token" element={<InviteAcceptPage />} />
      {/* The standalone shared-entity view (P3, ADR-0008). Authenticated but
          OUTSIDE the app shell: no space sidebar, no product tabs, no
          breadcrumb chain into a space the viewer cannot enter. Access is
          governed entirely by the share, enforced server-side. */}
      <Route
        path="/shared/:entityType/:entityId"
        element={<RequireAuth><SharedEntityPage /></RequireAuth>}
      />
      <Route element={<RequireAuth><AppShell /></RequireAuth>}>
        {/* Home: user- and org-scoped pages under the static "Your work" panel */}
        <Route element={<HomeLayout />}>
          <Route path="/" element={<HomeOverviewPage />} />
          <Route path="home/:dashboardId" element={<HomeDashboardPage />} />
          <Route path="search" element={<SearchPage />} />
          <Route path="spaces" element={<SpaceDirectoryPage />} />
          <Route path="settings" element={<SettingsPage />} />
          <Route path="settings/:section" element={<SettingsPage />} />
          <Route path="admin/workflows" element={<WorkflowAdminPage />} />

          {/* The administration area (P2.5 W3): People, Teams, Access,
              Spaces, Audit log — org admins only; everyone else gets the
              branded 404, matching the API's RequireOrgAdmin404. P2's teams
              admin relocated here (same functionality, new home). */}
          <Route path="admin" element={<AdminLayout />}>
            <Route index element={<Navigate to="/admin/people" replace />} />
            <Route path="people" element={<PeoplePage />} />
            <Route path="teams" element={<TeamsAdminPage />} />
            <Route path="access" element={<AccessMatrixPage />} />
            <Route path="spaces" element={<SpacesAdminPage />} />
            <Route path="audit-log" element={<AuditLogPage />} />
          </Route>
        </Route>

        <Route path="dashboard" element={<Navigate to="/" replace />} />

        {/* A bare module URL (product tab) forwards to a space of that module */}
        <Route path=":module" element={<ModuleLandingRedirect />} />

        {/* Every space sub-route renders inside SpaceLayout: the sidebar is a
            layout concern and must survive sub-route changes (ADR-0005). */}
        <Route path=":module/:spaceId" element={<SpaceLayout />}>
          <Route index element={<ModuleIndexRoute />} />
          <Route path="tickets" element={<ErrorBoundary><TicketListPage /></ErrorBoundary>} />
          <Route path="tickets/:ticketId" element={<ErrorBoundary><TicketDetailPage /></ErrorBoundary>} />
          <Route path="board" element={<ErrorBoundary><ModuleBoardRoute /></ErrorBoundary>} />
          <Route path="backlog" element={<ErrorBoundary><ModuleBacklogRoute /></ErrorBoundary>} />
          <Route path="backlog/:itemKey" element={<ErrorBoundary><ItemDetailPage /></ErrorBoundary>} />
          <Route path="sprints" element={<ErrorBoundary><ModuleSprintsRoute /></ErrorBoundary>} />
          <Route path="roadmap" element={<ErrorBoundary><ModuleRoadmapRoute /></ErrorBoundary>} />
          <Route path="labels" element={<ErrorBoundary><ModuleLabelsRoute /></ErrorBoundary>} />
          <Route path="reports" element={<ErrorBoundary><ReportsPage /></ErrorBoundary>} />
          <Route path="pages/:pageId" element={<ErrorBoundary><WikiPage /></ErrorBoundary>} />
          <Route path="search" element={<SpacePlaceholderPage feature="search" />} />
          <Route path="recent" element={<SpacePlaceholderPage feature="recent" />} />
          <Route path="starred" element={<SpacePlaceholderPage feature="starred" />} />
          <Route path="drafts" element={<SpacePlaceholderPage feature="drafts" />} />
          <Route path="settings" element={<ErrorBoundary><SpaceSettingsPage /></ErrorBoundary>} />
          {/* Unknown sub-routes keep the space chrome and render the branded
              empty state, never a blank body. */}
          <Route path="*" element={<SpacePlaceholderPage feature="unknown" />} />
        </Route>

        {/* Catch-all: a URL that matches no route must render the branded
            not-found state, never a blank body. */}
        <Route
          path="*"
          element={
            <main className="min-h-screen bg-[var(--color-bg)] pt-[var(--topnav-height)]">
              <div className="mx-auto max-w-[1280px] px-[var(--space-4)] py-[var(--space-6)]">
                <NotFoundPage />
              </div>
            </main>
          }
        />
      </Route>
    </Routes>
  );
}
