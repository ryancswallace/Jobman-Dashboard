import type * as Wire from "../../contracts/typescript/dashboard.generated";

// Test-only synthetic wire source. No jobs execute and no credentials exist.
// Generate only a requested page; never put 10k nodes/100k edges in a response.
export const graphFixture = {
  deploymentId: "75000000-0000-4000-8000-000000000001",
  namespaceId: "75000000-0000-4000-8000-000000000002",
  graphId: "75000000-0000-4000-8000-000000000003",
  nodes: 10000,
  edges: 100000,
};
export const graphNodeId = (index: number) =>
  `76000000-0000-4000-8000-${String(index + 1).padStart(12, "0")}`;
export const graphNodeName = (index: number) =>
  `node-${String(index).padStart(5, "0")}`;
export const graphFixturePath = `/api/v1/deployments/${graphFixture.deploymentId}/namespaces/${graphFixture.namespaceId}/workloads/graph/${graphFixture.graphId}`;
const at = "2026-10-04T12:00:00Z";
const meta = () => ({
  completeness: "complete",
  sources: [
    {
      deploymentId: graphFixture.deploymentId,
      namespaceId: graphFixture.namespaceId,
      status: "available",
      asOf: at,
      fetchedAt: at,
    },
  ],
  fetchedAt: at,
});
// Integer adjacency avoids allocating all 100k wire edges for a selected-node read.
// The independent topology oracle still enumerates every edge through graphFixtureEdge.
function outgoing(index: number): number[] {
  if (index === 0) return Array.from({ length: 9999 }, (_, n) => n + 1);
  if (index >= 1 && index <= 9000)
    return Array.from({ length: 10 }, (_, n) => index + n + 1);
  return index === 9001 ? [9002] : [];
}
function incoming(index: number): number[] {
  if (index === 0) return [];
  const values = [0];
  for (let n = Math.max(1, index - 10); n <= Math.min(9000, index - 1); n++)
    values.push(n);
  if (index === 9002) values.push(9001);
  return values;
}
function nodeIndex(id: string | null): number {
  const index = Number(id?.split("-").at(-1)) - 1;
  if (
    !Number.isInteger(index) ||
    index < 0 ||
    index >= graphFixture.nodes ||
    graphNodeId(index) !== id
  )
    throw Error("invalid fixture node");
  return index;
}
export function graphFixtureNode(index: number): Wire.WorkloadChild {
  if (!Number.isInteger(index) || index < 0 || index >= graphFixture.nodes)
    throw Error("invalid fixture node");
  return {
    id: graphNodeId(index),
    index: String(index),
    name: graphNodeName(index),
    dependencyCounts: {
      total: String(incoming(index).length),
      satisfied: "0",
      waiting: String(incoming(index).length),
      unsatisfied: "0",
    },
    job: {
      deploymentId: graphFixture.deploymentId,
      namespaceId: graphFixture.namespaceId,
      id: graphNodeId(index),
      name: `Synthetic job ${index}`,
      targetId: "synthetic-inert",
      revision: "1",
      createdAt: at,
      updatedAt: at,
      desiredState: "run",
      phase: "accepted",
      confidence: "",
      group: { graphId: graphFixture.graphId, graphIndex: index },
      labels: { fixture: "graph-ceiling" },
    },
  };
}
export function graphFixtureEdge(index: number): Wire.GraphEdge {
  if (!Number.isInteger(index) || index < 0 || index >= graphFixture.edges)
    throw Error("invalid fixture edge");
  const extra = index - 9999;
  const from = extra < 0 ? 0 : 1 + Math.floor(extra / 10);
  const to = extra < 0 ? index + 1 : from + 1 + (extra % 10);
  return edgePair(from, to);
}
function edgePair(from: number, to: number): Wire.GraphEdge {
  return {
    from: graphNodeName(from),
    to: graphNodeName(to),
    fromJobId: graphNodeId(from),
    toJobId: graphNodeId(to),
    predicate: "success",
    outcomes: [],
    upstreamPhase: "accepted",
    state: "waiting",
  };
}
const workload: Wire.Workload = {
  deploymentId: graphFixture.deploymentId,
  namespaceId: graphFixture.namespaceId,
  id: graphFixture.graphId,
  name: "Synthetic ceiling graph",
  kind: "graph",
  createdAt: at,
  asOf: at,
  revision: "1",
  totalChildren: "10000",
  phase: "accepted",
  counts: {
    active: "0",
    terminal: "0",
    success: "0",
    failure: "0",
    cancelled: "0",
    waiting: "10000",
    skipped: "0",
    blocked: "0",
  },
  concurrency: "1",
  unsatisfiedPolicy: "skip",
};
type Continuation = { key: string; offset: number };
export class GraphWireFixture {
  private cursors = new Map<string, Continuation>();
  readonly calls: { path: string; signal?: AbortSignal | null }[] = [];
  maximumResponseBytes = 0;
  maximumNodes = 0;
  maximumEdges = 0;
  denied = false;
  private cursor(key: string, offset: number) {
    const token = `synthetic-cursor-${this.cursors.size + 1}`;
    this.cursors.set(token, { key, offset });
    return token;
  }
  respond(path: string, signal?: AbortSignal | null): Response {
    this.calls.push({ path, signal });
    if (this.denied)
      return this.json(
        {
          code: "authorization_unavailable",
          message: "Current source authorization is unavailable.",
        },
        503,
      );
    const url = new URL(path, "https://synthetic.invalid");
    if (!url.pathname.startsWith(graphFixturePath))
      throw Error("unexpected fixture route");
    const suffix = url.pathname.slice(graphFixturePath.length);
    const nodeId = url.searchParams.get("nodeId");
    const direction = url.searchParams.get("direction") ?? "";
    const key = `${suffix}:${nodeId}:${direction}`;
    const continuation = url.searchParams.get("cursor");
    const known = continuation ? this.cursors.get(continuation) : undefined;
    if (continuation && (!known || known.key !== key))
      return this.json(
        {
          code: "invalid_cursor",
          message: "Cursor does not match this graph view.",
        },
        400,
      );
    const offset = known?.offset ?? 0;
    const limit = Number(url.searchParams.get("limit") ?? "50");
    if (
      !Number.isInteger(limit) ||
      limit < 1 ||
      limit > (suffix === "/dependencies" ? 500 : 200)
    )
      throw Error("unbounded fixture request");
    if (!suffix) {
      const children = Array.from(
        { length: Math.min(limit, graphFixture.nodes - offset) },
        (_, n) => graphFixtureNode(offset + n),
      );
      this.maximumNodes = Math.max(this.maximumNodes, children.length);
      return this.json({
        workload,
        children,
        total: "10000",
        ...meta(),
        ...(offset + children.length < graphFixture.nodes
          ? { nextCursor: this.cursor(key, offset + children.length) }
          : {}),
      } satisfies Wire.WorkloadDetail);
    }
    if (suffix === "/dependencies") {
      const items: Wire.GraphEdge[] = [];
      let total: number;
      if (!nodeId) {
        total = graphFixture.edges;
        for (let i = offset; i < Math.min(offset + limit, total); i++)
          items.push(graphFixtureEdge(i));
      } else {
        const index = nodeIndex(nodeId);
        if (!["", "incoming", "outgoing"].includes(direction))
          throw Error("invalid fixture direction");
        const pairs = [
          ...(direction === "outgoing"
            ? []
            : incoming(index).map((from) => [from, index])),
          ...(direction === "incoming"
            ? []
            : outgoing(index).map((to) => [index, to])),
        ];
        total = pairs.length;
        for (const [from, to] of pairs.slice(offset, offset + limit))
          items.push(edgePair(from, to));
      }
      this.maximumEdges = Math.max(this.maximumEdges, items.length);
      return this.json({
        items,
        total: String(total),
        ...meta(),
        ...(offset + items.length < total
          ? { nextCursor: this.cursor(key, offset + items.length) }
          : {}),
      } satisfies Wire.GraphEdgePage);
    }
    if (suffix === "/neighborhood") {
      const center = nodeIndex(nodeId);
      const maxNodes = Number(url.searchParams.get("maxNodes"));
      const maxEdges = Number(url.searchParams.get("maxEdges"));
      if (
        !Number.isInteger(maxNodes) ||
        maxNodes < 1 ||
        maxNodes > 200 ||
        !Number.isInteger(maxEdges) ||
        maxEdges < 1 ||
        maxEdges > 500
      )
        throw Error("unbounded fixture neighborhood");
      const neighbors = new Set([
        center,
        ...incoming(center),
        ...outgoing(center),
      ]);
      const indices = [
        center,
        ...[...neighbors].filter((n) => n !== center).sort((a, b) => a - b),
      ]
        .slice(0, maxNodes)
        .sort((a, b) => a - b);
      const nodes = indices.map(graphFixtureNode),
        selected = new Set(indices);
      const edges: Wire.GraphEdge[] = [];
      let totalEdges = 0;
      for (const from of [...neighbors].sort((a, b) => a - b)) {
        for (const to of outgoing(from)) {
          if (!neighbors.has(to)) continue;
          totalEdges++;
          if (selected.has(from) && selected.has(to) && edges.length < maxEdges)
            edges.push(edgePair(from, to));
        }
      }
      this.maximumNodes = Math.max(this.maximumNodes, nodes.length);
      this.maximumEdges = Math.max(this.maximumEdges, edges.length);
      return this.json({
        centerId: nodeId!,
        nodes,
        edges,
        totalNodes: String(neighbors.size),
        totalEdges: String(totalEdges),
        omittedNodes: String(neighbors.size - nodes.length),
        omittedEdges: String(totalEdges - edges.length),
        ...meta(),
      } satisfies Wire.GraphNeighborhood);
    }
    throw Error("unexpected fixture operation");
  }
  private json(body: unknown, status = 200) {
    const text = JSON.stringify(body);
    this.maximumResponseBytes = Math.max(
      this.maximumResponseBytes,
      new TextEncoder().encode(text).length,
    );
    return new Response(text, {
      status,
      headers: { "Content-Type": "application/json" },
    });
  }
}
