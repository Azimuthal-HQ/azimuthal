import { Routes, Route, Navigate } from 'react-router-dom';
import { RequireAuth } from './components/auth/RequireAuth';
import { ErrorBoundary } from './components/ErrorBoundary';
import { AppShell } from './shell/AppShell';
import { HomeLayout } from './shell/HomeLayout';
import { SpaceLayout } from './shell/SpaceLayout';
import { ModuleLandingRedirect } from './shell/ModuleLandingRedirect';
import { NotFoundPage } from './shell/NotFoundPage';
import { LoginPage } from './pages/auth/LoginPage';
import { HomePage } from './pages/home/HomePage';
import { DashboardsListPage } from './pages/dashboards/DashboardsListPage';
import { DashboardPage } from './pages/dashboards/DashboardPage';
import { LegacyHomeDashboardRedirect } from './pages/dashboards/LegacyHomeDashboardRedirect';
import { SearchPage } from './pages/home/SearchPage';
import { TicketListPage } from './pages/beacon/TicketListPage';
import { TicketDetailPage } from './pages/beacon/TicketDetailPage';
import { ReportsPage } from './pages/beacon/ReportsPage';
import { WikiPage } from './pages/codex/WikiPage';
import { DraftsPage } from './pages/codex/DraftsPage';
import { TagPage } from './pages/codex/TagPage';
import { ItemDetailPage } from './pages/vector/ItemDetailPage';
import {
  ModuleBacklogRoute,
  ModuleBoardRoute,
  ModuleIndexRoute,
  ModuleLabelsRoute,
  ModuleQueueBuilderRoute,
  ModuleQueueDetailRoute,
  ModuleQueuesRoute,
  ModuleRoadmapRoute,
  ModuleSprintsRoute,
} from './pages/space/ModuleRoutes';
import { SpacePlaceholderPage } from './pages/space/SpacePlaceholderPage';
import { SpaceSettingsPage } from './pages/space/SpaceSettingsPage';
import { SpaceDirectoryPage } from './pages/spaces/SpaceDirectoryPage';
import { ViewsListPage } from './pages/views/ViewsListPage';
import { ViewDetailPage } from './pages/views/ViewDetailPage';
import { ViewBuilderPage } from './pages/views/ViewBuilderPage';
import { SettingsPage } from './pages/settings/SettingsPage';
import { WorkflowAdminPage } from './pages/settings/WorkflowAdminPage';
import { InviteAcceptPage } from './pages/auth/InviteAcceptPage';
import { SharedEntityPage } from './pages/shared/SharedEntityPage';
import { RequirePortalSession } from './components/portal/RequirePortalSession';
import { PortalLayout } from './pages/portal/PortalLayout';
import { PortalSignInPage } from './pages/portal/PortalSignInPage';
import { PortalRedeemPage } from './pages/portal/PortalRedeemPage';
import { PortalRequestsPage } from './pages/portal/PortalRequestsPage';
import { PortalNewRequestPage } from './pages/portal/PortalNewRequestPage';
import { PortalRequestDetailPage } from './pages/portal/PortalRequestDetailPage';
import { PortalNotFoundPage } from './pages/portal/PortalNotFoundPage';
import { AdminLayout } from './pages/admin/AdminLayout';
import { PeoplePage } from './pages/admin/PeoplePage';
import { TeamsAdminPage } from './pages/admin/TeamsAdminPage';
import { AccessMatrixPage } from './pages/admin/AccessMatrixPage';
import { SpacesAdminPage } from './pages/admin/SpacesAdminPage';
import { AuditLogPage } from './pages/admin/AuditLogPage';
import { OrgSettingsPage } from './pages/admin/OrgSettingsPage';
import { ItemTypesAdminPage } from './pages/admin/ItemTypesAdminPage';
import { CustomFieldsAdminPage } from './pages/admin/CustomFieldsAdminPage';

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
      {/* The customer portal (migration 044). OUTSIDE the auth wall and outside
          the shell, and not merely for layout reasons: an external requester
          has no users row, no membership and no grant, so RequireAuth would
          bounce them to /login and AppShell would render a space switcher and
          module tabs for containers they must never learn exist. Its own
          PortalLayout supplies the frame and RequirePortalSession the guard,
          reading the per-portal token rather than the internal one.

          /portal/:portalKey/signin/:linkToken is a CONTRACT WITH THE BACKEND,
          not a layout choice: internal/core/portal/service.go emails
          {APP_BASE_URL}/portal/{portalKey}/signin/{rawToken} as a path, no
          server route matches it, and the SPA handler serves index.html — so
          this declaration is the only thing that makes an emailed link work.

          The subtree declares its OWN path="*". The shell's catch-all lives
          inside the shell route, so without this an unknown /portal/... URL
          would render the internal chrome and redirect a signed-out customer
          to /login — the zero-context guarantee broken by the router. */}
      <Route path="/portal/:portalKey" element={<PortalLayout />}>
        <Route index element={<PortalSignInPage />} />
        <Route path="signin/:linkToken" element={<PortalRedeemPage />} />
        <Route element={<RequirePortalSession />}>
          <Route path="requests" element={<PortalRequestsPage />} />
          <Route path="requests/new" element={<PortalNewRequestPage />} />
          <Route path="requests/:reference" element={<PortalRequestDetailPage />} />
        </Route>
        <Route path="*" element={<PortalNotFoundPage />} />
      </Route>
      <Route element={<RequireAuth><AppShell /></RequireAuth>}>
        {/* Home: user- and org-scoped pages under the static "Your work" panel */}
        <Route element={<HomeLayout />}>
          {/* Home is the caller's default dashboard, seeded on a first visit
              (P5, decision log D4). */}
          <Route path="/" element={<HomePage />} />

          {/* Dashboards (P5, ADR-0009). A TOP-LEVEL destination for the same
              reason saved views are: a dashboard arranges gadgets that cross
              modules and containers, and its API is org-scoped, so there is no
              space to hang it under. Spec §7 sketches
              /:module/:spaceId/dashboards/:id, which a cross-container
              surface cannot express — the identical contradiction the
              reconciliation doc already recorded for views. Static before
              dynamic, as at /views. */}
          <Route path="dashboards" element={<DashboardsListPage />} />
          <Route path="dashboards/:dashboardId" element={<DashboardPage />} />

          {/* The interim Home routes. /home/:id was the dashboard placeholder
              and /home/new the sidebar's "new dashboard" link; both now belong
              to the real surface, and old links keep working rather than
              hitting the catch-all 404. */}
          <Route path="home" element={<Navigate to="/dashboards" replace />} />
          <Route path="home/new" element={<Navigate to="/dashboards" replace />} />
          <Route path="home/:dashboardId" element={<LegacyHomeDashboardRedirect />} />

          <Route path="search" element={<SearchPage />} />
          <Route path="spaces" element={<SpaceDirectoryPage />} />

          {/* Saved views (P4, ADR-0009). A TOP-LEVEL destination, not a space
              sub-route: a view crosses modules and containers by design and its
              API is org-scoped, so there is no space to hang it under and no
              module that owns it. It sits beside Home for the same reason the
              space directory does — both are org-scoped, neither is a product.
              /views/new must exist for its own sake: SaveAsViewButton on the
              ticket list and the backlog navigates straight to it. */}
          <Route path="views" element={<ViewsListPage />} />
          <Route path="views/new" element={<ViewBuilderPage />} />
          <Route path="views/:viewId" element={<ViewDetailPage />} />
          <Route path="views/:viewId/edit" element={<ViewBuilderPage />} />

          <Route path="settings" element={<SettingsPage />} />
          {/* Org settings moved to the admin panel; keep old links working. */}
          <Route path="settings/organization" element={<Navigate to="/admin/settings" replace />} />
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
            <Route path="item-types" element={<ItemTypesAdminPage />} />
            <Route path="custom-fields" element={<CustomFieldsAdminPage />} />
            <Route path="audit-log" element={<AuditLogPage />} />
            <Route path="settings" element={<OrgSettingsPage />} />
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
          {/* Beacon queues (P4). "new" is a static segment, so React Router
              ranks it above ":queueId" — the builder is never mistaken for a
              queue whose id happens to be spelled that way. */}
          <Route path="queues" element={<ErrorBoundary><ModuleQueuesRoute /></ErrorBoundary>} />
          <Route path="queues/new" element={<ErrorBoundary><ModuleQueueBuilderRoute /></ErrorBoundary>} />
          <Route path="queues/:queueId" element={<ErrorBoundary><ModuleQueueDetailRoute /></ErrorBoundary>} />
          <Route path="queues/:queueId/edit" element={<ErrorBoundary><ModuleQueueBuilderRoute /></ErrorBoundary>} />
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
          {/* Real since issue #15: GET …/wiki/drafts shipped in PR #73 and the
              sidebar has linked here since the navigation collapse. */}
          <Route path="drafts" element={<ErrorBoundary><DraftsPage /></ErrorBoundary>} />
          {/* The tag browse (U4). It sits under a space because every Codex
              route does — the space id is the reader's context, not the
              query's scope, which spans every space they can read. See
              components/codex/tagLinks.ts. */}
          <Route path="tags/:label" element={<ErrorBoundary><TagPage /></ErrorBoundary>} />
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
