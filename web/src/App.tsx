import { BrowserRouter, Route, Routes } from "react-router-dom";
import { SessionProvider } from "./lib/session";
import { Layout } from "./components/Layout";
import { OverviewPage } from "./pages/Overview";
import { JobsPage } from "./pages/Jobs";
import { JobDetailPage } from "./pages/JobDetail";
import { WorkloadsPage, WorkloadDetailPage } from "./pages/Workloads";
import { TargetsPage, TargetDetailPage } from "./pages/Targets";
import { AlertsPage } from "./pages/Alerts";
import { InboxPage, InboxDetailPage } from "./pages/Inbox";
import { SettingsPage } from "./pages/Settings";
import { NotFound } from "./components/States";
export function App() {
  return (
    <BrowserRouter>
      <SessionProvider>
        <Routes>
          <Route element={<Layout />}>
            <Route index element={<OverviewPage />} />
            <Route path="jobs" element={<JobsPage />} />
            <Route
              path="deployments/:deploymentId/namespaces/:namespaceId/jobs/:jobId"
              element={<JobDetailPage />}
            />
            <Route path="workloads" element={<WorkloadsPage />} />
            <Route
              path="deployments/:deploymentId/namespaces/:namespaceId/workloads/:kind/:id"
              element={<WorkloadDetailPage />}
            />
            <Route path="targets" element={<TargetsPage />} />
            <Route
              path="deployments/:deploymentId/namespaces/:namespaceId/targets/:targetId"
              element={<TargetDetailPage />}
            />
            <Route path="alerts" element={<AlertsPage />} />
            <Route path="inbox" element={<InboxPage />} />
            <Route path="inbox/:inboxId" element={<InboxDetailPage />} />
            <Route path="settings" element={<SettingsPage />} />
            <Route path="*" element={<NotFound />} />
          </Route>
        </Routes>
      </SessionProvider>
    </BrowserRouter>
  );
}
