import { BrowserRouter, Routes, Route } from 'react-router-dom';
import { Layout } from '@/components/Layout';
import { AuthGuard } from '@/components/AuthGuard';
import { LoginPage } from '@/pages/LoginPage';
import { FleetStatusPage } from '@/pages/FleetStatusPage';
import { AgentsPage } from '@/pages/AgentsPage';
import { CostPage } from '@/pages/CostPage';
import { TracePage } from '@/pages/TracePage';

export function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route
          element={
            <AuthGuard>
              <Layout />
            </AuthGuard>
          }
        >
          <Route path="/" element={<FleetStatusPage />} />
          <Route path="/agents" element={<AgentsPage />} />
          <Route path="/costs" element={<CostPage />} />
          <Route path="/traces" element={<TracePage />} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}
