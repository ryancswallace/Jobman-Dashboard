import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { WorkloadDetailPage } from "./Workloads";
import { GraphView } from "../components/GraphView";
import { decodeGraphNeighborhood } from "../lib/workloads";
import { layoutGraph, type LayoutNode } from "../lib/graphLayout";
import { decodeBootstrap } from "../lib/api";
import { auditAccessibility } from "../test-accessibility";
import {
  GraphWireFixture,
  graphFixture,
  graphFixturePath,
  graphNodeId,
  graphNodeName,
  graphFixtureEdge,
  graphFixtureNode,
} from "../test-graph-fixture";

const session = vi.hoisted(() => ({
  identity: "graph-viewer",
  bootstrap: {} as ReturnType<typeof decodeBootstrap>,
}));
vi.mock("../lib/session", () => ({ useSession: () => session }));
class LayoutWorker {
  onmessage?: (event: { data: ReturnType<typeof layoutGraph> }) => void;
  terminated = false;
  postMessage({ nodes }: { nodes: LayoutNode[] }) {
    queueMicrotask(() => {
      if (!this.terminated) this.onmessage?.({ data: layoutGraph(nodes) });
    });
  }
  terminate() {
    this.terminated = true;
  }
}
let fixture: GraphWireFixture;
let pending:
  | ((
      path: string,
      signal?: AbortSignal | null,
    ) => Promise<Response> | undefined)
  | undefined;
const route = graphFixturePath.replace("/api/v1", "");
function view() {
  return (
    <MemoryRouter initialEntries={[route]}>
      <main>
        <Routes>
          <Route
            path="/deployments/:deploymentId/namespaces/:namespaceId/workloads/:kind/:id"
            element={<WorkloadDetailPage />}
          />
        </Routes>
      </main>
    </MemoryRouter>
  );
}
const nodesTable = () =>
  screen.getByRole("columnheader", { name: "Node / child" }).closest("table")!;
const edgeTable = () =>
  screen
    .getByRole("columnheader", { name: "Predicate state" })
    .closest("table")!;
const rows = (table: HTMLTableElement) =>
  within(table).getAllByRole("row").slice(1);
beforeEach(() => {
  session.identity = "graph-viewer";
  session.bootstrap = decodeBootstrap({
    apiVersion: "jobman.dashboard/v1",
    account: { id: "synthetic-viewer", displayName: "Synthetic viewer" },
    completeness: "complete",
    fixtureMode: true,
    deployments: [
      {
        id: graphFixture.deploymentId,
        name: "Synthetic Control",
        status: "available",
        namespaces: [
          {
            id: graphFixture.namespaceId,
            name: "Synthetic graph",
            roles: ["viewer"],
            capabilities: ["namespace.read", "jobs.read", "groups.read"],
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
  fixture = new GraphWireFixture();
  pending = undefined;
  vi.stubGlobal("Worker", LayoutWorker);
  vi.stubGlobal(
    "fetch",
    vi.fn(
      (path: string, options?: RequestInit) =>
        pending?.(path, options?.signal) ??
        Promise.resolve(fixture.respond(path, options?.signal)),
    ),
  );
});
afterEach(() => vi.unstubAllGlobals());

it("navigates a ceiling graph with bounded tables, omissions, keyboard recentering and current-node semantics", async () => {
  render(view());
  await screen.findByRole("heading", { name: "Synthetic ceiling graph" });
  expect(rows(nodesTable())).toHaveLength(50);
  expect(
    screen.getByText(
      "50 children loaded of 10,000 at this child-page observation",
    ),
  ).toBeVisible();
  const user = userEvent.setup();
  const first = within(nodesTable()).getByRole("button", {
    name: graphNodeName(0),
  });
  first.focus();
  await user.keyboard("{Enter}");
  const diagram = await screen.findByRole("group", {
    name: "Source-reported dependencies",
  });
  expect(within(diagram).getAllByRole("button")).toHaveLength(50);
  expect(
    within(diagram).getByRole("button", { name: /^node-00000,/ }),
  ).toHaveAttribute("aria-current", "true");
  expect(
    screen.getByText(
      /Not shown in this neighborhood: 9,950 nodes, 99,900 edges/,
    ),
  ).toBeVisible();
  const neighborhood = screen.getByRole("region", {
    name: "Neighborhood node list",
  });
  const recenter = within(neighborhood).getByRole("button", {
    name: graphNodeName(1),
  });
  recenter.focus();
  await user.keyboard(" ");
  await waitFor(() =>
    expect(
      within(
        screen.getByRole("group", { name: "Source-reported dependencies" }),
      ).getByRole("button", { name: /^node-00001,/ }),
    ).toHaveAttribute("aria-current", "true"),
  );
  expect(
    within(nodesTable()).getByRole("button", { name: graphNodeName(1) }),
  ).toHaveAttribute("aria-current", "true");
  expect(
    within(
      screen.getByRole("region", { name: "Neighborhood node list" }),
    ).getByRole("button", { name: graphNodeName(1) }),
  ).toHaveAttribute("aria-current", "true");
  await auditAccessibility();
  expect(
    within(nodesTable()).getByRole("link", { name: "Synthetic job 1" }),
  ).toHaveAttribute(
    "href",
    `/deployments/${graphFixture.deploymentId}/namespaces/${graphFixture.namespaceId}/jobs/${graphNodeId(1)}`,
  );
  expect(fixture.maximumNodes).toBeLessThanOrEqual(50);
  expect(fixture.maximumEdges).toBeLessThanOrEqual(100);
  expect(fixture.maximumResponseBytes).toBeLessThan(2 << 20);
});

it("replaces pages and resets dependency cursors when the selected direction changes", async () => {
  render(view());
  await screen.findByRole("heading", { name: "Synthetic ceiling graph" });
  fireEvent.click(
    within(nodesTable()).getByRole("button", {
      name: graphNodeName(0),
    }),
  );
  const direction = await screen.findByRole("combobox", {
    name: "Dependency direction",
  });
  fireEvent.change(direction, { target: { value: "outgoing" } });
  await screen.findByText("100 returned of 9,999 matching dependencies");
  expect(rows(edgeTable())).toHaveLength(100);
  fireEvent.click(screen.getByRole("button", { name: /Next dependency page/ }));
  await waitFor(() =>
    expect(
      within(edgeTable()).getByRole("button", {
        name: graphNodeName(101),
      }),
    ).toBeVisible(),
  );
  expect(
    within(edgeTable()).queryByRole("button", {
      name: graphNodeName(1),
    }),
  ).not.toBeInTheDocument();
  fireEvent.change(direction, { target: { value: "incoming" } });
  await screen.findByText("No dependencies match this view");
  expect(
    new URL(
      fixture.calls.at(-1)!.path,
      "https://synthetic.invalid",
    ).searchParams.has("cursor"),
  ).toBe(false);
  fireEvent.click(screen.getByRole("button", { name: /^Next page/ }));
  await waitFor(() =>
    expect(
      within(nodesTable()).getByRole("button", {
        name: graphNodeName(50),
      }),
    ).toBeVisible(),
  );
  expect(
    within(nodesTable()).queryByRole("button", {
      name: graphNodeName(0),
    }),
  ).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "First page" }));
  await waitFor(() =>
    expect(
      within(nodesTable()).getByRole("button", {
        name: graphNodeName(0),
      }),
    ).toBeVisible(),
  );
});

it("clears private graph content on lost authority and ignores a cancelled old-account response", async () => {
  const rendered = render(view());
  await screen.findByRole("heading", { name: "Synthetic ceiling graph" });
  let finish: ((response: Response) => void) | undefined;
  let signal: AbortSignal | null | undefined;
  pending = (path, currentSignal) => {
    if (path.includes("cursor=")) {
      signal = currentSignal;
      return new Promise((resolve) => {
        finish = resolve;
      });
    }
  };
  fireEvent.click(screen.getByRole("button", { name: /^Next page/ }));
  await waitFor(() => expect(finish).toBeDefined());
  pending = undefined;
  fixture.denied = true;
  session.identity = "other-account";
  rendered.rerender(view());
  await screen.findByText("Current source authorization is unavailable.");
  expect(signal?.aborted).toBe(true);
  expect(
    screen.queryByRole("heading", { name: "Graph nodes" }),
  ).not.toBeInTheDocument();
  fixture.denied = false;
  const old = fixture.respond(`${graphFixturePath}?limit=50`);
  await act(async () => finish!(old));
  expect(
    screen.queryByRole("heading", { name: "Graph nodes" }),
  ).not.toBeInTheDocument();
  await auditAccessibility();
});

it("defines exactly 10000 source nodes and 100000 unique forward edges without fabricating execution", () => {
  const keys = new Set<string>();
  const incoming = new Uint32Array(graphFixture.nodes);
  for (let i = 0; i < graphFixture.edges; i++) {
    const edge = graphFixtureEdge(i);
    if (!(edge.fromJobId < edge.toJobId))
      throw Error("fixture is not a forward DAG");
    keys.add(`${edge.fromJobId}:${edge.toJobId}`);
    incoming[Number(edge.toJobId.split("-").at(-1)) - 1]++;
  }
  expect(keys.size).toBe(100000);
  for (let i = 0; i < graphFixture.nodes; i++) {
    expect(graphFixtureNode(i).dependencyCounts?.waiting).toBe(
      String(incoming[i]),
    );
  }
  expect(graphFixtureNode(9999).job.currentRun).toBeUndefined();
  expect(() => graphFixtureNode(10000)).toThrow();
  expect(() => graphFixtureEdge(100000)).toThrow();
});

async function renderCeilingDiagram() {
  const response = fixture.respond(
    `${graphFixturePath}/neighborhood?nodeId=${graphNodeId(0)}&maxNodes=200&maxEdges=500`,
  );
  const decoded = decodeGraphNeighborhood(await response.json());
  render(
    <main>
      <h1>Ceiling graph</h1>
      <GraphView
        nodes={decoded.data.nodes}
        selected={graphNodeId(0)}
        onSelect={() => {}}
        omittedNodes={decoded.data.omittedNodes}
        omittedEdges={decoded.data.omittedEdges}
      />
    </main>,
  );
  const diagram = await screen.findByRole("group", {
    name: "Source-reported dependencies",
  });
  expect(within(diagram).getAllByRole("button")).toHaveLength(200);
  expect(diagram.querySelectorAll("path.graph-edge")).toHaveLength(500);
  expect(
    screen.getByText(
      /Not shown in this neighborhood: 9,800 nodes, 99,500 edges/,
    ),
  ).toBeVisible();
}

it("renders all 200 nodes and 500 edges through the decoder and layout", async () => {
  await renderCeilingDiagram();
});
// A full SVG axe pass is deliberately opt-in and has its own bounded audit
// budget; it is not a user interaction or source-latency performance gate.
it.runIf(import.meta.env.VITE_GRAPH_CEILING_ACCEPTANCE === "1")(
  "audits the full 200 node and 500 edge diagram with the unchanged accessibility rule set",
  async () => {
    await renderCeilingDiagram();
    await auditAccessibility();
  },
  60000,
);

// Accessibility roles/keyboard semantics are exercised above. The full walk's
// oracle uses table DOM membership, avoiding repeated jsdom accessibility-tree
// reconstruction for every one of the 110,000 rows. All page transitions still
// use the actual buttons, fetch layer, decoder and React view.
function traversalRows(header: string) {
  const table = [...document.querySelectorAll("table")].find((item) =>
    item.tHead?.textContent?.includes(header),
  );
  if (!table?.tBodies[0]) throw Error("Page table has not arrived");
  return [...table.tBodies[0].rows];
}
async function clickPage(label: string) {
  await act(async () => {
    const button = screen.getByText(label, { selector: "button" });
    fireEvent.click(button);
  });
}
// Opt-in real-component traversal; no browser/reader/live-source claim.
it.runIf(import.meta.env.VITE_GRAPH_CEILING_ACCEPTANCE === "1")(
  "walks all 200 node pages and 1000 edge pages through the actual client controls",
  async () => {
    render(view());
    await screen.findByRole("heading", { name: "Synthetic ceiling graph" });
    const nodes = new Set<string>();
    for (let page = 0; page < 200; page++) {
      await waitFor(() =>
        expect(traversalRows("Node / child")[0]).toHaveTextContent(
          graphNodeName(page * 50),
        ),
      );
      const current = traversalRows("Node / child");
      expect(current).toHaveLength(50);
      for (const row of current) {
        const links = row.querySelectorAll("a");
        expect(links).toHaveLength(1);
        nodes.add(links[0].getAttribute("href")!);
      }
      if (page < 199) await clickPage("Next page →");
    }
    expect(nodes.size).toBe(10000);
    expect(
      screen.queryByText("Next page →", { selector: "button" }),
    ).not.toBeInTheDocument();
    console.info(
      "Graph ceiling: 200 node pages, 10000 unique source-qualified nodes",
    );
    fireEvent.click(screen.getByText("Browse all graph dependencies"));
    const edges = new Set<string>();
    for (let page = 0; page < 1000; page++) {
      const expected = graphFixtureEdge(page * 100);
      await waitFor(() => {
        const row = traversalRows("Predicate state")[0];
        expect(row).toHaveTextContent(expected.from);
        expect(row).toHaveTextContent(expected.to);
      });
      const current = traversalRows("Predicate state");
      expect(current).toHaveLength(100);
      for (const row of current) {
        const links = row.querySelectorAll("a");
        expect(links).toHaveLength(2);
        edges.add(
          `${links[0].getAttribute("href")}:${links[1].getAttribute("href")}`,
        );
      }
      if ((page + 1) % 200 === 0)
        console.info(
          `Graph ceiling: ${page + 1} dependency pages, ${edges.size} unique edges`,
        );
      if (page < 999) await clickPage("Next dependency page →");
    }
    expect(edges.size).toBe(100000);
    expect(
      screen.queryByText("Next dependency page →", { selector: "button" }),
    ).not.toBeInTheDocument();
    expect(fixture.maximumNodes).toBeLessThanOrEqual(50);
    expect(fixture.maximumEdges).toBeLessThanOrEqual(100);
    expect(fixture.maximumResponseBytes).toBeLessThan(2 << 20);
  },
  300000,
);
