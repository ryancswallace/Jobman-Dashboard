import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { JobDetailPage } from "./JobDetail";
const session = vi.hoisted(() => ({
  identity: "alice",
  bootstrap: {
    preferences: { timezone: "UTC", refreshSeconds: 0 },
    sources: [],
  },
}));
vi.mock("../lib/session", () => ({ useSession: () => session }));
const id = (n: number) =>
  `71000000-0000-4000-8000-${String(n).padStart(12, "0")}`;
const job = {
  deploymentId: id(1),
  namespaceId: id(2),
  id: id(3),
  name: "Run selection example",
  targetId: id(8),
  phase: "terminal",
  outcome: "success",
  desiredState: "run",
  confidence: "fresh",
  revision: "3",
  createdAt: "2026-10-04T12:00:00Z",
  updatedAt: "2026-10-04T12:00:00Z",
  labels: {},
};
const run = {
  id: id(4),
  number: "9007199254740993",
  phase: "terminal",
  desiredState: "run",
  outcome: "failure",
  createdAt: job.createdAt,
  updatedAt: job.updatedAt,
  executionId: id(6),
  executionPhase: "terminal",
  targetId: id(8),
  targetGenerationId: id(9),
  backend: "subprocess",
};
const old = { ...run, id: id(5), number: "2", executionId: id(7) };
const meta = {
  completeness: "complete",
  fetchedAt: job.updatedAt,
  sources: [
    {
      deploymentId: job.deploymentId,
      namespaceId: job.namespaceId,
      status: "available",
      asOf: job.updatedAt,
      fetchedAt: job.updatedAt,
    },
  ],
};
const path = `/deployments/${job.deploymentId}/namespaces/${job.namespaceId}/jobs/${job.id}`;
type Fetch = (input: string, options?: RequestInit) => Promise<Response>;
let fetcher: ReturnType<typeof vi.fn<Fetch>>;
beforeEach(() => {
  fetcher = vi.fn(async (input: string, options?: RequestInit) => {
    const u = new URL(input, "https://dashboard.test");
    if (u.pathname.endsWith(`/jobs/${job.id}`))
      return Response.json({ job, fetchedAt: job.updatedAt });
    if (u.pathname.endsWith("/runs"))
      return Response.json({
        ...meta,
        items: u.searchParams.has("cursor") ? [old] : [run],
        total: "2",
        ...(!u.searchParams.has("cursor") ? { nextCursor: "opaque-page" } : {}),
      });
    if (u.pathname.endsWith(`/runs/${run.id}`))
      return Response.json({ ...meta, run });
    if (u.pathname.endsWith(`/runs/${old.id}`))
      return Response.json({ ...meta, run: old });
    if (u.pathname.endsWith("/logs"))
      return Response.json({
        bytesBase64: btoa("historic log"),
        stream: "stdout",
        runId: old.id,
        runNumber: old.number,
        executionId: old.executionId,
        startOffset: "0",
        endOffset: "12",
        state: "complete",
        truncated: false,
      });
    if (u.pathname.endsWith("/artifacts"))
      return Response.json({ ...meta, items: [], total: "0" });
    if (u.pathname.endsWith("/reports") && options?.method === "POST")
      return Response.json(
        {
          deploymentId: job.deploymentId,
          namespaceId: job.namespaceId,
          jobId: job.id,
          taskId: "task",
          state: "queued",
          profile: "metadata",
          runId: old.id,
          sourceRevision: "3",
          createdAt: job.createdAt,
          expiresAt: "2026-11-03T12:00:00Z",
          outdated: false,
        },
        { status: 202 },
      );
    if (u.pathname.endsWith("/reports"))
      return Response.json({ items: [], fetchedAt: job.updatedAt });
    if (u.pathname.endsWith("/reports/task"))
      return Response.json({
        deploymentId: job.deploymentId,
        namespaceId: job.namespaceId,
        jobId: job.id,
        taskId: "task",
        state: "queued",
        profile: "metadata",
        runId: old.id,
        sourceRevision: "3",
        createdAt: job.createdAt,
        expiresAt: "2026-11-03T12:00:00Z",
        outdated: false,
      });
    return Response.json(
      { code: "not_found_or_inaccessible", message: "Not accessible." },
      { status: 404 },
    );
  });
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => vi.unstubAllGlobals());
function mount(query = "") {
  return render(
    <MemoryRouter initialEntries={[path + query]}>
      <Routes>
        <Route
          path="/deployments/:deploymentId/namespaces/:namespaceId/jobs/:jobId"
          element={<JobDetailPage />}
        />
      </Routes>
    </MemoryRouter>,
  );
}
it("paginates actual run references and pins logs, artifacts and new reports to selection", async () => {
  mount();
  await screen.findByText("Run selection example");
  expect(screen.getByRole("link", { name: "Open in app" })).toHaveAttribute(
    "href",
    `jobman-dashboard://job/${job.deploymentId}/${job.namespaceId}/${job.id}`,
  );
  expect(fetcher.mock.calls.some(([u]) => String(u).includes("/runs"))).toBe(
    false,
  );
  fireEvent.click(screen.getByRole("button", { name: "Choose a run" }));
  await screen.findByRole("button", { name: `Select run ${run.number}` });
  fireEvent.click(screen.getByRole("button", { name: "Next run page" }));
  fireEvent.click(await screen.findByRole("button", { name: "Select run 2" }));
  await screen.findByText(/Run 2 ·/);
  expect(screen.getByRole("button", { name: "Select run 2" })).toHaveAttribute(
    "aria-current",
    "true",
  );
  fireEvent.click(screen.getByRole("button", { name: "Logs" }));
  await screen.findByText("historic log");
  expect(
    fetcher.mock.calls.find(([u]) => String(u).includes("/logs?"))?.[0],
  ).toContain("runNumber=2");
  fireEvent.click(screen.getByRole("button", { name: "Output files" }));
  await screen.findByText("No output file details");
  expect(screen.getByLabelText("Artifact run number")).toHaveValue("2");
  expect(screen.getByLabelText("Artifact run number")).toHaveAttribute(
    "readonly",
  );
  fireEvent.click(screen.getByRole("button", { name: "Diagnosis" }));
  await screen.findByRole("button", { name: "Generate report" });
  expect(screen.getByLabelText("Report run ID")).toHaveValue(old.id);
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: "Generate report" }),
    ).toBeEnabled(),
  );
  fireEvent.click(screen.getByRole("button", { name: "Generate report" }));
  await waitFor(() =>
    expect(
      fetcher.mock.calls.some(
        ([u, o]) => String(u).endsWith("/reports") && o?.method === "POST",
      ),
    ).toBe(true),
  );
  const post = fetcher.mock.calls.find(([, o]) => o?.method === "POST")!;
  expect(JSON.parse(String(post[1]?.body))).toEqual({
    profile: "metadata",
    runId: old.id,
  });
  fireEvent.click(screen.getByRole("button", { name: "Clear run selection" }));
  await screen.findByText(
    "Latest logs · output files from all runs · diagnosis of recent runs",
  );
  expect(screen.getByLabelText("Report run ID")).toHaveValue("");
});
it("does not fall back to current logs when a historical reference is inaccessible", async () => {
  mount(`?runId=${id(99)}&tab=logs`);
  await screen.findByText("Not accessible.");
  expect(fetcher.mock.calls.some(([u]) => String(u).includes("/logs?"))).toBe(
    false,
  );
  expect(
    screen.getByRole("button", { name: "Clear run selection" }),
  ).toBeVisible();
});
it("keeps historical run facts separate from current job metadata", async () => {
  mount(`?runId=${old.id}`);
  const heading = await screen.findByRole("heading", {
    name: "Selected run details · run 2",
  });
  const selected = within(heading.closest("section")!);
  expect(selected.getByText(old.executionId)).toBeVisible();
  expect(selected.getByText(old.targetGenerationId)).toBeVisible();
  expect(selected.getByText("Failure")).toBeVisible();
  expect(selected.getByText("Run record created")).toBeVisible();
  expect(
    screen.getByRole("heading", { name: "Current job details" }),
  ).toBeVisible();
  expect(
    screen.getByRole("heading", { name: "Current job timeline" }),
  ).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "Clear run selection" }));
  await waitFor(() =>
    expect(
      screen.queryByRole("heading", { name: "Selected run details · run 2" }),
    ).toBeNull(),
  );
  expect(screen.queryByText(old.executionId)).toBeNull();
});
it("shows scheduler-reported backend separately from execution placement", async () => {
  const original = fetcher.getMockImplementation()!;
  fetcher.mockImplementation(async (input: string, options?: RequestInit) =>
    String(input).endsWith(`/jobs/${job.id}`)
      ? Response.json({
          job: {
            ...job,
            backend: "placement-backend",
            scheduler: { backend: "scheduler-backend" },
          },
          fetchedAt: job.updatedAt,
        })
      : original(input, options),
  );
  mount();
  const current = await screen.findByRole("heading", {
    name: "Current job details",
  });
  expect(
    within(current.closest("section")!).getByText("placement-backend"),
  ).toBeVisible();
  const scheduler = screen.getByRole("heading", {
    name: "Current job scheduler details",
  });
  expect(
    within(scheduler.closest("section")!).getByText("scheduler-backend"),
  ).toBeVisible();
});
it("rejects mismatched selected artifact provenance before displaying it", async () => {
  const original = fetcher.getMockImplementation()!;
  fetcher.mockImplementation(async (input: string, o?: RequestInit) =>
    String(input).includes("/artifacts?")
      ? Response.json({
          ...meta,
          total: "1",
          items: [
            {
              id: id(90),
              name: "wrong-run-secret",
              runId: run.id,
              runNumber: run.number,
              executionId: run.executionId,
            },
          ],
        })
      : original(input, o),
  );
  mount(`?runId=${old.id}&tab=artifacts`);
  await screen.findByText("Artifact metadata does not match the selected run.");
  expect(screen.queryByText("wrong-run-secret")).not.toBeInTheDocument();
});
