import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { auditAccessibility } from "./test-accessibility";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { App } from "./App";

const deployment = "71000000-0000-4000-8000-000000000001";
const namespace = "71000000-0000-4000-8000-000000000002";
const jobID = "71000000-0000-4000-8000-000000000003";
const prefix = `/deployments/${deployment}/namespaces/${namespace}`;
const now = "2026-10-04T12:00:00Z";
const page = (items: unknown[]) => ({
  items,
  sources: [],
  completeness: "complete",
  fetchedAt: now,
});
const bootstrap = () => ({
  apiVersion: "jobman.dashboard/v1",
  account: { id: "synthetic-accessibility", displayName: "Synthetic viewer" },
  completeness: "complete",
  deployments: [
    {
      id: deployment,
      name: "Lab Control",
      status: "available",
      namespaces: [
        {
          id: namespace,
          name: "Research",
          roles: ["viewer"],
          capabilities: ["namespace.read", "jobs.read", "logs.read"],
          authorizationVersion: "1",
          authorizationCheckedAt: new Date().toISOString(),
          authorizationExpiresAt: new Date(Date.now() + 120000).toISOString(),
        },
      ],
    },
  ],
  preferences: {
    revision: "1",
    timezone: "UTC",
    appearance: "light",
    refreshSeconds: 0,
  },
});
const job = {
  deploymentId: deployment,
  namespaceId: namespace,
  id: jobID,
  name: "Synthetic analysis",
  targetId: "target",
  revision: "1",
  phase: "terminal",
  desiredState: "run",
  outcome: "failure",
  confidence: "current",
  createdAt: now,
  updatedAt: now,
  completedAt: now,
  labels: {},
};
const target = {
  deploymentId: deployment,
  namespaceId: namespace,
  targetId: "target",
  name: "Synthetic target",
  kind: "host",
  state: "active",
  revision: "1",
  createdAt: now,
  updatedAt: now,
  asOf: now,
  generation: {
    id: "generation",
    number: "1",
    executionBackend: "subprocess",
    transport: "agent-api",
    runtimes: ["native"],
    operatingSystems: ["linux"],
    architectures: ["arm64"],
    capabilities: [],
    partitions: [],
    partitionCount: "0",
    partitionsTruncated: false,
    artifactStores: [],
    provider: { kind: "on-prem" },
  },
};
const workload = {
  deploymentId: deployment,
  namespaceId: namespace,
  id: "collection",
  name: "Synthetic collection",
  kind: "collection",
  revision: "1",
  createdAt: now,
  asOf: now,
  totalChildren: "1",
  counts: { failure: "1" },
};

let failure: { code: string; message: string } | undefined;
beforeEach(() => {
  failure = undefined;
  history.replaceState(null, "", "/");
  const storage = new Map<string, string>();
  vi.stubGlobal("localStorage", {
    getItem: (key: string) => storage.get(key) ?? null,
    setItem: (key: string, value: string) => storage.set(key, value),
    removeItem: (key: string) => storage.delete(key),
  });
  vi.stubGlobal("scrollTo", vi.fn());
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string) => {
      const url = new URL(input, "http://localhost");
      const path = url.pathname;
      if (path === "/api/v1/bootstrap") return Response.json(bootstrap());
      if (failure) return Response.json(failure, { status: 503 });
      if (path === "/api/v1/overview")
        return Response.json({
          active: 0,
          awaitingExecution: 0,
          running: 0,
          evidenceAttention: 0,
          missingCompletionTime: 0,
          terminal: { success: 0, failure: 1 },
          window: { from: "2026-10-03T12:00:00Z", to: now },
          sources: [],
          completeness: "complete",
          fetchedAt: now,
        });
      if (path === "/api/v1/jobs") return Response.json(page([job]));
      if (path === `/api/v1${prefix}/jobs/${jobID}`)
        return Response.json({ job, fetchedAt: now });
      if (path.endsWith("/logs"))
        return Response.json({
          executionId: "execution",
          runId: "run",
          stream: "stdout",
          startOffset: "0",
          endOffset: "14",
          state: "complete",
          bytesBase64: "U3ludGhldGljIGxvZwo=",
          truncated: false,
          capturedAt: now,
        });
      if (path.endsWith("/artifacts"))
        return Response.json({ ...page([]), total: "0" });
      if (path.endsWith("/reports")) return Response.json({ ...page([]) });
      if (path === "/api/v1/targets")
        return Response.json({ ...page([target]), total: "1", totals: [] });
      if (path.endsWith("/targets/target"))
        return Response.json({ ...page([]), target });
      if (path.endsWith("/partitions"))
        return Response.json({
          ...page([]),
          targetId: "target",
          generationId: "generation",
          total: "0",
        });
      if (path.startsWith("/api/v1/workloads/"))
        return Response.json({ ...page([workload]), total: "1", totals: [] });
      if (path.endsWith("/workloads/collection/collection"))
        return Response.json({
          ...page([]),
          workload,
          total: "1",
          children: [{ id: jobID, index: "0", job }],
        });
      if (path === "/api/v1/rules")
        return Response.json({ items: [], fetchedAt: now });
      if (path === "/api/v1/devices") return Response.json({ items: [] });
      if (path === "/api/v1/inbox")
        return Response.json({
          ...page([]),
          unreadCount: "0",
          unavailableSources: 0,
          inaccessibleScopes: 0,
        });
      throw new Error(`Missing synthetic accessibility fixture for ${path}`);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

it.each([
  ["/", "Overview"],
  ["/jobs", "Jobs"],
  ["/workloads", "Workloads"],
  ["/targets", "Targets"],
  ["/inbox", "Inbox"],
  ["/alerts", "Alert rules"],
  ["/settings", "Settings"],
  [`${prefix}/jobs/${jobID}`, "Synthetic analysis"],
  [`${prefix}/jobs/${jobID}?tab=logs`, "Synthetic analysis"],
  [`${prefix}/jobs/${jobID}?tab=artifacts`, "Synthetic analysis"],
  [`${prefix}/jobs/${jobID}?tab=reports`, "Synthetic analysis"],
  [`${prefix}/targets/target`, "Synthetic target"],
  [`${prefix}/workloads/collection/collection`, "Synthetic collection"],
  ["/missing-page", "Page not found"],
])(
  "has accessible names, landmarks and structure on %s",
  async (path, title) => {
    history.replaceState(null, "", path);
    render(<App />);
    await screen.findByRole("heading", { level: 1, name: title });
    await waitFor(() =>
      expect(screen.queryByText(/^Loading .*…$/)).not.toBeInTheDocument(),
    );
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.getAllByRole("main")).toHaveLength(1);
    await auditAccessibility();
  },
);

it("audits the open scope picker and alert editor", async () => {
  history.replaceState(null, "", "/alerts");
  render(<App />);
  await screen.findByRole("button", { name: "+ New alert rule" });
  await userEvent.click(screen.getByText("MONITORING SCOPE"));
  expect(
    screen.getByRole("heading", { name: "Choose your scope" }),
  ).toBeVisible();
  await auditAccessibility();
  await userEvent.click(screen.getByText("MONITORING SCOPE"));
  await userEvent.click(
    screen.getByRole("button", { name: "+ New alert rule" }),
  );
  await screen.findByRole("textbox", { name: "Rule name" });
  await auditAccessibility();
});

it("announces unavailable data accessibly instead of rendering an empty list", async () => {
  history.replaceState(null, "", "/jobs");
  failure = {
    code: "authorization_unavailable",
    message: "Directory verification is unavailable",
  };
  render(<App />);
  expect(await screen.findByRole("alert")).toHaveTextContent(failure.message);
  await auditAccessibility();
});

it("supports keyboard entry and places route focus on the new heading", async () => {
  render(<App />);
  await screen.findByRole("heading", { level: 1, name: "Overview" });
  const user = userEvent.setup();
  await user.tab();
  expect(screen.getByRole("link", { name: "Skip to content" })).toHaveFocus();
  await user.click(screen.getByRole("link", { name: "Jobs" }));
  await waitFor(() =>
    expect(
      screen.getByRole("heading", { level: 1, name: "Jobs" }),
    ).toHaveFocus(),
  );
  await user.click(
    await screen.findByRole("link", { name: "Synthetic analysis" }),
  );
  await waitFor(() =>
    expect(
      screen.getByRole("heading", { level: 1, name: "Synthetic analysis" }),
    ).toHaveFocus(),
  );
});
