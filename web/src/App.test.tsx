import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, it, expect, vi } from "vitest";
import { App } from "./App";
const ns = {
  id: "ns",
  name: "Research",
  roles: ["viewer", "operator"],
  capabilities: ["jobs.read", "logs.read"],
  authorizationVersion: "1",
  authorizationCheckedAt: new Date().toISOString(),
  authorizationExpiresAt: new Date(Date.now() + 120000).toISOString(),
};
const bootstrap = {
  apiVersion: "jobman.dashboard/v1",
  fixtureMode: true,
  completeness: "complete",
  account: { id: "alice", displayName: "Lab Alice" },
  deployments: [
    { id: "east", name: "Lab East", status: "available", namespaces: [ns] },
    { id: "west", name: "Lab West", status: "available", namespaces: [ns] },
  ],
  preferences: {
    revision: "1",
    timezone: "UTC",
    appearance: "light",
    refreshSeconds: 0,
  },
  csrfToken: "fixture-csrf",
};
const job = {
  deploymentId: "east",
  namespaceId: "ns",
  id: "duplicate-id",
  name: "Synthetic test",
  targetId: "slurm",
  revision: "1",
  phase: "running",
  desiredState: "cancel",
  confidence: "stale",
  createdAt: "2026-10-03T12:00:00Z",
  updatedAt: "2026-10-03T12:05:00Z",
  labels: {},
};
const page = (items: unknown[]) => ({
  items,
  completeness: "complete",
  sources: [],
  fetchedAt: new Date().toISOString(),
});
type FixtureFetch = (input: string, options?: RequestInit) => Promise<Response>;
let fetcher: ReturnType<typeof vi.fn<FixtureFetch>>;
beforeEach(() => {
  // Node's native experimental Web Storage can shadow jsdom's implementation.
  const storage = new Map<string, string>();
  vi.stubGlobal("localStorage", {
    getItem: (key: string) => storage.get(key) ?? null,
    setItem: (key: string, value: string) => storage.set(key, String(value)),
    removeItem: (key: string) => storage.delete(key),
    clear: () => storage.clear(),
    key: (index: number) => [...storage.keys()][index] ?? null,
    get length() {
      return storage.size;
    },
  } satisfies Storage);
  history.replaceState(null, "", "/");
  fetcher = vi
    .fn<FixtureFetch>()
    .mockImplementation(async (input: string, options?: RequestInit) => {
      const url = new URL(input, "http://localhost");
      if (url.pathname === "/api/v1/bootstrap") return Response.json(bootstrap);
      if (url.pathname === "/api/v1/jobs")
        return Response.json(page([job, { ...job, deploymentId: "west" }]));
      if (url.pathname === "/api/v1/overview")
        return Response.json({
          active: 2,
          awaitingExecution: 0,
          running: 2,
          evidenceAttention: 2,
          missingCompletionTime: 0,
          terminal: { success: 0, failure: 0 },
          window: { from: "2026-10-02T12:00:00Z", to: "2026-10-03T12:00:00Z" },
          sources: [],
          completeness: "complete",
        });
      if (url.pathname.endsWith("/jobs/duplicate-id"))
        return Response.json({ job, fetchedAt: new Date().toISOString() });
      if (options?.method === "POST")
        return Response.json({ id: "new", revision: "1" }, { status: 201 });
      return Response.json(page([]));
    });
  vi.stubGlobal("fetch", fetcher);
  vi.stubGlobal("scrollTo", vi.fn());
});
afterEach(() => {
  vi.unstubAllGlobals();
});
describe("web monitoring workflows", () => {
  it("labels fixture mode and preserves two equal job IDs in separate source links", async () => {
    render(<App />);
    expect(await screen.findByText(/Limited sample preview/)).toBeVisible();
    const links = await screen.findAllByRole("link", {
      name: "Synthetic test",
    });
    expect(links).toHaveLength(2);
    expect(links.map((a) => a.getAttribute("href"))).toEqual(
      expect.arrayContaining([
        "/deployments/east/namespaces/ns/jobs/duplicate-id",
        "/deployments/west/namespaces/ns/jobs/duplicate-id",
      ]),
    );
    expect(
      screen.queryByRole("button", { name: /cancel job/i }),
    ).not.toBeInTheDocument();
  });
  it("shows cancellation intent without inventing a cancelled outcome or start time", async () => {
    history.replaceState(
      null,
      "",
      "/deployments/east/namespaces/ns/jobs/duplicate-id",
    );
    render(<App />);
    expect(await screen.findByText(/Cancellation is requested/)).toBeVisible();
    expect(screen.getByText("Stale")).toBeVisible();
    expect(screen.queryByText("Cancelled")).not.toBeInTheDocument();
    expect(screen.getByText("Started").nextElementSibling).toHaveTextContent(
      "Unavailable",
    );
  });
  it("sends filters to the API and keeps them in the shareable URL", async () => {
    history.replaceState(null, "", "/jobs");
    render(<App />);
    await screen.findAllByRole("link", { name: "Synthetic test" });
    await userEvent.selectOptions(
      screen.getByRole("combobox", { name: "Phase" }),
      "running",
    );
    await waitFor(() => expect(location.search).toContain("phase=running"));
    expect(
      fetcher.mock.calls.some(([path]) => path.includes("phase=running")),
    ).toBe(true);
  });
  it("renders imported provenance, exact run identity and zero group indices separately from unavailable execution time", async () => {
    history.replaceState(
      null,
      "",
      "/deployments/east/namespaces/ns/jobs/duplicate-id",
    );
    const previous = fetcher.getMockImplementation()!;
    fetcher.mockImplementation(async (input: string, options?: RequestInit) =>
      input.endsWith("/jobs/duplicate-id")
        ? Response.json({
            job: {
              ...job,
              imported: true,
              targetGenerationId: "generation-id",
              disposition: "skipped",
              currentRun: {
                id: "run-id",
                number: "9007199254740993",
                executionId: "execution-id",
              },
              lifecycle: {
                startedRecordedAt: "2026-10-03T15:00:00Z",
                startedProvenance: "agent_event",
              },
              group: {
                collectionId: "collection-id",
                collectionIndex: 0,
                graphId: "graph-id",
                graphIndex: 0,
              },
              scheduler: { reason: "Resources", cluster: "lab-cluster" },
            },
          })
        : previous(input, options),
    );
    render(<App />);
    expect(await screen.findByText(/Imported history/)).toBeVisible();
    expect(screen.getByText("Started").nextElementSibling).toHaveTextContent(
      "Unavailable",
    );
    expect(
      screen.getByText("Start recorded").nextElementSibling,
    ).not.toHaveTextContent("Unavailable");
    expect(
      screen.getByText("Start provenance").nextElementSibling,
    ).toHaveTextContent("Agent event");
    expect(screen.getByText("Run").nextElementSibling).toHaveTextContent(
      "9007199254740993",
    );
    expect(
      screen.getByText("Execution ID").nextElementSibling,
    ).toHaveTextContent("execution-id");
    expect(screen.getByRole("link", { name: "collection-id" })).toHaveAttribute(
      "href",
      "/deployments/east/namespaces/ns/workloads/collection/collection-id",
    );
    expect(
      screen.getByRole("link", { name: "collection-id" }).parentElement,
    ).toHaveTextContent("item 0");
    expect(
      screen.getByRole("link", { name: "graph-id" }).parentElement,
    ).toHaveTextContent("node 0");
    expect(
      screen.getByText("Graph disposition").nextElementSibling,
    ).toHaveTextContent("Skipped");
    expect(screen.getByText("Reason").nextElementSibling).toHaveTextContent(
      "Resources",
    );
  });
  it("preserves a removed explicit namespace instead of broadening to other grants", async () => {
    history.replaceState(null, "", "/jobs?sources=east&namespace=removed");
    render(<App />);
    await waitFor(() =>
      expect(
        fetcher.mock.calls.some(([path]) => {
          const url = new URL(path, "http://localhost");
          return (
            url.pathname === "/api/v1/jobs" &&
            url.searchParams.get("scope") ===
              JSON.stringify([{ deploymentId: "east", namespaceId: "removed" }])
          );
        }),
      ).toBe(true),
    );
  });
  it("creates an opt-in namespace rule across two sources with all terminal outcomes", async () => {
    const nsID = "71000000-0000-4000-8000-000000000001";
    const deployments = [
      "71000000-0000-4000-8000-000000000002",
      "71000000-0000-4000-8000-000000000003",
    ];
    const originalFetch = fetcher.getMockImplementation()!;
    fetcher.mockImplementation(async (input: string, options?: RequestInit) => {
      if (input === "/api/v1/bootstrap")
        return Response.json({
          ...bootstrap,
          deployments: bootstrap.deployments.map((source, index) => ({
            ...source,
            id: deployments[index],
            namespaces: [
              {
                ...ns,
                id: nsID,
                capabilities: ["namespace.read", "jobs.read"],
              },
            ],
          })),
        });
      if (input === "/api/v1/rules" && options?.method === "POST") {
        const request = JSON.parse(String(options.body));
        return Response.json(
          {
            ...request,
            id: "71000000-0000-4000-8000-000000000004",
            revision: "1",
            scopes: request.namespaces.map((ref: object) => ({
              ...ref,
              status: "pending",
            })),
            inaccessibleScopes: 0,
            unavailableScopes: 0,
            createdAt: new Date().toISOString(),
            updatedAt: new Date().toISOString(),
          },
          { status: 201 },
        );
      }
      return originalFetch(input, options);
    });
    history.replaceState(null, "", "/alerts");
    render(<App />);
    await screen.findByRole("button", { name: "+ New alert rule" });
    await userEvent.click(
      screen.getByRole("button", { name: "+ New alert rule" }),
    );
    await userEvent.type(
      screen.getByRole("textbox", { name: "Rule name" }),
      "Synthetic team updates",
    );
    await userEvent.click(
      screen.getByRole("radio", { name: /All jobs in selected namespaces/ }),
    );
    const fieldset = screen.getByRole("group", {
      name: "Authorized namespaces",
    });
    await userEvent.click(
      within(fieldset).getByRole("checkbox", { name: "Lab East / Research" }),
    );
    await userEvent.click(
      within(fieldset).getByRole("checkbox", { name: "Lab West / Research" }),
    );
    await userEvent.click(screen.getByRole("button", { name: "All terminal" }));
    await userEvent.click(screen.getByRole("button", { name: "Save rule" }));
    await waitFor(() =>
      expect(
        fetcher.mock.calls.some(([, options]) => options?.method === "POST"),
      ).toBe(true),
    );
    const mutation = fetcher.mock.calls.find(
      ([, options]) => options?.method === "POST",
    )!;
    const mutationOptions = mutation[1];
    if (typeof mutationOptions?.body !== "string")
      throw new Error("Expected a serialized rule request");
    expect(JSON.parse(mutationOptions.body)).toMatchObject({
      scope: "namespace_jobs",
      outcomeMode: "all_terminal",
      namespaces: [
        { deploymentId: deployments[0], namespaceId: nsID },
        { deploymentId: deployments[1], namespaceId: nsID },
      ],
    });
    expect(new Headers(mutationOptions.headers).get("X-CSRF-Token")).toBe(
      "fixture-csrf",
    );
  });
  it("preserves denied and network failures rather than pretending lists are empty", async () => {
    history.replaceState(null, "", "/targets");
    fetcher.mockImplementation(async (input: string) =>
      input === "/api/v1/bootstrap"
        ? Response.json(bootstrap)
        : Response.json(
            {
              code: "authorization_unavailable",
              message: "Directory verification is unavailable",
            },
            { status: 503 },
          ),
    );
    render(<App />);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Directory verification is unavailable",
    );
    expect(screen.queryByText("No targets available")).not.toBeInTheDocument();
  });
});

it("offline sign-out clears sensitive content and does not silently restore the session", async () => {
  history.replaceState(null, "", "/settings");
  render(<App />);
  await screen.findByRole("button", { name: "Sign out" });
  const previous = fetcher.getMockImplementation()!;
  fetcher.mockImplementation((input: string, options?: RequestInit) =>
    input === "/auth/logout"
      ? Promise.reject(new TypeError("offline"))
      : previous(input, options),
  );
  await userEvent.click(screen.getByRole("button", { name: "Sign out" }));
  expect(
    await screen.findByText(/The server could not confirm sign-out/),
  ).toBeVisible();
  expect(screen.queryByText("Lab Alice")).not.toBeInTheDocument();
  expect(window.localStorage.getItem("jobman.signoutPending")).toBe("1");
});

describe("group monitoring workflows", () => {
  const workload = {
    deploymentId: "east",
    namespaceId: "ns",
    id: "graph-one",
    kind: "graph",
    name: "Synthetic pipeline",
    revision: "12",
    createdAt: "2026-10-03T10:00:00Z",
    asOf: "2026-10-03T12:05:00Z",
    totalChildren: "9007199254740993",
    counts: { active: "2", waiting: "1" },
    unsatisfiedPolicy: "skip",
  };
  it("shows source dependency counts, uses bounded neighborhoods and pages filtered source edges", async () => {
    history.replaceState(
      null,
      "",
      "/deployments/east/namespaces/ns/workloads/graph/graph-one",
    );
    vi.stubGlobal(
      "Worker",
      class {
        onmessage?: (event: { data: unknown }) => void;
        postMessage(data: { nodes: { id: string }[] }) {
          this.onmessage?.({
            data: {
              positions: data.nodes.map((node, i) => ({
                id: node.id,
                x: i * 220,
                y: 20,
              })),
              width: 700,
              height: 180,
            },
          });
        }
        terminate() {}
      },
    );
    const child = {
      id: "node-two",
      name: "Build",
      index: "0",
      job: { ...job, id: "node-two" },
      disposition: "run",
      dependencyCounts: {
        total: "5",
        satisfied: "1",
        waiting: "4",
        unsatisfied: "0",
      },
    };
    const upstream = {
      id: "node-one",
      name: "Prepare",
      index: "1",
      job: { ...job, id: "node-one" },
    };
    const edge = {
      from: "Prepare",
      to: "Build",
      fromJobId: "node-one",
      toJobId: "node-two",
      predicate: "afterok",
      outcomes: [],
      upstreamPhase: "running",
      state: "waiting",
    };
    const previous = fetcher.getMockImplementation()!;
    fetcher.mockImplementation(async (input: string, options?: RequestInit) => {
      const url = new URL(input, "http://localhost");
      if (url.pathname.endsWith("/graph/graph-one"))
        return Response.json({
          ...page([]),
          workload,
          children: [child],
          total: "9007199254740993",
        });
      if (url.pathname.endsWith("/neighborhood"))
        return Response.json({
          ...page([]),
          centerId: "node-two",
          nodes: [child, upstream],
          edges: [edge],
          totalNodes: "6",
          totalEdges: "5",
          omittedNodes: "4",
          omittedEdges: "4",
        });
      if (url.pathname.endsWith("/dependencies"))
        return Response.json({
          ...page([edge]),
          total: "501",
          nextCursor: url.searchParams.has("cursor")
            ? undefined
            : "edge-page-2",
        });
      return previous(input, options);
    });
    render(<App />);
    expect(await screen.findByText("Total 5")).toBeVisible();
    expect(screen.getByText("Waiting 4")).toBeVisible();
    expect(
      screen.queryByText("No prerequisites reported"),
    ).not.toBeInTheDocument();
    expect(screen.getAllByText("9,007,199,254,740,993").length).toBeGreaterThan(
      0,
    );
    await userEvent.click(
      screen.getByRole("button", { name: "Inspect dependencies" }),
    );
    expect(
      await screen.findByRole("region", { name: "Neighborhood node list" }),
    ).toBeVisible();
    expect(
      screen.getByText(/Not shown in this neighborhood: 4 nodes, 4 edges/),
    ).toBeVisible();
    const inspector = screen.getByRole("region", {
      name: "Dependency inspection",
    });
    expect(
      within(inspector).getByRole("link", { name: "Open upstream job" }),
    ).toHaveAttribute("href", "/deployments/east/namespaces/ns/jobs/node-one");
    expect(
      fetcher.mock.calls.some(
        ([path]) =>
          path.includes("/neighborhood?") &&
          path.includes("nodeId=node-two") &&
          path.includes("maxNodes=50") &&
          path.includes("maxEdges=100"),
      ),
    ).toBe(true);
    await userEvent.click(
      within(inspector).getByRole("button", { name: "Next dependency page →" }),
    );
    await waitFor(() =>
      expect(
        fetcher.mock.calls.some(
          ([path]) =>
            path.includes("cursor=edge-page-2") &&
            path.includes("nodeId=node-two") &&
            path.includes("direction=incoming"),
        ),
      ).toBe(true),
    );
    await userEvent.selectOptions(
      screen.getByRole("combobox", { name: "Dependency direction" }),
      "outgoing",
    );
    await waitFor(() =>
      expect(
        fetcher.mock.calls.some(
          ([path]) =>
            path.includes("direction=outgoing") && !path.includes("cursor="),
        ),
      ).toBe(true),
    );
  });
  it("labels partial catalog counts as a subtotal and keeps duplicate IDs source-qualified", async () => {
    history.replaceState(null, "", "/workloads?kind=graph");
    const previous = fetcher.getMockImplementation()!;
    fetcher.mockImplementation(async (input: string, options?: RequestInit) =>
      input.startsWith("/api/v1/workloads/")
        ? Response.json({
            ...page([workload, { ...workload, deploymentId: "west" }]),
            completeness: "partial",
            total: "2",
            totals: [],
          })
        : previous(input, options),
    );
    render(<App />);
    const links = await screen.findAllByRole("link", {
      name: "Synthetic pipeline",
    });
    expect(links.map((link) => link.getAttribute("href"))).toEqual(
      expect.arrayContaining([
        "/deployments/east/namespaces/ns/workloads/graph/graph-one",
        "/deployments/west/namespaces/ns/workloads/graph/graph-one",
      ]),
    );
    expect(
      screen.getByText("2 workloads in available sources (subtotal)"),
    ).toBeVisible();
  });
});

it("pages artifact metadata with exact run/size and restarts paging when selecting a run", async () => {
  history.replaceState(
    null,
    "",
    "/deployments/east/namespaces/ns/jobs/duplicate-id?tab=artifacts",
  );
  const previous = fetcher.getMockImplementation()!;
  fetcher.mockImplementation(async (input: string, options?: RequestInit) => {
    const url = new URL(input, "http://localhost");
    if (!url.pathname.endsWith("/artifacts")) return previous(input, options);
    const next = url.searchParams.has("cursor");
    const run = url.searchParams.get("runNumber") ?? "3";
    return Response.json({
      ...page([
        {
          id: `execution/${next ? "second" : "first"}`,
          name: next ? "second-result" : "first-result",
          runId: "real-run",
          runNumber: run,
          executionId: "real-execution",
          targetGenerationId: "generation",
          sizeBytes: "9007199254740993",
          checksum: "sha256:published-checksum",
          publishedAt: "2026-10-03T12:00:00Z",
          availability: "metadata_only",
        },
      ]),
      total: "2",
      ...(!next ? { nextCursor: "artifact-next" } : {}),
    });
  });
  render(<App />);
  expect(await screen.findByText("first-result")).toBeVisible();
  expect(screen.getByText("9,007,199,254,740,993 bytes")).toBeVisible();
  expect(
    screen.getByText("Published metadata; bytes unverified"),
  ).toBeVisible();
  expect(
    screen.queryByRole("link", { name: /download/i }),
  ).not.toBeInTheDocument();
  await userEvent.click(
    screen.getByRole("button", { name: "Next artifact page →" }),
  );
  expect(await screen.findByText("second-result")).toBeVisible();
  expect(location.search).toContain("artifactCursor=artifact-next");
  await userEvent.type(
    screen.getByRole("textbox", { name: "Artifact run number" }),
    "2",
  );
  expect(await screen.findByText("first-result")).toBeVisible();
  expect(location.search).toContain("artifactRun=2");
  expect(location.search).not.toContain("artifactCursor");
  expect(
    fetcher.mock.calls.some(
      ([path]) => path.includes("runNumber=2") && !path.includes("cursor="),
    ),
  ).toBe(true);
});
