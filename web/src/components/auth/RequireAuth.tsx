import { Navigate, useLocation } from 'react-router-dom';
import { isAuthenticated } from '../../lib/auth';

interface RequireAuthProps {
  children: React.ReactNode;
}

/** Redirects unauthenticated users to /login, preserving the intended destination. */
export function RequireAuth({ children }: RequireAuthProps) {
  const location = useLocation();

  if (!isAuthenticated()) {
    const redirect = location.pathname !== '/' ? `?redirect=${encodeURIComponent(location.pathname)}` : '';
    return <Navigate to={`/login${redirect}`} replace />;
  }

  return <>{children}</>;
}
