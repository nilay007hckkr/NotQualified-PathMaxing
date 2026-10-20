import { NavLink, Outlet, useNavigate } from 'react-router';
import { useLogout, useMe } from '../auth/useAuth';

export function Layout() {
  const me = useMe();
  const logout = useLogout();
  const navigate = useNavigate();
  const user = me.data;

  return (
    <div className="app-shell">
      <a className="skip-link" href="#main">
        Skip to content
      </a>
      <header className="topbar">
        <NavLink to="/" className="brand" aria-label="PathMaxing home">
          <img src={`${import.meta.env.BASE_URL}favicon.svg`} alt="" width={28} height={28} />
          <span>PathMaxing</span>
        </NavLink>
        <nav aria-label="Main" className="nav">
          <NavLink to="/" end>
            Overview
          </NavLink>
          <NavLink to="/links">Links</NavLink>
        </nav>
        {user && (
          <div className="account">
            <span className="account-name" title={user.email}>
              {user.name || user.email}
              {user.role === 'admin' && <span className="badge badge-admin">admin</span>}
            </span>
            <button
              type="button"
              className="btn btn-secondary btn-small"
              disabled={logout.isPending}
              onClick={() => logout.mutate(undefined, { onSettled: () => navigate('/login') })}
            >
              Log out
            </button>
          </div>
        )}
      </header>
      <main id="main" className="content">
        <Outlet />
      </main>
    </div>
  );
}
