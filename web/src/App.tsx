import { Route, Routes } from 'react-router';
import { RequireAuth } from './auth/RequireAuth';
import { Layout } from './components/Layout';
import { EmptyState } from './components/States';
import { AbuseQueuePage, AdminLayout, AdminLinksPage, AdminOverviewPage, AuditPage, BlocklistPage, UsersPage } from './pages/AdminPages';
import { BioPagesPage } from './pages/BioPagesPage';
import { CampaignDetailsPage, CampaignsPage } from './pages/CampaignsPage';
import { DashboardPage } from './pages/DashboardPage';
import { ImportPage } from './pages/ImportPage';
import { LinkDetailsPage } from './pages/LinkDetailsPage';
import { LinksPage } from './pages/LinksPage';
import { LoginPage } from './pages/LoginPage';
import { NewLinkPage } from './pages/NewLinkPage';
import { SignupPage } from './pages/SignupPage';

export function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route path="/signup" element={<SignupPage />} />
      <Route
        element={
          <RequireAuth>
            <Layout />
          </RequireAuth>
        }
      >
        <Route index element={<DashboardPage />} />
        <Route path="links" element={<LinksPage />} />
        <Route path="links/new" element={<NewLinkPage />} />
        <Route path="links/:id" element={<LinkDetailsPage />} />
        <Route path="campaigns" element={<CampaignsPage />} />
        <Route path="campaigns/:id" element={<CampaignDetailsPage />} />
        <Route path="import" element={<ImportPage />} />
        <Route path="bio" element={<BioPagesPage />} />
        <Route
          path="admin"
          element={
            <RequireAuth adminOnly>
              <AdminLayout />
            </RequireAuth>
          }
        >
          <Route index element={<AdminOverviewPage />} />
          <Route path="abuse" element={<AbuseQueuePage />} />
          <Route path="links" element={<AdminLinksPage />} />
          <Route path="blocklist" element={<BlocklistPage />} />
          <Route path="users" element={<UsersPage />} />
          <Route path="audit" element={<AuditPage />} />
        </Route>
        <Route path="*" element={<EmptyState title="This page doesn’t exist." />} />
      </Route>
    </Routes>
  );
}
