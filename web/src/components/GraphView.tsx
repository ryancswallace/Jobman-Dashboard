import { useEffect, useState } from "react";
import type { GraphNode } from "../lib/models";
import { count, title } from "../lib/format";
export function GraphView({
  nodes,
  selected,
  onSelect,
  omittedNodes,
  omittedEdges,
}: {
  nodes: GraphNode[];
  selected?: string;
  onSelect: (id: string) => void;
  omittedNodes?: string;
  omittedEdges?: string;
}) {
  const [layout, setLayout] = useState<{
    key: string;
    positions: { id: string; x: number; y: number }[];
    width: number;
    height: number;
  }>();
  const key = JSON.stringify(
    nodes.map((n) => ({
      id: n.id,
      parents: n.dependencies?.map((d) => d.upstreamNodeId) ?? [],
    })),
  );
  useEffect(() => {
    let live = true;
    const worker = new Worker(
      new URL("../lib/graph.worker.ts", import.meta.url),
      { type: "module" },
    );
    worker.onmessage = (e) => {
      if (live) setLayout({ ...e.data, key });
    };
    worker.postMessage({ nodes: JSON.parse(key) });
    return () => {
      live = false;
      worker.terminate();
    };
  }, [key]);
  const edges = nodes
    .flatMap((node) =>
      (node.dependencies ?? []).map((d) => ({
        from: d.upstreamNodeId,
        to: node.id,
        status: d.status,
      })),
    )
    .slice(0, 500);
  return (
    <section className="panel">
      <div className="panel-heading">
        <div>
          <h2>Graph neighborhood</h2>
          <p>
            Selected node and its bounded neighborhood. Use the paginated node
            and dependency lists for complete navigation.
          </p>
        </div>
      </div>
      <div
        className="graph-scroll"
        role="region"
        aria-label="Dependency graph diagram; equivalent node list follows"
        tabIndex={0}
      >
        {layout?.key === key ? (
          <svg
            width={layout.width}
            height={layout.height}
            role="group"
            aria-label="Source-reported dependencies"
          >
            <defs>
              <marker
                id="arrow"
                markerWidth="10"
                markerHeight="10"
                refX="9"
                refY="3"
                orient="auto"
              >
                <path d="M0,0 L0,6 L9,3 z" fill="currentColor" />
              </marker>
            </defs>
            {edges.map((edge, i) => {
              const from = layout.positions.find((p) => p.id === edge.from),
                to = layout.positions.find((p) => p.id === edge.to);
              return from && to ? (
                <path
                  key={i}
                  d={`M ${from.x + 195} ${from.y + 31} C ${from.x + 225} ${from.y + 31},${to.x - 30} ${to.y + 31},${to.x} ${to.y + 31}`}
                  className="graph-edge"
                  markerEnd="url(#arrow)"
                />
              ) : null;
            })}
            {layout.positions.map((position) => {
              const node = nodes.find((n) => n.id === position.id);
              if (!node) return null;
              return (
                <g
                  key={node.id}
                  transform={`translate(${position.x},${position.y})`}
                  role="button"
                  aria-current={selected === node.id ? "true" : undefined}
                  tabIndex={0}
                  aria-label={`${node.name ?? node.id}, ${node.job.phase}, ${node.readiness ?? "readiness unavailable"}`}
                  onClick={() => onSelect(node.id)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault();
                      onSelect(node.id);
                    }
                  }}
                >
                  <rect
                    width="195"
                    height="66"
                    rx="8"
                    className={
                      selected === node.id
                        ? "graph-node selected"
                        : "graph-node"
                    }
                  />
                  <text x="12" y="24" className="graph-node-title">
                    {(node.name ?? node.id).slice(0, 23)}
                  </text>
                  <text x="12" y="47" className="graph-node-status">
                    {title(node.disposition || node.job.phase)}
                  </text>
                </g>
              );
            })}
          </svg>
        ) : (
          <p className="panel-body">Laying out this node page…</p>
        )}
      </div>
      {(omittedNodes || omittedEdges) && (
        <p className="panel-note">
          Not shown in this neighborhood: {count(omittedNodes)} nodes,{" "}
          {count(omittedEdges)} edges. Continue through the paginated node list.
        </p>
      )}
    </section>
  );
}
