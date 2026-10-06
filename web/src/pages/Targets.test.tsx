import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { TargetDetailPage } from "./Targets";
vi.mock("../lib/session", () => ({
  useSession: () => ({ identity: "alice:east:ns", bootstrap: { sources: [] } }),
}));
const t = {
  deploymentId: "east",
  namespaceId: "ns",
  targetId: "id",
  name: "Synthetic target",
  kind: "slurm",
  state: "active",
  revision: "4",
  createdAt: "2026-10-03T12:00:00Z",
  updatedAt: "2026-10-03T12:00:00Z",
  asOf: "2026-10-03T12:00:00Z",
  generation: {
    id: "gen-1",
    number: "9007199254740993",
    executionBackend: "slurm",
    transport: "agent-api",
    runtimes: [],
    operatingSystems: [],
    architectures: [],
    capabilities: [],
    partitions: [],
    partitionCount: "201",
    partitionsTruncated: true,
    artifactStores: [],
    provider: { kind: "on-prem" },
  },
};
const meta = { sources: [], completeness: "complete", fetchedAt: t.asOf };
afterEach(() => vi.unstubAllGlobals());
it("refreshes target generation and resets partition history after replacement", async () => {
  let generation = "gen-1";
  const calls: string[] = [];
  const fetcher = vi.fn(async (path: string) => {
    calls.push(path);
    const u = new URL(path, "http://localhost");
    if (u.pathname.endsWith("/partitions")) {
      if (u.searchParams.get("cursor")) {
        generation = "gen-2";
        return Response.json(
          { code: "target_changed", message: "Target changed" },
          { status: 409 },
        );
      }
      return Response.json({
        ...meta,
        targetId: "id",
        generationId: u.searchParams.get("generationId"),
        items: [
          { name: generation === "gen-1" ? "batch" : "gpu", isDefault: true },
        ],
        total: "201",
        nextCursor: "next",
      });
    }
    return Response.json({
      ...meta,
      target: { ...t, generation: { ...t.generation, id: generation } },
    });
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <MemoryRouter
      initialEntries={["/deployments/east/namespaces/ns/targets/id"]}
    >
      <Routes>
        <Route
          path="/deployments/:deploymentId/namespaces/:namespaceId/targets/:targetId"
          element={<TargetDetailPage />}
        />
      </Routes>
    </MemoryRouter>,
  );
  expect(await screen.findByText("batch (default)")).toBeVisible();
  expect(screen.getAllByText(/9007199254740993/).length).toBeGreaterThan(0);
  await userEvent.click(
    screen.getByRole("button", { name: "Next partition page" }),
  );
  expect(
    await screen.findByRole("button", {
      name: "Refresh target and restart partitions",
    }),
  ).toBeVisible();
  expect(screen.queryByText("batch (default)")).not.toBeInTheDocument();
  await userEvent.click(
    screen.getByRole("button", {
      name: "Refresh target and restart partitions",
    }),
  );
  expect(await screen.findByText("gpu (default)")).toBeVisible();
  await waitFor(() =>
    expect(
      calls.some(
        (p) => p.includes("generationId=gen-2") && !p.includes("cursor="),
      ),
    ).toBe(true),
  );
  expect(
    screen.getByRole("button", { name: "Previous partition page" }),
  ).toBeDisabled();
});

it("bounds unusually long provider text with an explicit disclosure", async () => {
  const { BoundedTargetText } = await import("./Targets");
  const value = "us-" + "a".repeat(100000) + "-1";
  const { container } = render(<BoundedTargetText value={value} />);
  expect(container.textContent!.length).toBeLessThan(700);
  expect(screen.getByText(/Showing the first 512 characters/)).toBeVisible();
});
