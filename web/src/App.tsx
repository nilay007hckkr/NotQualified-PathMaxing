import { Route, Routes } from 'react-router';
import { RequireAuth } from './auth/RequireAuth';
import { Layout } from './components/Layout';
import { EmptyState } from './components/States';
import { DashboardPage } from './pages/DashboardPage';
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
        <Route path="*" element={<EmptyState title="This page doesn’t exist." />} />
      </Route>
    </Routes>
  );
}
