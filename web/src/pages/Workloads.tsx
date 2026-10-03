import { useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { useSession } from "../lib/session";
import { apiQuery, resourcePath } from "../lib/transport";
import { useResource } from "../lib/useResource";
import {
  decodePage,
  decodeJob,
  decodeMeta,
  type JobDTO,
  type SourceDTO,
} from "../lib/api";
import type {
  GraphNode,
  Workload,
  WorkloadDetail,
  Result,
} from "../lib/models";
import {
  count,
  jobRoute,
  scopeQuery,
  sourceLabel,
  title,
  workloadRoute,
} from "../lib/format";
import {
  Completeness,
  Empty,
  ErrorNotice,
  Freshness,
  PageHeader,
  Spinner,
  Status,
} from "../components/States";
import { GraphView } from "../components/GraphView";
const decodeWorkloads = (value: unknown) => decodePage<Workload>(value);
function decodeWorkloadDetail(value: unknown): Result<WorkloadDetail> {
  const dto = value as {
    workload: Workload;
    children: (Omit<GraphNode, "job"> & { job: JobDTO })[];
    nextCursor?: string;
    sources: SourceDTO[];
    completeness: string;
    fetchedAt: string;
    omittedNodes?: string;
    omittedEdges?: string;
  };
  return {
    data: {
      workload: dto.workload,
      children: dto.children.map((c) => ({ ...c, job: decodeJob(c.job) })),
      omittedNodes: dto.omittedNodes,
      omittedEdges: dto.omittedEdges,
    },
    meta: decodeMeta(dto),
  };
}
export function WorkloadsPage() {
  const { bootstrap, identity, scope } = useSession(),
    [search, setSearch] = useSearchParams(),
    kind = search.get("kind") ?? "collection";
  const result = useResource(
    apiQuery(`workloads/${kind}`, {
      ...scopeQuery(scope, bootstrap.sources),
      limit: 50,
      cursor: search.get("cursor") ?? undefined,
    }),
    identity,
    bootstrap.preferences.refreshSeconds * 1000,
    decodeWorkloads,
  );
  return (
    <>
      <PageHeader
        title="Workloads"
        description="Explore collections, Slurm arrays, and source-owned dependency graphs."
        actions={
          <button className="button secondary" onClick={result.refresh}>
            ↻ Refresh
          </button>
        }
      />
      <nav className="tabs" aria-label="Workload type">
        {[
          ["collection", "Collections"],
          ["array", "Slurm arrays"],
          ["graph", "Dependency graphs"],
        ].map(([value, label]) => (
          <button
            key={value}
            className={kind === value ? "selected" : ""}
            aria-current={kind === value ? "page" : undefined}
            onClick={() => {
              const q = new URLSearchParams(search);
              q.set("kind", value);
              q.delete("cursor");
              setSearch(q);
            }}
          >
            {label}
          </button>
        ))}
      </nav>
      <div className="page-meta">
        <Freshness
          fetchedAt={result.fetchedAt}
          loading={result.loading}
          error={!!result.error}
        />
        <span>Complete summaries · bounded child pages</span>
      </div>
      {result.error && (
        <ErrorNotice error={result.error} retry={result.refresh} />
      )}
      <Completeness meta={result.data?.meta} />
      {!result.data && result.loading ? (
        <Spinner label="Loading workloads" />
      ) : result.data?.data.length ? (
        <section className="panel">
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Workload</th>
                  <th>Source / namespace</th>
                  <th>Children</th>
                  <th>Source-reported results</th>
                  <th>Policy</th>
                </tr>
              </thead>
              <tbody>
                {result.data.data.map((w) => (
                  <tr key={`${w.deploymentId}:${w.namespaceId}:${w.id}`}>
                    <td>
                      <Link className="job-name" to={workloadRoute(w)}>
                        {w.name || w.id}
                      </Link>
                      <span className="secondary-line mono">{w.id}</span>
                    </td>
                    <td>
                      {sourceLabel(
                        bootstrap.sources,
                        w.deploymentId,
                        w.namespaceId,
                      )}
                    </td>
                    <td>{count(w.totalChildren)}</td>
                    <td>
                      <div className="count-chips">
                        {Object.entries(w.counts).map(([key, value]) => (
                          <span className="count-chip" key={key}>
                            {title(key)} {count(value)}
                          </span>
                        ))}
                      </div>
                    </td>
                    <td>{w.failurePolicy ?? "Unavailable"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className="pagination">
            <span>Wrappers are not counted as additional jobs.</span>
            {result.data.meta.nextCursor && (
              <button
                className="button secondary"
                onClick={() => {
                  const q = new URLSearchParams(search);
                  q.set("cursor", result.data!.meta.nextCursor!);
                  setSearch(q);
                }}
              >
                Next page →
              </button>
            )}
          </div>
        </section>
      ) : (
        !result.error && (
          <Empty
            title={`No ${kind === "array" ? "Slurm arrays" : kind === "graph" ? "dependency graphs" : "collections"} in this scope`}
          >
            Workloads become available when reported by the selected Control
            deployments.
          </Empty>
        )
      )}
    </>
  );
}
export function WorkloadDetailPage() {
  const {
      deploymentId = "",
      namespaceId = "",
      kind = "",
      id = "",
    } = useParams(),
    { identity, bootstrap } = useSession(),
    [search, setSearch] = useSearchParams(),
    [selected, setSelected] = useState<string>();
  const path = resourcePath(
    { deploymentId, namespaceId },
    `workloads/${encodeURIComponent(kind)}`,
    id,
  );
  const result = useResource(
      `${path}?${new URLSearchParams({ limit: "50", ...(search.get("cursor") ? { cursor: search.get("cursor")! } : {}) })}`,
      identity,
      bootstrap.preferences.refreshSeconds * 1000,
      decodeWorkloadDetail,
    ),
    detail = result.data?.data,
    w = detail?.workload;
  return (
    <>
      <Link
        className="back-link"
        to={`/workloads?kind=${encodeURIComponent(kind)}`}
      >
        ← Back to workloads
      </Link>
      <PageHeader
        eyebrow={sourceLabel(bootstrap.sources, deploymentId, namespaceId)}
        title={w?.name || id}
        description={`${title(kind)} · ${id}`}
        actions={
          <button className="button secondary" onClick={result.refresh}>
            ↻ Refresh
          </button>
        }
      />
      {result.error && (
        <ErrorNotice error={result.error} retry={result.refresh} />
      )}
      <Completeness meta={result.data?.meta} />
      {!detail && result.loading ? (
        <Spinner label="Loading workload" />
      ) : (
        detail &&
        w && (
          <>
            <div className="metric-grid workload-metrics">
              <div className="metric-card purple">
                <span>Total child jobs</span>
                <strong>{count(w.totalChildren)}</strong>
                <small>Complete source summary</small>
              </div>
              {Object.entries(w.counts).map(([key, value]) => (
                <div className="metric-card" key={key}>
                  <span>{title(key)}</span>
                  <strong>{count(value)}</strong>
                </div>
              ))}
            </div>
            <section className="panel">
              <dl className="facts compact">
                <div>
                  <dt>Concurrency limit</dt>
                  <dd>{w.concurrency ?? "Unavailable"}</dd>
                </div>
                <div>
                  <dt>Failure policy</dt>
                  <dd>{w.failurePolicy ?? "Unavailable"}</dd>
                </div>
                {kind === "array" && (
                  <div>
                    <dt>Scheduler array ID</dt>
                    <dd>{w.arrayId ?? "Not observed"}</dd>
                  </div>
                )}
              </dl>
            </section>
            {kind === "graph" && (
              <GraphView
                nodes={detail.children}
                onSelect={setSelected}
                selected={selected}
                omittedNodes={detail.omittedNodes}
                omittedEdges={detail.omittedEdges}
              />
            )}
            <section className="panel">
              <div className="panel-heading">
                <div>
                  <h2>
                    {kind === "graph"
                      ? "Nodes and dependencies"
                      : kind === "array"
                        ? "Array tasks"
                        : "Child jobs"}
                  </h2>
                  <p>
                    {kind === "graph"
                      ? "Accessible source-reported node list. Readiness is evaluated by Control."
                      : "Each row keeps its original Jobman job identity."}
                  </p>
                </div>
              </div>
              <div className="table-wrap">
                <table>
                  <thead>
                    <tr>
                      <th>
                        {kind === "array" ? "Task index" : "Node / child"}
                      </th>
                      <th>Job</th>
                      <th>Phase / outcome</th>
                      <th>Readiness / disposition</th>
                      {kind === "graph" && <th>Dependencies</th>}
                    </tr>
                  </thead>
                  <tbody>
                    {detail.children.map((node) => (
                      <tr
                        key={node.id}
                        className={selected === node.id ? "selected-row" : ""}
                      >
                        <td>
                          {kind === "array" ? (
                            (node.taskIndex ?? "Unavailable")
                          ) : (
                            <button
                              className="text-button"
                              onClick={() => setSelected(node.id)}
                            >
                              {node.id}
                            </button>
                          )}
                        </td>
                        <td>
                          <Link to={jobRoute(node.job)}>
                            {node.job.name || node.job.jobId}
                          </Link>
                        </td>
                        <td>
                          <Status value={node.job.phase} />
                          {node.job.outcome && (
                            <span className="secondary-line">
                              {title(node.job.outcome)}
                            </span>
                          )}
                        </td>
                        <td>
                          {title(node.readiness)}
                          {node.disposition && (
                            <span className="secondary-line">
                              {title(node.disposition)}
                            </span>
                          )}
                        </td>
                        {kind === "graph" && (
                          <td>
                            {node.dependencies?.length ? (
                              <ul className="dependency-list">
                                {node.dependencies.map((d, i) => (
                                  <li key={i}>
                                    <span className="mono">
                                      {d.upstreamNodeId}
                                    </span>{" "}
                                    · {d.predicate} ·{" "}
                                    <strong>{d.status}</strong>
                                  </li>
                                ))}
                              </ul>
                            ) : (
                              "No prerequisites reported"
                            )}
                          </td>
                        )}
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              <div className="pagination">
                <span>
                  {detail.children.length} children loaded of{" "}
                  {count(w.totalChildren)} total
                </span>
                <div>
                  {search.get("cursor") && (
                    <button
                      className="button secondary"
                      onClick={() => setSearch({})}
                    >
                      First page
                    </button>
                  )}
                  {result.data?.meta.nextCursor && (
                    <button
                      className="button secondary"
                      onClick={() =>
                        setSearch({ cursor: result.data!.meta.nextCursor! })
                      }
                    >
                      Next page →
                    </button>
                  )}
                </div>
              </div>
            </section>
          </>
        )
      )}
    </>
  );
}
