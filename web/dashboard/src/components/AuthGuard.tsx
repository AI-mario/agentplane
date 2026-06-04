import { Navigate } from 'react-router-dom';
import { isAuthenticated } from '@/lib/auth';

/**
 * Route guard: redirects unauthenticated users to login without exposing data (Requirement 13.8).
 */
export function AuthGuard({ children }: { children: React.ReactNode }) {
  if (!isAuthenticated()) {
    return <Navigate to="/login" replace />;
  }
  return <>{children}</>;
}
