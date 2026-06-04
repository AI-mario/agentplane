import { Link, Outlet, useNavigate } from 'react-router-dom';
import { clearAuth } from '@/lib/auth';
import { AlertsPanel } from './AlertsPanel';

/**
 * Main application layout with responsive nav.
 */
export function Layout() {
  const navigate = useNavigate();

  const handleLogout = () => {
    clearAuth();
    navigate('/login');
  };

  return (
    <div className="app-layout">
      <nav className="sidebar" aria-label="Main navigation">
        <div className="sidebar-brand">
          <h1>AgentPlane</h1>
        </div>
        <ul className="sidebar-nav">
          <li><Link to="/">Fleet Status</Link></li>
          <li><Link to="/agents">Agents</Link></li>
          <li><Link to="/costs">Costs</Link></li>
          <li><Link to="/traces">Traces</Link></li>
        </ul>
        <div className="sidebar-footer">
          <button onClick={handleLogout} className="logout-btn">
            Sign Out
          </button>
        </div>
      </nav>
      <main className="main-content">
        <AlertsPanel />
        <Outlet />
      </main>
    </div>
  );
}
