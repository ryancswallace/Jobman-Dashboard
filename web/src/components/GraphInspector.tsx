import { useState } from "react";
import { Link } from "react-router-dom";
import type { NamespaceRef } from "../lib/models";
import {
  decodeGraphDependencies,
  decodeGraphNeighborhood,
} from "../lib/workloads";
import { useResource } from "../lib/useResource";
import { useSession } from "../lib/session";
import { count, jobRoute, timestamp, title } from "../lib/format";
import { GraphView } from "./GraphView";
import {
  Completeness,
  Empty,
  ErrorNotice,
  Freshness,
  Spinner,
  Status,
} from "./States";

export function GraphInspector(props: {
  path: string;
  scope: NamespaceRef;
  selected?: string;
  onSelect: (id: string) => void;
  onClear: () => void;
}) {
  return <Inspector key={props.selected ?? "all"} {...props} />;
}
function Inspector({
  path,
  scope,
  selected,
  onSelect,
  onClear,
}: {
  path: string;
  scope: NamespaceRef;
  selected?: string;
  onSelect: (id: string) => void;
  onClear: () => void;
}) {
  const { identity, bootstrap } = useSession();
  const [direction, setDirection] = useState(selected ? "incoming" : "");
  const [cursor, setCursor] = useState<string>();
  const interval = bootstrap.preferences.refreshSeconds * 1000;
  const neighborhood = useResource(
    selected
      ? `${path}/neighborhood?${new URLSearchParams({ nodeId: selected, maxNodes: "50", maxEdges: "100" })}`
      : null,
    identity,
    interval,
    decodeGraphNeighborhood,
  );
  const dependencies = useResource(
    `${path}/dependencies?${new URLSearchParams({ limit: "100", ...(selected ? { nodeId: selected } : {}), ...(direction ? { direction } : {}), ...(cursor ? { cursor } : {}) })}`,
    identity,
    cursor ? 0 : interval,
    decodeGraphDependencies,
  );
  return (
    <>
      {selected ? (
        <>
          {neighborhood.error && (
            <ErrorNotice
              error={neighborhood.error}
              retry={neighborhood.refresh}
            />
          )}
          <Completeness meta={neighborhood.data?.meta} />
          {neighborhood.loading && !neighborhood.data && (
            <Spinner label="Loading graph neighborhood" />
          )}
          {neighborhood.data && (
            <>
              <GraphView
                nodes={neighborhood.data.data.nodes}
                selected={selected}
                onSelect={onSelect}
                omittedNodes={neighborhood.data.data.omittedNodes}
                omittedEdges={neighborhood.data.data.omittedEdges}
              />
              <section className="panel" aria-label="Neighborhood node list">
                <div className="panel-heading">
                  <div>
                    <h2>Neighborhood nodes</h2>
                    <p>
                      Returned {neighborhood.data.data.nodes.length} of{" "}
                      {count(neighborhood.data.data.totalNodes)} nodes and{" "}
                      {neighborhood.data.data.edges.length} of{" "}
                      {count(neighborhood.data.data.totalEdges)} edges. This is
                      a local neighborhood, not the entire graph.
                    </p>
                  </div>
                </div>
                <div className="table-wrap">
                  <table>
                    <thead>
                      <tr>
                        <th>Node</th>
                        <th>Job</th>
                        <th>Source phase</th>
                      </tr>
                    </thead>
                    <tbody>
                      {neighborhood.data.data.nodes.map((node) => (
                        <tr key={node.id}>
                          <td>
                            <button
                              className="text-button"
                              aria-current={
                                selected === node.id ? "true" : undefined
                              }
                              onClick={() => onSelect(node.id)}
                            >
                              {node.name ?? node.id}
                            </button>
                          </td>
                          <td>
                            <Link to={jobRoute(node.job)}>
                              {node.job.name ?? node.id}
                            </Link>
                          </td>
                          <td>
                            <Status value={node.job.phase} />
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
                <p className="panel-note">
                  Source observation:{" "}
                  {timestamp(
                    neighborhood.data.meta.contributions[0]?.observedAt,
                    bootstrap.preferences.timezone,
                  )}
                  . Readiness and predicates are reported by Control.
                </p>
              </section>
            </>
          )}
        </>
      ) : (
        <p className="notice info">
          Select a graph node to inspect its bounded neighborhood. The
          dependency list below includes the whole graph through pagination.
        </p>
      )}
      <details className="dependency-disclosure" open={!!selected}>
        <summary>
          {selected
            ? "Inspect selected node dependencies"
            : "Browse all graph dependencies"}
        </summary>
        <section className="panel" aria-label="Dependency inspection">
          <div className="panel-heading">
            <div>
              <h2>
                {selected ? "Selected node dependencies" : "Graph dependencies"}
              </h2>
              <p>
                {selected ? selected : "All graph nodes"} · Source-reported
                predicates and states
              </p>
            </div>
            <button
              className="button secondary"
              onClick={() =>
                cursor ? setCursor(undefined) : dependencies.refresh()
              }
            >
              Refresh dependencies
            </button>
            {selected && (
              <button className="button secondary" onClick={onClear}>
                All graph dependencies
              </button>
            )}
          </div>
          {selected && (
            <div className="panel-body">
              <label>
                Dependency direction{" "}
                <select
                  value={direction}
                  onChange={(event) => {
                    setDirection(event.target.value);
                    setCursor(undefined);
                  }}
                >
                  <option value="incoming">Incoming prerequisites</option>
                  <option value="outgoing">Outgoing dependents</option>
                  <option value="">Both directions</option>
                </select>
              </label>
            </div>
          )}
          <Freshness
            fetchedAt={dependencies.fetchedAt}
            loading={dependencies.loading}
            error={!!dependencies.error}
          />
          {dependencies.error && (
            <ErrorNotice
              error={dependencies.error}
              retry={dependencies.refresh}
            />
          )}
          <Completeness meta={dependencies.data?.meta} />
          {dependencies.loading && !dependencies.data ? (
            <Spinner label="Loading dependencies" />
          ) : (
            dependencies.data && (
              <>
                {dependencies.data.data.items.length ? (
                  <div className="table-wrap">
                    <table>
                      <thead>
                        <tr>
                          <th>Upstream node</th>
                          <th>Downstream node</th>
                          <th>Predicate</th>
                          <th>Upstream phase / outcome</th>
                          <th>Predicate state</th>
                        </tr>
                      </thead>
                      <tbody>
                        {dependencies.data.data.items.map((edge) => (
                          <tr key={`${edge.fromJobId}:${edge.toJobId}`}>
                            <td>
                              <button
                                className="text-button"
                                onClick={() => onSelect(edge.fromJobId)}
                              >
                                {edge.from}
                              </button>
                              <Link
                                className="secondary-line"
                                to={jobRoute({
                                  ...scope,
                                  jobId: edge.fromJobId,
                                })}
                              >
                                Open upstream job
                              </Link>
                            </td>
                            <td>
                              <button
                                className="text-button"
                                onClick={() => onSelect(edge.toJobId)}
                              >
                                {edge.to}
                              </button>
                              <Link
                                className="secondary-line"
                                to={jobRoute({ ...scope, jobId: edge.toJobId })}
                              >
                                Open downstream job
                              </Link>
                            </td>
                            <td>
                              {edge.predicate}
                              {edge.outcomes.length > 0 && (
                                <span className="secondary-line">
                                  Outcomes: {edge.outcomes.join(", ")}
                                </span>
                              )}
                            </td>
                            <td>
                              {title(edge.upstreamPhase)}
                              {edge.upstreamOutcome && (
                                <span className="secondary-line">
                                  {title(edge.upstreamOutcome)}
                                </span>
                              )}
                            </td>
                            <td>{title(edge.state)}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                ) : (
                  <Empty title="No dependencies match this view">
                    Control reported no dependency edges for this selection.
                  </Empty>
                )}
                <div className="pagination">
                  <span>
                    {dependencies.data.data.items.length} returned of{" "}
                    {count(dependencies.data.data.total)} matching dependencies
                  </span>
                  <div>
                    {cursor && (
                      <button
                        className="button secondary"
                        onClick={() => setCursor(undefined)}
                      >
                        First dependency page
                      </button>
                    )}
                    {dependencies.data.meta.nextCursor && (
                      <button
                        className="button secondary"
                        onClick={() =>
                          setCursor(dependencies.data!.meta.nextCursor)
                        }
                      >
                        Next dependency page →
                      </button>
                    )}
                  </div>
                </div>
              </>
            )
          )}
        </section>
      </details>
    </>
  );
}
