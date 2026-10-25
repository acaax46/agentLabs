import { Navigate, Route, Routes } from 'react-router-dom';
import { AuthPages } from './auth/pages';
import { useAuth } from './auth/context';
import { Shell } from './layout/Shell';
import { OverviewPage } from './platform/pages/OverviewPage';
import { ProjectDetailPage, ProjectsPage } from './platform/pages/ProjectsPage';
import { AgentPlaygroundPage, AgentsPage } from './platform/pages/AgentsPage';
import { ToolsPage } from './platform/pages/ToolsPage';
import { ToolCallDetailPage, ToolCallsPage } from './platform/pages/ToolCallsPage';
import { VersionsPage } from './platform/pages/VersionsPage';
import { P1Unavailable } from './platform/p1Availability';
import { RunDetailPage, RunsPage } from './platform/pages/RunsPage';
import { ConnectionsPage } from './platform/pages/ConnectionsPage';
import { KeysPage } from './platform/pages/KeysPage';
import { UsagePage } from './platform/pages/UsagePage';
import { ArtifactsPage } from './platform/pages/ArtifactsPage';
import { SettingsPage } from './platform/pages/SettingsPage';

export function App() {
  const auth = useAuth();

  if (auth.pending) {
    return (
      <main className="wb-auth-loading" role="status">
        <span className="wb-spinner" />
        正在确认登录状态…
      </main>
    );
  }

  if (!auth.user) {
    return (
      <Routes>
        <Route path="/login" element={<AuthPages />} />
        <Route path="/register" element={<AuthPages key="register" registration />} />
        <Route path="*" element={<Navigate to="/login" replace />} />
      </Routes>
    );
  }

  return (
    <Shell>
      <Routes>
        <Route path="/" element={<Navigate to="/overview" replace />} />
        <Route path="/login" element={<Navigate to="/overview" replace />} />
        <Route path="/register" element={<Navigate to="/overview" replace />} />
        <Route path="/overview" element={<OverviewPage />} />
        <Route path="/projects" element={<ProjectsPage />} />
        <Route path="/projects/:id" element={<ProjectDetailPage />} />
        <Route path="/agents" element={<AgentsPage />} />
        <Route path="/agents/:id" element={<AgentPlaygroundPage />} />
        <Route path="/agents/:id/versions" element={<VersionsPage />} />
        <Route path="/tools" element={<ToolsPage />} />
        <Route path="/tool-calls" element={<ToolCallsPage />} />
        <Route path="/tool-calls/:id" element={<ToolCallDetailPage />} />
        <Route path="/runs" element={<RunsPage />} />
        <Route path="/runs/:id" element={<RunDetailPage />} />
        <Route path="/connections" element={<ConnectionsPage />} />
        <Route path="/keys" element={<KeysPage />} />
        <Route path="/usage" element={<UsagePage />} />
        <Route path="/artifacts" element={<ArtifactsPage />} />
        <Route path="/settings" element={<SettingsPage />} />
        <Route path="/approvals" element={<P1Unavailable feature="approvals" />} />
        <Route path="/webhooks" element={<P1Unavailable feature="webhooks" />} />
        <Route path="/projects/:id/members" element={<P1Unavailable feature="members" />} />
        <Route path="/agents/:id/draft" element={<P1Unavailable feature="versions" />} />
        <Route path="/connections/custom" element={<P1Unavailable feature="custom-endpoints" />} />
        <Route path="/account" element={<Navigate to="/settings#account" replace />} />
        <Route path="/environment" element={<Navigate to="/settings#environment" replace />} />
        <Route path="/budgets" element={<Navigate to="/usage" replace />} />
        <Route path="/runtime" element={<Navigate to="/runs" replace />} />
        <Route path="/users" element={<Navigate to="/settings#permissions" replace />} />
        <Route path="*" element={<Navigate to="/overview" replace />} />
      </Routes>
    </Shell>
  );
}
