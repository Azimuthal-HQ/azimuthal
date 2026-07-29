import { Navigate, useParams } from 'react-router-dom';

/**
 * `/home/:dashboardId` → `/dashboards/:dashboardId`.
 *
 * The interim Home carried a `/home/:dashboardId` route holding a "dashboards
 * are on their way" placeholder, and the Home sidebar linked `/home/new` at
 * it. P5 moves the real surface to `/dashboards`, so both old paths forward
 * rather than falling through to the app-level catch-all — a bookmark from
 * before this phase should land somewhere, and a 404 for a route the product
 * itself used to link is the kind of breakage nobody reports.
 */
export function LegacyHomeDashboardRedirect() {
  const { dashboardId } = useParams();
  return <Navigate to={dashboardId ? `/dashboards/${dashboardId}` : '/dashboards'} replace />;
}
