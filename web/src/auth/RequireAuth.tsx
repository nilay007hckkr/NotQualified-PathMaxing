import type { ReactNode } from 'react';
import { Navigate, useLocation } from 'react-router';
import { ErrorState, Loading } from '../components/States';
import { useMe } from './useAuth';

/** Renders children for a signed-in user (an admin, if adminOnly) and sends anyone else to the login page. */
export function RequireAuth({ children, adminOnly = false }: { children: ReactNode; adminOnly?: boolean }) {
  const me = useMe();
  const location = useLocation();

  if (me.isPending) return <Loading label="Checking your session" />;
  if (me.isError) return <ErrorState error={me.error} onRetry={() => me.refetch()} />;
  if (!me.data) return <Navigate to="/login" replace state={{ from: location.pathname + location.search }} />;
  if (adminOnly && me.data.role !== 'admin') return <Navigate to="/" replace />;
  return <>{children}</>;
}
